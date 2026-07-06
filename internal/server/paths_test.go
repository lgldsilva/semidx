package server

import "testing"

func TestValidateRelativePath(t *testing.T) {
	for _, p := range []string{"src/main.go", "docs/readme.md", "a/b/c.txt"} {
		if err := validateRelativePath(p); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}
	for _, p := range []string{"", ".", "..", "../etc/passwd", "foo/../../bar", "/abs/path.go"} {
		if err := validateRelativePath(p); err == nil {
			t.Errorf("%q: expected error", p)
		}
	}
}
