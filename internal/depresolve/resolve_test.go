package depresolve

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveProjectRunsNativeTools(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"go.mod", "pom.xml", "build.gradle"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("manifest"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r := NewWithRunner(func(_ context.Context, _ string, tool string, _ ...string) ([]byte, error) {
		switch tool {
		case "go":
			return []byte(`{"Path":"example.com/app","Main":true}{"Path":"github.com/acme/lib","Version":"v1.2.3"}`), nil
		case "mvn":
			return []byte("[INFO] +- org.slf4j:slf4j-api:jar:2.0.13:compile"), nil
		case "gradle":
			return []byte("+--- com.fasterxml.jackson.core:jackson-core:2.17.1"), nil
		default:
			t.Fatalf("unexpected tool %q", tool)
			return nil, nil
		}
	})
	deps, err := r.ResolveProject(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 3 {
		t.Fatalf("got %d deps: %+v", len(deps), deps)
	}
}

func TestResolveToolError(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := NewWithRunner(func(context.Context, string, string, ...string) ([]byte, error) { return nil, os.ErrNotExist })
	if _, err := r.ResolveProject(context.Background(), root); err == nil {
		t.Fatal("expected resolver error")
	}
}
