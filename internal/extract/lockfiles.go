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
	reGoSumModule  = regexp.MustCompile(`^(\S+)\s+(\S+)`)
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
	firstLines := sampleFirstLines(text)
	if len(firstLines) == 0 {
		return ""
	}

	first := firstLines[0]

	// JSON lockfile (package-lock.json).
	if strings.HasPrefix(first, "{") {
		return "json"
	}

	// TOML-based lockfiles (Cargo.lock / poetry.lock). Check before yarn
	// because poetry.lock may have a comment header ("# Poetry lockfile")
	// before "[[package]]".
	if fmt := detectTOMLLockFormat(first, firstLines); fmt != "" {
		return fmt
	}

	// Yarn lockfile: scan all sample lines for an entry pattern or a comment
	// header containing "yarn"/"Yarn".
	if fmt := detectYarnFormat(firstLines); fmt != "" {
		return fmt
	}

	// go.sum: lines are "module version hash".
	if fmt := detectGoSumFormat(first); fmt != "" {
		return fmt
	}

	// Gemfile.lock has section headers like GEM, PATH, PLATFORMS, DEPENDENCIES.
	if isGemfileHeader(first) {
		return "gemfile"
	}

	return ""
}

// sampleFirstLines returns the first up-to-5 non-blank lines of text.
func sampleFirstLines(text string) []string {
	scanner := bufio.NewScanner(strings.NewReader(text))
	firstLines := make([]string, 0, 5)
	for scanner.Scan() && len(firstLines) < 5 {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			firstLines = append(firstLines, line)
		}
	}
	return firstLines
}

// detectYarnFormat returns "yarn" if any of the sample lines looks like a
// yarn.lock entry ("name@version:") or a comment header containing "yarn".
func detectYarnFormat(sample []string) string {
	for _, line := range sample {
		if strings.Contains(line, "@") && strings.HasSuffix(line, ":") {
			return "yarn"
		}
	}
	// Fallback for comment-only headers like "# yarn.lock".
	for _, line := range sample {
		if strings.HasPrefix(line, "#") &&
			(strings.Contains(line, "yarn") || strings.Contains(line, "Yarn")) {
			return "yarn"
		}
	}
	return ""
}

// detectGoSumFormat returns "gosum" if the first line looks like a go.sum
// entry (module version hash), otherwise "".
// A valid go.sum line has a version starting with "v" (e.g. "mod v1.0.0") or
// a module-like first field (contains "/") with at least three fields
// (e.g. "github.com/foo 1.0.0 h1:abc"). The "=" check is intentionally omitted
// because go.sum hashes may contain "=" (e.g. "h1:abc="); TOML-based lockfiles
// are checked first, so "name = value" lines never reach this function.
func detectGoSumFormat(first string) string {
	fields := strings.Fields(first)
	if len(fields) >= 2 && !strings.HasPrefix(first, "#") {
		if strings.HasPrefix(fields[1], "v") {
			return "gosum"
		}
		if len(fields) >= 3 && strings.Contains(fields[0], "/") {
			return "gosum"
		}
	}
	return ""
}

// detectTOMLLockFormat returns "poetry" or "cargo" if any of the sample lines
// contains "[[package]]" (TOML-based lockfile), otherwise "".
func detectTOMLLockFormat(first string, firstLines []string) string {
	hasPackage := false
	for _, l := range firstLines {
		if strings.HasPrefix(l, "[[package]]") {
			hasPackage = true
			break
		}
	}
	if !hasPackage {
		return ""
	}
	for _, l := range firstLines {
		if strings.Contains(l, "poetry") || strings.Contains(l, "Poetry") {
			return "poetry"
		}
	}
	return "cargo"
}

// isGemfileHeader reports whether the line is a Gemfile.lock section header.
func isGemfileHeader(first string) bool {
	switch first {
	case "GEM", "PATH", "PLATFORMS", "DEPENDENCIES":
		return true
	}
	return false
}

// extractJSONLock scans a JSON lockfile (package-lock.json) line-by-line for
// "name" and "version" pairs.
func extractJSONLock(text string) string {
	lines := strings.Split(text, "\n")
	var entries []string
	var currentName, currentVer string

	for _, line := range lines {
		currentName, currentVer = collectJSONEntry(line, currentName, currentVer, &entries)
	}
	if currentName != "" && currentVer != "" {
		entries = append(entries, currentName+"@"+currentVer)
	}

	if len(entries) == 0 {
		return strings.TrimSpace(text)
	}
	return strings.Join(entries, "\n")
}

// collectJSONEntry processes a single line of a JSON lockfile, emitting a
// completed name@version pair when a new "name" key is seen. It returns the
// updated currentName and currentVer.
func collectJSONEntry(line, currentName, currentVer string, entries *[]string) (string, string) {
	if m := reJSONLockName.FindStringSubmatch(line); m != nil {
		if currentName != "" && currentVer != "" {
			*entries = append(*entries, currentName+"@"+currentVer)
		}
		currentName = m[1]
		currentVer = ""
	}
	if m := reJSONLockVer.FindStringSubmatch(line); m != nil {
		currentVer = m[1]
	}
	return currentName, currentVer
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
