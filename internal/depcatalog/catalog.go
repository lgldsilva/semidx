// Package depcatalog extracts a normalized dependency catalog from project
// manifests. It intentionally separates declaration parsing from resolution:
// an adapter can identify what a project asks for without pretending that a
// lockfile or build tool has resolved the graph.
package depcatalog

import (
	"encoding/json"
	"fmt"
	"html"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Ecosystem string

const (
	EcosystemGo        Ecosystem = "go"
	EcosystemNPM       Ecosystem = "npm"
	EcosystemMaven     Ecosystem = "maven"
	EcosystemGradle    Ecosystem = "gradle"
	EcosystemSwift     Ecosystem = "swift"
	EcosystemCocoaPods Ecosystem = "cocoapods"
)

// ValidEcosystem reports whether an ecosystem has a registered manifest
// adapter. It is also used at API boundaries for customer-agent payloads.
func ValidEcosystem(e Ecosystem) bool {
	switch e {
	case EcosystemGo, EcosystemNPM, EcosystemMaven, EcosystemGradle, EcosystemSwift, EcosystemCocoaPods:
		return true
	default:
		return false
	}
}

// Dependency is a normalized declaration. ResolvedVersion is populated only
// when a lockfile or a real resolver supplies an exact version.
type Dependency struct {
	Ecosystem       Ecosystem `json:"ecosystem"`
	Name            string    `json:"name"`
	NormalizedName  string    `json:"normalized_name"`
	Constraint      string    `json:"constraint,omitempty"`
	ResolvedVersion string    `json:"resolved_version,omitempty"`
	Scope           string    `json:"scope,omitempty"`
	Source          string    `json:"source,omitempty"`
	Manifest        string    `json:"manifest"`
	Direct          bool      `json:"direct"`
}

type Adapter interface {
	Name() string
	Match(path string) bool
	Parse(path string, content []byte) ([]Dependency, error)
}

var adapters = []Adapter{
	goModAdapter{}, packageJSONAdapter{}, pomAdapter{}, gradleAdapter{},
	swiftPackageAdapter{}, packageResolvedAdapter{}, podfileAdapter{}, podLockAdapter{},
}

// ParseManifest dispatches a known manifest to the appropriate adapter.
func ParseManifest(path string, content []byte) ([]Dependency, error) {
	for _, adapter := range adapters {
		if !adapter.Match(path) {
			continue
		}
		deps, err := adapter.Parse(path, content)
		if err != nil {
			return nil, fmt.Errorf("parse %s with %s: %w", path, adapter.Name(), err)
		}
		return deduplicate(deps), nil
	}
	return nil, nil
}

// ParseManifests parses several path/content pairs and returns one stable,
// duplicate-free catalog. Unknown files are ignored.
func ParseManifests(files map[string][]byte) ([]Dependency, error) {
	var out []Dependency
	for path, content := range files {
		deps, err := ParseManifest(path, content)
		if err != nil {
			return nil, err
		}
		out = append(out, deps...)
	}
	return deduplicate(out), nil
}

func dependency(ecosystem Ecosystem, name, constraint, scope, source, manifest string) Dependency {
	name = strings.TrimSpace(name)
	return Dependency{
		Ecosystem: ecosystem, Name: name, NormalizedName: NormalizeName(ecosystem, name),
		Constraint: strings.TrimSpace(constraint), Scope: strings.TrimSpace(scope),
		Source: strings.TrimSpace(source), Manifest: filepath.ToSlash(manifest), Direct: true,
	}
}

func NormalizeName(ecosystem Ecosystem, name string) string {
	name = strings.TrimSpace(name)
	if ecosystem == EcosystemGo || ecosystem == EcosystemMaven || ecosystem == EcosystemGradle || ecosystem == EcosystemNPM {
		return strings.ToLower(name)
	}
	return strings.ToLower(strings.TrimPrefix(name, "./"))
}

func deduplicate(deps []Dependency) []Dependency {
	seen := make(map[string]Dependency, len(deps))
	for _, dep := range deps {
		if dep.Name == "" || dep.NormalizedName == "" {
			continue
		}
		key := string(dep.Ecosystem) + "\x00" + dep.NormalizedName + "\x00" + dep.Scope
		if old, ok := seen[key]; ok {
			if old.ResolvedVersion == "" {
				old.ResolvedVersion = dep.ResolvedVersion
			}
			if old.Constraint == "" {
				old.Constraint = dep.Constraint
			}
			seen[key] = old
			continue
		}
		seen[key] = dep
	}
	out := make([]Dependency, 0, len(seen))
	for _, dep := range seen {
		out = append(out, dep)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Ecosystem != out[j].Ecosystem {
			return out[i].Ecosystem < out[j].Ecosystem
		}
		return out[i].NormalizedName < out[j].NormalizedName
	})
	return out
}

type goModAdapter struct{}

func (goModAdapter) Name() string           { return "go.mod" }
func (goModAdapter) Match(path string) bool { return filepath.Base(path) == "go.mod" }
func (goModAdapter) Parse(path string, content []byte) ([]Dependency, error) {
	var out []Dependency
	block := false
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "require (") {
			block = true
			continue
		}
		if block && line == ")" {
			block = false
			continue
		}
		if strings.HasPrefix(line, "require ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "require "))
		}
		if !block && !strings.Contains(raw, "require ") {
			continue
		}
		fields := strings.Fields(strings.Split(line, "//")[0])
		if len(fields) < 2 || strings.HasPrefix(fields[0], "//") {
			continue
		}
		scope := "direct"
		if strings.Contains(strings.Join(fields[2:], " "), "indirect") {
			scope = "indirect"
		}
		dep := dependency(EcosystemGo, fields[0], fields[1], scope, "go modules", path)
		dep.Direct = scope == "direct"
		out = append(out, dep)
	}
	return out, nil
}

type packageJSONAdapter struct{}

func (packageJSONAdapter) Name() string           { return "package.json" }
func (packageJSONAdapter) Match(path string) bool { return filepath.Base(path) == "package.json" }
func (packageJSONAdapter) Parse(path string, content []byte) ([]Dependency, error) {
	var doc struct {
		Dependencies     map[string]string `json:"dependencies"`
		DevDependencies  map[string]string `json:"devDependencies"`
		PeerDependencies map[string]string `json:"peerDependencies"`
	}
	if err := json.Unmarshal(content, &doc); err != nil {
		return nil, err
	}
	var out []Dependency
	for name, constraint := range doc.Dependencies {
		out = append(out, dependency(EcosystemNPM, name, constraint, "runtime", "npm manifest", path))
	}
	for name, constraint := range doc.DevDependencies {
		d := dependency(EcosystemNPM, name, constraint, "development", "npm manifest", path)
		out = append(out, d)
	}
	for name, constraint := range doc.PeerDependencies {
		d := dependency(EcosystemNPM, name, constraint, "peer", "npm manifest", path)
		out = append(out, d)
	}
	return out, nil
}

var (
	xmlDepRe       = regexp.MustCompile(`(?s)<dependency>(.*?)</dependency>`)
	xmlFieldRe     = regexp.MustCompile(`(?s)<%s>\s*([^<]+?)\s*</%s>`)
	gradleDepRe    = regexp.MustCompile(`(?m)\b(implementation|api|compileOnly|runtimeOnly|testImplementation|testRuntimeOnly|classpath|kapt|annotationProcessor)\s*\(?\s*["']([^"']+)["']`)
	packageSwiftRe = regexp.MustCompile(`\.package\s*\(\s*(?:url:\s*)?["']([^"']+)["'](?:\s*,\s*(from|exact|upToNextMajor|upToNextMinor):\s*["']([^"']+)["'])?`)
	podRe          = regexp.MustCompile(`(?m)^\s*pod\s+["']([^"']+)["'](?:\s*,\s*["']([^"']+)["'])?`)
	podLockRe      = regexp.MustCompile(`(?m)^\s*-\s+([^ (]+)\s*\(([^)]+)\)`)
)

type pomAdapter struct{}

func (pomAdapter) Name() string           { return "pom.xml" }
func (pomAdapter) Match(path string) bool { return filepath.Base(path) == "pom.xml" }
func (pomAdapter) Parse(path string, content []byte) ([]Dependency, error) {
	var out []Dependency
	for _, block := range xmlDepRe.FindAllStringSubmatch(string(content), -1) {
		group := xmlValue(block[1], "groupId")
		artifact := xmlValue(block[1], "artifactId")
		if group == "" || artifact == "" {
			continue
		}
		scope := xmlValue(block[1], "scope")
		if scope == "" {
			scope = "compile"
		}
		out = append(out, dependency(EcosystemMaven, group+":"+artifact, xmlValue(block[1], "version"), scope, "maven manifest", path))
	}
	return out, nil
}
func xmlValue(block, field string) string {
	re := regexp.MustCompile(fmt.Sprintf(xmlFieldRe.String(), field, field))
	m := re.FindStringSubmatch(block)
	if len(m) == 2 {
		return strings.TrimSpace(html.UnescapeString(m[1]))
	}
	return ""
}

type gradleAdapter struct{}

func (gradleAdapter) Name() string { return "Gradle build script" }
func (gradleAdapter) Match(path string) bool {
	base := filepath.Base(path)
	return base == "build.gradle" || base == "build.gradle.kts"
}
func (gradleAdapter) Parse(path string, content []byte) ([]Dependency, error) {
	var out []Dependency
	for _, match := range gradleDepRe.FindAllStringSubmatch(string(content), -1) {
		parts := strings.Split(match[2], ":")
		if len(parts) < 2 {
			continue
		}
		constraint := ""
		if len(parts) > 2 {
			constraint = strings.Join(parts[2:], ":")
		}
		d := dependency(EcosystemGradle, parts[0]+":"+parts[1], constraint, match[1], "gradle manifest", path)
		out = append(out, d)
	}
	return out, nil
}

type swiftPackageAdapter struct{}

func (swiftPackageAdapter) Name() string           { return "Package.swift" }
func (swiftPackageAdapter) Match(path string) bool { return filepath.Base(path) == "Package.swift" }
func (swiftPackageAdapter) Parse(path string, content []byte) ([]Dependency, error) {
	var out []Dependency
	for _, match := range packageSwiftRe.FindAllStringSubmatch(string(content), -1) {
		name := strings.TrimSuffix(filepath.Base(strings.TrimSuffix(match[1], ".git")), "/")
		constraint := ""
		if len(match) > 3 {
			constraint = match[3]
		}
		source := match[1]
		out = append(out, dependency(EcosystemSwift, name, constraint, "runtime", source, path))
	}
	return out, nil
}

type packageResolvedAdapter struct{}

func (packageResolvedAdapter) Name() string { return "Package.resolved" }
func (packageResolvedAdapter) Match(path string) bool {
	return filepath.Base(path) == "Package.resolved"
}
func (packageResolvedAdapter) Parse(path string, content []byte) ([]Dependency, error) {
	var doc struct {
		Pins []struct {
			Identity string `json:"identity"`
			Package  string `json:"package"`
			Location string `json:"location"`
			State    struct {
				Version string `json:"version"`
			} `json:"state"`
		} `json:"pins"`
	}
	if err := json.Unmarshal(content, &doc); err != nil {
		return nil, err
	}
	var out []Dependency
	for _, pin := range doc.Pins {
		name := pin.Identity
		if name == "" {
			name = pin.Package
		}
		d := dependency(EcosystemSwift, name, "", "resolved", pin.Location, path)
		d.ResolvedVersion = pin.State.Version
		out = append(out, d)
	}
	return out, nil
}

type podfileAdapter struct{}

func (podfileAdapter) Name() string           { return "Podfile" }
func (podfileAdapter) Match(path string) bool { return filepath.Base(path) == "Podfile" }
func (podfileAdapter) Parse(path string, content []byte) ([]Dependency, error) {
	var out []Dependency
	for _, match := range podRe.FindAllStringSubmatch(string(content), -1) {
		out = append(out, dependency(EcosystemCocoaPods, match[1], match[2], "runtime", "cocoapods manifest", path))
	}
	return out, nil
}

type podLockAdapter struct{}

func (podLockAdapter) Name() string           { return "Podfile.lock" }
func (podLockAdapter) Match(path string) bool { return filepath.Base(path) == "Podfile.lock" }
func (podLockAdapter) Parse(path string, content []byte) ([]Dependency, error) {
	var out []Dependency
	for _, match := range podLockRe.FindAllStringSubmatch(string(content), -1) {
		d := dependency(EcosystemCocoaPods, match[1], "", "resolved", "cocoapods lockfile", path)
		d.ResolvedVersion = match[2]
		out = append(out, d)
	}
	return out, nil
}
