package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateDatasetRejectsUnsafeAndDuplicateEntries(t *testing.T) {
	base := Dataset{Version: 2, Queries: []Query{{
		ID: "q1", Query: "find auth", Relevant: []Relevance{{File: "auth.go", Grade: 3}},
	}}}
	for name, mutate := range map[string]func(*Dataset){
		"duplicate id":  func(ds *Dataset) { ds.Queries = append(ds.Queries, ds.Queries[0]) },
		"absolute path": func(ds *Dataset) { ds.Queries[0].Relevant[0].File = "/tmp/auth.go" },
		"parent path":   func(ds *Dataset) { ds.Queries[0].Relevant[0].File = "../auth.go" },
		"bad grade":     func(ds *Dataset) { ds.Queries[0].Relevant[0].Grade = 4 },
		"empty query":   func(ds *Dataset) { ds.Queries[0].Query = " " },
		"empty id":      func(ds *Dataset) { ds.Queries[0].ID = " " },
		"no relevant":   func(ds *Dataset) { ds.Queries[0].Relevant = nil },
		"duplicate file": func(ds *Dataset) {
			ds.Queries[0].Relevant = append(ds.Queries[0].Relevant, ds.Queries[0].Relevant[0])
		},
		"bad line range": func(ds *Dataset) {
			ds.Queries[0].Relevant[0].LineStart, ds.Queries[0].Relevant[0].LineEnd = 10, 2
		},
		"negative parent path": func(ds *Dataset) { ds.Queries[0].NegativeFiles = []string{"../bad.go"} },
		"relevant is negative": func(ds *Dataset) { ds.Queries[0].NegativeFiles = []string{"auth.go"} },
	} {
		t.Run(name, func(t *testing.T) {
			ds := cloneDataset(base)
			mutate(&ds)
			if err := Validate(ds); err == nil {
				t.Fatalf("Validate(%s) unexpectedly succeeded", name)
			}
		})
	}
	if err := Validate(Dataset{Version: 99, Queries: base.Queries}); err == nil {
		t.Fatal("future dataset version should fail")
	}
	if err := Validate(Dataset{Version: 2}); err == nil {
		t.Fatal("empty dataset should fail")
	}
}

func TestLoadDatasetSupportsV2AndLegacy(t *testing.T) {
	dir := t.TempDir()
	v2 := filepath.Join(dir, "v2.json")
	if err := os.WriteFile(v2, []byte(`{"version":2,"queries":[{"id":"q1","query":"auth","relevant":[{"file":"auth.go","grade":3}]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ds, err := LoadDataset(v2)
	if err != nil || ds.Version != 2 || ds.Queries[0].Relevant[0].Grade != 3 {
		t.Fatalf("LoadDataset v2 = %+v, err %v", ds, err)
	}
	legacy := filepath.Join(dir, "legacy.json")
	if err := os.WriteFile(legacy, []byte(`[{"query":"auth","relevant_files":["auth.go"]}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	ds, err = LoadDataset(legacy)
	if err != nil || ds.Version != 1 || ds.Queries[0].Relevant[0].Grade != 1 {
		t.Fatalf("LoadDataset legacy = %+v, err %v", ds, err)
	}
}

func TestLoadDatasetRejectsUnreadableAndMalformedInput(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadDataset(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("missing dataset should fail")
	}
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte(`{"not":"a dataset"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDataset(path); err == nil {
		t.Fatal("malformed dataset shape should fail")
	}
}

func TestDatasetSHA256IsStable(t *testing.T) {
	ds := Dataset{Version: 2, Queries: []Query{{ID: "q", Query: "auth", Relevant: []Relevance{{File: "auth.go", Grade: 3}}}}}
	a, err := DatasetSHA256(ds)
	if err != nil {
		t.Fatal(err)
	}
	b, err := DatasetSHA256(ds)
	if err != nil || a != b || len(a) != 64 {
		t.Fatalf("hashes = %q, %q, err %v", a, b, err)
	}
	if strings.TrimSpace(a) == "" {
		t.Fatal("empty hash")
	}
}

func TestSmokeDatasetFixtureIsValid(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "eval", "retrieval-smoke.json")
	ds, err := LoadDataset(path)
	if err != nil {
		t.Fatal(err)
	}
	if ds.Version != CurrentDatasetVersion || len(ds.Queries) != 5 {
		t.Fatalf("smoke dataset = version %d, %d queries", ds.Version, len(ds.Queries))
	}
}

func TestValidateFilesRejectsMissingCorpusFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "auth.go"), []byte("package auth"), 0o600); err != nil {
		t.Fatal(err)
	}
	ds := Dataset{Version: 2, Queries: []Query{{
		ID: "q", Query: "auth", Relevant: []Relevance{{File: "auth.go", Grade: 3}},
	}}}
	if err := ValidateFiles(ds, root); err != nil {
		t.Fatalf("existing corpus rejected: %v", err)
	}
	ds.Queries[0].NegativeFiles = []string{"missing.go"}
	if err := ValidateFiles(ds, root); err == nil {
		t.Fatal("missing corpus file should fail validation")
	}
}

func TestGoldDatasetHasRequiredDistributionAndRealFiles(t *testing.T) {
	projectRoot := filepath.Join("..", "..")
	ds, err := LoadDataset(filepath.Join(projectRoot, "testdata", "eval", "semidx-retrieval-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	counts := make(map[string]int)
	for _, query := range ds.Queries {
		counts[query.Intent]++
	}
	want := map[string]int{
		"behavior": 15, "symbol": 10, "flow": 10,
		"documentation": 5, "ambiguous": 5, "project-resolution": 5,
	}
	if len(ds.Queries) != 50 {
		t.Fatalf("gold dataset has %d queries, want 50", len(ds.Queries))
	}
	for intent, minimum := range want {
		if counts[intent] < minimum {
			t.Errorf("intent %s has %d queries, want at least %d", intent, counts[intent], minimum)
		}
	}
	if err := ValidateFiles(ds, projectRoot); err != nil {
		t.Fatal(err)
	}
}

func cloneDataset(in Dataset) Dataset {
	out := Dataset{Version: in.Version, Queries: make([]Query, len(in.Queries))}
	copy(out.Queries, in.Queries)
	for i := range out.Queries {
		out.Queries[i].Relevant = append([]Relevance(nil), in.Queries[i].Relevant...)
	}
	return out
}
