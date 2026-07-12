package extract

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"fmt"
	"io"
	"path"
	"strings"
	"unicode/utf8"
)

// Generic archive limits for zip-bomb and resource protection.
const (
	maxArchiveEntries   = 50        // maximum entries per archive
	maxArchiveEntrySize = 10 << 20  // 10 MiB per entry
	maxArchiveTotalSize = 100 << 20 // 100 MiB total extracted text
	maxArchiveDepth     = 2         // max nesting depth (0 = top-level)
)

// genericArchiveExts maps archive type identifiers to their recognised file
// extensions. Compound extensions (.tar.gz, .tar.bz2) are listed first so
// the longest suffix matches before the shorter one.
var genericArchiveExts = []string{".tar.gz", ".tar.bz2", ".zip", ".tar"}

// archiveType returns the recognised archive extension for name, handling
// compound extensions (.tar.gz, .tar.bz2). Returns "" if the name does not
// match any known generic archive format.
func archiveType(name string) string {
	lower := strings.ToLower(name)
	for _, ext := range genericArchiveExts {
		if strings.HasSuffix(lower, ext) {
			return ext
		}
	}
	return ""
}

// isGenericArchive reports whether name has a recognised generic archive extension.
func isGenericArchive(name string) bool {
	return archiveType(name) != ""
}

// extractGenericArchive expands a .zip, .tar, .tar.gz or .tar.bz2 into one Doc
// per text entry, with aggressive caps to prevent zip-bomb resource exhaustion.
// Each text entry is indexed as-is (valid UTF-8 content only). Virtual paths
// use the format "archive.zip!path/to/entry".
func extractGenericArchive(name string, data []byte) (docs []Doc, err error) {
	defer func() {
		if r := recover(); r != nil {
			docs, err = nil, fmt.Errorf("extract: generic archive decoder panicked: %v", r)
		}
	}()

	return extractArchiveEntries(name, data, 0)
}

// extractArchiveEntries does the actual archive expansion with depth tracking
// for nested archive protection.
func extractArchiveEntries(name string, data []byte, depth int) ([]Doc, error) {
	archType := archiveType(name)
	if archType == "" {
		return nil, fmt.Errorf("extract: unknown archive type: %s", name)
	}

	switch archType {
	case ".zip":
		return extractZipEntries(name, data, depth)
	case ".tar":
		return extractTarEntries(name, data, nil, depth)
	case ".tar.gz":
		gr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("extract: %s: gzip: %w", name, err)
		}
		defer func() { _ = gr.Close() }()
		return extractTarEntries(name, data, gr, depth)
	case ".tar.bz2":
		bz := bzip2.NewReader(bytes.NewReader(data))
		return extractTarEntries(name, data, bz, depth)
	default:
		return nil, fmt.Errorf("extract: unsupported archive type: %s", archType)
	}
}

// extractZipEntries reads entries from a zip archive.
func extractZipEntries(name string, data []byte, depth int) ([]Doc, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("extract: %s: open zip: %w", name, err)
	}

	var docs []Doc
	totalSize := 0

	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if len(docs) >= maxArchiveEntries || totalSize >= maxArchiveTotalSize {
			break
		}

		entryDocs := tryProcessZipEntry(f, name, depth, &totalSize)
		docs = append(docs, entryDocs...)
	}

	return docs, nil
}

// cleanEntryName normalises an archive entry name for use in a virtual-path
// identifier, stripping leading separators and ".." traversal. Entries are
// never written to disk (the result is only a chunk identifier), so this is
// defence-in-depth and consistency rather than a zip-slip fix.
func cleanEntryName(entryName string) string {
	return strings.TrimPrefix(path.Clean("/"+entryName), "/")
}

func tryProcessZipEntry(f *zip.File, name string, depth int, totalSize *int) []Doc {
	entryName := cleanEntryName(f.Name)
	virtualPath := name + "!" + entryName
	entryExt := strings.ToLower(path.Ext(entryName))

	if depth < maxArchiveDepth && isGenericArchive(entryName) {
		entryData, err := readZipEntryBytes(f)
		if err != nil {
			return nil
		}
		innerDocs, _ := extractArchiveEntries(virtualPath, entryData, depth+1)
		return innerDocs
	}

	if !isTextEntry(entryExt) {
		return nil
	}

	raw, err := readZipEntryBytes(f)
	if err != nil || !utf8.Valid(raw) {
		return nil
	}
	text := string(raw)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if *totalSize+len(text) > maxArchiveTotalSize {
		return nil
	}
	*totalSize += len(text)
	return []Doc{{Path: virtualPath, Text: text}}
}

// extractTarEntries reads entries from a tar archive, optionally wrapped in a
// decompression reader (gzip or bzip2). The data parameter is the raw bytes of
// the compressed archive (used for recursive decompression of nested archives).
func extractTarEntries(name string, data []byte, decompress io.Reader, depth int) ([]Doc, error) {
	var tr *tar.Reader
	if decompress != nil {
		tr = tar.NewReader(decompress)
	} else {
		tr = tar.NewReader(bytes.NewReader(data))
	}

	var docs []Doc
	totalSize := 0
	entryCount := 0

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			// tar.Reader memoises its error: after a non-EOF failure every
			// subsequent Next returns the same error, so "continue" would spin
			// forever on a corrupt archive (CPU 100%). Stop reading instead.
			break
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		entryCount++
		if entryCount > maxArchiveEntries || totalSize >= maxArchiveTotalSize {
			break
		}

		entryDocs := tryProcessTarEntry(tr, hdr, name, depth, &totalSize)
		docs = append(docs, entryDocs...)

		if totalSize >= maxArchiveTotalSize {
			break
		}
	}

	return docs, nil
}

func tryProcessTarEntry(tr *tar.Reader, hdr *tar.Header, name string, depth int, totalSize *int) []Doc {
	entryName := cleanEntryName(hdr.Name)
	virtualPath := name + "!" + entryName
	entryExt := strings.ToLower(path.Ext(entryName))

	if depth < maxArchiveDepth && isGenericArchive(entryName) {
		entryData, err := io.ReadAll(io.LimitReader(tr, maxArchiveEntrySize))
		if err != nil {
			return nil
		}
		innerDocs, _ := extractArchiveEntries(virtualPath, entryData, depth+1)
		return innerDocs
	}

	if !isTextEntry(entryExt) {
		return nil
	}

	raw, err := io.ReadAll(io.LimitReader(tr, maxArchiveEntrySize))
	if err != nil || !utf8.Valid(raw) {
		return nil
	}
	text := string(raw)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if *totalSize+len(text) > maxArchiveTotalSize {
		return nil
	}
	*totalSize += len(text)
	return []Doc{{Path: virtualPath, Text: text}}
}

// isTextEntry reports whether a file extension is likely to hold text content
// suitable for indexing from an archive.
func isTextEntry(ext string) bool {
	switch ext {
	case ".txt", ".md", ".markdown", ".html", ".htm", ".xml", ".json",
		".yaml", ".yml", ".toml", ".ini", ".cfg", ".conf",
		".csv", ".tsv", ".log", ".properties", ".env",
		".go", ".py", ".js", ".ts", ".java", ".rb", ".rs", ".sh", ".bash",
		".c", ".h", ".cpp", ".hpp", ".css", ".scss", ".less",
		".sql", ".r", ".m", ".swift", ".kt", ".scala", ".groovy",
		".gradle", ".sbt", ".tf", ".dockerfile":
		return true
	default:
		return false
	}
}

// readZipEntryBytes reads the content of a zip file entry, capped at maxArchiveEntrySize.
func readZipEntryBytes(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(io.LimitReader(rc, maxArchiveEntrySize))
}
