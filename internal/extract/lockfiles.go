package extract

import (
	"bufio"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	reJSONLockName = regexp.MustCompile(`"name"\s*:\s*"([^"]+)"`)
	reJSONLockVer  = regexp.MustCompile(`"version"\s*:\s*"([^"]+)"`)
	reGoSumModule  = regexp.MustCompile(`^(\S+)\s+v?(\S+)`)
	reTOMLName     = regexp.MustCompile(`^name\s*=\s*"([^"]+)"`)
	reTOMLVer      = regexp.MustCompile(`^version\s*=\s*"([^"]+)"`)
	reGemName      = regexp.MustCompile(`^\s{4}(\S+)\s+\(([^)]+)\)`)
	reYarnEntry    = regexp.MustCompile(`^\s*"?([^":]+@[^"]+)"?\s*:\s*$`)
)

func init() {
	RegisterName([]string{
		"package-lock.json",
		"yarn.lock",
		"poetry.lock",
		"Gemfile.lock",
		"go.sum",
		"Cargo.lock",
	}, extractLockfile)
}

// extractLockfile auto-detects the lockfile format from content and extracts
// package@version pairs in a structured format.
func extractLockfile(data []byte) (string, error) {
	if !utf8.Valid(data) {
		return "", ErrNotText
	}

	text := string(data)
	switch detectLockFormat(text) {
	case "json":
		return extractJSONLock(text), nil
	case "yarn":
		return extractYarnLock(text), nil
	case "gosum":
		return extractGoSum(text), nil
	case "cargo":
		return extractCargoLock(text), nil
	case "poetry":
		return extractPoetryLock(text), nil
	case "gemfile":
		return extractGemfileLock(text), nil
	default:
		return text, nil // fallback to raw text
	}
}

// detectLockFormat samples the first few non-blank lines to determine the
// lockfile format.
func detectLockFormat(text string) string {
	scanner := bufio.NewScanner(strings.NewReader(text))
	firstLines := make([]string, 0, 5)
	for scanner.Scan() && len(firstLines) < 5 {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			firstLines = append(firstLines, line)
		}
	}
	if len(firstLines) == 0 {
		return ""
	}

	first := firstLines[0]

	// JSON lockfile (package-lock.json).
	if strings.HasPrefix(first, "{") {
		return "json"
	}

	// Yarn lockfile: comment header followed by entry, or starts with an entry.
	if strings.HasPrefix(first, "#") {
		return "yarn"
	}
	if strings.Contains(first, "@") && strings.HasSuffix(first, ":") {
		return "yarn"
	}

	// go.sum: lines are "module version hash". Skip lines with = (TOML) or
	// # (comment).
	fields := strings.Fields(first)
	if len(fields) >= 2 && !strings.Contains(first, "=") && !strings.HasPrefix(first, "#") {
		if strings.HasPrefix(fields[1], "v") || strings.Count(first, " ") >= 2 {
			return "gosum"
		}
	}

	// TOML-based lockfiles (Cargo.lock / poetry.lock).
	if strings.HasPrefix(first, "[[package]]") {
		for _, l := range firstLines {
			if strings.Contains(l, "poetry") || strings.Contains(l, "Poetry") {
				return "poetry"
			}
		}
		return "cargo"
	}

	// Gemfile.lock has section headers like GEM, PATH, PLATFORMS, DEPENDENCIES.
	switch first {
	case "GEM", "PATH", "PLATFORMS", "DEPENDENCIES":
		return "gemfile"
	}

	return ""
}

// extractJSONLock scans a JSON lockfile (package-lock.json) line-by-line for
// "name" and "version" pairs.
func extractJSONLock(text string) string {
	lines := strings.Split(text, "\n")
	var entries []string
	var currentName, currentVer string

	for _, line := range lines {
		if m := reJSONLockName.FindStringSubmatch(line); m != nil {
			if currentName != "" && currentVer != "" {
				entries = append(entries, currentName+"@"+currentVer)
			}
			currentName = m[1]
			currentVer = ""
		}
		if m := reJSONLockVer.FindStringSubmatch(line); m != nil {
			currentVer = m[1]
		}
	}
	if currentName != "" && currentVer != "" {
		entries = append(entries, currentName+"@"+currentVer)
	}

	if len(entries) == 0 {
		return strings.TrimSpace(text)
	}
	return strings.Join(entries, "\n")
}

// extractYarnLock extracts package specifiers from a yarn.lock file. Each entry
// key looks like "name@version" or "@scope/name@version".
func extractYarnLock(text string) string {
	var entries []string
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := scanner.Text()
		if m := reYarnEntry.FindStringSubmatch(line); m != nil {
			entries = append(entries, m[1])
		}
	}
	if len(entries) == 0 {
		return strings.TrimSpace(text)
	}
	return strings.Join(entries, "\n")
}

// extractGoSum scans a go.sum for module@version pairs.
func extractGoSum(text string) string {
	var entries []string
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if m := reGoSumModule.FindStringSubmatch(line); m != nil {
			entries = append(entries, m[1]+"@"+m[2])
		}
	}
	if len(entries) == 0 {
		return strings.TrimSpace(text)
	}
	return strings.Join(entries, "\n")
}

// extractCargoLock parses a Cargo.lock (TOML [[package]] sections) for name@version.
func extractCargoLock(text string) string {
	var entries []string
	var currentName string
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if m := reTOMLName.FindStringSubmatch(line); m != nil {
			currentName = m[1]
		}
		if m := reTOMLVer.FindStringSubmatch(line); m != nil && currentName != "" {
			entries = append(entries, currentName+"@"+m[1])
			currentName = ""
		}
	}
	if len(entries) == 0 {
		return strings.TrimSpace(text)
	}
	return strings.Join(entries, "\n")
}

// extractPoetryLock parses a poetry.lock (same TOML structure as Cargo.lock).
func extractPoetryLock(text string) string {
	return extractCargoLock(text)
}

// extractGemfileLock scans a Gemfile.lock for gem specs (indented lines with
// name (version)).
func extractGemfileLock(text string) string {
	var entries []string
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := scanner.Text()
		if m := reGemName.FindStringSubmatch(line); m != nil {
			entries = append(entries, m[1]+"@"+m[2])
		}
	}
	if len(entries) == 0 {
		return strings.TrimSpace(text)
	}
	return strings.Join(entries, "\n")
}
