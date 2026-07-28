package indexfingerprint

import "testing"

func TestComputeCorpusIsStableAcrossMapOrder(t *testing.T) {
	a := ComputeCorpus("remote:github.com/acme/app", "abc", map[string]string{
		"b.go": "hash-b", "a.go": "hash-a",
	})
	b := ComputeCorpus("remote:github.com/acme/app", "abc", map[string]string{
		"a.go": "hash-a", "b.go": "hash-b",
	})
	if a != b || len(a) != 64 {
		t.Fatalf("fingerprints = %q, %q", a, b)
	}
}

func TestComputeCorpusChangesForEveryIndexCoordinate(t *testing.T) {
	base := map[string]string{"a.go": "hash-a"}
	ref := ComputeCorpus("id", "commit", base)
	checks := []struct {
		name string
		got  string
	}{
		{"identity", ComputeCorpus("other", "commit", base)},
		{"commit", ComputeCorpus("id", "other", base)},
		{"file hash", ComputeCorpus("id", "commit", map[string]string{"a.go": "other"})},
		{"file path", ComputeCorpus("id", "commit", map[string]string{"b.go": "hash-a"})},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if check.got == ref {
				t.Fatalf("coordinate %s did not change fingerprint", check.name)
			}
		})
	}
}
