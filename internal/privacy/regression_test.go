package privacy

import (
	"path/filepath"
	"testing"
)

// Common secret-bearing files that the exact-token matcher used to miss, so
// they were sent to cloud embedders.
func TestIsSensitiveCommonSecretFiles(t *testing.T) {
	for _, p := range []string{
		"credentials.json", "gcp/service-credentials.json", "secrets.yaml",
		"tokens.json", "passwords.txt", "keys/readme.md",
		"home/.ssh/id_rsa", "id_ed25519", "id_ecdsa", ".npmrc", ".netrc",
		".pgpass", ".pypirc", ".htpasswd", "cert.p12", "store.pfx",
		"keystore.jks", "prod.tfvars", "terraform.tfstate", "app.keystore",
		"kubeconfig", ".dockercfg",
	} {
		if !IsSensitive(filepath.FromSlash(p)) {
			t.Errorf("IsSensitive(%q) = false, want true", p)
		}
	}
}

func TestIsSensitiveNoFalsePositives(t *testing.T) {
	for _, p := range []string{"keyboard.go", "monkeys.go", "tokenizer.go", "README.md", "main.go"} {
		if IsSensitive(p) {
			t.Errorf("IsSensitive(%q) = true, want false", p)
		}
	}
}
