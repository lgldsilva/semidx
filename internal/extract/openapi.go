package extract

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

func init() {
	Register(".yaml", extractOpenAPI)
	Register(".yml", extractOpenAPI)
	Register(".json", extractOpenAPI)
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
		if title, ok := info["title"].(string); ok {
			fmt.Fprintf(&b, "Title: %s\n", title)
		}
		if version, ok := info["version"].(string); ok {
			fmt.Fprintf(&b, "Version: %s\n", version)
		}
	}

	if paths, ok := doc["paths"].(map[string]interface{}); ok {
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

// extractYAMLOpenAPI does a lightweight, line-by-line extraction of info and
// paths from a YAML-formatted OpenAPI spec. It tracks indentation to determine
// when we are inside the info: or paths: section.
func extractYAMLOpenAPI(text string) string {
	lines := strings.Split(text, "\n")
	var title, version string
	var endpoints []string
	inInfo := false
	inPaths := false
	currentPath := ""

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		indent := len(line) - len(strings.TrimLeft(line, " "))

		switch {
		case indent == 0 && strings.HasPrefix(trimmed, "info:"):
			inInfo = true
			inPaths = false
			currentPath = ""
			continue
		case indent == 0 && strings.HasPrefix(trimmed, "paths:"):
			inInfo = false
			inPaths = true
			currentPath = ""
			continue
		case indent == 0 && !strings.HasPrefix(trimmed, "openapi:") && !strings.HasPrefix(trimmed, "swagger:"):
			inInfo = false
			inPaths = false
		}

		if inInfo && indent == 2 {
			if strings.HasPrefix(trimmed, "title:") {
				title = strings.TrimSpace(strings.TrimPrefix(trimmed, "title:"))
				title = strings.Trim(title, `"'`)
			}
			if strings.HasPrefix(trimmed, "version:") {
				version = strings.TrimSpace(strings.TrimPrefix(trimmed, "version:"))
				version = strings.Trim(version, `"'`)
			}
		}

		if inPaths && indent == 2 {
			if strings.HasPrefix(trimmed, "/") {
				currentPath = strings.TrimSuffix(trimmed, ":")
				continue
			}
		}

		if inPaths && indent == 4 && currentPath != "" {
			method := strings.TrimSuffix(trimmed, ":")
			if isHTTPMethod(method) {
				endpoints = append(endpoints, strings.ToUpper(method)+" "+currentPath)
			}
		}

		if inPaths && indent > 0 && indent < 2 {
			inPaths = false
			currentPath = ""
		}
	}

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
