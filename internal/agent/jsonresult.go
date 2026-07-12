package agent

import "encoding/json"

// JSONResult marshals v to compact JSON. Returns a readable error string
// on failure. All tools should return JSONResult(...) for consistent
// token-friendly output.
func JSONResult(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return `{"error":"failed to marshal result: ` + err.Error() + `"}`
	}
	return string(data)
}
