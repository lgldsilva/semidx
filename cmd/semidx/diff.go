package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

// symbol describes a code symbol extracted from source.
type symbol struct {
	Name     string
	FilePath string
	Line     int
	Kind     string // "func", "type", "method", "const", "var"
	// Signature is the full declaration line (for detecting changes).
	Signature string
}

// symbolSet is a set of symbols keyed by "kind:name" for comparison.
type symbolSet map[string]*symbol

// Symbol diff types.
const (
	diffNew     = "new"
	diffRemoved = "removed"
	diffChanged = "changed"
)

// diffResult holds one symbol-level diff entry.
type diffResult struct {
	Type      string // "new", "removed", "changed"
	Symbol    symbol
	OldSymbol *symbol // set for changed symbols
}

func newDiffCmd(d *deps) *cobra.Command {
	return &cobra.Command{
		Use:   "diff <ref1>..<ref2>",
		Short: "Show semantic diff between two git refs (new/removed/changed symbols)",
		Long: `Compare two git references and report new, removed, and changed code symbols.
Accepts both "ref1..ref2" (changes in ref2 since ref1) and "ref1...ref2"
(changes introduced by ref2 since it diverged from ref1) syntax.

Uses git to find changed files and extracts Go symbols from them.`,
		Example: `  semidx diff main..feat/oauth2
  semidx diff main...feat/oauth2`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			refSpec := args[0]
			ref1, ref2, err := parseRefRange(refSpec)
			if err != nil {
				return err
			}
			return runDiff(ref1, ref2)
		},
	}
}

// parseRefRange parses "ref1..ref2" or "ref1...ref2" into two refs.
func parseRefRange(s string) (ref1, ref2 string, err error) {
	if strings.Contains(s, "...") {
		parts := strings.SplitN(s, "...", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", "", fmt.Errorf("invalid ref range: %q (expected ref1...ref2)", s)
		}
		return parts[0], parts[1], nil
	}
	if strings.Contains(s, "..") {
		parts := strings.SplitN(s, "..", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", "", fmt.Errorf("invalid ref range: %q (expected ref1..ref2)", s)
		}
		return parts[0], parts[1], nil
	}
	return "", "", fmt.Errorf("invalid ref range: %q (expected ref1..ref2 or ref1...ref2)", s)
}

// runDiff performs the semantic diff between two git refs.
func runDiff(ref1, ref2 string) error {
	// Get changed files.
	changedFiles, err := getChangedFiles(ref1, ref2)
	if err != nil {
		return fmt.Errorf("get changed files: %w", err)
	}
	if len(changedFiles) == 0 {
		fmt.Printf("Semantic Diff: %s → %s\n", ref1, ref2)
		fmt.Println(strings.Repeat("─", 40))
		fmt.Println("No changed files.")
		return nil
	}

	var newSymbols, removedSymbols, changedSymbols []diffResult

	for _, filePath := range changedFiles {
		// Get old content.
		oldContent, err := getFileAtRef(filePath, ref1)
		if err != nil {
			// File may not exist at ref1 (new file).
			oldContent = ""
		}
		// Get new content.
		newContent, err := getFileAtRef(filePath, ref2)
		if err != nil {
			// File may have been deleted.
			newContent = ""
		}

		oldSymbols := extractSymbols(filePath, oldContent)
		newSymbolsMap := extractSymbols(filePath, newContent)

		// Find new and changed symbols.
		for key, ns := range newSymbolsMap {
			if os, exists := oldSymbols[key]; !exists {
				newSymbols = append(newSymbols, diffResult{Type: diffNew, Symbol: *ns})
			} else if ns.Signature != os.Signature {
				changedSymbols = append(changedSymbols, diffResult{
					Type:      diffChanged,
					Symbol:    *ns,
					OldSymbol: os,
				})
			}
		}

		// Find removed symbols.
		for key, os := range oldSymbols {
			if _, exists := newSymbolsMap[key]; !exists {
				removedSymbols = append(removedSymbols, diffResult{Type: diffRemoved, Symbol: *os})
			}
		}
	}

	// Sort output by file path then line.
	sort.Slice(newSymbols, func(i, j int) bool {
		if newSymbols[i].Symbol.FilePath != newSymbols[j].Symbol.FilePath {
			return newSymbols[i].Symbol.FilePath < newSymbols[j].Symbol.FilePath
		}
		return newSymbols[i].Symbol.Line < newSymbols[j].Symbol.Line
	})
	sort.Slice(removedSymbols, func(i, j int) bool {
		if removedSymbols[i].Symbol.FilePath != removedSymbols[j].Symbol.FilePath {
			return removedSymbols[i].Symbol.FilePath < removedSymbols[j].Symbol.FilePath
		}
		return removedSymbols[i].Symbol.Line < removedSymbols[j].Symbol.Line
	})
	sort.Slice(changedSymbols, func(i, j int) bool {
		if changedSymbols[i].Symbol.FilePath != changedSymbols[j].Symbol.FilePath {
			return changedSymbols[i].Symbol.FilePath < changedSymbols[j].Symbol.FilePath
		}
		return changedSymbols[i].Symbol.Line < changedSymbols[j].Symbol.Line
	})

	// Print the diff.
	fmt.Printf("Semantic Diff: %s → %s\n", ref1, ref2)
	fmt.Println(strings.Repeat("─", 40))

	if len(newSymbols) > 0 {
		fmt.Printf("\nNew symbols (%d):\n", len(newSymbols))
		for _, dr := range newSymbols {
			fmt.Printf("  + %s (%s:%d)\n", dr.Symbol.Name, dr.Symbol.FilePath, dr.Symbol.Line)
		}
	}

	if len(removedSymbols) > 0 {
		fmt.Printf("\nRemoved symbols (%d):\n", len(removedSymbols))
		for _, dr := range removedSymbols {
			fmt.Printf("  - %s (%s:%d)\n", dr.Symbol.Name, dr.Symbol.FilePath, dr.Symbol.Line)
		}
	}

	if len(changedSymbols) > 0 {
		fmt.Printf("\nChanged signatures (%d):\n", len(changedSymbols))
		for _, dr := range changedSymbols {
			fmt.Printf("  ~ %s — changed signature\n", dr.Symbol.Name)
		}
	}

	if len(newSymbols)+len(removedSymbols)+len(changedSymbols) == 0 {
		fmt.Println("No semantic differences found.")
	}

	return nil
}

// getChangedFiles returns the list of files changed between two refs using git diff.
func getChangedFiles(ref1, ref2 string) ([]string, error) {
	cmd := exec.Command("git", "diff", "--name-only", "--diff-filter=ACMR", ref1+".."+ref2)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	var files []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			files = append(files, line)
		}
	}
	return files, scanner.Err()
}

// getFileAtRef reads a file's content at a given git ref.
func getFileAtRef(filePath, ref string) (string, error) {
	cmd := exec.Command("git", "show", ref+":"+filePath)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// goFuncPattern matches Go function/method declarations.
var goFuncPattern = regexp.MustCompile(`^func\s+(?:\([^)]*\)\s+)?(\w[\w]*)\s*\(`)

// goTypePattern matches Go type declarations.
var goTypePattern = regexp.MustCompile(`^type\s+(\w[\w]*)\s`)

// goConstVarPattern matches Go const/var declarations.
var goConstVarPattern = regexp.MustCompile(`^(const|var)\s+(\w[\w]*)`)

// extractSymbols extracts Go symbols from source content.
func extractSymbols(filePath, content string) symbolSet {
	symbols := make(symbolSet)
	if content == "" {
		return symbols
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(line)

		// Skip comments and blank lines.
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
		}

		// Match function declarations.
		if m := goFuncPattern.FindStringSubmatch(trimmed); len(m) > 1 {
			name := m[1]
			key := "func:" + name
			symbols[key] = &symbol{
				Name:      name,
				FilePath:  filePath,
				Line:      lineNum,
				Kind:      "func",
				Signature: trimmed,
			}
			continue
		}

		// Match type declarations.
		if m := goTypePattern.FindStringSubmatch(trimmed); len(m) > 1 {
			name := m[1]
			key := "type:" + name
			symbols[key] = &symbol{
				Name:      name,
				FilePath:  filePath,
				Line:      lineNum,
				Kind:      "type",
				Signature: trimmed,
			}
			continue
		}

		// Match const/var declarations.
		if m := goConstVarPattern.FindStringSubmatch(trimmed); len(m) > 2 {
			kind := m[1]
			name := m[2]
			key := kind + ":" + name
			symbols[key] = &symbol{
				Name:      name,
				FilePath:  filePath,
				Line:      lineNum,
				Kind:      kind,
				Signature: trimmed,
			}
		}
	}
	return symbols
}
