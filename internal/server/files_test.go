package server

import (
	"strings"
	"testing"
)

func TestValidatePreEmbeddedChunks(t *testing.T) {
	chunks := []embeddedChunk{{StartLine: 1, EndLine: 2, Content: "hi", Embedding: []float32{1, 0, 0}}}
	if err := validatePreEmbeddedChunks("src/a.go", chunks, 3, "m"); err != nil {
		t.Fatalf("valid: %v", err)
	}
	if err := validatePreEmbeddedChunks(".env", chunks, 3, "m"); err == nil {
		t.Fatal("sensitive path should fail")
	}
	bad := []embeddedChunk{{Embedding: []float32{1}}}
	if err := validatePreEmbeddedChunks("a.go", bad, 3, "m"); err == nil {
		t.Fatal("dims mismatch")
	}
}

func TestValidateBatchBody(t *testing.T) {
	if err := validateBatchBody(&batchRequestBody{
		Files:  []batchFileInput{{Path: "../x"}},
		Delete: []string{"ok.go"},
	}); err == nil || !strings.Contains(err.Error(), "invalid path") {
		t.Fatalf("traversal: %v", err)
	}
	if err := validateBatchBody(&batchRequestBody{
		Files: []batchFileInput{{Path: "main.go", Content: "x"}},
	}); err != nil {
		t.Fatalf("valid: %v", err)
	}
}
