package pgcheck

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestInspectDSNUnreachable(t *testing.T) {
	// Port 1 is reserved and never listening, so this exercises the connect
	// failure without waiting for a DNS or firewall timeout.
	_, err := InspectDSN(context.Background(), "postgres://u:p@127.0.0.1:1/db?sslmode=disable")
	if err == nil {
		t.Fatal("InspectDSN against a closed port should fail")
	}
}

func TestInspectDSNMalformed(t *testing.T) {
	if _, err := InspectDSN(context.Background(), "not-a-dsn://%%"); err == nil {
		t.Fatal("InspectDSN with a malformed DSN should fail")
	}
}

// TestInspectDSNAgainstPgvector probes a real pgvector image, which is what the
// probe must agree with: the extensions are absent until created, and the
// halfvec operator class exists on a current pgvector.
func TestInspectDSNAgainstPgvector(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	ctx := context.Background()

	ctr, err := postgres.Run(ctx, "pgvector/pgvector:pg16",
		postgres.WithDatabase("semidx"),
		postgres.WithUsername("semidx"),
		postgres.WithPassword("semidx"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(180*time.Second),
			wait.ForListeningPort("5432/tcp"),
		),
	)
	if err != nil {
		t.Fatalf("start pgvector container: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	// Fresh database: pgvector is available on the image but not created yet.
	before, err := InspectDSN(ctx, dsn)
	if err != nil {
		t.Fatalf("InspectDSN: %v", err)
	}
	if before.ServerVersion == "" {
		t.Error("server version not reported")
	}
	if before.Vector.Installed {
		t.Error("vector reported as installed on a fresh database")
	}
	if !before.Vector.Available {
		t.Error("vector should be available on the pgvector image")
	}
	if !before.CanCreate {
		t.Error("the image's own role should be allowed to create extensions")
	}
	if len(before.Findings()) == 0 {
		t.Error("a database without the extensions should produce findings")
	}

	code, _, err := ctr.Exec(ctx, []string{"psql", "-U", "semidx", "-d", "semidx", "-c",
		"CREATE EXTENSION vector; CREATE EXTENSION pg_trgm;"})
	if err != nil || code != 0 {
		t.Fatalf("create extensions: exit %d, err %v", code, err)
	}

	after, err := InspectDSN(ctx, dsn)
	if err != nil {
		t.Fatalf("InspectDSN after create: %v", err)
	}
	if !after.Vector.Installed || !after.Trgm.Installed {
		t.Fatalf("extensions still missing: %+v", after)
	}
	if !after.HalfvecOpclass {
		t.Error("a current pgvector must expose halfvec_cosine_ops")
	}
	if got := after.Findings(); len(got) != 0 {
		t.Errorf("a ready database still reports findings: %v", got)
	}
	if !strings.HasPrefix(after.ServerVersion, "16") {
		t.Errorf("server version = %q, want the pg16 image", after.ServerVersion)
	}
}
