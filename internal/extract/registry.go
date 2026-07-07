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

// archiveExts are container formats expanded into many Docs via the JAR extractor
// (archive/zip with .class processing). Generic archives (.zip, .tar, …) are
// handled by a separate registry — see genericArchiveExts.
var archiveExts = map[string]bool{".jar": true, ".war": true, ".aar": true}

// ExtractAll turns a file into one or more searchable Docs. Documents map to a
// single Doc; archives fan out to one Doc per entry. Unknown types return
// ErrUnsupported; encrypted inputs ErrEncrypted. Panics are recovered by the
// underlying single-file Extract / by extractArchive so a bad file never crashes
// the indexer.
func ExtractAll(name string, data []byte) ([]Doc, error) {
	ext := strings.ToLower(filepath.Ext(name))
	// Check compound archive extensions first (.tar.gz, .tar.bz2).
	if archType := archiveType(name); archType != "" {
		return extractGenericArchive(name, data)
	}
	if archiveExts[ext] {
		return extractArchive(name, data)
	}
	if _, ok := byExt[ext]; ok {
		text, err := Extract(name, data)
		if err != nil {
			return nil, err
		}
		return []Doc{{Path: name, Text: text}}, nil
	}
	return nil, fmt.Errorf("%w: %q", ErrUnsupported, ext)
}
