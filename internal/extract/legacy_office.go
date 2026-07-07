package extract

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// libreOfficeAvailable is set once at init and checked before registering legacy
// Office extractors so semidx never blocks on a missing tool.
var libreOfficeAvailable bool

func init() {
	if _, err := exec.LookPath("libreoffice"); err == nil {
		libreOfficeAvailable = true
		for _, ext := range []string{".doc", ".xls", ".ppt"} {
			Register(ext, extractLegacyOffice)
		}
	} else {
		log.Printf("extract: libreoffice not found in $PATH — .doc/.xls/.ppt support disabled")
	}
}

// extractLegacyOffice converts a legacy Microsoft Office document (.doc/.xls/.ppt)
// to plain text by spawning libreoffice --headless --convert-to txt. The
// conversion uses a 30-second timeout and writes the original bytes to a temp
// file with the correct extension so libreoffice recognises the format.
func extractLegacyOffice(data []byte) (string, error) {
	if !libreOfficeAvailable {
		return "", fmt.Errorf("extract: libreoffice not available")
	}

	// Create a temporary directory for the input and output files.
	tmpDir, err := os.MkdirTemp("", "semidx-legacy-*")
	if err != nil {
		return "", fmt.Errorf("extract: legacy office: create temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// The caller provides data but extractLegacyOffice is registered per-extension,
	// so we give the temp file the actual extension (preserved from the caller's
	// name — but we don't have the name here). Instead we write to a temp file
	// whose extension the caller must determine… but extractors only get []byte.
	//
	// We probe the first few bytes for OLE2 magic (all three formats are OLE2
	// compound files) and default to .doc as a safe guess since it is the most
	// common; libreoffice handles the extension-based dispatch internally.
	ext := ".doc"
	if len(data) >= 8 {
		if isOLECompound(data) {
			// All three are OLE2, so we cannot distinguish them by magic alone.
			// Default to .doc — libreoffice auto-detects the actual format anyway.
			ext = ".doc"
		} else if bytes.HasPrefix(data, []byte{0x09, 0x08, 0x10, 0x00, 0x00, 0x06, 0x05, 0x00}) {
			ext = ".xls" // BIFF8 workbook
		} else if bytes.HasPrefix(data, []byte{0x0F, 0x00, 0xE8, 0x03}) {
			ext = ".ppt" // PowerPoint magic
		}
	}

	inPath := filepath.Join(tmpDir, "document"+ext)
	if err := os.WriteFile(inPath, data, 0o600); err != nil {
		return "", fmt.Errorf("extract: legacy office: write input: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// #nosec G204 -- the command arguments are fixed strings (libreoffice flags)
	// and the temp file path is created by this function. No user input.
	cmd := exec.CommandContext(ctx, "libreoffice",
		"--headless",
		"--convert-to", "txt:Text",
		"--outdir", tmpDir,
		inPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("extract: legacy office: libreoffice failed: %w\n%s", err, string(out))
	}

	// LibreOffice writes a .txt file with the same base name as the input.
	outPath := filepath.Join(tmpDir, "document.txt")
	result, err := os.ReadFile(outPath)
	if err != nil {
		return "", fmt.Errorf("extract: legacy office: read output: %w", err)
	}
	return string(result), nil
}
