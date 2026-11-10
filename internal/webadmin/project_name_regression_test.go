package webadmin

import (
	"net/http"
	"testing"
)

// Regression: the admin create-project path accepted any name except "", ".",
// "/" — "../evil" then flowed into gitsync's filepath.Join(DataDir, "repos",
// name) and escaped the repos directory.
func TestValidateProjectNameRejectsTraversal(t *testing.T) {
	for _, name := range []string{"../evil", "a/b", "..", "-x", ".hidden", `a\b`, "a b"} {
		if _, status, msg := validateProjectName(name); status != http.StatusBadRequest || msg == "" {
			t.Errorf("validateProjectName(%q) accepted (status=%d)", name, status)
		}
	}
	for _, name := range []string{"app", "my-repo.v2", "x_y"} {
		if got, status, msg := validateProjectName(name); status != 0 || msg != "" || got != name {
			t.Errorf("validateProjectName(%q) rejected: %d %q", name, status, msg)
		}
	}
}
