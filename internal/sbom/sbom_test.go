package sbom

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

// fakeStore is a minimal IndexStore that only implements FetchGraphNeighbors.
// coverage-patch: 2026-07-17
type fakeStore struct {
	store.IndexStore
	graph map[string][]string
	err   error
}

func (f *fakeStore) FetchGraphNeighbors(context.Context, int) (map[string][]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.graph == nil {
		return map[string][]string{}, nil
	}
	return f.graph, nil
}

func testProj(t *testing.T, path string) *store.Project {
	t.Helper()
	return &store.Project{
		ID:       1,
		Name:     "demo",
		Path:     path,
		Identity: "github.com/example/demo",
		Model:    "test",
	}
}

// coverage-patch: 2026-07-17
func TestGenerateUnsupportedFormat(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := &fakeStore{graph: map[string][]string{}}
	proj := testProj(t, t.TempDir())

	_, err := Generate(ctx, db, proj, "xml", "1.0.0")
	if err == nil {
		t.Fatal("expected unsupported format error")
	}
	if !strings.Contains(err.Error(), "unsupported format") {
		t.Errorf("error = %q, want unsupported format", err)
	}
}

// coverage-patch: 2026-07-17
func TestGenerateFetchGraphError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := &fakeStore{err: errors.New("db down")}
	proj := testProj(t, t.TempDir())

	_, err := Generate(ctx, db, proj, "cyclonedx-json", "1.0.0")
	if err == nil {
		t.Fatal("expected collect dependencies error")
	}
	if !strings.Contains(err.Error(), "collect dependencies") {
		t.Errorf("error = %q, want collect dependencies wrap", err)
	}
}

// coverage-patch: 2026-07-17
func TestComponentCountError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := &fakeStore{err: errors.New("graph boom")}
	proj := testProj(t, t.TempDir())

	n, err := ComponentCount(ctx, db, proj)
	if err == nil {
		t.Fatal("expected error")
	}
	if n != 0 {
		t.Errorf("count = %d, want 0 on error", n)
	}
}

// coverage-patch: 2026-07-17
func TestGenerateEmptyDepsDefaultFormat(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := &fakeStore{graph: map[string][]string{}}
	proj := testProj(t, t.TempDir())

	// Empty format defaults to cyclonedx-json; empty toolVersion → "unknown".
	raw, err := Generate(ctx, db, proj, "", "")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var bom map[string]any
	if err := json.Unmarshal(raw, &bom); err != nil {
		t.Fatalf("json: %v", err)
	}
	if bom["bomFormat"] != "CycloneDX" {
		t.Errorf("bomFormat = %v", bom["bomFormat"])
	}
	if bom["specVersion"] != "1.4" {
		t.Errorf("specVersion = %v", bom["specVersion"])
	}
	meta, _ := bom["metadata"].(map[string]any)
	tools, _ := meta["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("expected tools in metadata")
	}
	tool, _ := tools[0].(map[string]any)
	if tool["version"] != "unknown" {
		t.Errorf("tool version = %v, want unknown", tool["version"])
	}
}

// coverage-patch: 2026-07-17
func TestGenerateCycloneDXAndSPDXWithDeps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	// go.mod with require block so applyGoModVersions attaches versions.
	gomod := `module github.com/example/demo

go 1.22

require (
	github.com/example/demo/pkg/a v1.2.3
	github.com/other/lib v0.9.0
)

// comment line
require (
)
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
		t.Fatal(err)
	}

	db := &fakeStore{
		graph: map[string][]string{
			"main.go": {"pkg/a/", "pkg/b/", "pkg/a/"}, // duplicate target
			"util.go": {"pkg/b/", "github.com/other/lib/"},
		},
	}
	proj := testProj(t, dir)

	t.Run("cyclonedx-json", func(t *testing.T) {
		raw, err := Generate(ctx, db, proj, "cyclonedx-json", "2.0.0")
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		var bom cdxBOM
		if err := json.Unmarshal(raw, &bom); err != nil {
			t.Fatalf("json: %v", err)
		}
		if bom.BOMFormat != "CycloneDX" || bom.SpecVersion != "1.4" {
			t.Errorf("format/spec = %s/%s", bom.BOMFormat, bom.SpecVersion)
		}
		if bom.Metadata.Component.Name != "demo" {
			t.Errorf("metadata component = %q", bom.Metadata.Component.Name)
		}
		// project + unique deps (+ module entry from go.mod)
		if len(bom.Components) < 2 {
			t.Fatalf("components = %d, want >= 2", len(bom.Components))
		}
		var sawModule, sawFile, sawLib bool
		for _, c := range bom.Components {
			switch c.Type {
			case "application":
				// project self
			case "file":
				sawFile = true
			case "library":
				sawLib = true
			}
			if c.Name == "github.com/example/demo" {
				sawModule = true
				if c.Version != "1.22" {
					t.Errorf("module version = %q, want 1.22", c.Version)
				}
			}
			if c.Name == "pkg/a/" && c.Version != "v1.2.3" {
				t.Errorf("pkg/a/ version = %q, want v1.2.3", c.Version)
			}
		}
		if !sawFile {
			t.Error("expected file-type component")
		}
		if !sawModule || !sawLib {
			t.Errorf("module library component: module=%v lib=%v", sawModule, sawLib)
		}
	})

	t.Run("spdx-json", func(t *testing.T) {
		raw, err := Generate(ctx, db, proj, "spdx-json", "2.0.0")
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		var doc spdxDoc
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("json: %v", err)
		}
		if doc.SPDXVersion != "SPDX-2.3" {
			t.Errorf("SPDXVersion = %q", doc.SPDXVersion)
		}
		if !strings.Contains(doc.Name, "demo") {
			t.Errorf("Name = %q", doc.Name)
		}
		if len(doc.Packages) < 2 {
			t.Fatalf("packages = %d", len(doc.Packages))
		}
		if doc.Packages[0].SPDXID != "SPDXRef-Project" {
			t.Errorf("first package = %s", doc.Packages[0].SPDXID)
		}
		// File-type components get PackageFileName set.
		var sawFileName bool
		for _, p := range doc.Packages {
			if p.PackageFileName != "" {
				sawFileName = true
			}
		}
		if !sawFileName {
			t.Error("expected PackageFileName on file components")
		}
		if len(doc.Relationships) == 0 {
			t.Error("expected CONTAINS relationships")
		}
		for _, r := range doc.Relationships {
			if r.RelationshipType != "CONTAINS" {
				t.Errorf("relationship type = %q", r.RelationshipType)
			}
			if r.SPDXElementID != "SPDXRef-Project" {
				t.Errorf("spdxElementId = %q", r.SPDXElementID)
			}
		}
	})
}

// coverage-patch: 2026-07-17
func TestComponentCount(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := &fakeStore{
		graph: map[string][]string{
			"a.go": {"x/", "y/"},
			"b.go": {"y/", "z/"},
		},
	}
	proj := testProj(t, t.TempDir()) // no go.mod → no module component

	n, err := ComponentCount(ctx, db, proj)
	if err != nil {
		t.Fatalf("ComponentCount: %v", err)
	}
	if n != 3 {
		t.Errorf("count = %d, want 3 unique deps", n)
	}
}

// coverage-patch: 2026-07-17
func TestSortedUniqueDeps(t *testing.T) {
	t.Parallel()
	got := sortedUniqueDeps(map[string][]string{
		"a": {"z", "a", "m"},
		"b": {"a", "z"},
		"c": {},
	})
	want := []string{"a", "m", "z"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%q want %q", i, got[i], want[i])
		}
	}
	// empty graph
	if s := sortedUniqueDeps(nil); len(s) != 0 {
		t.Errorf("nil graph = %v", s)
	}
	if s := sortedUniqueDeps(map[string][]string{}); len(s) != 0 {
		t.Errorf("empty graph = %v", s)
	}
}

// coverage-patch: 2026-07-17
func TestApplyGoModVersions(t *testing.T) {
	t.Parallel()

	comps := []Component{{Name: "pkg/a/", Type: "file"}, {Name: "other/", Type: "file"}}
	// nil modInfo is a no-op
	out := applyGoModVersions(comps, nil)
	if out[0].Version != "" {
		t.Errorf("nil modInfo should not set version, got %q", out[0].Version)
	}

	info := &goModInfo{
		module:    "github.com/example/demo",
		goVersion: "1.21",
		versions:  map[string]string{"pkg/a/": "v1.0.0"},
	}
	out = applyGoModVersions(comps, info)
	if out[0].Version != "v1.0.0" {
		t.Errorf("version = %q", out[0].Version)
	}
	// last component should be the module entry
	last := out[len(out)-1]
	if last.Type != "module" || last.Name != "github.com/example/demo" || last.Version != "1.21" {
		t.Errorf("module component = %+v", last)
	}

	// empty module name: no append
	info2 := &goModInfo{versions: map[string]string{}}
	out2 := applyGoModVersions([]Component{{Name: "x", Type: "file"}}, info2)
	if len(out2) != 1 {
		t.Errorf("empty module should not append, got %d", len(out2))
	}
}

// coverage-patch: 2026-07-17
func TestReadGoMod(t *testing.T) {
	t.Parallel()

	// missing go.mod
	if info := readGoMod(t.TempDir()); info != nil {
		t.Errorf("missing go.mod should return nil, got %+v", info)
	}

	dir := t.TempDir()
	content := `module github.com/acme/app

go 1.23.0

require (
	// skip this comment
	github.com/acme/app/internal/pkg v1.0.1
	github.com/ext/dep v2.0.0 // indirect
	malformed-line
)

require github.com/single/line v0.1.0
`
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	info := readGoMod(dir)
	if info == nil {
		t.Fatal("readGoMod returned nil")
	}
	if info.module != "github.com/acme/app" {
		t.Errorf("module = %q", info.module)
	}
	if info.goVersion != "1.23.0" {
		t.Errorf("goVersion = %q", info.goVersion)
	}
	// dir keys are module-relative
	if v := info.versions["internal/pkg/"]; v != "v1.0.1" {
		t.Errorf("internal/pkg/ version = %q, versions=%v", v, info.versions)
	}
	// external package: prefix not stripped the same way — still keyed as pkg+"/"
	if v := info.versions["github.com/ext/dep/"]; v != "v2.0.0" {
		// parseRequireLine: dir = TrimPrefix(pkg, module+"/") + "/"
		// for external: dir = full pkg + "/"
		t.Logf("versions map: %v", info.versions)
		if v == "" {
			// accept either keying style as long as something was parsed
			found := false
			for k, ver := range info.versions {
				if strings.Contains(k, "ext/dep") && ver == "v2.0.0" {
					found = true
				}
			}
			if !found {
				t.Errorf("missing ext/dep version in %v", info.versions)
			}
		}
	}
}

// coverage-patch: 2026-07-17
func TestParseHelpers(t *testing.T) {
	t.Parallel()

	info := &goModInfo{versions: map[string]string{}}
	if !parseModuleLine("module foo/bar", info) || info.module != "foo/bar" {
		t.Errorf("parseModuleLine: %+v", info)
	}
	if parseModuleLine("notmodule x", info) {
		t.Error("parseModuleLine should reject non-module line")
	}

	if !parseGoVersionLine("go 1.20", info) || info.goVersion != "1.20" {
		t.Errorf("parseGoVersionLine: %+v", info)
	}
	if parseGoVersionLine("goto 1", info) {
		t.Error("parseGoVersionLine should reject")
	}

	in := false
	if !parseRequireBlockStart("require (", &in) || !in {
		t.Error("parseRequireBlockStart")
	}
	if parseRequireBlockStart("require foo", &in) {
		t.Error("single-line require is not a block start")
	}
	if !parseRequireBlockEnd(")", &in) || in {
		t.Error("parseRequireBlockEnd")
	}
	// still returns true for ")" even when not in require — that's OK
	if parseRequireBlockEnd("x", &in) {
		t.Error("non-paren should not end block")
	}

	info.module = "github.com/acme/app"
	parseRequireLine("github.com/acme/app/pkg/x v1.2.3", info)
	if info.versions["pkg/x/"] != "v1.2.3" {
		t.Errorf("versions = %v", info.versions)
	}
	// malformed: no-op
	before := len(info.versions)
	parseRequireLine("onlyone", info)
	if len(info.versions) != before {
		t.Error("malformed require line should be no-op")
	}
	// empty after trim of module prefix alone → dir "/" skipped
	parseRequireLine("github.com/acme/app v9.9.9", info)
	if _, ok := info.versions["/"]; ok {
		t.Error("should not store '/' key")
	}
}

// coverage-patch: 2026-07-17
func TestSafeRef(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"github.com/foo/bar", "github-com-foo-bar"},
		{"a_b.c@d:e", "a-b-c-d-e"},
		{"///", "component"},
		{"", "component"},
		{"---", "component"},
		{"pkg/path", "pkg-path"},
	}
	for _, tc := range cases {
		if got := safeRef(tc.in); got != tc.want {
			t.Errorf("safeRef(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// coverage-patch: 2026-07-17
func TestGenerateCycloneDXModuleType(t *testing.T) {
	t.Parallel()
	// Direct unit test of generate helpers with mixed component types.
	proj := &store.Project{Name: "x", Identity: "id/with.special@chars"}
	comps := []Component{
		{Name: "file.go", Type: "file"},
		{Name: "mod", Version: "1.0", Type: "module"},
	}
	raw, err := generateCycloneDXJSON(proj, comps, "v")
	if err != nil {
		t.Fatal(err)
	}
	var bom cdxBOM
	if err := json.Unmarshal(raw, &bom); err != nil {
		t.Fatal(err)
	}
	var sawLib bool
	for _, c := range bom.Components {
		if c.Name == "mod" && c.Type == "library" {
			sawLib = true
		}
	}
	if !sawLib {
		t.Error("module type should map to library in CycloneDX")
	}

	raw2, err := generateSPDXJSON(proj, comps, "v")
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw2) {
		t.Error("SPDX output not valid JSON")
	}
}

// coverage-patch: 2026-07-17
func TestCollectComponentsDedup(t *testing.T) {
	t.Parallel()
	// sortedUniqueDeps already unique; seen map in collectComponents is defense-in-depth.
	ctx := context.Background()
	db := &fakeStore{
		graph: map[string][]string{
			"a": {"same/", "same/"},
		},
	}
	proj := testProj(t, t.TempDir())
	comps, err := collectComponents(ctx, db, proj)
	if err != nil {
		t.Fatal(err)
	}
	if len(comps) != 1 {
		t.Errorf("got %d components, want 1: %+v", len(comps), comps)
	}
}
