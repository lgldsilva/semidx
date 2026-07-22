package codeintel

import (
	"testing"

	"github.com/lgldsilva/semidx/internal/analyzer"
)

func TestLookupSymbolAtLine(t *testing.T) {
	syms := []analyzer.Symbol{
		{Name: "func1", Kind: "function", StartLine: 10, EndLine: 20},
		{Name: "func2", Kind: "function", StartLine: 25, EndLine: 35},
		{Name: "func3", Kind: "function", StartLine: 40, EndLine: 50},
	}

	tests := []struct {
		name     string
		line     int
		wantName string
	}{
		{
			name:     "exact match at start",
			line:     10,
			wantName: "func1",
		},
		{
			name:     "exact match in middle",
			line:     15,
			wantName: "func1",
		},
		{
			name:     "exact match at end",
			line:     20,
			wantName: "func1",
		},
		{
			name:     "nearest symbol above",
			line:     22,
			wantName: "func1",
		},
		{
			name:     "line before first symbol",
			line:     5,
			wantName: "func1",
		},
		{
			name:     "line after last symbol",
			line:     55,
			wantName: "func3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := lookupSymbolAtLine(syms, tt.line)
			if got == nil || got.Name != tt.wantName {
				t.Errorf("lookupSymbolAtLine(%d) = %v, want symbol named %s", tt.line, got, tt.wantName)
			}
		})
	}
}

func TestLookupSymbolAtLine_SingleSymbol(t *testing.T) {
	syms := []analyzer.Symbol{
		{Name: "onlyFunc", Kind: "function", StartLine: 10, EndLine: 20},
	}

	// Line after should still return the only symbol
	got := lookupSymbolAtLine(syms, 100)
	if got == nil || got.Name != "onlyFunc" {
		t.Errorf("lookupSymbolAtLine(100) with single symbol = %v, want onlyFunc", got)
	}
}

func TestFindDirectCallers(t *testing.T) {
	graph := map[string][]string{
		"cmd/main.go":         {"internal/auth/", "internal/store/"},
		"internal/api/api.go": {"internal/auth/", "internal/models/"},
		"internal/web/web.go": {"internal/api/"},
	}

	tests := []struct {
		name    string
		fileDir string
		want    []string
	}{
		{
			name:    "auth package has two callers",
			fileDir: "internal/auth/",
			want:    []string{"cmd/main.go", "internal/api/api.go"},
		},
		{
			name:    "api package has one caller",
			fileDir: "internal/api/",
			want:    []string{"internal/web/web.go"},
		},
		{
			name:    "no callers",
			fileDir: "internal/unused/",
			want:    []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findDirectCallers(graph, tt.fileDir)
			if len(got) != len(tt.want) {
				t.Errorf("findDirectCallers() returned %d items, want %d", len(got), len(tt.want))
				return
			}
			gotMap := make(map[string]bool)
			for _, g := range got {
				gotMap[g] = true
			}
			for _, w := range tt.want {
				if !gotMap[w] {
					t.Errorf("findDirectCallers() missing %q in result", w)
				}
			}
		})
	}
}

func TestFindDirectCallers_EmptyGraph(t *testing.T) {
	graph := map[string][]string{}
	result := findDirectCallers(graph, "any/dir/")
	if len(result) != 0 {
		t.Errorf("findDirectCallers(empty graph) = %v, want empty slice", result)
	}
}

func TestCollectTransitiveCallers(t *testing.T) {
	graph := map[string][]string{
		"cmd/main.go":         {"internal/auth/"},
		"internal/api/api.go": {"internal/auth/"},
		"internal/web/web.go": {"internal/api/"},
	}

	directCallers := []string{"internal/api/api.go"}
	excludeFile := "internal/auth/token.go"

	result := collectTransitiveCallers(graph, directCallers, excludeFile)

	// internal/web/web.go imports internal/api/, so it's a transitive caller
	if len(result) != 1 || result[0] != "internal/web/web.go" {
		t.Errorf("collectTransitiveCallers() = %v, want [internal/web/web.go]", result)
	}
}

func TestCollectTransitiveCallers_NoTransitive(t *testing.T) {
	graph := map[string][]string{
		"internal/api/api.go": {"internal/auth/"},
	}

	directCallers := []string{"internal/api/api.go"}
	excludeFile := "internal/auth/token.go"

	result := collectTransitiveCallers(graph, directCallers, excludeFile)

	if len(result) != 0 {
		t.Errorf("collectTransitiveCallers() with no transitive = %v, want empty", result)
	}
}

func TestCollectTransitiveCallers_ExcludesDirectCallers(t *testing.T) {
	graph := map[string][]string{
		"internal/api/api.go": {"internal/auth/"},
		"internal/web/web.go": {"internal/api/"},
		"cmd/main.go":         {"internal/api/"},
	}

	directCallers := []string{"internal/api/api.go"}
	excludeFile := "internal/auth/token.go"

	result := collectTransitiveCallers(graph, directCallers, excludeFile)

	// Should not include internal/api/api.go (it's a direct caller)
	for _, r := range result {
		if r == "internal/api/api.go" {
			t.Errorf("collectTransitiveCallers() includes direct caller: %v", result)
		}
	}
}

func TestCollectTransitiveCallers_ExcludesFile(t *testing.T) {
	graph := map[string][]string{
		"internal/auth/token.go": {"internal/models/"},
		"internal/api/api.go":    {"internal/auth/"},
		"internal/web/web.go":    {"internal/api/"},
	}

	directCallers := []string{"internal/api/api.go"}
	excludeFile := "internal/auth/token.go"

	result := collectTransitiveCallers(graph, directCallers, excludeFile)

	// Should exclude internal/auth/token.go even if it appears in the graph
	for _, r := range result {
		if r == "internal/auth/token.go" {
			t.Errorf("collectTransitiveCallers() includes excluded file: %v", result)
		}
	}
}
