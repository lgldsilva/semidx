package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestUserMessagesAreEnglish enforces REQ-CLI-06: no mixed PT/EN user-facing
// messages in the CLI command package.
func TestUserMessagesAreEnglish(t *testing.T) {
	bannedPT := []string{
		" não ", " erro ", " inválido", " invalido", " falhou", " usuário", " senha",
		" projeto ", " arquivo ", " obrigatório", " obrigatorio", " deve ",
	}
	err := filepath.Walk(".", func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := " " + strings.ToLower(string(b)) + " "
		for _, tok := range bannedPT {
			if strings.Contains(src, tok) {
				t.Errorf("%s contains banned Portuguese token %q", path, strings.TrimSpace(tok))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk cmd/semidx: %v", err)
	}
}
