package webadmin

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/lgldsilva/semidx/internal/analyzer"
	"github.com/lgldsilva/semidx/internal/imports"
	"github.com/lgldsilva/semidx/internal/store"
)

// apiProjectExplain returns symbol-level context for path:line (CLI explain).
// Query: path=...&line=N
func (a *Admin) apiProjectExplain(w http.ResponseWriter, r *http.Request, ac *authCtx) {
	_ = ac
	name := r.PathValue("project")
	filePath := strings.TrimSpace(r.URL.Query().Get("path"))
	lineStr := strings.TrimSpace(r.URL.Query().Get("line"))
	if filePath == "" || lineStr == "" {
		writeJSONErr(w, http.StatusBadRequest, "path and line are required")
		return
	}
	line, err := strconv.Atoi(lineStr)
	if err != nil || line < 1 {
		writeJSONErr(w, http.StatusBadRequest, "invalid line")
		return
	}
	proj, err := a.store.GetProject(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, spaErrProjectNotFound)
		return
	}
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	root := proj.Path
	if root == "" {
		a.explainFromIndex(w, r, proj, filePath, line)
		return
	}

	absPath, ok := confinedPath(root, filePath)
	if !ok {
		writeJSONErr(w, http.StatusBadRequest, "path escapes project root")
		return
	}
	// #nosec G304 -- absPath from confinedPath (Rel + IsLocal)
	content, err := os.ReadFile(absPath)
	if err != nil {
		a.explainFromIndex(w, r, proj, filePath, line)
		return
	}

	syms := analyzer.Symbols(filePath, content)
	target := symbolAtLine(syms, line)
	if target == nil {
		writeJSONErr(w, http.StatusNotFound, "no symbol found at that line")
		return
	}

	modulePath := detectModulePath(root)
	fileImports := imports.Analyze(filePath, content, modulePath)
	sort.Strings(fileImports)

	importers := importersOfFile(r.Context(), a.store, proj.ID, filePath)
	tests := findTestFiles(root, filePath, target.Name)

	display := target.Name
	if pkg := goPackageName(content); pkg != "" {
		display = pkg + "." + target.Name
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"symbol":       display,
		"kind":         target.Kind,
		"path":         filePath,
		"start_line":   target.StartLine,
		"end_line":     target.EndLine,
		"dependencies": fileImports,
		"importers":    importers,
		"tests":        tests,
		"source":       "disk",
	})
}

func (a *Admin) explainFromIndex(w http.ResponseWriter, r *http.Request, proj *store.Project, filePath string, line int) {
	chunks, err := fetchExplainChunks(r.Context(), a.store, proj, filePath)
	if err != nil {
		a.log.Error("explain index chunks failed", "err", err)
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return
	}
	if len(chunks) == 0 {
		writeJSONErr(w, http.StatusNotFound, "file not on disk and not in index")
		return
	}
	var content strings.Builder
	for _, c := range chunks {
		content.WriteString(c.Content)
		content.WriteByte('\n')
	}
	bytes := []byte(content.String())
	syms := analyzer.Symbols(filePath, bytes)
	target := symbolAtLine(syms, line)
	importers := importersOfFile(r.Context(), a.store, proj.ID, filePath)

	out := map[string]any{
		"path":       filePath,
		"line":       line,
		"importers":  importers,
		"source":     "index",
		"chunk_hits": len(chunks),
	}
	if target != nil {
		out["symbol"] = target.Name
		out["kind"] = target.Kind
		out["start_line"] = target.StartLine
		out["end_line"] = target.EndLine
	} else {
		for _, c := range chunks {
			if line >= c.StartLine && line <= c.EndLine {
				out["start_line"] = c.StartLine
				out["end_line"] = c.EndLine
				out["snippet"] = c.Content
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func fetchExplainChunks(ctx context.Context, st store.Store, proj *store.Project, filePath string) ([]store.SearchResult, error) {
	dims := proj.Dims
	if dims <= 0 {
		dims = 1024
	}
	chunks, err := st.FetchChunksByPath(ctx, proj.ID, filePath, dims, 64)
	if err == nil && len(chunks) > 0 {
		return chunks, nil
	}
	for _, d := range []int{store.KeywordDims, 768, 1024, 1536, 3072} {
		chunks, err = st.FetchChunksByPath(ctx, proj.ID, filePath, d, 64)
		if err == nil && len(chunks) > 0 {
			return chunks, nil
		}
	}
	return chunks, err
}

func symbolAtLine(syms []analyzer.Symbol, line int) *analyzer.Symbol {
	for i := range syms {
		s := &syms[i]
		if line >= s.StartLine && line <= s.EndLine {
			return s
		}
	}
	var best *analyzer.Symbol
	for i := range syms {
		s := &syms[i]
		if line >= s.StartLine {
			if best == nil || s.StartLine > best.StartLine {
				best = s
			}
		}
	}
	return best
}

func importersOfFile(ctx context.Context, st store.Store, projectID int, filePath string) []string {
	graph, err := st.FetchGraphNeighbors(ctx, projectID)
	if err != nil {
		return nil
	}
	fileDir := filepath.ToSlash(filepath.Dir(filePath))
	if fileDir != "." && !strings.HasSuffix(fileDir, "/") {
		fileDir += "/"
	}
	var importers []string
	seen := map[string]bool{}
	for src, targets := range graph {
		for _, tgt := range targets {
			t := filepath.ToSlash(tgt)
			if t == fileDir || strings.TrimSuffix(t, "/") == strings.TrimSuffix(fileDir, "/") {
				if !seen[src] {
					seen[src] = true
					importers = append(importers, src)
				}
				break
			}
		}
	}
	sort.Strings(importers)
	return importers
}

func goPackageName(content []byte) string {
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "package ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "package "))
		}
	}
	return ""
}

func detectModulePath(root string) string {
	gm, ok := confinedPath(root, "go.mod")
	if !ok {
		return ""
	}
	// #nosec G304 -- gm from confinedPath (fixed "go.mod" under root)
	data, err := os.ReadFile(gm)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}

// confinedPath joins root and rel and returns the absolute path only when the
// result stays inside root. filepath.IsAbs rejects absolute user input;
// filepath.Rel + filepath.IsLocal are the sanitizers CodeQL recognizes for
// go/path-injection (HasPrefix alone is not enough).
func confinedPath(root, rel string) (string, bool) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", false
	}
	rootClean := filepath.Clean(root)
	abs := filepath.Clean(filepath.Join(rootClean, rel))
	relOut, err := filepath.Rel(rootClean, abs)
	if err != nil || !filepath.IsLocal(relOut) {
		return "", false
	}
	return abs, true
}

func findTestFiles(root, filePath, symbolName string) []string {
	absFile, ok := confinedPath(root, filePath)
	if !ok {
		return nil
	}
	dir := filepath.Dir(absFile)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var result []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, "_test.go") && !strings.HasSuffix(name, "_test.py") && !strings.HasSuffix(name, ".test.js") {
			continue
		}
		relPath := filepath.ToSlash(filepath.Join(filepath.Dir(filePath), name))
		testAbs, ok := confinedPath(root, relPath)
		if !ok {
			continue
		}
		// #nosec G304 -- testAbs from confinedPath
		data, err := os.ReadFile(testAbs)
		if err != nil {
			continue
		}
		if strings.Contains(string(data), symbolName) {
			result = append(result, relPath)
		}
	}
	sort.Strings(result)
	return result
}
