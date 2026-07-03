package extract

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"
)

// TestExtractJARRealFixture runs against a real compiled jar (com.example.Greeter
// + its source + a .properties + MANIFEST) so the class-file parser is exercised
// on genuine bytecode, per the validate-with-real-inputs standard.
func TestExtractJARRealFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/greeter.jar")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	docs, err := ExtractAll("greeter.jar", data)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) == 0 {
		t.Fatal("no docs extracted from jar")
	}

	byPath := map[string]string{}
	for _, d := range docs {
		byPath[d.Path] = d.Text
	}

	// The .class entry yields its API surface: fully-qualified class name + the
	// method name (from the constant pool). Paths are archive-namespaced.
	classDoc, ok := byPath["greeter.jar!com/example/Greeter.class"]
	if !ok {
		t.Fatalf("class entry missing; got paths %v", keys(byPath))
	}
	for _, want := range []string{"com.example.Greeter", "greetUserWithArgon2id"} {
		if !strings.Contains(classDoc, want) {
			t.Errorf("class API surface missing %q:\n%s", want, classDoc)
		}
	}
	// Bytecode descriptors must not leak as searchable symbols.
	if strings.Contains(classDoc, "()V") || strings.Contains(classDoc, "Ljava/lang") {
		t.Errorf("descriptor noise leaked into class surface:\n%s", classDoc)
	}

	// The embedded source and the .properties resource are indexed as-is.
	if src, ok := byPath["greeter.jar!com/example/Greeter.java"]; !ok || !strings.Contains(src, "argon2id") {
		t.Errorf("embedded .java source not extracted verbatim")
	}
	if props, ok := byPath["greeter.jar!app.properties"]; !ok || !strings.Contains(props, "exponential") {
		t.Errorf("resource .properties not extracted")
	}
}

func TestExtractArchiveInvalidZip(t *testing.T) {
	if _, err := ExtractAll("bad.jar", []byte("not a zip at all")); err == nil {
		t.Error("invalid archive should error, not panic")
	}
}

func TestClassAPIRejectsNonClass(t *testing.T) {
	if _, err := classAPI([]byte{0x00, 0x01, 0x02, 0x03}); !errors.Is(err, errNotClass) {
		t.Errorf("classAPI on non-class = %v; want errNotClass", err)
	}
	// Truncated (valid magic, nothing else) must error, not panic.
	if _, err := classAPI([]byte{0xCA, 0xFE, 0xBA, 0xBE}); err == nil {
		t.Error("truncated class should error")
	}
}

func TestSupportedIncludesArchives(t *testing.T) {
	for _, n := range []string{"a.jar", "b.war", "c.aar", "d.pdf", "e.md"} {
		if !Supported(n) {
			t.Errorf("Supported(%q) = false", n)
		}
	}
	if Supported("x.bin") {
		t.Error("Supported(x.bin) = true; want false")
	}
}

// TestExtractArchiveSkipsUnparseableEntries builds a jar with a bogus .class and
// confirms it is skipped rather than failing the whole archive.
func TestExtractArchiveSkipsUnparseableEntries(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("Broken.class")
	_, _ = w.Write([]byte("not really a class"))
	w2, _ := zw.Create("notes.txt")
	_, _ = w2.Write([]byte("plain resource text"))
	_ = zw.Close()

	docs, err := ExtractAll("x.jar", buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	// The bogus class is skipped; the text resource survives.
	if len(docs) != 1 || !strings.Contains(docs[0].Text, "plain resource") {
		t.Errorf("expected only the text resource, got %+v", docs)
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
