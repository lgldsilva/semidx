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

// Source identifiers for detected licenses.
const (
	sourceFilename = "filename"
	sourceHeader   = "header"
	sourceDeclared = "declared"
)

// unknownLicense is the human-readable label for an undetected license.
const unknownLicense = "unknown"

// SPDX ID constants used across patterns, permissiveness checks, and tests.
const (
	spdxApache2    = "Apache-2.0"
	spdxMIT        = "MIT"
	spdxGPL2       = "GPL-2.0-only"
	spdxGPL3       = "GPL-3.0-only"
	spdxLGPL21     = "LGPL-2.1-only"
	spdxLGPL3      = "LGPL-3.0-only"
	spdxBSD2       = "BSD-2-Clause"
	spdxBSD3       = "BSD-3-Clause"
	spdxMPL2       = "MPL-2.0"
	spdxUnlicense  = "Unlicense"
	spdxZlib       = "Zlib"
	spdxISC        = "ISC"
	spdxPostgreSQL = "PostgreSQL"
	spdxAGPL3      = "AGPL-3.0-only"
	spdxEUPL12     = "EUPL-1.2"
	spdx0BSD       = "0BSD"
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
			info.Source = sourceFilename
			return info
		}
	}

	// Priority 2: SPDX header in first 30 lines.
	if info := detectSPDXHeader(content); info.SPDXID != "" {
		info.Source = sourceHeader
		return info
	}

	// Priority 3: declared license pattern (e.g. "MIT License" in a comment).
	if info := detectDeclared(content); info.SPDXID != "" {
		info.Source = sourceDeclared
		return info
	}

	return Info{}
}

// ---------------------------------------------------------------------------
// License file names
// ---------------------------------------------------------------------------

// licenseFileNamePrefixes lists the prefixes that identify a license file when
// the basename is not an exact match.
var licenseFileNamePrefixes = []string{
	"license.", "licence.",
	"license-", "licence-",
}

func isLicenseFileName(basename string) bool {
	switch basename {
	case "license", "copying", "copying.lib":
		return true
	}
	for _, prefix := range licenseFileNamePrefixes {
		if strings.HasPrefix(basename, prefix) {
			return true
		}
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
	{spdxID: spdxApache2, text: "apache license, version 2.0"},
	{spdxID: spdxMIT, text: "permission is hereby granted, free of charge, to any person obtaining a copy"},
	{spdxID: spdxGPL2, text: "gnu general public license, version 2"},
	{spdxID: spdxGPL3, text: "gnu general public license, version 3"},
	{spdxID: spdxLGPL21, text: "gnu lesser general public license, version 2.1"},
	{spdxID: spdxLGPL3, text: "gnu lesser general public license, version 3"},
	{spdxID: spdxBSD2, text: "redistribution and use in source and binary forms, with or without modification"},
	{spdxID: spdxBSD3, text: "redistribution and use in source and binary forms, with or without"},
	{spdxID: spdxMPL2, text: "mozilla public license, version 2.0"},
	{spdxID: spdxUnlicense, text: "this is free and unencumbered software released into the public domain"},
	{spdxID: spdxZlib, text: "this software is provided 'as-is', without any express or implied warranty"},
	{spdxID: spdxISC, text: "isc license"},
	{spdxID: spdxPostgreSQL, text: "postgresql license"},
	{spdxID: spdxAGPL3, text: "gnu affero general public license, version 3"},
	{spdxID: spdxEUPL12, text: "european union public licence"},
	{spdxID: spdx0BSD, text: "zero-clause bsd"},
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
	{regexp.MustCompile(`(?i)^\s*[#*/\-\s]*MIT\s+License`), spdxMIT, 0.6},
	{regexp.MustCompile(`(?i)^\s*[#*/\-\s]*Apache\s+License\b`), spdxApache2, 0.6},
	{regexp.MustCompile(`(?i)^\s*[#*/\-\s]*Licensed under the Apache License, Version 2.0`), spdxApache2, 0.7},
	{regexp.MustCompile(`(?i)^\s*[#*/\-\s]*GNU (Lesser )?General Public License`), spdxGPL3, 0.5},
	{regexp.MustCompile(`(?i)^\s*[#*/\-\s]*BSD [23]-Clause`), spdxBSD3, 0.6},
}

// matchDeclaredLine scans declaredPatterns for a match against line and returns
// the SPDX ID, confidence score, and whether a match was found.
func matchDeclaredLine(line string) (spdx string, score float64, ok bool) {
	for _, dp := range declaredPatterns {
		if dp.re.MatchString(line) {
			return dp.spdx, dp.score, true
		}
	}
	return "", 0, false
}

func detectDeclared(content string) Info {
	scanner := bufio.NewScanner(strings.NewReader(content))
	limit := 0
	for scanner.Scan() && limit < 30 {
		line := scanner.Text()
		limit++
		if spdx, score, ok := matchDeclaredLine(line); ok {
			return Info{SPDXID: spdx, Confidence: score}
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
	case spdxMIT, spdxApache2, spdxBSD2, spdxBSD3,
		spdxISC, spdxUnlicense, spdx0BSD, spdxZlib, spdxPostgreSQL,
		"CC0-1.0", "CC-BY-4.0":
		return true
	}
	return false
}

// String returns a human-readable summary of the Info.
func (i Info) String() string {
	if i.SPDXID == "" {
		return unknownLicense
	}
	conf := fmt.Sprintf("%.0f%%", i.Confidence*100)
	return fmt.Sprintf("%s (%s, from %s)", i.SPDXID, conf, i.Source)
}
