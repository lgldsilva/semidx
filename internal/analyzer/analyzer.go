// Package analyzer extracts symbol names with line ranges from source code
// using tree-sitter grammars. It dispatches by file extension to
// language-specific extractors via a table-driven map.
//
// All extractors are best-effort: parse failures return nil silently.
// Deduplication is applied within each file so the same symbol never appears
// twice per list.
package analyzer

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// Symbol represents one named declaration in a source file.
type Symbol struct {
	Name      string // e.g. "main"
	Kind      string // e.g. "func", "method", "type", "class", "interface", "enum"
	StartLine int    // 1-based line of the declaration start (from tree-sitter Row + 1)
	EndLine   int    // 1-based line of the declaration end
}

// extractor describes how to extract symbols from one language.
type extractor struct {
	lang     func() *gotreesitter.Language
	queryStr string
}

// extractors maps file extensions to their extractor definition. Queries are
// compiled once at first use (see initCompiled).
var extractors = map[string]extractor{
	".go":    {grammars.GoLanguage, goQuery},
	".java":  {grammars.JavaLanguage, javaQuery},
	".kt":    {grammars.JavaLanguage, javaQuery},
	".scala": {grammars.JavaLanguage, javaQuery},
	".js":    {grammars.JavascriptLanguage, jsQuery},
	".jsx":   {grammars.JavascriptLanguage, jsQuery},
	".mjs":   {grammars.JavascriptLanguage, jsQuery},
	".cjs":   {grammars.JavascriptLanguage, jsQuery},
	".ts":    {grammars.TypescriptLanguage, tsQuery},
	".tsx":   {grammars.TsxLanguage, tsxQuery},
	".py":    {grammars.PythonLanguage, pyQuery},
	".tf":    {grammars.HclLanguage, hclQuery},
}

const goQuery = `
(function_declaration name: (identifier) @name) @decl
(method_declaration name: (field_identifier) @name) @decl
(type_spec name: (type_identifier) @name) @decl
`

const javaQuery = `
(class_declaration name: (identifier) @name) @decl
(method_declaration name: (identifier) @name) @decl
(interface_declaration name: (identifier) @name) @decl
(enum_declaration name: (identifier) @name) @decl
(record_declaration name: (identifier) @name) @decl
`

const jsQuery = `
(function_declaration name: (identifier) @name) @decl
(class_declaration name: (identifier) @name) @decl
(method_definition name: (property_identifier) @name) @decl
`

const tsQuery = `
(function_declaration name: (identifier) @name) @decl
(method_definition name: (property_identifier) @name) @decl
(class_declaration name: (type_identifier) @name) @decl
(interface_declaration name: (type_identifier) @name) @decl
(enum_declaration name: (identifier) @name) @decl
`

const pyQuery = `
(function_definition name: (identifier) @name) @decl
(class_definition name: (identifier) @name) @decl
`

const hclQuery = `
(block (identifier) @name) @decl
`

const tsxQuery = `
(function_declaration name: (identifier) @name) @decl
(method_definition name: (property_identifier) @name) @decl
(class_declaration name: [(identifier) (type_identifier)] @name) @decl
(interface_declaration name: (type_identifier) @name) @decl
(enum_declaration name: (identifier) @name) @decl
`

// compiledExtractor holds a pre-compiled query and its language.
type compiledExtractor struct {
	query *gotreesitter.Query
	lang  *gotreesitter.Language
}

var (
	compileOnce sync.Once
	compiledMap map[string]*compiledExtractor
	initErr     error
)

// initCompiled compiles all queries once. Errors are returned via getCompiled.
func initCompiled() {
	compiledMap = make(map[string]*compiledExtractor, len(extractors))
	for ext, e := range extractors {
		lang := e.lang()
		q, err := gotreesitter.NewQuery(e.queryStr, lang)
		if err != nil {
			initErr = fmt.Errorf("analyzer: compile query for %s: %w", ext, err)
			return
		}
		compiledMap[ext] = &compiledExtractor{query: q, lang: lang}
	}
}

// getCompiled returns the compiled extractor for the given extension, or nil
// if the extension is not supported or initialization failed.
func getCompiled(ext string) *compiledExtractor {
	compileOnce.Do(initCompiled)
	if initErr != nil {
		return nil
	}
	return compiledMap[ext]
}

// Symbols extracts symbol names and line ranges from source code identified by
// path (used only for extension detection). Returns nil for unsupported
// extensions or parse failures (best-effort).
func Symbols(path string, content []byte) []Symbol {
	if len(content) == 0 {
		return nil
	}
	ext := strings.ToLower(filepath.Ext(path))
	ce := getCompiled(ext)
	if ce == nil {
		return nil
	}
	return extract(ce, content)
}

// extract parses content with the given compiled extractor and returns the
// deduplicated symbol list.
func extract(ce *compiledExtractor, content []byte) []Symbol {
	parser := gotreesitter.NewParser(ce.lang)
	tree, err := parser.Parse(content)
	if err != nil || tree == nil {
		return nil
	}
	defer tree.Release()

	root := tree.RootNode()
	if root == nil {
		return nil
	}

	cursor := ce.query.Exec(root, ce.lang, content)
	var symbols []Symbol
	for {
		m, ok := cursor.NextMatch()
		if !ok {
			break
		}
		var declNode, nameNode *gotreesitter.Node
		for _, cap := range m.Captures {
			switch cap.Name {
			case "decl":
				declNode = cap.Node
			case "name":
				nameNode = cap.Node
			}
		}
		if declNode == nil || nameNode == nil {
			continue
		}
		symbols = append(symbols, Symbol{
			Name:      nameNode.Text(content),
			Kind:      kindFromNodeType(declNode.Type(ce.lang)),
			StartLine: int(declNode.StartPoint().Row) + 1,
			EndLine:   int(declNode.EndPoint().Row) + 1,
		})
	}
	return dedupe(symbols)
}

// kindFromNodeType maps tree-sitter node type names to short Kind strings.
func kindFromNodeType(nt string) string {
	switch nt {
	case "function_declaration":
		return "func"
	case "method_declaration", "method_definition":
		return "method"
	case "type_spec":
		return "type"
	case "class_declaration":
		return "class"
	case "interface_declaration":
		return "interface"
	case "enum_declaration":
		return "enum"
	case "record_declaration":
		return "record"
	case "function_definition":
		return "func"
	case "class_definition":
		return "class"
	case "block":
		return "block"
	default:
		return nt
	}
}
