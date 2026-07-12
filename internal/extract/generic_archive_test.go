package extract

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// buildZip creates an in-memory .zip from a map of entry names to content.
func buildZip(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// buildTar creates an in-memory .tar from a map of entry names to content.
func buildTar(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, content := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name: name,
			Size: int64(len(content)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// buildTarGz creates an in-memory .tar.gz from a map of entry names to content.
func buildTarGz(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for name, content := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name: name,
			Size: int64(len(content)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExtractGenericArchiveZip(t *testing.T) {
	data := buildZip(t, map[string]string{
		"readme.txt":  "Hello from zip",
		"src/main.go": "package main\nfunc main() {}",
		"data.json":   `{"key": "value"}`,
		"binary.bin":  "", // empty — should be skipped
	})
	docs, err := ExtractAll("archive.zip", data)
	if err != nil {
		t.Fatalf("ExtractAll zip: unexpected error: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected at least one doc from zip")
	}
	// Virtual paths should use archive!entry format.
	found := false
	for _, d := range docs {
		if strings.Contains(d.Path, "archive.zip!") {
			found = true
		}
		if strings.Contains(d.Text, "Hello from zip") {
			found = true
		}
	}
	if !found {
		t.Errorf("zip docs missing expected content: %+v", docs)
	}
}

func TestExtractGenericArchiveTar(t *testing.T) {
	data := buildTar(t, map[string]string{
		"notes.txt": "Hello from tar",
		"code.py":   "print('hello')",
	})
	docs, err := ExtractAll("archive.tar", data)
	if err != nil {
		t.Fatalf("ExtractAll tar: unexpected error: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected at least one doc from tar")
	}
	found := false
	for _, d := range docs {
		if strings.Contains(d.Text, "Hello from tar") {
			found = true
		}
	}
	if !found {
		t.Errorf("tar docs missing: %+v", docs)
	}
}

func TestExtractGenericArchiveTarGz(t *testing.T) {
	data := buildTarGz(t, map[string]string{
		"file.txt": "Hello from tar.gz",
	})
	docs, err := ExtractAll("archive.tar.gz", data)
	if err != nil {
		t.Fatalf("ExtractAll tar.gz: unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc from tar.gz, got %d: %+v", len(docs), docs)
	}
	if !strings.Contains(docs[0].Text, "Hello from tar.gz") {
		t.Errorf("tar.gz text missing: %q", docs[0].Text)
	}
}

func TestExtractGenericArchiveInvalidZip(t *testing.T) {
	if _, err := ExtractAll("bad.zip", []byte("not a zip")); err == nil {
		t.Error("invalid zip should error")
	}
}

func TestExtractGenericArchiveEmptyZip(t *testing.T) {
	data := buildZip(t, map[string]string{})
	docs, err := ExtractAll("empty.zip", data)
	if err != nil {
		t.Fatalf("ExtractAll empty zip: unexpected error: %v", err)
	}
	if len(docs) != 0 {
		t.Errorf("empty zip should yield no docs, got %d", len(docs))
	}
}

func TestExtractGenericArchiveSkipsBinary(t *testing.T) {
	// Entries with binary extensions should not be indexed.
	data := buildZip(t, map[string]string{
		"readme.txt":  "hello",
		"photo.png":   string([]byte{0x89, 0x50, 0x4E, 0x47}),
		"binary.bin":  string([]byte{0x00, 0x01, 0x02}),
		"archive.zip": "not-a-zip", // binary content with .zip ext
	})
	docs, err := ExtractAll("archive.zip", data)
	if err != nil {
		t.Fatalf("ExtractAll: unexpected error: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("expected 1 text doc, got %d: %+v", len(docs), docs)
	}
	if docs[0].Text != "hello" {
		t.Errorf("expected 'hello', got %q", docs[0].Text)
	}
}

func TestExtractGenericArchiveZipCaps(t *testing.T) {
	// Create more entries than maxArchiveEntries to test the cap.
	entries := make(map[string]string, maxArchiveEntries+10)
	for i := 0; i < maxArchiveEntries+10; i++ {
		entries[fmt.Sprintf("file%d.txt", i)] = "content"
	}
	data := buildZip(t, entries)
	docs, err := ExtractAll("capped.zip", data)
	if err != nil {
		t.Fatalf("ExtractAll: unexpected error: %v", err)
	}
	if len(docs) > maxArchiveEntries {
		t.Errorf("zip cap: got %d docs, want ≤ %d", len(docs), maxArchiveEntries)
	}
}

func TestExtractAllZipOnlyThroughExtractAll(t *testing.T) {
	// .zip files should NOT be handled by Extract (single-file), only by ExtractAll.
	if _, err := Extract("test.zip", []byte("content")); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Extract(zip) should be ErrUnsupported, got %v", err)
	}
}

func TestExtractGenericArchiveNestedZip(t *testing.T) {
	// Create a nested zip: outer.zip contains inner.zip
	innerData := buildZip(t, map[string]string{
		"inner.txt": "nested content",
	})
	outerData := buildZip(t, map[string]string{
		"outer.txt":  "outer content",
		"nested.zip": string(innerData),
	})
	docs, err := ExtractAll("outer.zip", outerData)
	if err != nil {
		t.Fatalf("ExtractAll: unexpected error: %v", err)
	}
	if len(docs) == 0 {
		t.Fatal("expected docs from nested zip")
	}
	texts := make(map[string]string)
	for _, d := range docs {
		texts[d.Path] = d.Text
	}
	// Should find both outer and inner content
	if !strings.Contains(texts["outer.zip!outer.txt"], "outer content") {
		t.Errorf("missing outer content: %+v", texts)
	}
	if !strings.Contains(texts["outer.zip!nested.zip!inner.txt"], "nested content") {
		t.Errorf("missing nested content: %+v", texts)
	}
}

func TestExtractGenericArchiveTarDepth(t *testing.T) {
	// Create a tar that contains a zip. Depth 2 should be allowed (inner zip).
	innerData := buildZip(t, map[string]string{
		"deep.txt": "deep content",
	})
	outerData := buildTar(t, map[string]string{
		"top.txt":    "top content",
		"nested.zip": string(innerData),
	})
	docs, err := ExtractAll("outer.tar", outerData)
	if err != nil {
		t.Fatalf("ExtractAll: unexpected error: %v", err)
	}
	texts := make(map[string]string)
	for _, d := range docs {
		texts[d.Path] = d.Text
	}
	if !strings.Contains(texts["outer.tar!top.txt"], "top content") {
		t.Errorf("missing top content: %+v", texts)
	}
}
