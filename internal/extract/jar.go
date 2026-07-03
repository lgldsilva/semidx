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
		if f.FileInfo().IsDir() {
			continue
		}
		entry := f.Name
		lower := strings.ToLower(entry)
		ext := strings.ToLower(path.Ext(entry))

		switch {
		case ext == ".class":
			raw, rerr := readZipEntry(f)
			if rerr != nil {
				continue
			}
			text, cerr := classAPI(raw)
			if cerr != nil {
				continue // unparseable class — skip, don't fail the whole jar
			}
			if dec != nil {
				if src, ok := dec.decompile(raw); ok {
					text += "\n" + src
				}
			}
			docs = append(docs, Doc{Path: name + "!" + entry, Text: text})

		case textEntryExts[ext] || strings.HasSuffix(lower, "/manifest.mf") || lower == "manifest.mf":
			raw, rerr := readZipEntry(f)
			if rerr != nil || !utf8.Valid(raw) {
				continue
			}
			if s := strings.TrimSpace(string(raw)); s != "" {
				docs = append(docs, Doc{Path: name + "!" + entry, Text: string(raw)})
			}
		}
	}
	return docs, nil
}

func readZipEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(io.LimitReader(rc, maxArchiveEntry))
}
