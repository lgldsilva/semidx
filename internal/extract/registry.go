package extract

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Doc is one extracted logical file: a searchable Text plus the Path it should be
// indexed under. Single-file documents produce one Doc; archives (a .jar) produce
// one per meaningful entry, path-namespaced as "archive.jar!entry".
type Doc struct {
	Path string
	Text string
}

// archiveExts are container formats expanded into many Docs.
var archiveExts = map[string]bool{".jar": true, ".war": true, ".aar": true}

// byName maps exact base filenames (no extension) to their decoder. Registered
// via RegisterName. Useful for Makefile, Dockerfile, LICENSE, etc.
var byName = map[string]extractor{}

// RegisterName adds a custom extractor for the given base filename (e.g.
// "Makefile", "Dockerfile"). Panics if name is already registered.
func RegisterName(names []string, fn extractor) {
	for _, n := range names {
		if _, ok := byName[n]; ok {
			panic("extract: duplicate name registration for " + n)
		}
		byName[n] = fn
	}
}

// ExtractAll turns a file into one or more searchable Docs. Documents map to a
// single Doc; archives fan out to one Doc per entry. Unknown types return
// ErrUnsupported; encrypted inputs ErrEncrypted. Panics are recovered by the
// underlying single-file Extract / by extractArchive so a bad file never crashes
// the indexer.
func ExtractAll(name string, data []byte) ([]Doc, error) {
	ext := strings.ToLower(filepath.Ext(name))
	if archiveExts[ext] {
		return extractArchive(name, data)
	}
	// Try extension-based first, then name-based fallback.
	if fn, ok := byExtFunc(name); ok {
		text, err := fn(name, data)
		if err != nil {
			return nil, err
		}
		return []Doc{{Path: name, Text: text}}, nil
	}
	return nil, fmt.Errorf("%w: %q", ErrUnsupported, name)
}

// byExtFunc resolves an extractor for name, checking exact base filename first
// (RegisterName) and extension-based extractors (Register) second. Name matches
// take precedence over extension matches so that specific files (e.g.
// "package-lock.json") are handled by their dedicated extractor rather than the
// generic extension handler (e.g. ".json").
func byExtFunc(name string) (func(string, []byte) (string, error), bool) {
	base := filepath.Base(name)
	if fn, ok := byName[base]; ok {
		return adaptExtractor(fn), true
	}
	ext := strings.ToLower(filepath.Ext(name))
	if fn, ok := byExt[ext]; ok {
		return adaptExtractor(fn), true
	}
	return nil, false
}

// adaptExtractor wraps an extractor ([]byte)→(string,error) to match the
// (string, []byte)→(string,error) signature needed by ExtractAll.
func adaptExtractor(fn extractor) func(string, []byte) (string, error) {
	return func(_ string, data []byte) (string, error) { return fn(data) }
}
