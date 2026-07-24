package gitmeta

import "testing"

func TestRedactURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"https://example.com/org/repo.git", "https://example.com/org/repo.git"},
		{"https://lgldsilva:s3cret@gitea.example/org/repo.git", "https://gitea.example/org/repo.git"},
		{"https://token@host/a/b.git", "https://host/a/b.git"},
		{"git@gitea.example:org/repo.git", "gitea.example:org/repo.git"},
		{"  https://u:p@h/x  ", "https://h/x"},
	}
	for _, tc := range cases {
		if got := RedactURL(tc.in); got != tc.want {
			t.Errorf("RedactURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
