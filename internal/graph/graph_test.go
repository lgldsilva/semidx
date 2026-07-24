package graph

import (
	"context"
	"strings"
	"testing"
)

func TestNormalizeAndPackageDir(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, norm, pkg string
	}{
		{"internal/store/store.go", "internal/store/store.go", "internal/store/"},
		{`internal\store\store.go`, "internal/store/store.go", "internal/store/"},
		{"./cmd/main.go", "cmd/main.go", "cmd/"},
		{"internal/worker/", "internal/worker/", "internal/worker/"},
		{"internal/worker", "internal/worker", "internal/worker/"},
		{"", "", ""},
		{".", "", ""},
	}
	for _, tc := range cases {
		if got := Normalize(tc.in); got != tc.norm {
			t.Errorf("Normalize(%q)=%q want %q", tc.in, got, tc.norm)
		}
		if got := PackageDir(tc.in); got != tc.pkg {
			t.Errorf("PackageDir(%q)=%q want %q", tc.in, got, tc.pkg)
		}
	}
}

func sampleIndex() *Index {
	// main.go imports worker + util packages; worker imports util.
	neighbors := map[string][]string{
		"main.go":                {"internal/worker/", "pkg/util/"},
		"internal/worker/run.go": {"pkg/util/"},
		"cmd/semidx/main.go":     {"internal/store/"},
	}
	files := []string{
		"main.go",
		"internal/worker/run.go",
		"internal/worker/helper.go", // no outbound imports — only via files list
		"pkg/util/help.go",
		"internal/store/store.go",
		"cmd/semidx/main.go",
	}
	return Build(neighbors, files)
}

func TestShortestPathDirectedFileToFile(t *testing.T) {
	t.Parallel()
	idx := sampleIndex()
	res := idx.ShortestPath(context.Background(), "main.go", "pkg/util/help.go", Budget{}, false)
	if !res.Found || !res.Directed {
		t.Fatalf("want directed found, got %+v", res)
	}
	if res.Hops[0] != "main.go" || res.Hops[len(res.Hops)-1] != "pkg/util/help.go" {
		t.Fatalf("hops=%v", res.Hops)
	}
	// Expect: main.go → pkg/util/ → pkg/util/help.go
	if res.Length < 2 {
		t.Fatalf("length=%d hops=%v edges=%+v", res.Length, res.Hops, res.Edges)
	}
	for _, e := range res.Edges {
		if e.Reverse {
			t.Errorf("directed path must not mark reverse: %+v", e)
		}
	}
}

func TestShortestPathViaWorker(t *testing.T) {
	t.Parallel()
	idx := sampleIndex()
	res := idx.ShortestPath(context.Background(), "main.go", "internal/worker/helper.go", Budget{}, false)
	if !res.Found {
		t.Fatalf("not found: %+v", res)
	}
	joined := strings.Join(res.Hops, " → ")
	if !strings.Contains(joined, "internal/worker/") {
		t.Fatalf("expected package hop in %s", joined)
	}
	if res.Hops[len(res.Hops)-1] != "internal/worker/helper.go" {
		t.Fatalf("end=%v", res.Hops)
	}
}

func TestShortestPathNotFoundDirected(t *testing.T) {
	t.Parallel()
	idx := sampleIndex()
	// store is imported by cmd, not by main.go — no directed path main→store
	res := idx.ShortestPath(context.Background(), "main.go", "internal/store/store.go", Budget{}, false)
	if res.Found {
		t.Fatalf("unexpected path: %+v", res)
	}
}

func TestShortestPathUndirectedFallback(t *testing.T) {
	t.Parallel()
	idx := sampleIndex()
	// util does not import main; undirected can walk reverse imports
	res := idx.ShortestPath(context.Background(), "pkg/util/help.go", "main.go", Budget{}, true)
	if !res.Found {
		t.Fatalf("want undirected path, got %+v", res)
	}
	if res.Directed {
		t.Fatal("expected Directed=false for fallback")
	}
	var sawReverse bool
	for _, e := range res.Edges {
		if e.Reverse {
			sawReverse = true
		}
	}
	if !sawReverse {
		t.Fatalf("undirected path should include reverse edges: %+v", res.Edges)
	}
}

func TestShortestPathSameNode(t *testing.T) {
	t.Parallel()
	idx := sampleIndex()
	res := idx.ShortestPath(context.Background(), "main.go", "main.go", Budget{}, false)
	if !res.Found || res.Length != 0 || len(res.Hops) != 1 {
		t.Fatalf("%+v", res)
	}
}

func TestShortestPathMaxDepthTruncates(t *testing.T) {
	t.Parallel()
	idx := sampleIndex()
	res := idx.ShortestPath(context.Background(), "main.go", "pkg/util/help.go", Budget{MaxDepth: 1}, false)
	// depth 1: main → pkg/util/ only; cannot reach file without second hop
	if res.Found {
		t.Fatalf("expected not found at depth 1: %+v", res)
	}
}

func TestShortestPathEmptyEndpoints(t *testing.T) {
	t.Parallel()
	idx := sampleIndex()
	if idx.ShortestPath(context.Background(), "", "main.go", Budget{}, false).Found {
		t.Fatal("empty from")
	}
	if idx.ShortestPath(context.Background(), "main.go", "", Budget{}, false).Found {
		t.Fatal("empty to")
	}
}

func TestSubgraphEgo(t *testing.T) {
	t.Parallel()
	idx := sampleIndex()
	sg := idx.Subgraph(context.Background(), "main.go", Budget{MaxDepth: 2, MaxEdgesOut: 100})
	if len(sg.Nodes) < 2 || len(sg.Edges) < 1 {
		t.Fatalf("nodes=%d edges=%d", len(sg.Nodes), len(sg.Edges))
	}
	var seedOK bool
	for _, n := range sg.Nodes {
		if n.ID == "main.go" && n.Seed {
			seedOK = true
		}
	}
	if !seedOK {
		t.Fatalf("seed not marked: %+v", sg.Nodes)
	}
}

func TestSubgraphEdgeCapTruncates(t *testing.T) {
	t.Parallel()
	idx := sampleIndex()
	sg := idx.Subgraph(context.Background(), "main.go", Budget{MaxDepth: 3, MaxEdgesOut: 1})
	if len(sg.Edges) > 1 {
		t.Fatalf("cap violated: %d", len(sg.Edges))
	}
	if len(sg.Edges) == 1 && !sg.Truncated {
		t.Fatal("expected truncated when more edges exist")
	}
}

func TestSubgraphEmpty(t *testing.T) {
	t.Parallel()
	idx := Build(nil, nil)
	sg := idx.Subgraph(context.Background(), "", Budget{})
	if len(sg.Nodes) != 0 || len(sg.Edges) != 0 {
		t.Fatalf("%+v", sg)
	}
}

func TestBuildDedupAndSort(t *testing.T) {
	t.Parallel()
	idx := Build(map[string][]string{
		"a.go": {"pkg/x", "pkg/x/", "pkg/x"},
	}, []string{"a.go", "pkg/x/f.go"})
	targets := idx.out["a.go"]
	if len(targets) != 1 || targets[0] != "pkg/x/" {
		t.Fatalf("targets=%v", targets)
	}
	files := idx.filesInPkg["pkg/x/"]
	if len(files) != 1 || files[0] != "pkg/x/f.go" {
		t.Fatalf("filesInPkg=%v", files)
	}
}

func TestMaxVisitNodes(t *testing.T) {
	t.Parallel()
	targets := make([]string, 0, 50)
	files := []string{"hub.go"}
	for i := 0; i < 50; i++ {
		pkg := "p" + itoa(i) + "/"
		targets = append(targets, pkg)
		files = append(files, pkg+"a.go")
	}
	neighbors := map[string][]string{"hub.go": targets}
	idx := Build(neighbors, files)
	res := idx.ShortestPath(context.Background(), "hub.go", "p49/a.go", Budget{MaxVisitNodes: 3, MaxDepth: 8}, false)
	if res.Found {
		t.Fatal("should not find under tiny visit budget")
	}
	if !res.Truncated {
		t.Fatal("expected truncated")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestDeterministicHopOrder(t *testing.T) {
	t.Parallel()
	idx := sampleIndex()
	a := idx.ShortestPath(context.Background(), "main.go", "pkg/util/help.go", Budget{}, false)
	b := idx.ShortestPath(context.Background(), "main.go", "pkg/util/help.go", Budget{}, false)
	if strings.Join(a.Hops, "|") != strings.Join(b.Hops, "|") {
		t.Fatalf("non-deterministic: %v vs %v", a.Hops, b.Hops)
	}
}

func TestShortestPathCycleDoesNotLoop(t *testing.T) {
	t.Parallel()
	// a ↔ b via packages: a.go → pkg/b/ → b.go → pkg/a/ → a.go
	idx := Build(map[string][]string{
		"pkg/a/a.go": {"pkg/b/"},
		"pkg/b/b.go": {"pkg/a/"},
	}, []string{"pkg/a/a.go", "pkg/b/b.go"})
	res := idx.ShortestPath(context.Background(), "pkg/a/a.go", "pkg/b/b.go", Budget{}, false)
	if !res.Found {
		t.Fatalf("expected path through cycle graph: %+v", res)
	}
	sg := idx.Subgraph(context.Background(), "pkg/a/a.go", Budget{MaxDepth: 4, MaxEdgesOut: 50})
	if len(sg.Nodes) == 0 {
		t.Fatal("subgraph empty")
	}
	// BFS with seen set must terminate; edge count is finite.
	if len(sg.Edges) > 20 {
		t.Fatalf("unexpected explosion on cycle: edges=%d", len(sg.Edges))
	}
}

func TestShortestPathContextCancel(t *testing.T) {
	t.Parallel()
	targets := make([]string, 0, 80)
	files := []string{"hub.go"}
	for i := 0; i < 80; i++ {
		pkg := "p" + itoa(i) + "/"
		targets = append(targets, pkg)
		files = append(files, pkg+"a.go")
	}
	idx := Build(map[string][]string{"hub.go": targets}, files)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := idx.ShortestPath(ctx, "hub.go", "p79/a.go", Budget{MaxDepth: 8}, false)
	if res.Found {
		t.Fatal("cancelled walk must not report found")
	}
	if !res.Truncated {
		t.Fatal("cancelled walk must set truncated")
	}
	sg := idx.Subgraph(ctx, "hub.go", Budget{MaxDepth: 3, MaxEdgesOut: 500})
	if !sg.Truncated {
		t.Fatal("cancelled subgraph must set truncated")
	}
}
