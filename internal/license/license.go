// Package license detects project and file licences from LICENSE files,
// SPDX headers, and inline declarations.
package license

import (
	"bufio"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Info describes a detected license.
type Info struct {
	SPDXID     string  // "MIT", "Apache-2.0", "GPL-3.0", etc.
	Confidence float64 // 0.0–1.0
	Source     string  // "filename", "header", "declared"
}

// Detect inspects file content for license information and returns the most
// confident match.  It works on three levels, checked in order:
//
//  1. If the path's basename matches a known license file pattern (LICENSE*,
//     LICENSE-*, COPYING, etc.) the content is matched against known license
//     texts via substring/pattern matching.
//  2. The first 30 lines are scanned for SPDX-License-Identifier headers.
//  3. The first 30 lines are scanned for common "Licensed under" and
//     "License" declaration patterns.
func Detect(path, content string) Info {
	basename := strings.ToLower(filepath.Base(path))

	// Priority 1: license file matching (basename heuristic).
	if isLicenseFileName(basename) {
		if info := matchLicenseText(content); info.SPDXID != "" {
			info.Source = "filename"
			return info
		}
	}

	// Priority 2: SPDX header in first 30 lines.
	if info := detectSPDXHeader(content); info.SPDXID != "" {
		info.Source = "header"
		return info
	}

	// Priority 3: declared license pattern (e.g. "MIT License" in a comment).
	if info := detectDeclared(content); info.SPDXID != "" {
		info.Source = "declared"
		return info
	}

	return Info{}
}

// ---------------------------------------------------------------------------
// License file names
// ---------------------------------------------------------------------------

func isLicenseFileName(basename string) bool {
	if basename == "license" || basename == "copying" || basename == "copying.lib" {
		return true
	}
	if strings.HasPrefix(basename, "license.") || strings.HasPrefix(basename, "licence.") {
		return true
	}
	if strings.HasPrefix(basename, "license-") || strings.HasPrefix(basename, "licence-") {
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// License text matching via pattern detection
// ---------------------------------------------------------------------------

// licenseSignature maps a unique substring (or regex pattern) to an SPDX ID.
// The more distinctive the snippet, the higher the confidence.
type licenseSig struct {
	spdxID string
	text   string // substring to search for (case-insensitive)
}

var licenseTextPatterns = []licenseSig{
	{spdxID: "Apache-2.0", text: "apache license, version 2.0"},
	{spdxID: "MIT", text: "permission is hereby granted, free of charge, to any person obtaining a copy"},
	{spdxID: "GPL-2.0-only", text: "gnu general public license, version 2"},
	{spdxID: "GPL-3.0-only", text: "gnu general public license, version 3"},
	{spdxID: "LGPL-2.1-only", text: "gnu lesser general public license, version 2.1"},
	{spdxID: "LGPL-3.0-only", text: "gnu lesser general public license, version 3"},
	{spdxID: "BSD-2-Clause", text: "redistribution and use in source and binary forms, with or without modification"},
	{spdxID: "BSD-3-Clause", text: "redistribution and use in source and binary forms, with or without"},
	{spdxID: "MPL-2.0", text: "mozilla public license, version 2.0"},
	{spdxID: "Unlicense", text: "this is free and unencumbered software released into the public domain"},
	{spdxID: "Zlib", text: "this software is provided 'as-is', without any express or implied warranty"},
	{spdxID: "ISC", text: "isc license"},
	{spdxID: "PostgreSQL", text: "postgresql license"},
	{spdxID: "AGPL-3.0-only", text: "gnu affero general public license, version 3"},
	{spdxID: "EUPL-1.2", text: "european union public licence"},
	{spdxID: "0BSD", text: "zero-clause bsd"},
}

func matchLicenseText(content string) Info {
	lower := strings.ToLower(content)
	for _, sig := range licenseTextPatterns {
		if strings.Contains(lower, sig.text) {
			// Penalise very short snippets to avoid false matches from generic
			// phrases; prefer longer, more distinctive texts.
			conf := 0.7
			if len(sig.text) > 80 {
				conf = 0.9
			}
			return Info{SPDXID: sig.spdxID, Confidence: conf}
		}
	}
	return Info{}
}

// ---------------------------------------------------------------------------
// SPDX header detection
// ---------------------------------------------------------------------------

var spdxRe = regexp.MustCompile(`(?i)SPDX-License-Identifier:\s*([\w.-]+)`)

func detectSPDXHeader(content string) Info {
	scanner := bufio.NewScanner(strings.NewReader(content))
	limit := 0
	for scanner.Scan() && limit < 30 {
		line := scanner.Text()
		limit++
		if m := spdxRe.FindStringSubmatch(line); len(m) > 1 {
			return Info{SPDXID: strings.TrimSpace(m[1]), Confidence: 0.95}
		}
	}
	return Info{}
}

// ---------------------------------------------------------------------------
// Declared license detection (e.g. "MIT License" in package metadata)
// ---------------------------------------------------------------------------

var declaredPatterns = []struct {
	re    *regexp.Regexp
	spdx  string
	score float64
}{
	{regexp.MustCompile(`(?i)^\s*[#*/\-\s]*MIT\s+License`), "MIT", 0.6},
	{regexp.MustCompile(`(?i)^\s*[#*/\-\s]*Apache\s+License\b`), "Apache-2.0", 0.6},
	{regexp.MustCompile(`(?i)^\s*[#*/\-\s]*Licensed under the Apache License, Version 2.0`), "Apache-2.0", 0.7},
	{regexp.MustCompile(`(?i)^\s*[#*/\-\s]*GNU (Lesser )?General Public License`), "GPL-3.0-only", 0.5},
	{regexp.MustCompile(`(?i)^\s*[#*/\-\s]*BSD [23]-Clause`), "BSD-3-Clause", 0.6},
}

func detectDeclared(content string) Info {
	scanner := bufio.NewScanner(strings.NewReader(content))
	limit := 0
	for scanner.Scan() && limit < 30 {
		line := scanner.Text()
		limit++
		for _, dp := range declaredPatterns {
			if dp.re.MatchString(line) {
				return Info{SPDXID: dp.spdx, Confidence: dp.score}
			}
		}
		// Also check for "Licensed under the <SPDX>".
		if strings.Contains(strings.ToLower(line), "licensed under") {
			for _, dp := range declaredPatterns {
				if dp.re.MatchString(line) {
					// Already caught above; just confirming the pattern.
					return Info{SPDXID: dp.spdx, Confidence: dp.score + 0.1}
				}
			}
		}
	}
	return Info{}
}

// NormaliseSPDX normalises a candidate SPDX ID (e.g. "MIT License" → "MIT").
func NormaliseSPDX(id string) string {
	id = strings.TrimSpace(id)
	id = strings.TrimSuffix(id, " License")
	id = strings.TrimSuffix(id, " license")
	return id
}

// IsPermissive reports whether the given SPDX ID is a permissive (non-copyleft)
// license suitable for use as a dependency without viral obligations.
func IsPermissive(spdxID string) bool {
	switch spdxID {
	case "MIT", "Apache-2.0", "BSD-2-Clause", "BSD-3-Clause",
		"ISC", "Unlicense", "0BSD", "Zlib", "PostgreSQL",
		"CC0-1.0", "CC-BY-4.0":
		return true
	}
	return false
}

// String returns a human-readable summary of the Info.
func (i Info) String() string {
	if i.SPDXID == "" {
		return "unknown"
	}
	conf := fmt.Sprintf("%.0f%%", i.Confidence*100)
	return fmt.Sprintf("%s (%s, from %s)", i.SPDXID, conf, i.Source)
}
