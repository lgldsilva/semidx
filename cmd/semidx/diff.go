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
			ref1, ref2, threeDot, err := parseRefRange(refSpec)
			if err != nil {
				return err
			}
			return runDiff(ref1, ref2, threeDot)
		},
	}
}

// parseRefRange parses "ref1..ref2" or "ref1...ref2" into two refs and range kind.
func parseRefRange(s string) (ref1, ref2 string, threeDot bool, err error) {
	if strings.Contains(s, "...") {
		parts := strings.SplitN(s, "...", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", "", false, fmt.Errorf("invalid ref range: %q (expected ref1...ref2)", s)
		}
		return parts[0], parts[1], true, nil
	}
	if strings.Contains(s, "..") {
		parts := strings.SplitN(s, "..", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return "", "", false, fmt.Errorf("invalid ref range: %q (expected ref1..ref2)", s)
		}
		return parts[0], parts[1], false, nil
	}
	return "", "", false, fmt.Errorf("invalid ref range: %q (expected ref1..ref2 or ref1...ref2)", s)
}

// runDiff performs the semantic diff between two git refs.
func runDiff(ref1, ref2 string, threeDot bool) error {
	// "" = the process working directory, i.e. the repo the user runs `semidx
	// diff` in. Tests call getChangedFiles/getFileAtRef directly with a temp dir.
	changedFiles, err := getChangedFiles("", ref1, ref2, threeDot)
	if err != nil {
		return fmt.Errorf("get changed files: %w", err)
	}
	if len(changedFiles) == 0 {
		fmt.Printf("Semantic Diff: %s → %s\n", ref1, ref2)
		fmt.Println(strings.Repeat("─", 40))
		fmt.Println("No changed files.")
		return nil
	}

	newSymbols, removedSymbols, changedSymbols := collectDiffSymbols(changedFiles, ref1, ref2)
	sortDiffResults(newSymbols, removedSymbols, changedSymbols)
	printDiffResults(ref1, ref2, newSymbols, removedSymbols, changedSymbols)
	return nil
}

func collectDiffSymbols(changedFiles []string, ref1, ref2 string) (newSym, removed, changed []diffResult) {
	for _, filePath := range changedFiles {
		oldContent, _ := getFileAtRef("", filePath, ref1)
		newContent, _ := getFileAtRef("", filePath, ref2)

		oldSymbols := extractSymbols(filePath, oldContent)
		newSymbolsMap := extractSymbols(filePath, newContent)

		for key, ns := range newSymbolsMap {
			if os, exists := oldSymbols[key]; !exists {
				newSym = append(newSym, diffResult{Type: diffNew, Symbol: *ns})
			} else if ns.Signature != os.Signature {
				changed = append(changed, diffResult{Type: diffChanged, Symbol: *ns, OldSymbol: os})
			}
		}
		for key, os := range oldSymbols {
			if _, exists := newSymbolsMap[key]; !exists {
				removed = append(removed, diffResult{Type: diffRemoved, Symbol: *os})
			}
		}
	}
	return
}

func sortDiffResults(newSyms, remSyms, chgSyms []diffResult) {
	byPathThenLine := func(list []diffResult) func(i, j int) bool {
		return func(i, j int) bool {
			if list[i].Symbol.FilePath != list[j].Symbol.FilePath {
				return list[i].Symbol.FilePath < list[j].Symbol.FilePath
			}
			return list[i].Symbol.Line < list[j].Symbol.Line
		}
	}
	sort.Slice(newSyms, byPathThenLine(newSyms))
	sort.Slice(remSyms, byPathThenLine(remSyms))
	sort.Slice(chgSyms, byPathThenLine(chgSyms))
}

func printDiffResults(ref1, ref2 string, newSyms, remSyms, chgSyms []diffResult) {
	fmt.Printf("Semantic Diff: %s → %s\n", ref1, ref2)
	fmt.Println(strings.Repeat("─", 40))

	if len(newSyms) > 0 {
		fmt.Printf("\nNew symbols (%d):\n", len(newSyms))
		for _, dr := range newSyms {
			fmt.Printf("  + %s (%s:%d)\n", dr.Symbol.Name, dr.Symbol.FilePath, dr.Symbol.Line)
		}
	}
	if len(remSyms) > 0 {
		fmt.Printf("\nRemoved symbols (%d):\n", len(remSyms))
		for _, dr := range remSyms {
			fmt.Printf("  - %s (%s:%d)\n", dr.Symbol.Name, dr.Symbol.FilePath, dr.Symbol.Line)
		}
	}
	if len(chgSyms) > 0 {
		fmt.Printf("\nChanged signatures (%d):\n", len(chgSyms))
		for _, dr := range chgSyms {
			fmt.Printf("  ~ %s — changed signature\n", dr.Symbol.Name)
		}
	}
	if len(newSyms)+len(remSyms)+len(chgSyms) == 0 {
		fmt.Println("No semantic differences found.")
	}
}

// safeGitRef reports whether ref is a plausible git reference (no shell
// metacharacters and does not start with a dash which would be parsed as a flag).
func safeGitRef(ref string) bool {
	if ref == "" || ref[0] == '-' {
		return false
	}
	for _, r := range ref {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '_', r == '/', r == '-', r == '~', r == '^', r == '@', r == '{', r == '}':
		default:
			return false
		}
	}
	return true
}

// getChangedFiles returns files changed between two refs. dir is the git
// working directory ("" means the current process working directory — the
// normal case for the `semidx diff` command; tests pass a temp repo so they
// never touch the process CWD).
func getChangedFiles(dir, ref1, ref2 string, threeDot bool) ([]string, error) {
	if !safeGitRef(ref1) || !safeGitRef(ref2) {
		return nil, fmt.Errorf("invalid git ref: %q..%q contains unsafe characters", ref1, ref2)
	}
	sep := ".."
	if threeDot {
		sep = "..."
	}
	// The revision range must come BEFORE '--'; anything after '--' is a
	// pathspec, so the old order made git treat "ref1..ref2" as a (missing)
	// path and always return nothing.
	// #nosec G204 -- refs validated via safeGitRef; '--' ends options so no pathspec is injected
	cmd := exec.Command("git")
	cmd.Args = []string{"git", "diff", "--name-only", "--diff-filter=ACMR", ref1 + sep + ref2, "--"}
	cmd.Dir = dir
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

// safeGitFilepath reports whether path is safe for use as a git tree-path.
func safeGitFilepath(path string) bool {
	return !strings.Contains(path, ":") && !strings.HasPrefix(path, "-") && !strings.Contains(path, "..")
}

// getFileAtRef reads a file's content at a given git ref. dir is the git
// working directory ("" = process CWD; tests pass a temp repo).
func getFileAtRef(dir, filePath, ref string) (string, error) {
	if !safeGitRef(ref) || !safeGitFilepath(filePath) {
		return "", fmt.Errorf("invalid git ref or file path: %q:%q", ref, filePath)
	}
	// "git show <ref>:<path>" takes an object spec, not a pathspec, so the
	// leading '--' (which forces pathspec interpretation) made it read nothing.
	// #nosec G204 -- ref/path validated via safeGitRef and safeGitFilepath
	cmd := exec.Command("git")
	cmd.Args = []string{"git", "show", ref + ":" + filePath}
	cmd.Dir = dir
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
