package pending

import (
	"os"
	"testing"
)

func TestSaveLoadRemove(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	key := "path:/abs/proj"

	if r, err := Load(key); err != nil || r != nil {
		t.Fatalf("empty Load = %+v, %v; want nil,nil", r, err)
	}

	if err := Save(key, &Registry{Project: "proj", Model: "bge-m3", Files: []string{"/a/x.pdf", "/a/y.xlsx"}}); err != nil {
		t.Fatal(err)
	}
	r, err := Load(key)
	if err != nil {
		t.Fatal(err)
	}
	if r == nil || r.Project != "proj" || r.Model != "bge-m3" || len(r.Files) != 2 {
		t.Fatalf("Load = %+v", r)
	}

	// The registry may hold private file paths → 0600.
	p, _ := fileFor(key)
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm = %o, want 600", perm)
	}

	// Saving an empty list removes the registry.
	if err := Save(key, &Registry{Files: nil}); err != nil {
		t.Fatal(err)
	}
	if r, _ := Load(key); r != nil {
		t.Error("empty Save should remove the registry")
	}
	// Removing an absent key is a no-op.
	if err := Remove(key); err != nil {
		t.Errorf("Remove(absent) = %v", err)
	}
}

func TestDistinctKeysDoNotCollide(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := Save("path:/one/backend", &Registry{Files: []string{"/one/a"}}); err != nil {
		t.Fatal(err)
	}
	if err := Save("path:/two/backend", &Registry{Files: []string{"/two/b"}}); err != nil {
		t.Fatal(err)
	}
	r1, _ := Load("path:/one/backend")
	r2, _ := Load("path:/two/backend")
	if r1 == nil || r2 == nil || r1.Files[0] != "/one/a" || r2.Files[0] != "/two/b" {
		t.Errorf("same-basename projects collided: r1=%+v r2=%+v", r1, r2)
	}
}
