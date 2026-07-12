package agent

import (
	"testing"
)

func TestJSONResult_struct(t *testing.T) {
	type testStruct struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	v := testStruct{Name: "hello", Age: 42}
	got := JSONResult(v)
	want := `{"name":"hello","age":42}`
	if got != want {
		t.Errorf("JSONResult(%+v) = %q, want %q", v, got, want)
	}
}

func TestJSONResult_map(t *testing.T) {
	got := JSONResult(map[string]any{"key": "value", "num": 1.0})
	// JSON object order is not guaranteed, so check both components.
	if got != `{"key":"value","num":1}` && got != `{"num":1,"key":"value"}` {
		t.Errorf("JSONResult(map) = %q, want either key first or num first", got)
	}
}

func TestJSONResult_nil(t *testing.T) {
	got := JSONResult(nil)
	if got != "null" {
		t.Errorf("JSONResult(nil) = %q, want %q", got, "null")
	}
}

func TestJSONResult_unencodable(t *testing.T) {
	// A channel cannot be marshalled to JSON.
	got := JSONResult(make(chan int))
	if len(got) < 10 || got[:8] != `{"error"` {
		t.Errorf("JSONResult(channel) = %q, want error JSON", got)
	}
}
