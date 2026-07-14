package agent

import (
	"encoding/json"
	"strings"
)

// redactedPlaceholder replaces argument values whose keys look like secrets.
const redactedPlaceholder = "[redacted]"

// secretKeyHints flags tool-argument keys that look like credentials
// (case-insensitive substring match). config.IsSecret exists but is tailored
// to SEMIDX_* env names (it lacks passwd/credential/authorization), so stream
// sanitization uses this broader, local heuristic instead.
var secretKeyHints = []string{
	"key", "token", "secret", "password", "passwd", "dsn", "credential", "authorization",
}

// isSecretArgKey reports whether a JSON argument key hints at a credential.
func isSecretArgKey(key string) bool {
	k := strings.ToLower(key)
	for _, hint := range secretKeyHints {
		if strings.Contains(k, hint) {
			return true
		}
	}
	return false
}

// SanitizeToolArgs prepares a tool call's raw JSON arguments for exposure on a
// user-facing stream: every string value is truncated to maxLen runes and any
// value whose key hints at a credential is replaced with "[redacted]"
// (recursively, so nested objects and arrays are covered too). Invalid JSON
// degrades to the truncated raw text encoded as a JSON string, so the result
// is always valid JSON. maxLen <= 0 disables truncation.
func SanitizeToolArgs(argsJSON string, maxLen int) json.RawMessage {
	if !json.Valid([]byte(argsJSON)) {
		return rawJSONString(argsJSON, maxLen)
	}
	// UseNumber keeps numeric literals verbatim (no float64 round-trip that
	// could rewrite big ints in the echoed args).
	dec := json.NewDecoder(strings.NewReader(argsJSON))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return rawJSONString(argsJSON, maxLen)
	}
	out, err := json.Marshal(sanitizeArgValue(v, maxLen))
	if err != nil {
		return rawJSONString(argsJSON, maxLen)
	}
	return out
}

// sanitizeArgValue walks a decoded JSON value redacting secret-keyed values
// and truncating string leaves.
func sanitizeArgValue(v any, maxLen int) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if isSecretArgKey(k) {
				out[k] = redactedPlaceholder
				continue
			}
			out[k] = sanitizeArgValue(val, maxLen)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = sanitizeArgValue(val, maxLen)
		}
		return out
	case string:
		s, _ := truncateRunes(t, maxLen)
		return s
	default:
		return v
	}
}

// rawJSONString encodes s, truncated to maxLen runes, as a JSON string.
func rawJSONString(s string, maxLen int) json.RawMessage {
	t, _ := truncateRunes(s, maxLen)
	out, err := json.Marshal(t)
	if err != nil { // unreachable: a Go string always marshals
		return json.RawMessage(`""`)
	}
	return out
}

// PreviewToolResult bounds a tool result for a user-facing stream preview,
// truncating at maxLen runes and reporting whether it cut anything.
// maxLen <= 0 disables truncation.
func PreviewToolResult(result string, maxLen int) (string, bool) {
	return truncateRunes(result, maxLen)
}

// truncateRunes cuts s after maxLen runes; the bool reports whether it cut.
func truncateRunes(s string, maxLen int) (string, bool) {
	if maxLen <= 0 {
		return s, false
	}
	count := 0
	for i := range s {
		if count == maxLen {
			return s[:i], true
		}
		count++
	}
	return s, false
}
