package depresolve

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolverSwiftAndCocoaPods(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Package.swift"), []byte("// swift"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Podfile"), []byte("pod 'Alamofire'"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := NewWithRunner(func(_ context.Context, dir, tool string, _ ...string) ([]byte, error) {
		switch tool {
		case "swift":
			return []byte(`{"name":"app","dependencies":[{"name":"swift-log","url":"https://example.test/swift-log"}]}`), nil
		case "pod":
			if err := os.WriteFile(filepath.Join(dir, "Podfile.lock"), []byte("PODS:\n  - Alamofire (5.9.0)\n"), 0o600); err != nil {
				return nil, err
			}
			return nil, nil
		default:
			t.Fatalf("unexpected tool %q", tool)
			return nil, nil
		}
	})
	deps, err := r.ResolveProject(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(deps) != 2 {
		t.Fatalf("got %d deps: %+v", len(deps), deps)
	}
}

func TestResolverConstructionAndToolError(t *testing.T) {
	if New() == nil {
		t.Fatal("New returned nil")
	}
	rootErr := errors.New("boom")
	err := &ToolError{Tool: "swift", Err: rootErr}
	if !strings.Contains(err.Error(), "dependency resolver swift: boom") {
		t.Errorf("ToolError.Error() = %q", err.Error())
	}
	if !errors.Is(err, rootErr) {
		t.Error("ToolError should unwrap its cause")
	}
	if out, runErr := runCommand(context.Background(), t.TempDir(), "true"); runErr != nil || len(out) != 0 {
		t.Fatalf("runCommand = %q, %v", out, runErr)
	}
}
