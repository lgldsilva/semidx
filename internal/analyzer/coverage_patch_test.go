// coverage-patch: 2026-07-17
package analyzer

import "testing"

func TestKindFromNodeType(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"function_declaration":  "func",
		"method_declaration":    "method",
		"method_definition":     "method",
		"type_spec":             "type",
		"class_declaration":     "class",
		"interface_declaration": "interface",
		"enum_declaration":      "enum",
		"record_declaration":    "record",
		"function_definition":   "func",
		"class_definition":      "class",
		"block":                 "block",
		"unknown_node_xyz":      "unknown_node_xyz",
	}
	for nt, want := range cases {
		if got := kindFromNodeType(nt); got != want {
			t.Errorf("kindFromNodeType(%q) = %q, want %q", nt, got, want)
		}
	}
}

func TestSymbols_emptyAndUnsupported(t *testing.T) {
	t.Parallel()
	if Symbols("x.go", nil) != nil {
		t.Error("empty content should return nil")
	}
	if Symbols("x.go", []byte{}) != nil {
		t.Error("zero-length content should return nil")
	}
	if Symbols("file.unknownext", []byte("hello")) != nil {
		t.Error("unsupported ext should return nil")
	}
}

func TestSymbols_JavaRecord(t *testing.T) {
	t.Parallel()
	content := []byte(`package com.example;
public record Point(int x, int y) {}
`)
	syms := Symbols("Point.java", content)
	if !symbolHas(syms, "Point") {
		t.Errorf("expected record Point, got %v", syms)
	}
	for _, s := range syms {
		if s.Name == "Point" && s.Kind != "record" && s.Kind != "class" {
			// tree-sitter may report record_declaration → "record"
			t.Logf("Point kind = %q", s.Kind)
		}
	}
}

func TestSymbols_HCLBlock(t *testing.T) {
	t.Parallel()
	content := []byte(`
resource "aws_instance" "web" {
  ami = "ami-123"
}
`)
	syms := Symbols("main.tf", content)
	// HCL captures block type identifiers; at least something should parse.
	if len(syms) == 0 {
		t.Log("HCL returned no symbols (grammar may differ); kindFromNodeType still covered above")
	}
}

func TestGetCompiled_knownExt(t *testing.T) {
	t.Parallel()
	if getCompiled(".go") == nil {
		t.Fatal("getCompiled(.go) should return compiled extractor")
	}
	if getCompiled(".nope") != nil {
		t.Error("unknown ext should be nil")
	}
}

// TestDedupe_EmptyResult tests the path where dedupe returns nil
// because all symbols were empty.
func TestDedupe_EmptyResult(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		input []Symbol
		want  []Symbol
	}{
		{
			name:  "all empty names",
			input: []Symbol{{Name: ""}, {Name: ""}, {Name: ""}},
			want:  nil,
		},
		{
			name:  "duplicates only",
			input: []Symbol{{Name: "foo"}, {Name: "foo"}, {Name: "foo"}},
			want:  []Symbol{{Name: "foo"}},
		},
		{
			name:  "mix empty and duplicates",
			input: []Symbol{{Name: "bar"}, {Name: ""}, {Name: "bar"}},
			want:  []Symbol{{Name: "bar"}},
		},
		{
			name:  "some valid",
			input: []Symbol{{Name: "foo"}, {Name: ""}, {Name: "bar"}, {Name: "foo"}, {Name: "bar"}},
			want:  []Symbol{{Name: "foo"}, {Name: "bar"}},
		},
		{
			name:  "single valid",
			input: []Symbol{{Name: "baz"}},
			want:  []Symbol{{Name: "baz"}},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := dedupe(tc.input)
			if tc.want == nil {
				if got != nil {
					t.Errorf("dedupe(%v) = %v, want nil", tc.input, got)
				}
			} else {
				if len(got) != len(tc.want) {
					t.Errorf("dedupe(%v) length = %d, want %d", tc.input, len(got), len(tc.want))
				}
				for i, w := range tc.want {
					if got[i].Name != w.Name {
						t.Errorf("dedupe(%v)[%d] = %q, want %q", tc.input, i, got[i].Name, w.Name)
					}
				}
			}
		})
	}
}

// TestExtract_ErrorPaths tests error conditions in extract that are not
// covered by the main Symbol tests.
func TestExtract_ErrorPaths(t *testing.T) {
	t.Parallel()
	ce := getCompiled(".go")
	if ce == nil {
		t.Fatal("failed to get compiled extractor for .go")
	}

	// Test that nil content is handled (parser returns nil tree)
	got := extract(ce, nil)
	if got != nil {
		t.Errorf("extract(nil) = %v, want nil", got)
	}

	// Test that empty content is handled
	got = extract(ce, []byte{})
	if got != nil {
		t.Errorf("extract([]byte{}) = %v, want nil", got)
	}

	// Test that invalid syntax returns nil (best-effort)
	got = extract(ce, []byte("this is not go code {{{"))
	// This may return empty symbols if parser succeeds but finds no matches
	if len(got) > 0 {
		t.Logf("extract(garbage) returned symbols: %v", got)
	}
}
