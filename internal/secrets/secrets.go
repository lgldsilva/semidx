// Package secrets wraps gitleaks for secret detection during indexing.
// It tags files that likely contain credentials, API keys, or tokens so the
// indexer can route them away from cloud embedding providers.
package secrets

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zricethezav/gitleaks/v8/detect"
	"github.com/zricethezav/gitleaks/v8/report"
)

// Finding models one secret detection result.
type Finding struct {
	RuleID      string
	Description string
	Severity    string // "CRITICAL", "HIGH", "MEDIUM", "LOW"
	StartLine   int
	EndLine     int
}

// ignoreFile is the project-root ignore file for secret scanning.
const ignoreFile = ".semidx-secrets-ignore"

// inlineAnnotation is the comment prefix that suppresses a finding on the
// preceding line.
const inlineAnnotation = "semidx:ignore-secret"

// Detector wraps a gitleaks detector for project-level secret scanning.
type Detector struct {
	gitleaks  *detect.Detector
	ignores   []string        // glob patterns from .semidx-secrets-ignore
	ignoreSet map[string]bool // resolved paths cached during a scan
}

// NewDetector creates a Detector with gitleaks' default configuration.
// If the project root contains a .semidx-secrets-ignore file, its patterns
// are loaded for path-based skipping.  Without gitleaks' defaults loaded
// successfully the detector will be nil (callers should check the returned
// detector before scanning).
func NewDetector(projectRoot string) (*Detector, error) {
	d, err := detect.NewDetectorDefaultConfig()
	if err != nil {
		return nil, fmt.Errorf("gitleaks new detector: %w", err)
	}

	det := &Detector{
		gitleaks:  d,
		ignoreSet: make(map[string]bool),
	}

	// Load project-level ignore file.
	ignorePath := filepath.Clean(filepath.Join(projectRoot, ignoreFile))
	if data, err := os.ReadFile(ignorePath); err == nil {
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			det.ignores = append(det.ignores, line)
		}
	}

	return det, nil
}

// Scan runs gitleaks detection on the given file content.
// path is the project-relative file path used for ignore matching and inline
// annotation detection.  The returned slice is empty when no secrets are found.
func (d *Detector) Scan(path string, content []byte) []Finding {
	if d == nil || d.gitleaks == nil {
		return nil
	}
	if d.isIgnored(path) {
		return nil
	}

	// Split content into lines for inline annotation checks.
	lines := strings.Split(string(content), "\n")

	findings := d.gitleaks.DetectBytes(content)
	if len(findings) == 0 {
		return nil
	}

	// Filter out findings with preceding inline ignore annotations and
	// normalise to our Finding type.
	out := make([]Finding, 0, len(findings))
	for _, f := range findings {
		if hasInlineIgnore(lines, f) {
			continue
		}
		out = append(out, convertFinding(f))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// AddGitleaksIgnore loads a gitleaks-format ignore file (.gitleaksignore) so
// that known false positives are skipped.  It delegates to the underlying
// gitleaks Detector.
func (d *Detector) AddGitleaksIgnore(path string) error {
	if d == nil || d.gitleaks == nil {
		return nil
	}
	return d.gitleaks.AddGitleaksIgnore(path)
}

// Close releases any resources held by the detector.
func (d *Detector) Close() {
	if d.gitleaks != nil {
		_ = d.gitleaks.Sema // not owned — no-op for safety
	}
}

// ---------------------------------------------------------------------------
// Ignore machinery
// ---------------------------------------------------------------------------

// isIgnored reports whether path matches any pattern in the ignore file.
func (d *Detector) isIgnored(path string) bool {
	if d.ignoreSet[path] {
		return true
	}
	for _, pattern := range d.ignores {
		matched, err := filepath.Match(pattern, path)
		if err == nil && matched {
			d.ignoreSet[path] = true
			return true
		}
		// Also try matching just the basename.
		if matched, err := filepath.Match(pattern, filepath.Base(path)); err == nil && matched {
			d.ignoreSet[path] = true
			return true
		}
	}
	return false
}

// hasInlineIgnore checks whether the line immediately preceding a finding
// contains the inline ignore annotation.  It also looks for the annotation
// on the same line inline (e.g. trailing comment).
func hasInlineIgnore(lines []string, f report.Finding) bool {
	// gitleaks may report StartLine=0 for one-line content; treat it as
	// line 1 for indexing purposes.
	lineIdx := f.StartLine
	if lineIdx < 1 {
		lineIdx = 1
	}
	// Check the line before the finding.
	if lineIdx > 1 && lineIdx-2 < len(lines) {
		if strings.Contains(lines[lineIdx-2], inlineAnnotation) {
			return true
		}
	}
	// Check the finding line itself (inline comment).
	if lineIdx-1 < len(lines) {
		if strings.Contains(lines[lineIdx-1], inlineAnnotation) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Severity mapping
// ---------------------------------------------------------------------------

// knownSeverity returns a severity label for a rule ID based on common
// gitleaks rules, or "MEDIUM" as a default.
func knownSeverity(ruleID string) string {
	ruleID = strings.ToLower(ruleID)
	if strings.Contains(ruleID, "aws") ||
		strings.Contains(ruleID, "gcp") ||
		strings.Contains(ruleID, "azure") ||
		strings.Contains(ruleID, "github") ||
		strings.Contains(ruleID, "gitlab") ||
		strings.Contains(ruleID, "slack") ||
		strings.Contains(ruleID, "discord") ||
		strings.Contains(ruleID, "stripe") ||
		strings.Contains(ruleID, "twilio") ||
		strings.Contains(ruleID, "private-key") {
		return "CRITICAL"
	}
	if strings.Contains(ruleID, "generic") ||
		strings.Contains(ruleID, "token") ||
		strings.Contains(ruleID, "password") ||
		strings.Contains(ruleID, "secret") ||
		strings.Contains(ruleID, "jwt") ||
		strings.Contains(ruleID, "api-key") {
		return "HIGH"
	}
	return "MEDIUM"
}

// convertFinding maps a gitleaks report.Finding to our Finding type.
func convertFinding(f report.Finding) Finding {
	return Finding{
		RuleID:      f.RuleID,
		Description: f.Description,
		Severity:    knownSeverity(f.RuleID),
		StartLine:   f.StartLine,
		EndLine:     f.EndLine,
	}
}

// Ensure compilation checks for interface satisfaction.
