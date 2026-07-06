package chunker

import (
	"go/parser"
	"go/token"
	"strconv"
	"strings"
)

// stdlibPrefixes contains the first path segment of every Go standard library
// package. An import whose first segment matches one of these is considered
// stdlib and excluded from results.
var stdlibPrefixes = map[string]bool{
	"archive":   true,
	"bufio":     true,
	"builtin":   true,
	"bytes":     true,
	"cmp":       true,
	"compress":  true,
	"container": true,
	"context":   true,
	"crypto":    true,
	"database":  true,
	"debug":     true,
	"embed":     true,
	"encoding":  true,
	"errors":    true,
	"flag":      true,
	"fmt":       true,
	"go":        true,
	"hash":      true,
	"html":      true,
	"image":     true,
	"index":     true,
	"internal":  true,
	"io":        true,
	"log":       true,
	"maps":      true,
	"math":      true,
	"mime":      true,
	"net":       true,
	"os":        true,
	"path":      true,
	"plugin":    true,
	"reflect":   true,
	"regexp":    true,
	"runtime":   true,
	"slices":    true,
	"sort":      true,
	"strconv":   true,
	"strings":   true,
	"sync":      true,
	"syscall":   true,
	"testing":   true,
	"text":      true,
	"time":      true,
	"unicode":   true,
	"unique":    true,
	"unsafe":    true,
}

// AnalyzeGoImports parses Go source and returns the directory paths of imported
// local packages. It strips the module prefix from import paths (e.g.,
// "github.com/lgldsilva/semidx/internal/chunker" -> "internal/chunker/").
// Stdlib and third-party imports are excluded. The function is safe to call
// on non-Go content (returns nil, nil).
//
// modulePath is the Go module path from go.mod (e.g. "github.com/lgldsilva/semidx").
// An empty modulePath means "treat all non-stdlib imports as local" (useful for
// simple test fixtures that don't have a go.mod).
func AnalyzeGoImports(content []byte, modulePath string) []string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", content, parser.ImportsOnly)
	if err != nil {
		return nil
	}

	seen := make(map[string]bool, len(f.Imports))
	result := make([]string, 0, len(f.Imports))

	for _, imp := range f.Imports {
		// Skip dot imports (e.g., `. "fmt"`).
		if imp.Name != nil && imp.Name.Name == "." {
			continue
		}

		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}

		// Determine the first path segment and skip stdlib.
		firstSeg := path
		if idx := strings.IndexByte(path, '/'); idx >= 0 {
			firstSeg = path[:idx]
		}
		if stdlibPrefixes[firstSeg] {
			continue
		}

		var dir string

		if modulePath != "" {
			// With a non-empty modulePath we require the import to be rooted in
			// the module; third-party imports (including those with a domain-
			// like first segment) are excluded.
			if !strings.HasPrefix(path, modulePath) {
				continue
			}
			// Skip a self-reference to the module root package itself.
			rest := strings.TrimPrefix(path, modulePath)
			rest = strings.TrimPrefix(rest, "/")
			if rest == "" {
				continue
			}
			dir = rest + "/"
		} else {
			// Empty modulePath: keep the full path (minus the leading
			// stdlib check already applied) as the local directory.
			dir = path + "/"
		}

		if !seen[dir] {
			seen[dir] = true
			result = append(result, dir)
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
}
