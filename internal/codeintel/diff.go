package codeintel

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

// SymbolDiff represents a single symbol-level difference.
type SymbolDiff struct {
	Type         string // "new", "removed", "changed"
	Name         string
	FilePath     string
	Line         int
	Kind         string // "func", "type", "method", "const", "var"
	Signature    string
	OldSignature string // set for changed symbols
}

// DiffResult contains the results of a semantic diff.
type DiffResult struct {
	Ref1    string
	Ref2    string
	New     []SymbolDiff
	Removed []SymbolDiff
	Changed []SymbolDiff
}

// ParseRefRange parses "ref1..ref2" or "ref1...ref2" into two refs and range kind.
func ParseRefRange(s string) (ref1, ref2 string, threeDot bool, err error) {
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

// Diff performs a semantic diff between two git refs.
// dir is the git working directory ("" means the current process working directory).
func Diff(dir, ref1, ref2 string, threeDot bool) (*DiffResult, error) {
	changedFiles, err := getChangedFiles(dir, ref1, ref2, threeDot)
	if err != nil {
		return nil, fmt.Errorf("get changed files: %w", err)
	}

	result := &DiffResult{
		Ref1: ref1,
		Ref2: ref2,
	}

	if len(changedFiles) == 0 {
		return result, nil
	}

	newSymbols, removedSymbols, changedSymbols := collectDiffSymbols(dir, changedFiles, ref1, ref2)
	sortDiffSymbols(&newSymbols, &removedSymbols, &changedSymbols)

	result.New = newSymbols
	result.Removed = removedSymbols
	result.Changed = changedSymbols

	return result, nil
}

// symbol describes a code symbol extracted from source.
type symbol struct {
	Name      string
	FilePath  string
	Line      int
	Kind      string // "func", "type", "method", "const", "var"
	Signature string
}

// symbolSet is a set of symbols keyed by "kind:name" for comparison.
type symbolSet map[string]*symbol

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

// safeGitRef reports whether ref is a plausible git reference.
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

// safeGitFilepath reports whether path is safe for use as a git tree-path.
func safeGitFilepath(path string) bool {
	return !strings.Contains(path, ":") && !strings.HasPrefix(path, "-") && !strings.Contains(path, "..")
}

// getChangedFiles returns files changed between two refs.
func getChangedFiles(dir, ref1, ref2 string, threeDot bool) ([]string, error) {
	if !safeGitRef(ref1) || !safeGitRef(ref2) {
		return nil, fmt.Errorf("invalid git ref: %q..%q contains unsafe characters", ref1, ref2)
	}
	sep := ".."
	if threeDot {
		sep = "..."
	}
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

// getFileAtRef reads a file's content at a given git ref.
func getFileAtRef(dir, filePath, ref string) (string, error) {
	if !safeGitRef(ref) || !safeGitFilepath(filePath) {
		return "", fmt.Errorf("invalid git ref or file path: %q:%q", ref, filePath)
	}
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

// collectDiffSymbols compares symbols in changed files.
func collectDiffSymbols(dir string, changedFiles []string, ref1, ref2 string) (newSym, removed, changed []SymbolDiff) {
	for _, filePath := range changedFiles {
		oldContent, _ := getFileAtRef(dir, filePath, ref1)
		newContent, _ := getFileAtRef(dir, filePath, ref2)

		oldSymbols := extractSymbols(filePath, oldContent)
		newSymbolsMap := extractSymbols(filePath, newContent)

		for key, ns := range newSymbolsMap {
			if os, exists := oldSymbols[key]; !exists {
				newSym = append(newSym, SymbolDiff{
					Type:      "new",
					Name:      ns.Name,
					FilePath:  ns.FilePath,
					Line:      ns.Line,
					Kind:      ns.Kind,
					Signature: ns.Signature,
				})
			} else if ns.Signature != os.Signature {
				changed = append(changed, SymbolDiff{
					Type:         "changed",
					Name:         ns.Name,
					FilePath:     ns.FilePath,
					Line:         ns.Line,
					Kind:         ns.Kind,
					Signature:    ns.Signature,
					OldSignature: os.Signature,
				})
			}
		}
		for key, os := range oldSymbols {
			if _, exists := newSymbolsMap[key]; !exists {
				removed = append(removed, SymbolDiff{
					Type:      "removed",
					Name:      os.Name,
					FilePath:  os.FilePath,
					Line:      os.Line,
					Kind:      os.Kind,
					Signature: os.Signature,
				})
			}
		}
	}
	return
}

// sortDiffSymbols sorts diff results by file path then line number.
func sortDiffSymbols(newSyms, remSyms, chgSyms *[]SymbolDiff) {
	byPathThenLine := func(i, j int, list []SymbolDiff) bool {
		if list[i].FilePath != list[j].FilePath {
			return list[i].FilePath < list[j].FilePath
		}
		return list[i].Line < list[j].Line
	}
	sort.Slice(*newSyms, func(i, j int) bool { return byPathThenLine(i, j, *newSyms) })
	sort.Slice(*remSyms, func(i, j int) bool { return byPathThenLine(i, j, *remSyms) })
	sort.Slice(*chgSyms, func(i, j int) bool { return byPathThenLine(i, j, *chgSyms) })
}
