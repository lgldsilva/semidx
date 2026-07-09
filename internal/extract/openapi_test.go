package extract

import (
	"strings"
	"testing"
)

// TestExtractOpenAPIJSON Passthrough: the content check looks for YAML-style
// "openapi:" but JSON has "openapi": with a quote before the colon,
// so JSON openapi specs currently pass through as raw text.
func TestExtractOpenAPIJSONPassthrough(t *testing.T) {
	t.Parallel()
	rawStr := `{
  "openapi": "3.0.0",
  "info": {"title": "Demo API"},
  "paths": {"/items": {"get": {}}}
}`
	got, err := extractOpenAPI([]byte(rawStr))
	if err != nil {
		t.Fatalf("extractOpenAPI: %v", err)
	}
	if got != rawStr {
		t.Fatalf("expected raw passthrough, got: %s", got)
	}
}

// formatOpenAPIDoc coverage via direct call (unexported, same package).
func TestFormatOpenAPIDocFull(t *testing.T) {
	t.Parallel()
	doc := map[string]interface{}{
		"info":  map[string]interface{}{"title": "Svc", "version": "9"},
		"paths": map[string]interface{}{"/x": map[string]interface{}{"get": map[string]interface{}{}}},
	}
	got := formatOpenAPIDoc(doc)
	if !strings.Contains(got, "Title: Svc") || !strings.Contains(got, "Version: 9") ||
		!strings.Contains(got, "GET /x") {
		t.Fatalf("formatOpenAPIDoc = %q", got)
	}
}

func TestExtractYAMLOpenAPIDirect(t *testing.T) {
	t.Parallel()
	got := extractYAMLOpenAPI("openapi: 3.0.0\ninfo:\n  title: Y\n  version: '1'\npaths:\n  /a:\n    get:\n")
	if !strings.Contains(got, "Title: Y") || !strings.Contains(got, "GET /a") {
		t.Fatalf("extractYAMLOpenAPI = %q", got)
	}
}

func TestExtractOpenAPIYAML(t *testing.T) {
	t.Parallel()
	raw := `openapi: 3.0.0
info:
  title: YAML Spec
  version: "2"
paths:
  /users:
    get:
    post:
`
	got, err := extractOpenAPI([]byte(raw))
	if err != nil {
		t.Fatalf("extractOpenAPI yaml: %v", err)
	}
	for _, want := range []string{"Title: YAML Spec", "Version: 2", "GET /users", "POST /users"} {
		if !strings.Contains(got, want) {
			t.Errorf("yaml result missing %q:\n%s", want, got)
		}
	}
}

func TestExtractOpenAPINonSpecPassthrough(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"name":"config","value":1}`)
	got, err := extractOpenAPI(raw)
	if err != nil {
		t.Fatalf("extractOpenAPI: %v", err)
	}
	if got != string(raw) {
		t.Fatalf("got %q, want passthrough", got)
	}
}

func TestFormatOpenAPIDocEmpty(t *testing.T) {
	t.Parallel()
	if got := formatOpenAPIDoc(map[string]interface{}{}); got != "No structured OpenAPI information found" {
		t.Fatalf("got %q", got)
	}
}

func TestOpenAPIHelpers(t *testing.T) {
	t.Parallel()
	if !isHTTPMethod("get") || isHTTPMethod("foo") {
		t.Fatal("isHTTPMethod mismatch")
	}
	m := map[string]interface{}{"title": "x", "n": 1}
	if getString(m, "title") != "x" || getString(m, "missing") != "" {
		t.Fatal("getString failed")
	}
	endpoints := collectEndpoints(map[string]interface{}{
		"/z": map[string]interface{}{"get": map[string]interface{}{}},
		"/a": map[string]interface{}{"post": map[string]interface{}{}, "bogus": map[string]interface{}{}},
	})
	if len(endpoints) != 2 || endpoints[0] != "POST /a" || endpoints[1] != "GET /z" {
		t.Fatalf("collectEndpoints = %#v", endpoints)
	}
}
