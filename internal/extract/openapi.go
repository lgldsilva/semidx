package extract

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

func init() {
	_ = Register(".yaml", extractOpenAPI)
	_ = Register(".yml", extractOpenAPI)
	_ = Register(".json", extractOpenAPI)
}

// extractOpenAPI checks whether data is an OpenAPI/Swagger spec (by looking for
// "openapi:" or "swagger:" in the first 200 bytes, case-insensitive). If it is,
// it extracts structured info (title, version, endpoints). Otherwise it returns
// the raw text as-is (graceful fallback for non-spec YAML/JSON files).
func extractOpenAPI(data []byte) (string, error) {
	if !utf8.Valid(data) {
		return "", ErrNotText
	}

	// Quick content check: does the first 200 bytes look like an OpenAPI spec?
	checkLen := 200
	if len(data) < checkLen {
		checkLen = len(data)
	}
	header := strings.ToLower(string(data[:checkLen]))
	if !strings.Contains(header, "openapi:") && !strings.Contains(header, "swagger:") {
		return string(data), nil // not an OpenAPI spec, return raw text
	}

	// Try full JSON parse.
	var doc map[string]interface{}
	if err := json.Unmarshal(data, &doc); err == nil {
		return formatOpenAPIDoc(doc), nil
	}

	// Line-by-line YAML extraction (no YAML library dependency).
	return extractYAMLOpenAPI(string(data)), nil
}

// formatOpenAPIDoc formats a parsed JSON OpenAPI document into structured text.
func formatOpenAPIDoc(doc map[string]interface{}) string {
	var b strings.Builder

	if info, ok := doc["info"].(map[string]interface{}); ok {
		if title := getString(info, "title"); title != "" {
			fmt.Fprintf(&b, "Title: %s\n", title)
		}
		if version := getString(info, "version"); version != "" {
			fmt.Fprintf(&b, "Version: %s\n", version)
		}
	}

	if paths, ok := doc["paths"].(map[string]interface{}); ok {
		endpoints := collectEndpoints(paths)
		if len(endpoints) > 0 {
			b.WriteString("Endpoints: ")
			b.WriteString(strings.Join(endpoints, ", "))
			b.WriteByte('\n')
		}
	}

	result := strings.TrimSpace(b.String())
	if result == "" {
		result = "No structured OpenAPI information found"
	}
	return result
}

// getString safely extracts a string value from a nested map, returning "" if
// absent or of the wrong type.
func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// collectEndpoints walks the paths section of an OpenAPI doc and returns a
// sorted list of "METHOD /path" strings for known HTTP methods only.
func collectEndpoints(paths map[string]interface{}) []string {
	var endpoints []string
	pathNames := make([]string, 0, len(paths))
	for p := range paths {
		pathNames = append(pathNames, p)
	}
	sort.Strings(pathNames)
	for _, p := range pathNames {
		if methods, ok := paths[p].(map[string]interface{}); ok {
			for m := range methods {
				if isHTTPMethod(m) {
					endpoints = append(endpoints, strings.ToUpper(m)+" "+p)
				}
			}
		}
	}
	return endpoints
}

// extractYAMLOpenAPI does a lightweight, line-by-line extraction of info and
// paths from a YAML-formatted OpenAPI spec. It tracks indentation to determine
// when we are inside the info: or paths: section.
func extractYAMLOpenAPI(text string) string {
	lines := strings.Split(text, "\n")
	var title, version string
	var endpoints []string
	state := newOpenAPIState()

	for _, line := range lines {
		processYAMLLine(state, line, &title, &version, &endpoints)
	}

	return formatOpenAPISummary(title, version, endpoints)
}

// processYAMLLine handles one raw line for the YAML OpenAPI extractor.
// It skips blanks/comments, updates state, extracts info fields at indent 2,
// tracks current path, and collects method endpoints.
func processYAMLLine(state *openapiState, line string, title, version *string, endpoints *[]string) {
	trimmed := strings.TrimSpace(line)
	if shouldSkipLine(trimmed) {
		return
	}
	indent := lineIndent(line)
	ep := state.processLine(trimmed, indent)
	if state.inInfo && indent == 2 {
		info := extractOpenAPIInfoField(trimmed)
		if info.title != "" {
			*title = info.title
		}
		if info.version != "" {
			*version = info.version
		}
	}
	if state.inPaths && indent == 2 && strings.HasPrefix(trimmed, "/") {
		state.currentPath = strings.TrimSuffix(trimmed, ":")
	}
	if ep != "" {
		*endpoints = append(*endpoints, ep)
	}
}

// shouldSkipLine reports whether a trimmed line should be ignored (blank or comment).
func shouldSkipLine(trimmed string) bool {
	return trimmed == "" || strings.HasPrefix(trimmed, "#")
}

// lineIndent returns the leading whitespace count (spaces only, as YAML uses spaces).
func lineIndent(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

type openapiState struct {
	inInfo      bool
	inPaths     bool
	currentPath string
}

func newOpenAPIState() *openapiState { return &openapiState{} }

func (s *openapiState) processLine(trimmed string, indent int) string {
	switch {
	case indent == 0 && strings.HasPrefix(trimmed, "info:"):
		s.inInfo = true
		s.inPaths = false
		s.currentPath = ""
	case indent == 0 && strings.HasPrefix(trimmed, "paths:"):
		s.inInfo = false
		s.inPaths = true
		s.currentPath = ""
	case indent == 0 && !strings.HasPrefix(trimmed, "openapi:") && !strings.HasPrefix(trimmed, "swagger:"):
		s.inInfo = false
		s.inPaths = false
	}

	if s.inPaths && indent == 4 && s.currentPath != "" {
		method := strings.TrimSuffix(trimmed, ":")
		if isHTTPMethod(method) {
			return strings.ToUpper(method) + " " + s.currentPath
		}
	}

	if s.inPaths && indent > 0 && indent < 2 {
		s.inPaths = false
		s.currentPath = ""
	}

	return ""
}

type infoFields struct{ title, version string }

func extractOpenAPIInfoField(trimmed string) infoFields {
	var res infoFields
	if strings.HasPrefix(trimmed, "title:") {
		t := strings.TrimSpace(strings.TrimPrefix(trimmed, "title:"))
		res.title = strings.Trim(t, `"'`)
	}
	if strings.HasPrefix(trimmed, "version:") {
		v := strings.TrimSpace(strings.TrimPrefix(trimmed, "version:"))
		res.version = strings.Trim(v, `"'`)
	}
	return res
}

func formatOpenAPISummary(title, version string, endpoints []string) string {
	var b strings.Builder
	if title != "" {
		fmt.Fprintf(&b, "Title: %s\n", title)
	}
	if version != "" {
		fmt.Fprintf(&b, "Version: %s\n", version)
	}
	if len(endpoints) > 0 {
		b.WriteString("Endpoints: ")
		b.WriteString(strings.Join(endpoints, ", "))
		b.WriteByte('\n')
	}

	result := strings.TrimSpace(b.String())
	if result == "" {
		result = "No structured OpenAPI information found"
	}
	return result
}

// isHTTPMethod reports whether a lowercase string is a known HTTP method.
func isHTTPMethod(m string) bool {
	switch strings.ToLower(m) {
	case "get", "post", "put", "patch", "delete", "head", "options", "trace":
		return true
	default:
		return false
	}
}
