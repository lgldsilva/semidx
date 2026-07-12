package extract

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode/utf8"
)

// maxArchiveEntry caps how many bytes we read from a single archive entry.
const maxArchiveEntry = 1 << 20 // 1 MiB

// textEntryExts inside an archive are indexed as-is (source and text resources).
var textEntryExts = map[string]bool{
	".java": true, ".kt": true, ".scala": true, ".groovy": true,
	".properties": true, ".xml": true, ".json": true, ".yaml": true, ".yml": true,
	".txt": true, ".md": true, ".sql": true, ".mf": true,
}

// extractArchive expands a JAR/WAR/AAR (a zip) into one Doc per meaningful entry:
// each .class becomes its API surface (constant-pool names, plus decompiled
// pseudo-Java when a decompiler is configured) and each source/text resource is
// indexed as-is. Entry Docs are path-namespaced as "archive!entry". A payload
// that is not a valid zip returns an error (never a panic).
func extractArchive(name string, data []byte) (docs []Doc, err error) {
	defer func() {
		if r := recover(); r != nil {
			docs, err = nil, fmt.Errorf("extract: archive decoder panicked: %v", r)
		}
	}()

	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("extract: not a valid archive: %w", err)
	}

	dec := newDecompiler() // nil unless SEMIDX_JAVA_DECOMPILER is configured
	for _, f := range zr.File {
		if doc, ok := archiveEntryDoc(name, f, dec); ok {
			docs = append(docs, doc)
		}
	}
	return docs, nil
}

// archiveEntryDoc turns one archive entry into a Doc: a .class becomes its API
// surface (optionally with decompiled source), a source/text resource is indexed
// as-is. Directories and unrecognised entries return ok=false.
func archiveEntryDoc(name string, f *zip.File, dec *decompiler) (Doc, bool) {
	if f.FileInfo().IsDir() {
		return Doc{}, false
	}
	switch {
	case strings.ToLower(path.Ext(f.Name)) == ".class":
		return classEntryDoc(name, f, dec)
	case isTextArchiveEntry(f.Name):
		return textEntryDoc(name, f)
	default:
		return Doc{}, false
	}
}

// isTextArchiveEntry reports whether an entry is a source/text resource indexed
// as-is (a known text extension or a JAR manifest).
func isTextArchiveEntry(entry string) bool {
	lower := strings.ToLower(entry)
	ext := strings.ToLower(path.Ext(entry))
	return textEntryExts[ext] || strings.HasSuffix(lower, "/manifest.mf") || lower == "manifest.mf"
}

// classEntryDoc builds the Doc for a .class entry from its constant-pool API
// surface, appending decompiled pseudo-Java when a decompiler is configured. An
// unreadable or unparseable class returns ok=false (skip, don't fail the jar).
func classEntryDoc(name string, f *zip.File, dec *decompiler) (Doc, bool) {
	raw, err := readZipEntry(f)
	if err != nil {
		return Doc{}, false
	}
	text, err := classAPI(raw)
	if err != nil {
		return Doc{}, false // unparseable class — skip, don't fail the whole jar
	}
	if dec != nil {
		if src, ok := dec.decompile(raw); ok {
			text += "\n" + src
		}
	}
	return Doc{Path: name + "!" + f.Name, Text: text}, true
}

// textEntryDoc builds the Doc for a source/text entry, skipping non-UTF-8 or
// blank content.
func textEntryDoc(name string, f *zip.File) (Doc, bool) {
	raw, err := readZipEntry(f)
	if err != nil || !utf8.Valid(raw) {
		return Doc{}, false
	}
	if strings.TrimSpace(string(raw)) == "" {
		return Doc{}, false
	}
	return Doc{Path: name + "!" + f.Name, Text: string(raw)}, true
}

func readZipEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(io.LimitReader(rc, maxArchiveEntry))
}
