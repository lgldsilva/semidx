package store

// Decision-support benchmark for ADR-7 (Postgres embedding de-duplication).
//
// It does NOT gate CI: it is skipped unless SEMIDX_BENCH_DEDUP=1 is set, and it
// needs a healthy Docker/testcontainers provider like the other store tests.
// Run it explicitly with:
//
//	SEMIDX_BENCH_DEDUP=1 go test ./internal/store -run TestDedupRecallBenchmark -v -timeout 20m
//
// The question it answers: if Postgres moves the embedding vector out of the
// per-project chunks_<dims> table into a shared dictionary (unique_embeddings)
// so each unique vector is stored once, does per-project vector search still
// recall the right chunks — and within RNF01 (≤5 ms/query)?
//
// Two schemas are populated with IDENTICAL data and queried with the exact SQL
// shapes each design implies:
//
//   - baseline  : one row per (project, chunk) with an inline embedding + a
//                 per-table HNSW index; search = ANN filtered by project_id.
//                 (This mirrors today's chunks_<dims>.)
//   - dedup (b) : a shared bench_dict(emb_hash, embedding) with the HNSW index,
//                 plus a thin bench_ref(project_id, emb_hash); search = global
//                 ANN over the dictionary, over-fetched K*F, then joined to the
//                 project's refs and re-ranked to top-K.
//
// Recall is measured against Go-computed exact cosine top-K per (query,project).

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

// benchRow is one (project, chunk) association plus its vector.
type benchRow struct {
	projectID int
	hash      string
	vec       []float32
}

// benchParams keeps the knobs in one place so the reported numbers are legible.
type benchParams struct {
	dims            int
	clusters        int // latent structure so ANN is not worst-case random
	sharedVectors   int // size of the shared-library dictionary pool
	projects        int
	ownPerProject   int // vectors unique to one project
	sharedPerProj   int // vectors drawn from the shared pool (cross-project dup)
	queries         int
	topK            int
	overFetchFactor []int // K*F candidates pulled from the global dictionary
	efSearch        int
}

func defaultBenchParams() benchParams {
	return benchParams{
		dims:            256,
		clusters:        40,
		sharedVectors:   400,
		projects:        16,
		ownPerProject:   120,
		sharedPerProj:   120,
		queries:         200,
		topK:            10,
		overFetchFactor: []int{1, 2, 4, 8, 16},
		efSearch:        100,
	}
}

func TestDedupRecallBenchmark(t *testing.T) {
	if os.Getenv("SEMIDX_BENCH_DEDUP") == "" {
		t.Skip("set SEMIDX_BENCH_DEDUP=1 to run the ADR-7 Postgres recall benchmark")
	}
	s := newTestStore(t)
	ctx := context.Background()
	p := defaultBenchParams()
	rng := newDetRNG(0x5EE5)

	// ---- generate clustered unit vectors -------------------------------
	centers := make([][]float32, p.clusters)
	for i := range centers {
		centers[i] = normalizeVec(randVec(rng, p.dims))
	}
	makeVec := func() []float32 {
		c := centers[rng.intn(p.clusters)]
		v := make([]float32, p.dims)
		for i := range v {
			v[i] = c[i] + 0.35*float32(rng.normal())
		}
		return normalizeVec(v)
	}

	// shared dictionary pool (cross-project duplicated content)
	shared := make([][]float32, p.sharedVectors)
	for i := range shared {
		shared[i] = makeVec()
	}

	type proj struct {
		id int
	}
	projects := make([]proj, p.projects)
	var rows []benchRow
	dictVecByHash := map[string][]float32{}

	for pi := range projects {
		projects[pi].id = pi + 1
		// own, unique vectors
		for j := 0; j < p.ownPerProject; j++ {
			v := makeVec()
			h := fmt.Sprintf("own-%d-%d", pi, j)
			rows = append(rows, benchRow{projects[pi].id, h, v})
			dictVecByHash[h] = v
		}
		// shared vectors: sample distinct indices from the shared pool
		for _, idx := range rng.sample(p.sharedVectors, p.sharedPerProj) {
			v := shared[idx]
			h := fmt.Sprintf("shared-%d", idx)
			rows = append(rows, benchRow{projects[pi].id, h, v})
			dictVecByHash[h] = v
		}
	}

	t.Logf("dims=%d projects=%d baseline_rows=%d unique_dict_vectors=%d (dedup ratio %.2fx)",
		p.dims, p.projects, len(rows), len(dictVecByHash),
		float64(len(rows))/float64(len(dictVecByHash)))

	// ---- create schemas ------------------------------------------------
	mustExec(t, ctx, s, `DROP TABLE IF EXISTS bench_chunks, bench_ref, bench_dict`)
	mustExec(t, ctx, s, fmt.Sprintf(`
		CREATE TABLE bench_chunks (
			id SERIAL PRIMARY KEY,
			project_id INT NOT NULL,
			emb_hash TEXT NOT NULL,
			embedding vector(%d) NOT NULL
		)`, p.dims))
	mustExec(t, ctx, s, fmt.Sprintf(`
		CREATE TABLE bench_dict (
			emb_hash TEXT PRIMARY KEY,
			embedding vector(%d) NOT NULL
		)`, p.dims))
	mustExec(t, ctx, s, `
		CREATE TABLE bench_ref (
			project_id INT NOT NULL,
			emb_hash TEXT NOT NULL,
			PRIMARY KEY (project_id, emb_hash)
		)`)
	defer func() {
		mustExec(t, context.Background(), s, `DROP TABLE IF EXISTS bench_chunks, bench_ref, bench_dict`)
	}()

	// ---- load data -----------------------------------------------------
	for _, r := range rows {
		mustExec(t, ctx, s, `INSERT INTO bench_chunks (project_id, emb_hash, embedding) VALUES ($1,$2,$3)`,
			r.projectID, r.hash, pgvector.NewVector(r.vec))
		mustExec(t, ctx, s, `INSERT INTO bench_ref (project_id, emb_hash) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
			r.projectID, r.hash)
	}
	for h, v := range dictVecByHash {
		mustExec(t, ctx, s, `INSERT INTO bench_dict (emb_hash, embedding) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
			h, pgvector.NewVector(v))
	}

	// ---- indexes -------------------------------------------------------
	mustExec(t, ctx, s, `CREATE INDEX bench_chunks_proj ON bench_chunks(project_id)`)
	mustExec(t, ctx, s, `CREATE INDEX bench_ref_proj ON bench_ref(project_id)`)
	mustExec(t, ctx, s, `CREATE INDEX bench_chunks_hnsw ON bench_chunks USING hnsw (embedding vector_cosine_ops)`)
	mustExec(t, ctx, s, `CREATE INDEX bench_dict_hnsw ON bench_dict USING hnsw (embedding vector_cosine_ops)`)

	// ---- queries -------------------------------------------------------
	// Each query targets one project; ground truth is the exact cosine top-K
	// within that project computed in Go.
	type q struct {
		vec       []float32
		projectID int
		truth     map[string]bool
	}
	queries := make([]q, p.queries)
	for i := range queries {
		pi := rng.intn(p.projects)
		qv := makeVec()
		queries[i] = q{vec: qv, projectID: projects[pi].id, truth: exactTopKHashes(rows, projects[pi].id, qv, p.topK)}
	}

	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, fmt.Sprintf("SET hnsw.ef_search = %d", p.efSearch)); err != nil {
		t.Fatalf("set ef_search: %v", err)
	}

	// baseline: ANN filtered by project_id
	{
		var recalls []float64
		var lat []time.Duration
		for _, qq := range queries {
			start := time.Now()
			got := queryHashes(t, ctx, conn, `
				SELECT emb_hash FROM bench_chunks
				WHERE project_id = $2
				ORDER BY embedding <=> $1 LIMIT $3`,
				pgvector.NewVector(qq.vec), qq.projectID, p.topK)
			lat = append(lat, time.Since(start))
			recalls = append(recalls, recallOf(got, qq.truth))
		}
		t.Logf("BASELINE (inline vector, ANN + project filter): recall@%d=%.3f  p50=%.2fms  p95=%.2fms",
			p.topK, mean(recalls), ms(pct(lat, 50)), ms(pct(lat, 95)))
	}

	// dedup (b): global ANN over-fetch K*F -> join project refs -> re-rank
	for _, f := range p.overFetchFactor {
		var recalls []float64
		var lat []time.Duration
		limit := p.topK * f
		for _, qq := range queries {
			start := time.Now()
			got := queryHashes(t, ctx, conn, `
				SELECT d.emb_hash FROM (
					SELECT emb_hash, embedding FROM bench_dict
					ORDER BY embedding <=> $1 LIMIT $3
				) d
				JOIN bench_ref r ON r.emb_hash = d.emb_hash AND r.project_id = $2
				ORDER BY d.embedding <=> $1 LIMIT $4`,
				pgvector.NewVector(qq.vec), qq.projectID, limit, p.topK)
			lat = append(lat, time.Since(start))
			recalls = append(recalls, recallOf(got, qq.truth))
		}
		t.Logf("DEDUP over-fetch x%-2d (LIMIT %-3d): recall@%d=%.3f  p50=%.2fms  p95=%.2fms",
			f, limit, p.topK, mean(recalls), ms(pct(lat, 50)), ms(pct(lat, 95)))
	}

	// dedup, project-first: filter bench_ref by project, join to the dictionary
	// for vectors, exact-rank. This is exact (recall 1.0) BUT cannot use the
	// dictionary HNSW — it scans the project's vectors, so latency scales with
	// project size (fine here at ~240/project; a seq-scan trap for large ones).
	{
		var recalls []float64
		var lat []time.Duration
		for _, qq := range queries {
			start := time.Now()
			got := queryHashes(t, ctx, conn, `
				SELECT r.emb_hash FROM bench_ref r
				JOIN bench_dict d ON d.emb_hash = r.emb_hash
				WHERE r.project_id = $2
				ORDER BY d.embedding <=> $1 LIMIT $3`,
				pgvector.NewVector(qq.vec), qq.projectID, p.topK)
			lat = append(lat, time.Since(start))
			recalls = append(recalls, recallOf(got, qq.truth))
		}
		t.Logf("DEDUP project-first (exact rank, NO dict ANN): recall@%d=%.3f  p50=%.2fms  p95=%.2fms",
			p.topK, mean(recalls), ms(pct(lat, 50)), ms(pct(lat, 95)))
	}

	// Document the query plans so the ADR write-up can cite why baseline uses an
	// efficient per-project path the dedup schema cannot reproduce.
	logPlan(t, ctx, conn, "BASELINE plan", `
		EXPLAIN (ANALYZE, BUFFERS, SUMMARY OFF)
		SELECT emb_hash FROM bench_chunks WHERE project_id = $2
		ORDER BY embedding <=> $1 LIMIT $3`,
		pgvector.NewVector(queries[0].vec), queries[0].projectID, p.topK)
	logPlan(t, ctx, conn, "DEDUP project-first plan", `
		EXPLAIN (ANALYZE, BUFFERS, SUMMARY OFF)
		SELECT r.emb_hash FROM bench_ref r JOIN bench_dict d ON d.emb_hash = r.emb_hash
		WHERE r.project_id = $2 ORDER BY d.embedding <=> $1 LIMIT $3`,
		pgvector.NewVector(queries[0].vec), queries[0].projectID, p.topK)
}

// logPlan runs an EXPLAIN and logs each plan line (indented) for the record.
func logPlan(t *testing.T, ctx context.Context, conn *pgxpool.Conn, label, sql string, args ...any) {
	t.Helper()
	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		t.Fatalf("explain %s: %v", label, err)
	}
	defer rows.Close()
	t.Logf("--- %s ---", label)
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		t.Logf("    %s", line)
	}
}

// ---- exact ground truth ------------------------------------------------

func exactTopKHashes(rows []benchRow, projectID int, q []float32, k int) map[string]bool {
	type sc struct {
		hash string
		dist float64
	}
	var scored []sc
	seen := map[string]bool{}
	for _, r := range rows {
		if r.projectID != projectID || seen[r.hash] {
			continue
		}
		seen[r.hash] = true
		scored = append(scored, sc{r.hash, cosineDist(q, r.vec)})
	}
	sort.Slice(scored, func(i, j int) bool { return scored[i].dist < scored[j].dist })
	out := map[string]bool{}
	for i := 0; i < k && i < len(scored); i++ {
		out[scored[i].hash] = true
	}
	return out
}

func recallOf(got []string, truth map[string]bool) float64 {
	if len(truth) == 0 {
		return 1
	}
	hit := 0
	for _, h := range got {
		if truth[h] {
			hit++
		}
	}
	return float64(hit) / float64(len(truth))
}

// ---- tiny helpers ------------------------------------------------------

func mustExec(t *testing.T, ctx context.Context, s *PgStore, sql string, args ...any) {
	t.Helper()
	if _, err := s.pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func queryHashes(t *testing.T, ctx context.Context, conn *pgxpool.Conn, sql string, args ...any) []string {
	t.Helper()
	rows, err := conn.Query(ctx, sql, args...)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}

func cosineDist(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 1
	}
	return 1 - dot/(math.Sqrt(na)*math.Sqrt(nb))
}

func normalizeVec(v []float32) []float32 {
	var n float64
	for _, x := range v {
		n += float64(x) * float64(x)
	}
	n = math.Sqrt(n)
	if n == 0 {
		return v
	}
	for i := range v {
		v[i] = float32(float64(v[i]) / n)
	}
	return v
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var s float64
	for _, x := range xs {
		s += x
	}
	return s / float64(len(xs))
}

func pct(ds []time.Duration, p int) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	cp := append([]time.Duration(nil), ds...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	idx := (p * (len(cp) - 1)) / 100
	return cp[idx]
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// ---- deterministic RNG (Date/rand globals are banned for reproducibility) ---

type detRNG struct{ state uint64 }

func newDetRNG(seed uint64) *detRNG { return &detRNG{state: seed | 1} }

func (r *detRNG) next() uint64 {
	// xorshift64*
	r.state ^= r.state >> 12
	r.state ^= r.state << 25
	r.state ^= r.state >> 27
	return r.state * 2685821657736338717
}

func (r *detRNG) float() float64 { return float64(r.next()>>11) / float64(1<<53) }

func (r *detRNG) intn(n int) int { return int(r.next() % uint64(n)) }

// normal returns a standard-normal sample via Box-Muller.
func (r *detRNG) normal() float64 {
	u1 := r.float()
	if u1 < 1e-12 {
		u1 = 1e-12
	}
	u2 := r.float()
	return math.Sqrt(-2*math.Log(u1)) * math.Cos(2*math.Pi*u2)
}

func randVec(r *detRNG, dims int) []float32 {
	v := make([]float32, dims)
	for i := range v {
		v[i] = float32(r.normal())
	}
	return v
}

// sample returns k distinct indices in [0,n) (k<=n).
func (r *detRNG) sample(n, k int) []int {
	if k > n {
		k = n
	}
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	for i := 0; i < k; i++ {
		j := i + r.intn(n-i)
		perm[i], perm[j] = perm[j], perm[i]
	}
	return perm[:k]
}
