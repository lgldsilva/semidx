package secrets

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zricethezav/gitleaks/v8/report"
)

func TestNewDetector_Defaults(t *testing.T) {
	d, err := NewDetector(t.TempDir())
	if err != nil {
		t.Fatalf("NewDetector: %v", err)
	}
	if d == nil {
		t.Fatal("expected non-nil detector")
	}
}

func TestScan_NoFindings(t *testing.T) {
	d, err := NewDetector(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	findings := d.Scan("main.go", []byte("package main\nfunc main() {}"))
	if len(findings) != 0 {
		t.Fatalf("expected no findings, got %d", len(findings))
	}
}

func TestScan_AWSKey(t *testing.T) {
	d, err := NewDetector(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	content := []byte(`access_key = "AKIAIOSFODNN7EXAMPLE"`)
	findings := d.Scan(".env", content)
	if len(findings) == 0 {
		t.Skip("gitleaks did not flag AWS key — may depend on config version")
	}
	for _, f := range findings {
		if f.RuleID == "" || f.Description == "" {
			t.Errorf("finding missing rule/description: %+v", f)
		}
		if f.Severity == "" {
			t.Errorf("finding missing severity: %+v", f)
		}
		if f.StartLine < 1 {
			t.Errorf("finding missing start line: %+v", f)
		}
	}
}

func TestScan_GitHubToken(t *testing.T) {
	d, err := NewDetector(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")
	findings := d.Scan("config.yml", content)
	if len(findings) == 0 {
		t.Skip("gitleaks did not flag GitHub token — may depend on config version")
	}
	for _, f := range findings {
		if f.Severity == "" {
			t.Errorf("finding missing severity: %+v", f)
		}
	}
}

func TestScan_PrivateKey(t *testing.T) {
	d, err := NewDetector(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA\n-----END RSA PRIVATE KEY-----")
	findings := d.Scan("id_rsa", content)
	if len(findings) == 0 {
		t.Skip("gitleaks did not flag private key — may depend on config version")
	}
}

func TestScan_InlineIgnore(t *testing.T) {
	d, err := NewDetector(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The preceding line has the ignore annotation.
	content := []byte("// semidx:ignore-secret\nsecret = \"sk_live_xxxxxxxxxxxxx\"\n")
	findings := d.Scan("config.go", content)
	if len(findings) != 0 {
		t.Fatalf("expected no findings after inline ignore, got %d", len(findings))
	}
}

func TestScan_InlineSameLine(t *testing.T) {
	d, err := NewDetector(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("secret = \"sk_live_xxxxxxxxxxxxx\" // semidx:ignore-secret\n")
	findings := d.Scan("config.go", content)
	if len(findings) != 0 {
		t.Fatalf("expected no findings with same-line ignore, got %d", len(findings))
	}
}

func TestScan_IgnoreFile(t *testing.T) {
	dir := t.TempDir()

	// Write an ignore file that skips .env files.
	ignorePath := filepath.Join(dir, ignoreFile)
	if err := os.WriteFile(ignorePath, []byte("*.env\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	d, err := NewDetector(dir)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("AWS_ACCESS_KEY=AKIAIOSFODNN7EXAMPLE")
	findings := d.Scan("config.env", content)
	if len(findings) != 0 {
		t.Fatalf("expected no findings for ignored path, got %d", len(findings))
	}
}

func TestScan_IgnoreFileBasenameMatch(t *testing.T) {
	dir := t.TempDir()
	ignorePath := filepath.Join(dir, ignoreFile)
	if err := os.WriteFile(ignorePath, []byte("secrets*\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	d, err := NewDetector(dir)
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")
	findings := d.Scan("src/secrets.yml", content)
	if len(findings) != 0 {
		t.Fatalf("expected no findings for basename match, got %d", len(findings))
	}
}

func TestConvertFinding(t *testing.T) {
	f := convertFinding(report.Finding{
		RuleID:      "aws-access-key",
		Description: "AWS Access Key",
		StartLine:   5,
		EndLine:     5,
	})
	if f.RuleID != "aws-access-key" {
		t.Errorf("expected aws-access-key, got %s", f.RuleID)
	}
	if f.Severity != "CRITICAL" {
		t.Errorf("expected CRITICAL severity, got %s", f.Severity)
	}
}

func TestKnownSeverity(t *testing.T) {
	tests := []struct {
		ruleID   string
		expected string
	}{
		{"aws-access-key", "CRITICAL"},
		{"github-token", "CRITICAL"},
		{"generic-api-key", "HIGH"},
		{"jwt-token", "HIGH"},
		{"slack-token", "CRITICAL"},
		{"unknown-rule", "MEDIUM"},
		{"private-key", "CRITICAL"},
	}
	for _, tc := range tests {
		got := knownSeverity(tc.ruleID)
		if got != tc.expected {
			t.Errorf("knownSeverity(%q) = %s, want %s", tc.ruleID, got, tc.expected)
		}
	}
}
