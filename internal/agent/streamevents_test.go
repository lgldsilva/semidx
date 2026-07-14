package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"pgregory.net/rapid"
)

func TestSanitizeToolArgs_examples(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{
			name: "secret keys redacted, plain keys kept",
			in:   `{"query":"auth","api_key":"sk-123","top_k":5}`,
			max:  100,
			want: `{"api_key":"[redacted]","query":"auth","top_k":5}`,
		},
		{
			name: "nested objects and arrays sanitized",
			in:   `{"cfg":{"Password":"hunter2","name":"x"},"list":[{"authorization":"Bearer y"}]}`,
			max:  100,
			want: `{"cfg":{"Password":"[redacted]","name":"x"},"list":[{"authorization":"[redacted]"}]}`,
		},
		{
			name: "long string values truncated",
			in:   `{"query":"abcdefghij"}`,
			max:  4,
			want: `{"query":"abcd"}`,
		},
		{
			name: "invalid JSON becomes a truncated JSON string",
			in:   `{"broken":`,
			max:  6,
			want: `"{\"brok"`,
		},
		{
			name: "big integers survive verbatim",
			in:   `{"n":12345678901234567890}`,
			max:  10,
			want: `{"n":12345678901234567890}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeToolArgs(tc.in, tc.max)
			if string(got) != tc.want {
				t.Errorf("SanitizeToolArgs(%q, %d) = %s, want %s", tc.in, tc.max, got, tc.want)
			}
		})
	}
}

func TestPreviewToolResult_examples(t *testing.T) {
	if p, tr := PreviewToolResult("hello", 500); p != "hello" || tr {
		t.Errorf("short result must pass through, got (%q, %v)", p, tr)
	}
	// Truncation is rune-aware, never mid-codepoint.
	if p, tr := PreviewToolResult("héllo", 2); p != "hé" || !tr {
		t.Errorf("rune truncation = (%q, %v), want (\"hé\", true)", p, tr)
	}
	if p, tr := PreviewToolResult("anything", 0); p != "anything" || tr {
		t.Errorf("maxLen<=0 must disable truncation, got (%q, %v)", p, tr)
	}
}

// secretishKey draws a key that must match the secret heuristic (a hint
// embedded in random casing between random padding).
func secretishKey(t *rapid.T, label string) string {
	hint := rapid.SampledFrom(secretKeyHints).Draw(t, label+"_hint")
	if rapid.Bool().Draw(t, label+"_upper") {
		hint = strings.ToUpper(hint)
	}
	prefix := rapid.StringMatching(`[a-z_]{0,5}`).Draw(t, label+"_pre")
	suffix := rapid.StringMatching(`[a-z_]{0,5}`).Draw(t, label+"_suf")
	return prefix + hint + suffix
}

// plainKey draws a key that never matches the secret heuristic.
func plainKey(t *rapid.T, label string) string {
	return rapid.StringMatching(`[qwm_]{1,8}`).Filter(func(s string) bool {
		return !isSecretArgKey(s)
	}).Draw(t, label)
}

// TestSanitizeToolArgs_secretKeysAlwaysRedacted: any value under a key that
// matches the secret heuristic comes out as "[redacted]", at any depth, and
// non-secret string values are rune-truncated prefixes of the originals.
func TestSanitizeToolArgs_secretKeysAlwaysRedacted(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		maxLen := rapid.IntRange(1, 50).Draw(t, "maxLen")
		secretKey := secretishKey(t, "secret")
		okKey := plainKey(t, "plain")
		secretVal := rapid.String().Draw(t, "secretVal")
		okVal := rapid.String().Draw(t, "okVal")

		in, err := json.Marshal(map[string]any{
			secretKey: secretVal,
			okKey:     okVal,
			"nested":  map[string]any{secretKey: secretVal, okKey: okVal},
			"list":    []any{map[string]any{secretKey: secretVal}},
		})
		if err != nil {
			t.Fatalf("marshal input: %v", err)
		}

		out := SanitizeToolArgs(string(in), maxLen)
		var got map[string]any
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("output is not valid JSON: %v (%s)", err, out)
		}

		checkRedacted := func(m map[string]any) {
			if m[secretKey] != redactedPlaceholder {
				t.Fatalf("secret key %q = %v, want %q", secretKey, m[secretKey], redactedPlaceholder)
			}
			if v, ok := m[okKey]; ok {
				s, isStr := v.(string)
				if !isStr {
					t.Fatalf("plain key %q should stay a string, got %T", okKey, v)
				}
				if utf8.RuneCountInString(s) > maxLen {
					t.Fatalf("plain value exceeds maxLen: %d > %d", utf8.RuneCountInString(s), maxLen)
				}
				if !strings.HasPrefix(okVal, s) {
					t.Fatalf("truncated value %q is not a prefix of %q", s, okVal)
				}
			}
		}
		checkRedacted(got)
		checkRedacted(got["nested"].(map[string]any))
		checkRedacted(got["list"].([]any)[0].(map[string]any))
	})
}

// TestSanitizeToolArgs_neverGrowsBeyondQuoteOverhead: with values at least as
// long as the placeholder, the sanitized output never exceeds the compact
// re-encoding of the input — redaction and truncation only shrink.
func TestSanitizeToolArgs_neverGrowsBeyondQuoteOverhead(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		maxLen := rapid.IntRange(1, 200).Draw(t, "maxLen")
		m := rapid.MapOfN(
			rapid.StringMatching(`[a-z_]{1,10}`),
			// ≥ len("[redacted]") runes so redaction can only shrink a value.
			rapid.StringN(len(redactedPlaceholder), 64, -1),
			1, 8,
		).Draw(t, "args")
		in, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("marshal input: %v", err)
		}

		out := SanitizeToolArgs(string(in), maxLen)
		if !json.Valid(out) {
			t.Fatalf("output is not valid JSON: %s", out)
		}
		if len(out) > len(in) {
			t.Fatalf("sanitize grew the payload: %d > %d (in=%s out=%s)", len(out), len(in), in, out)
		}
	})
}

// TestSanitizeToolArgs_invalidJSONNeverPanics: arbitrary (usually invalid)
// input never panics and degrades to a valid JSON string whose content is a
// rune-truncated prefix of the raw input.
func TestSanitizeToolArgs_invalidJSONNeverPanics(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		raw := rapid.String().Draw(t, "raw")
		maxLen := rapid.IntRange(-1, 100).Draw(t, "maxLen")

		out := SanitizeToolArgs(raw, maxLen)
		if !json.Valid(out) {
			t.Fatalf("output is not valid JSON: %s", out)
		}
		if json.Valid([]byte(raw)) {
			return // valid input takes the structured path, covered elsewhere
		}
		var s string
		if err := json.Unmarshal(out, &s); err != nil {
			t.Fatalf("invalid input must yield a JSON string, got %s", out)
		}
		if !strings.HasPrefix(raw, s) {
			t.Fatalf("degraded value %q is not a prefix of the raw input %q", s, raw)
		}
		if maxLen > 0 && utf8.RuneCountInString(s) > maxLen {
			t.Fatalf("degraded value exceeds maxLen: %d > %d", utf8.RuneCountInString(s), maxLen)
		}
	})
}

// TestPreviewToolResult_properties: the preview is always a prefix, never over
// maxLen runes, and the truncated flag is exact.
func TestPreviewToolResult_properties(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := rapid.String().Draw(t, "result")
		maxLen := rapid.IntRange(1, 600).Draw(t, "maxLen")

		preview, truncated := PreviewToolResult(s, maxLen)
		if !strings.HasPrefix(s, preview) {
			t.Fatalf("preview %q is not a prefix of %q", preview, s)
		}
		n := utf8.RuneCountInString(s)
		if truncated != (n > maxLen) {
			t.Fatalf("truncated = %v, want %v (runes=%d maxLen=%d)", truncated, n > maxLen, n, maxLen)
		}
		if got := utf8.RuneCountInString(preview); got > maxLen {
			t.Fatalf("preview has %d runes, max %d", got, maxLen)
		}
		if !truncated && preview != s {
			t.Fatalf("untruncated preview must equal the input")
		}
	})
}
