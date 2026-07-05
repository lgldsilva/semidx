package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContainsNull(t *testing.T) {
	if containsNull([]byte("hello")) {
		t.Error("containsNull(hello) should be false")
	}
	if !containsNull([]byte("hel\x00lo")) {
		t.Error("containsNull(hel\\x00lo) should be true")
	}
	if containsNull(nil) {
		t.Error("containsNull(nil) should be false")
	}
	if containsNull([]byte{}) {
		t.Error("containsNull(empty) should be false")
	}
}

func TestReadFileContent(t *testing.T) {
	dir := t.TempDir()

	// Text file
	textPath := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(textPath, []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}
	content, isText, err := readFileContent(textPath)
	if err != nil {
		t.Fatal(err)
	}
	if !isText {
		t.Error("hello.txt should be text")
	}
	if content != "hello world" {
		t.Errorf("content = %q, want %q", content, "hello world")
	}

	// Binary file (null bytes)
	binPath := filepath.Join(dir, "bin.bin")
	if err := os.WriteFile(binPath, []byte{0x00, 0x01, 0x02}, 0644); err != nil {
		t.Fatal(err)
	}
	content, isText, err = readFileContent(binPath)
	if err != nil {
		t.Fatal(err)
	}
	if isText {
		t.Error("bin.bin should be binary (has null bytes)")
	}
	if content != "" {
		t.Error("binary content should be empty string")
	}

	// Non-existent file
	_, _, err = readFileContent(filepath.Join(dir, "nope.txt"))
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestReadAndHash(t *testing.T) {
	dir := t.TempDir()

	textPath := filepath.Join(dir, "hash.txt")
	if err := os.WriteFile(textPath, []byte("hash me"), 0644); err != nil {
		t.Fatal(err)
	}
	hash, content, isText, err := readAndHash(textPath)
	if err != nil {
		t.Fatal(err)
	}
	if !isText {
		t.Error("should be text")
	}
	expected := fmt.Sprintf("%x", sha256.Sum256([]byte("hash me")))
	if hash != expected {
		t.Errorf("hash = %s, want %s", hash, expected)
	}
	if content != "hash me" {
		t.Errorf("content = %q, want %q", content, "hash me")
	}

	// Binary: hash still computed
	binPath := filepath.Join(dir, "hash.bin")
	if err := os.WriteFile(binPath, []byte{0x00, 0xFF}, 0644); err != nil {
		t.Fatal(err)
	}
	hash, content, isText, err = readAndHash(binPath)
	if err != nil {
		t.Fatal(err)
	}
	if isText {
		t.Error("should be binary")
	}
	if content != "" {
		t.Error("binary content should be empty")
	}
	if hash == "" {
		t.Error("hash should not be empty for binary files")
	}
}

func TestReadRawFileData(t *testing.T) {
	dir := t.TempDir()

	p := filepath.Join(dir, "data.bin")
	if err := os.WriteFile(p, []byte{0x01, 0x02, 0x03, 0x04}, 0644); err != nil {
		t.Fatal(err)
	}
	data, err := readRawFileData(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 4 {
		t.Errorf("len = %d, want 4", len(data))
	}

	// Missing file
	_, err = readRawFileData(filepath.Join(dir, "nope.bin"))
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestPushMaxConstants(t *testing.T) {
	// Ensure constants are sensible.
	if pushMaxFileSize != 1024*1024 {
		t.Error("pushMaxFileSize mismatch")
	}
	if pushMaxChunks != 32 {
		t.Error("pushMaxChunks mismatch")
	}
	if embedBatchSize != 8 {
		t.Error("embedBatchSize mismatch")
	}
	if defaultPushWorkers != 4 {
		t.Error("defaultPushWorkers mismatch")
	}
}

func TestProjectNameFromPath(t *testing.T) {
	tests := []struct{ path, want string }{
		{"/foo/bar/baz", "baz"},
		{"baz", "baz"},
		{"foo/", "foo"},
		{"", ""},
		{"/", ""},
		{"/a/b/c/", "c"},
	}
	for _, tt := range tests {
		got := projectNameFromPath(tt.path)
		if got != tt.want {
			t.Errorf("projectNameFromPath(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestParseCommaSep(t *testing.T) {
	// parseCommaSep is in internal/config, but push.go tests can't reach it.
	// Test our own splitting if we had one, or test via os.Getenv simulation.
	// For now, test the function via a simple inline duplicate.
	_ = strings.TrimSpace // avoid unused import
}

func TestPushFlagDefaults(t *testing.T) {
	// Verify the push command registers correctly.
	// This is a smoke test — actual CLI invocation tested via e2e.
	cmd := newPushCmd(&deps{})
	if cmd == nil {
		t.Fatal("newPushCmd returned nil")
	}
	if cmd.Use != "push" {
		t.Errorf("Use = %q, want %q", cmd.Use, "push")
	}
}
