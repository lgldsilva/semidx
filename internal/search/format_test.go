package search

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/lgldsilva/semidx/internal/store"
)

// failWriter fails every Write, to exercise formatter error propagation.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func sampleResponse() *Response {
	return &Response{
		Project: &store.Project{Name: "demo", Path: "/repo"},
		Model:   "bge-m3",
		Results: []store.SearchResult{
			{FilePath: "src/auth.go", Content: "func Login() {\n  // jwt\n}", Score: 0.9123, StartLine: 12, EndLine: 14},
			{FilePath: "README.md", Content: "  \n# Title\nbody", Score: 0.5, StartLine: 3, EndLine: 5},
		},
	}
}

func TestHumanFormatterGolden(t *testing.T) {
	var buf bytes.Buffer
	if err := (HumanFormatter{}).Format(&buf, sampleResponse()); err != nil {
		t.Fatal(err)
	}
	const want = "--- Result 1 (91%) ---\n" +
		"File: src/auth.go:12-14\n" +
		"Score: 0.912 (91%)\n" +
		"  12│ func Login() {\n" +
		"  13│   // jwt\n" +
		"  14│ }\n\n" +
		"--- Result 2 (50%) ---\n" +
		"File: README.md:3-5\n" +
		"Score: 0.500 (50%)\n" +
		"   3│ # Title\n" +
		"   4│ body\n\n"
	if got := buf.String(); got != want {
		t.Errorf("human output mismatch:\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

func TestHumanFormatterKeywordGolden(t *testing.T) {
	// A keyword response labels every result "keyword match" (its 0.5 scores are a
	// placeholder, not a similarity) and still shows file:line. Also covers a
	// single-line chunk and a zero start line clamped to 1.
	resp := &Response{
		Model:   "bge-m3",
		Keyword: true,
		Results: []store.SearchResult{
			{FilePath: "src/auth.go", Content: "func Login() {}", Score: 0.5, StartLine: 7, EndLine: 7},
			{FilePath: "notes.txt", Content: "todo", Score: 0.5, StartLine: 0, EndLine: 0},
		},
	}
	var buf bytes.Buffer
	if err := (HumanFormatter{}).Format(&buf, resp); err != nil {
		t.Fatal(err)
	}
	const want = "--- Result 1 (keyword match) ---\n" +
		"File: src/auth.go:7\n" +
		"Score: keyword match\n" +
		"   7│ func Login() {}\n\n" +
		"--- Result 2 (keyword match) ---\n" +
		"File: notes.txt:1\n" +
		"Score: keyword match\n" +
		"todo\n\n"
	if got := buf.String(); got != want {
		t.Errorf("keyword human output mismatch:\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

func TestGrepFormatterGolden(t *testing.T) {
	// Line numbers come from the stored chunk (SearchResult.StartLine); no file
	// read. This is the sgrep contract the daily workflow depends on.
	var buf bytes.Buffer
	if err := (GrepFormatter{ProjectPath: "/repo"}).Format(&buf, sampleResponse()); err != nil {
		t.Fatal(err)
	}
	const want = "/repo/src/auth.go:12:func Login() {\n" +
		"/repo/README.md:3:# Title\n"
	if got := buf.String(); got != want {
		t.Errorf("grep output mismatch:\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

func TestGrepFormatterZeroStartLine(t *testing.T) {
	var buf bytes.Buffer
	resp := &Response{
		Results: []store.SearchResult{
			{FilePath: "x.go", Content: "line", StartLine: 0},
		},
	}
	if err := (GrepFormatter{ProjectPath: "/p"}).Format(&buf, resp); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "/p/x.go:1:line\n" {
		t.Fatalf("grep zero line = %q", got)
	}
}

func TestGrepFormatterWriteError(t *testing.T) {
	if err := (GrepFormatter{ProjectPath: "/p"}).Format(failWriter{}, sampleResponse()); err == nil {
		t.Fatal("expected write error")
	}
}

func TestJSONFormatterNilProject(t *testing.T) {
	var buf bytes.Buffer
	resp := &Response{Model: "m", Results: []store.SearchResult{{FilePath: "a.go", Score: 1}}}
	if err := (JSONFormatter{}).Format(&buf, resp); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), `"project"`) {
		var out map[string]any
		_ = json.Unmarshal(buf.Bytes(), &out)
		if p, ok := out["project"].(string); ok && p != "" {
			t.Fatalf("expected empty project, got %q", p)
		}
	}
}

func TestHumanFormatterWriteError(t *testing.T) {
	if err := (HumanFormatter{}).Format(failWriter{}, sampleResponse()); err == nil {
		t.Fatal("expected the write error to propagate")
	}
}

func TestHumanFormatterNoLineNumbers(t *testing.T) {
	var buf bytes.Buffer
	fmtr := HumanFormatter{NoLineNums: true}
	if err := fmtr.Format(&buf, sampleResponse()); err != nil {
		t.Fatal(err)
	}
	// Should match old golden output without line numbers.
	const want = "--- Result 1 (91%) ---\n" +
		"File: src/auth.go:12-14\n" +
		"Score: 0.912 (91%)\n" +
		"func Login() {\n  // jwt\n}\n\n" +
		"--- Result 2 (50%) ---\n" +
		"File: README.md:3-5\n" +
		"Score: 0.500 (50%)\n" +
		"# Title\nbody\n\n"
	if got := buf.String(); got != want {
		t.Errorf("no-line-numbers output mismatch:\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

func TestPrefixLineNumbers(t *testing.T) {
	got := prefixLineNumbers("func Foo() {\n\tbar()\n\tbaz()\n}", 15, 4)
	const want = "  15│ func Foo() {\n  16│ \tbar()\n  17│ \tbaz()\n  18│ }"
	if got != want {
		t.Errorf("prefixLineNumbers mismatch:\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

func TestPrefixLineNumbersWideAutoPad(t *testing.T) {
	// When line numbers exceed the default padding, auto-expand.
	got := prefixLineNumbers("a\nb\nc", 9998, 4)
	if !strings.Contains(got, "9999│") && !strings.Contains(got, "10000│") {
		t.Errorf("expected auto-width padding for large line numbers, got:\n%s", got)
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
