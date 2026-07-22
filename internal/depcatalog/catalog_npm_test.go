package depcatalog

import "testing"

func TestParsePackageJSONScopes(t *testing.T) {
	deps, err := ParseManifest("package.json", []byte(`{
		"dependencies": {"react": "18.3.1"},
		"devDependencies": {"vitest": "2.0.0"},
		"peerDependencies": {"typescript": ">=5"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 3 {
		t.Fatalf("got %d dependencies: %+v", len(deps), deps)
	}
	wantScopes := map[string]string{"react": "runtime", "vitest": "development", "typescript": "peer"}
	for _, dep := range deps {
		if dep.Scope != wantScopes[dep.Name] {
			t.Errorf("%s scope = %q, want %q", dep.Name, dep.Scope, wantScopes[dep.Name])
		}
	}
}
