// Package imports extracts local dependency paths from source code across
// multiple languages. It dispatches by file extension to language-specific
// regex-based extractors.
//
// The Go extractor logic is copied from internal/chunker/ast_analyzer.go
// (AnalyzeGoImports) to avoid a circular dependency. Original author's
// attribution: chunker package.
package imports

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Pre-compiled regexes (package-level, not per-call)
// ---------------------------------------------------------------------------

var (
	// Markdown: [text](path/to/file.md) or [text](./relative.md)
	mdLinkRe = regexp.MustCompile(`\[([^\]]*)\]\(([^)]+)\)`)

	// Java / JVM languages (semicolon optional for Kotlin/Scala support)
	javaImportRe = regexp.MustCompile(`import\s+(?:static\s+)?([\w.*]+)\s*;?`)

	// TypeScript / JavaScript / JSX / TSX
	tsImportFromRe = regexp.MustCompile(`(?:import|export)\s+(?:.*?\s+from\s+)?['"]([^'"]+)['"]`)
	tsRequireRe    = regexp.MustCompile(`require\s*\(\s*['"]([^'"]+)['"]`)
	tsImportExprRe = regexp.MustCompile(`import\s*\(\s*['"]([^'"]+)['"]`)

	// Python
	pyFromImportRe = regexp.MustCompile(`from\s+([\w.]+)\s+import`)
	pyImportRe     = regexp.MustCompile(`^\s*import\s+([\w.]+)`)

	// Rust
	rustUseRe         = regexp.MustCompile(`use\s+([\w:]+)(?:::|;)`)
	rustExternCrateRe = regexp.MustCompile(`extern\s+crate\s+(\w+)`)

	// C / C++
	cIncludeRe = regexp.MustCompile(`#include\s+[<"]([^>"]+)[>"]`)

	// Ruby
	rubyRequireRe = regexp.MustCompile(`require(?:_relative)?\s+['"]([^'"]+)['"]`)

	// C#
	csUsingRe = regexp.MustCompile(`(?m)^\s*using\s+([\w.]+)\s*;`)
)

// ---------------------------------------------------------------------------
// Skip sets (stdlib / JRE / etc.)
// ---------------------------------------------------------------------------

// javaStdlibPrefixes are JRE / JDK packages that should never be treated as
// local project dependencies.
var javaStdlibPrefixes = []string{
	"java.",
	"javax.",
	"sun.",
	"com.sun.",
	"jdk.",
	"org.ietf.",
	"org.omg.",
	"org.w3c.",
	"org.xml.",
	"org.junit.",
}

// pythonStdlib contains the top-level names of Python's standard library.
// Modules not in this set are assumed to be local or third-party (both kept).
var pythonStdlib = map[string]bool{
	"abc": true, "aifc": true, "argparse": true, "asyncio": true,
	"base64": true, "builtins": true,
	"collections": true, "concurrent": true, "contextlib": true, "copy": true,
	"csv": true, "ctypes": true,
	"datetime": true, "decimal": true, "difflib": true,
	"email": true, "enum": true,
	"fileinput": true, "fnmatch": true, "fractions": true, "functools": true,
	"getopt": true, "getpass": true, "glob": true, "gzip": true,
	"hashlib": true, "heapq": true, "hmac": true, "html": true, "http": true,
	"importlib": true, "inspect": true, "io": true, "itertools": true,
	"json":    true,
	"logging": true,
	"math":    true, "mimetypes": true, "mmap": true, "multiprocessing": true,
	"os":      true,
	"pathlib": true, "pdb": true, "pickle": true, "platform": true,
	"pprint": true,
	"queue":  true,
	"random": true, "re": true,
	"select": true, "shelve": true, "shutil": true, "signal": true,
	"socket": true, "sqlite3": true, "ssl": true, "statistics": true,
	"string": true, "struct": true, "subprocess": true, "sys": true,
	"tempfile": true, "textwrap": true, "threading": true, "time": true,
	"tkinter": true, "traceback": true, "types": true, "typing": true,
	"unittest": true, "urllib": true, "uuid": true,
	"warnings": true, "wave": true, "weakref": true,
	"xml":     true,
	"zipfile": true, "zipimport": true,
}

// rustStdlibPrefixes are Rust's standard / core crates.
var rustStdlibPrefixes = []string{
	"std::",
	"core::",
	"alloc::",
	"proc_macro::",
}

// csharpStdlibPrefixes are .NET framework / runtime namespaces.
// Bare names (without trailing dot) match both the root namespace
// (e.g. "System") and its children ("System.Collections").
var csharpStdlibPrefixes = []string{
	"System",
	"Microsoft",
	"Windows",
}

// goStdlibPrefixes is copied from internal/chunker/ast_analyzer.go.
// It maps the first path segment of every Go standard library package.
var goStdlibPrefixes = map[string]bool{
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

// ---------------------------------------------------------------------------
// Analyze dispatches to language-specific extractors.
// ---------------------------------------------------------------------------

// Analyze extracts local import/dependency paths from source code.
// It dispatches by file extension to language-specific extractors.
// Returns directory paths (e.g. "internal/store/" for Go, "com/foo/" for Java).
// Returns nil for unsupported extensions or parse failures.
// modulePath is only needed for Go (to strip the module prefix); other languages ignore it.
func Analyze(path string, content []byte, modulePath string) []string {
	if len(content) == 0 {
		return nil
	}

	ext := strings.ToLower(filepath.Ext(path))

	switch ext {
	case ".go":
		return analyzeGo(content, modulePath)
	case ".java", ".kt", ".scala":
		return analyzeJava(content)
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs":
		return analyzeTS(path, content)
	case ".py":
		return analyzePython(content)
	case ".rs":
		return analyzeRust(content)
	case ".c", ".cpp", ".h", ".hpp", ".cc", ".cxx":
		return analyzeC(content)
	case ".rb":
		return analyzeRuby(path, content)
	case ".cs":
		return analyzeCsharp(content)
	case ".md", ".mdx", ".markdown", ".rst", ".adoc":
		return analyzeMarkdown(content)
	default:
		return nil
	}
}

// ---------------------------------------------------------------------------
// Go extractor (copied from internal/chunker/ast_analyzer.go with attribution)
// ---------------------------------------------------------------------------

// resolveImportDir resolves an import path to a local directory path.
// When modulePath is non-empty, only imports rooted in that module are kept.
func resolveImportDir(path, modulePath string) string {
	if modulePath != "" {
		if !strings.HasPrefix(path, modulePath) {
			return ""
		}
		// Skip a self-reference to the module root package itself.
		rest := strings.TrimPrefix(path, modulePath)
		rest = strings.TrimPrefix(rest, "/")
		if rest == "" {
			return ""
		}
		return rest + "/"
	}
	// Empty modulePath: keep the full path.
	return path + "/"
}

// analyzeGo parses Go source and returns the directory paths of imported
// local packages.  Logic copied from chunker.AnalyzeGoImports.
func analyzeGo(content []byte, modulePath string) []string {
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
		if goStdlibPrefixes[firstSeg] {
			continue
		}

		dir := resolveImportDir(path, modulePath)
		if dir == "" {
			continue
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

// ---------------------------------------------------------------------------
// Java / JVM extractor
// ---------------------------------------------------------------------------

// analyzeJava extracts package paths from Java/Kotlin/Scala import statements,
// converts dots to slashes, and filters JRE/JDK packages.
func analyzeJava(content []byte) []string {
	seen := make(map[string]bool)
	matches := javaImportRe.FindAllSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}

	result := make([]string, 0, len(matches))
	for _, m := range matches {
		pkg := string(m[1])

		if hasAnyPrefix(pkg, javaStdlibPrefixes) {
			continue
		}

		// Strip trailing class/interface/enum names (start with uppercase)
		// and wildcards so we get only the package directory.
		dir := javaPackageDir(pkg)
		if dir == "" {
			continue
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

// ---------------------------------------------------------------------------
// TypeScript / JavaScript extractor
// ---------------------------------------------------------------------------

// resolveTSImportPath resolves a TypeScript/JavaScript import path to a local
// directory. Returns empty string for non-local imports (npm packages, built-ins,
// scoped packages).
func resolveTSImportPath(p, srcDir string) string {
	// Handle @/ path alias (Vue/React/Next.js tsconfig paths).
	if strings.HasPrefix(p, "@/") {
		dir := strings.TrimPrefix(p, "@/")
		dir = filepath.Dir(dir)
		if dir == "." {
			return ""
		}
		return dir + "/"
	}

	// Skip scoped npm packages (@scope/package).
	if strings.HasPrefix(p, "@") {
		return ""
	}
	// Skip Node.js built-in modules (node:fs, node:path, …).
	if strings.HasPrefix(p, "node:") {
		return ""
	}
	// Skip bare names / external packages (not starting with ./ or ../).
	if !strings.HasPrefix(p, "./") && !strings.HasPrefix(p, "../") {
		return ""
	}

	// Resolve relative path to a directory.
	resolved := filepath.Join(srcDir, p)
	resolved = filepath.Clean(resolved) + "/"
	if resolved == "/" {
		return ""
	}
	return resolved
}

// analyzeTS extracts local import paths from TypeScript/JavaScript files.
// It handles `import ... from`, `require(...)`, and `import(...)` patterns.
// Only relative paths (./ or ../) are kept; npm packages and built-ins are
// excluded.  Relative paths are resolved against the source file's directory.
func analyzeTS(path string, content []byte) []string {
	seen := make(map[string]bool)
	srcDir := filepath.Dir(path)

	// Collect all candidate matches from the three patterns.
	type match struct{ raw []byte }
	candidates := make([]match, 0)

	for _, m := range tsImportFromRe.FindAllSubmatch(content, -1) {
		candidates = append(candidates, match{raw: m[1]})
	}
	for _, m := range tsRequireRe.FindAllSubmatch(content, -1) {
		candidates = append(candidates, match{raw: m[1]})
	}
	for _, m := range tsImportExprRe.FindAllSubmatch(content, -1) {
		candidates = append(candidates, match{raw: m[1]})
	}

	if len(candidates) == 0 {
		return nil
	}

	result := make([]string, 0, len(candidates))
	for _, c := range candidates {
		dir := resolveTSImportPath(string(c.raw), srcDir)
		if dir == "" {
			continue
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

// ---------------------------------------------------------------------------
// Python extractor
// ---------------------------------------------------------------------------

// analyzePython extracts module paths from Python `from X import Y` and
// `import X` statements, converts dots to slashes, and filters stdlib modules.
func analyzePython(content []byte) []string {
	seen := make(map[string]bool)
	result := make([]string, 0)

	// Match "from X import Y"
	for _, m := range pyFromImportRe.FindAllSubmatch(content, -1) {
		pkg := string(m[1])
		if isPythonStdlib(pkg) {
			continue
		}
		dir := strings.ReplaceAll(pkg, ".", "/") + "/"
		if !seen[dir] {
			seen[dir] = true
			result = append(result, dir)
		}
	}

	// Match "import X"
	for _, m := range pyImportRe.FindAllSubmatch(content, -1) {
		pkg := string(m[1])
		if isPythonStdlib(pkg) {
			continue
		}
		dir := strings.ReplaceAll(pkg, ".", "/") + "/"
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

// isPythonStdlib reports whether pkg is in Python's standard library by
// checking its top-level segment.
func isPythonStdlib(pkg string) bool {
	firstSeg := pkg
	if idx := strings.IndexByte(pkg, '.'); idx >= 0 {
		firstSeg = pkg[:idx]
	}
	return pythonStdlib[firstSeg]
}

// ---------------------------------------------------------------------------
// Rust extractor
// ---------------------------------------------------------------------------

// analyzeRust extracts crate paths from Rust `use` and `extern crate`
// statements, converts :: to /, and filters stdlib crates.
func analyzeRust(content []byte) []string {
	seen := make(map[string]bool)
	result := make([]string, 0)

	// Match "use foo::bar::Baz" or "use foo::bar"
	for _, m := range rustUseRe.FindAllSubmatch(content, -1) {
		pkg := string(m[1])
		if hasAnyPrefix(pkg, rustStdlibPrefixes) {
			continue
		}
		// Strip the last ::segment (the item name) to get the directory/module
		// containing the dependency.
		if idx := strings.LastIndex(pkg, "::"); idx >= 0 {
			pkg = pkg[:idx]
		}
		dir := strings.ReplaceAll(pkg, "::", "/") + "/"
		if !seen[dir] {
			seen[dir] = true
			result = append(result, dir)
		}
	}

	// Match "extern crate foo"
	for _, m := range rustExternCrateRe.FindAllSubmatch(content, -1) {
		pkg := string(m[1])
		// extern crate always refers to the crate root; skip stdlib crates.
		// rustStdlibPrefixes use the "std::" form, so append "::" to the bare
		// crate name for the prefix check (e.g. "std" → "std::").
		if hasAnyPrefix(pkg+"::", rustStdlibPrefixes) {
			continue
		}
		dir := pkg + "/"
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

// ---------------------------------------------------------------------------
// C / C++ extractor
// ---------------------------------------------------------------------------

// analyzeC extracts project include paths from C/C++ source.
// Only quoted includes ("...") are kept; angle-bracket includes (<...>) are
// treated as system headers and skipped.
func analyzeC(content []byte) []string {
	seen := make(map[string]bool)
	matches := cIncludeRe.FindAllSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}

	result := make([]string, 0, len(matches))
	for _, m := range matches {
		full := string(m[0])

		// Skip angle-bracket includes (system headers).
		if strings.Contains(full, "<") && !strings.Contains(full, `"`) {
			continue
		}

		p := string(m[1])
		dir := filepath.Dir(p)
		if dir == "." || dir == "/" {
			continue // header in the same directory or root — not useful
		}
		dir += "/"

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

// ---------------------------------------------------------------------------
// Ruby extractor
// ---------------------------------------------------------------------------

// analyzeRuby extracts local require paths from Ruby files.
// require_relative is always treated as local; plain require is kept only
// when the path starts with ./ or ../.
func analyzeRuby(path string, content []byte) []string {
	seen := make(map[string]bool)
	srcDir := filepath.Dir(path)

	matches := rubyRequireRe.FindAllSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}

	result := make([]string, 0, len(matches))
	for _, m := range matches {
		p := string(m[1])
		full := string(m[0])
		isRelative := strings.Contains(full, "require_relative")

		var resolved string

		if isRelative {
			// require_relative 'foo' → same directory as source.
			resolved = filepath.Dir(filepath.Join(srcDir, p)) + "/"
		} else {
			// Plain require: keep only paths starting with ./ or ../.
			if !strings.HasPrefix(p, "./") && !strings.HasPrefix(p, "../") {
				continue
			}
			resolved = filepath.Dir(filepath.Join(srcDir, p)) + "/"
		}

		if resolved == "/" || resolved == "./" {
			continue
		}
		if !seen[resolved] {
			seen[resolved] = true
			result = append(result, resolved)
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
}

// ---------------------------------------------------------------------------
// C# extractor
// ---------------------------------------------------------------------------

// analyzeCsharp extracts namespace paths from C# using directives,
// converts dots to slashes, and filters .NET framework namespaces.
func analyzeCsharp(content []byte) []string {
	seen := make(map[string]bool)
	matches := csUsingRe.FindAllSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}

	result := make([]string, 0, len(matches))
	for _, m := range matches {
		pkg := string(m[1])
		if hasAnyPrefix(pkg, csharpStdlibPrefixes) {
			continue
		}
		dir := strings.ReplaceAll(pkg, ".", "/") + "/"
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

// ---------------------------------------------------------------------------
// Markdown / RST / AsciiDoc extractor
// ---------------------------------------------------------------------------

// analyzeMarkdown extracts local link directory prefixes from Markdown
// (and related) documents. External URLs and anchor-only links are skipped.
// Directory prefixes are deduplicated.
func analyzeMarkdown(content []byte) []string {
	matches := mdLinkRe.FindAllSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(matches))
	var result []string
	for _, m := range matches {
		link := string(m[2])
		link = strings.TrimSpace(link)
		// Skip external URLs.
		if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") {
			continue
		}
		// Skip anchor-only links.
		if strings.HasPrefix(link, "#") {
			continue
		}
		// Extract directory prefix: "docs/foo.md" -> "docs/"
		// "./relative/file.md" -> "./relative/"
		// "../parent/file.md" -> "../parent/"
		dir := filepath.Dir(link)
		if dir == "." {
			continue // same-directory link, no useful path
		}
		dir = strings.TrimPrefix(dir, "./")
		key := dir + "/"
		if !seen[key] {
			seen[key] = true
			result = append(result, key)
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// javaPackageDir converts a Java import package path to a directory path,
// stripping trailing class/interface names (start with uppercase) and
// wildcards.  "com.foo.bar.Baz" → "com/foo/bar/".
func javaPackageDir(pkg string) string {
	parts := strings.Split(pkg, ".")
	// Walk from the end, dropping type names (uppercase first letter) and "*".
	for len(parts) > 0 {
		last := parts[len(parts)-1]
		if last == "*" || (len(last) > 0 && last[0] >= 'A' && last[0] <= 'Z') {
			parts = parts[:len(parts)-1]
		} else {
			break
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "/") + "/"
}

// hasAnyPrefix reports whether s starts with any of the given prefixes.
func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
