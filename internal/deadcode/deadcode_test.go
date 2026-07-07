package deadcode

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lgldsilva/semidx/internal/analyzer"
	"github.com/lgldsilva/semidx/internal/store"
)

// fakeStore implements store.IndexStore minimally for deadcode tests.
type fakeStore struct {
	store.IndexStore
	hashes  map[string]string
	graph   map[string][]string
	project *store.Project
}

func (f *fakeStore) ListFileHashes(_ context.Context, _ int) (map[string]string, error) {
	return f.hashes, nil
}

func (f *fakeStore) FetchGraphNeighbors(_ context.Context, _ int) (map[string][]string, error) {
	return f.graph, nil
}

func (f *fakeStore) GetProjectByID(_ context.Context, _ int) (*store.Project, error) {
	return f.project, nil
}

func (f *fakeStore) GetProjectByIdentity(_ context.Context, _ string) (*store.Project, error) {
	return f.project, nil
}

func TestClassify(t *testing.T) {
	tests := []struct {
		name         string
		symName      string
		kind         string
		hasImporters bool
		want         *Finding
	}{
		{
			name:         "unexported_no_importers",
			symName:      "parseV1",
			kind:         "func",
			hasImporters: false,
			want: &Finding{
				Symbol:     "parseV1",
				Kind:       "func",
				File:       "internal/legacy/old.go",
				StartLine:  42,
				Confidence: "confirmed",
			},
		},
		{
			name:         "exported_no_importers",
			symName:      "NewClient",
			kind:         "func",
			hasImporters: false,
			want: &Finding{
				Symbol:     "NewClient",
				Kind:       "func",
				File:       "pkg/public/exp.go",
				StartLine:  10,
				Confidence: "public-api",
			},
		},
		{
			name:         "exported_with_importers",
			symName:      "ValidateToken",
			kind:         "func",
			hasImporters: true,
			want:         nil, // not dead
		},
		{
			name:         "unexported_with_importers",
			symName:      "internalHelper",
			kind:         "func",
			hasImporters: true,
			want:         nil, // not dead
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sym := analyzer.Symbol{Name: tt.symName, Kind: tt.kind, StartLine: 42}
			if tt.want != nil {
				tt.want.File = ""
				tt.want.StartLine = 0
			}
			got := classify(sym, "", tt.hasImporters, len(tt.symName) > 0 && sym.Name[0] >= 'A' && sym.Name[0] <= 'Z')
			if tt.want == nil {
				if got != nil {
					t.Errorf("classify(%q, hasImporters=%v) = %+v, want nil", tt.symName, tt.hasImporters, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("classify(%q, hasImporters=%v) = nil, want non-nil", tt.symName, tt.hasImporters)
			}
			if got.Symbol != tt.symName {
				t.Errorf("Symbol = %q, want %q", got.Symbol, tt.symName)
			}
			if got.Confidence != tt.want.Confidence {
				t.Errorf("Confidence = %q, want %q", got.Confidence, tt.want.Confidence)
			}
		})
	}
}

func TestAggregateStats(t *testing.T) {
	findings := []Finding{
		{Symbol: "a", Confidence: "confirmed"},
		{Symbol: "b", Confidence: "confirmed"},
		{Symbol: "c", Confidence: "public-api"},
	}
	stats := AggregateStats(findings)
	if stats.TotalFindings != 3 {
		t.Errorf("TotalFindings = %d, want 3", stats.TotalFindings)
	}
	if stats.Confirmed != 2 {
		t.Errorf("Confirmed = %d, want 2", stats.Confirmed)
	}
	if stats.PublicAPI != 1 {
		t.Errorf("PublicAPI = %d, want 1", stats.PublicAPI)
	}
	if stats.TotalLines != 3 {
		t.Errorf("TotalLines = %d, want 3", stats.TotalLines)
	}
}

func TestAnalyze_EmptyProject(t *testing.T) {
	ctx := context.Background()
	fs := &fakeStore{
		hashes: map[string]string{},
		graph:  map[string][]string{},
	}
	tmpDir := t.TempDir()

	findings, err := Analyze(ctx, 1, fs, tmpDir)
	if err != nil {
		t.Fatalf("Analyze empty project: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("expected 0 findings, got %d", len(findings))
	}
}

func TestAnalyze_WithDeadCode(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	// Create a Go file with both exported and unexported symbols.
	srcDir := filepath.Join(tmpDir, "internal", "legacy")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	srcContent := []byte(`package legacy

// parseV1 is an old parser no longer used.
func parseV1(data string) string {
	return data
}

// FormatV1 formats data in the old format.
func FormatV1(data string) string {
	return data
}

// NewClient creates a new client (exported, but package not imported).
func NewClient() *Client {
	return &Client{}
}

type Client struct {
	Addr string
}
`)
	if err := os.WriteFile(filepath.Join(srcDir, "old.go"), srcContent, 0o644); err != nil {
		t.Fatal(err)
	}

	// Create another file that imports from this package, to test realistic
	// scenarios where some code IS used.
	usedDir := filepath.Join(tmpDir, "internal", "used")
	if err := os.MkdirAll(usedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	usedContent := []byte(`package used

func usedFunc() string {
	return "used"
}
`)
	if err := os.WriteFile(filepath.Join(usedDir, "used.go"), usedContent, 0o644); err != nil {
		t.Fatal(err)
	}

	fs := &fakeStore{
		hashes: map[string]string{
			"internal/legacy/old.go": "hash1",
			"internal/used/used.go":  "hash2",
		},
		graph: map[string][]string{
			// used.go imports the legacy package
			"internal/used/used.go": {"internal/legacy/"},
		},
	}

	findings, err := Analyze(ctx, 1, fs, tmpDir)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	// The used package's directory is imported (by the test setup), so its
	// symbols should NOT be dead.
	// The legacy package's directory "internal/legacy/" is imported by
	// "internal/used/used.go", so its symbols also have importers.
	// We set up the graph where "internal/used/used.go" imports "internal/legacy/",
	// but the question is: does anything import "internal/used/"?
	// Nothing imports "internal/used/" - so its symbols should be dead.
	//
	// Actually, let's trace:
	// - internal/used/used.go imports internal/legacy/
	// - So internal/legacy/ has importers (internal/used/used.go)
	// - internal/used/ has NO importers
	// - So used.go symbols should be confirmed dead, old.go symbols should be alive

	findingsMap := make(map[string]*Finding)
	for i := range findings {
		findingsMap[findings[i].Symbol] = &findings[i]
	}

	// Check that internal/used/used.go symbols are reported dead.
	for _, name := range []string{"usedFunc"} {
		f, ok := findingsMap[name]
		if !ok {
			t.Errorf("expected finding for %q in unused package", name)
			continue
		}
		if f.Confidence != "confirmed" {
			t.Errorf("%s confidence = %q, want 'confirmed'", name, f.Confidence)
		}
	}

	// Check that internal/legacy/old.go symbols are NOT dead (package IS imported).
	for _, name := range []string{"parseV1", "FormatV1", "NewClient"} {
		if _, ok := findingsMap[name]; ok {
			t.Errorf("unexpected finding for %q in imported package", name)
		}
	}
}

func TestAnalyze_NoImportersAtAll(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	srcDir := filepath.Join(tmpDir, "internal", "orphan")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte(`package orphan

func helper() string { return "" }

func PublicAPI() string { return "" }
`)
	if err := os.WriteFile(filepath.Join(srcDir, "orphan.go"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	fs := &fakeStore{
		hashes: map[string]string{
			"internal/orphan/orphan.go": "hash1",
		},
		graph: map[string][]string{}, // no edges at all
	}

	findings, err := Analyze(ctx, 1, fs, tmpDir)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}

	// Both symbols should be found.
	findingMap := make(map[string]string)
	for _, f := range findings {
		findingMap[f.Symbol] = f.Confidence
	}

	if findingMap["helper"] != "confirmed" {
		t.Errorf("helper confidence = %q, want 'confirmed'", findingMap["helper"])
	}
	if findingMap["PublicAPI"] != "public-api" {
		t.Errorf("PublicAPI confidence = %q, want 'public-api'", findingMap["PublicAPI"])
	}
}
