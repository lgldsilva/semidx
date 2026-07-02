package search

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

func sampleResponse() *Response {
	return &Response{
		Project: &store.Project{Name: "demo", Path: "/repo"},
		Model:   "bge-m3",
		Results: []store.SearchResult{
			{FilePath: "src/auth.go", Content: "func Login() {\n  // jwt\n}", Score: 0.9123},
			{FilePath: "README.md", Content: "  \n# Title\nbody", Score: 0.5},
		},
	}
}

func TestHumanFormatterGolden(t *testing.T) {
	var buf bytes.Buffer
	if err := (HumanFormatter{}).Format(&buf, sampleResponse()); err != nil {
		t.Fatal(err)
	}
	const want = "--- Result 1 (score: 0.9123) ---\n" +
		"File: src/auth.go\n" +
		"func Login() {\n  // jwt\n}\n\n" +
		"--- Result 2 (score: 0.5000) ---\n" +
		"File: README.md\n" +
		"# Title\nbody\n\n"
	if got := buf.String(); got != want {
		t.Errorf("human output mismatch:\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

func TestGrepFormatterGolden(t *testing.T) {
	// Deterministic line resolution so the golden doesn't depend on files.
	gf := GrepFormatter{
		ProjectPath: "/repo",
		FindLine: func(full, content string) int {
			if strings.Contains(full, "auth") {
				return 12
			}
			return 3
		},
	}
	var buf bytes.Buffer
	if err := gf.Format(&buf, sampleResponse()); err != nil {
		t.Fatal(err)
	}
	const want = "/repo/src/auth.go:12:func Login() {\n" +
		"/repo/README.md:3:# Title\n"
	if got := buf.String(); got != want {
		t.Errorf("grep output mismatch:\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

func TestJSONFormatter(t *testing.T) {
	var buf bytes.Buffer
	resp := sampleResponse()
	resp.Fallback = true
	if err := (JSONFormatter{}).Format(&buf, resp); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Project  string `json:"project"`
		Model    string `json:"model"`
		Fallback bool   `json:"fallback"`
		Results  []struct {
			File    string  `json:"file"`
			Score   float64 `json:"score"`
			Content string  `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if out.Project != "demo" || out.Model != "bge-m3" || !out.Fallback {
		t.Errorf("json header wrong: %+v", out)
	}
	if len(out.Results) != 2 || out.Results[0].File != "src/auth.go" || out.Results[0].Score != 0.9123 {
		t.Errorf("json results wrong: %+v", out.Results)
	}
	// JSON keeps content raw (untrimmed), unlike the human preview.
	if out.Results[1].Content != "  \n# Title\nbody" {
		t.Errorf("json content should be raw, got %q", out.Results[1].Content)
	}
}

func TestTruncatePreviewRuneSafe(t *testing.T) {
	// "héllo" — é is 2 bytes; truncating at 2 bytes must not split it.
	got := truncatePreview("héllo world", 2)
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected ellipsis, got %q", got)
	}
	// The kept prefix must be valid UTF-8 (no broken rune).
	trimmed := strings.TrimSuffix(got, "...")
	for _, r := range trimmed {
		if r == '�' {
			t.Errorf("truncation produced invalid rune in %q", got)
		}
	}
}

func TestFirstNonEmptyLine(t *testing.T) {
	cases := map[string]string{
		"  \n\t\nhello\nworld": "hello",
		"first":                "first",
		"":                     "",
		"   ":                  "",
	}
	for in, want := range cases {
		if got := firstNonEmptyLine(in); got != want {
			t.Errorf("firstNonEmptyLine(%q) = %q, want %q", in, got, want)
		}
	}
}
