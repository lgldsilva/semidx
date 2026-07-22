// Package depresolve executes native build/package tools on demand. It is
// deliberately separate from indexing: resolution may touch the network or
// mutate a workspace and must be an explicit worker/CLI action.
package depresolve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/lgldsilva/semidx/internal/depcatalog"
)

type CommandRunner func(context.Context, string, string, ...string) ([]byte, error)

type Resolver struct{ run CommandRunner }

func New() *Resolver                            { return &Resolver{run: runCommand} }
func NewWithRunner(run CommandRunner) *Resolver { return &Resolver{run: run} }

type ToolError struct {
	Tool string
	Err  error
}

func (e *ToolError) Error() string { return fmt.Sprintf("dependency resolver %s: %v", e.Tool, e.Err) }
func (e *ToolError) Unwrap() error { return e.Err }

// ResolveProject executes every applicable native resolver. The caller chooses
// when this is safe; ordinary indexing only parses manifests and lockfiles.
func (r *Resolver) ResolveProject(ctx context.Context, root string) ([]depcatalog.Dependency, error) {
	root = filepath.Clean(root)
	var out []depcatalog.Dependency
	if exists(root, "go.mod") {
		deps, err := r.resolveGo(ctx, root)
		if err != nil {
			return nil, err
		}
		out = append(out, deps...)
	}
	if exists(root, "pom.xml") {
		deps, err := r.resolveMaven(ctx, root)
		if err != nil {
			return nil, err
		}
		out = append(out, deps...)
	}
	if exists(root, "build.gradle") || exists(root, "build.gradle.kts") {
		deps, err := r.resolveGradle(ctx, root)
		if err != nil {
			return nil, err
		}
		out = append(out, deps...)
	}
	if exists(root, "Package.swift") {
		deps, err := r.resolveSwift(ctx, root)
		if err != nil {
			return nil, err
		}
		out = append(out, deps...)
	}
	if exists(root, "Podfile") {
		deps, err := r.resolvePods(ctx, root)
		if err != nil {
			return nil, err
		}
		out = append(out, deps...)
	}
	return mergeResolved(out), nil
}

func (r *Resolver) resolveGo(ctx context.Context, root string) ([]depcatalog.Dependency, error) {
	out, err := r.command(ctx, root, "go", "list", "-m", "-json", "all")
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	var deps []depcatalog.Dependency
	for {
		var mod struct {
			Path    string `json:"Path"`
			Version string `json:"Version"`
			Main    bool   `json:"Main"`
		}
		err := dec.Decode(&mod)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, &ToolError{Tool: "go list", Err: err}
		}
		if mod.Path == "" || mod.Main {
			continue
		}
		d := depcatalog.Dependency{Ecosystem: depcatalog.EcosystemGo, Name: mod.Path, NormalizedName: depcatalog.NormalizeName(depcatalog.EcosystemGo, mod.Path), ResolvedVersion: mod.Version, Scope: "resolved", Source: "go list -m", Manifest: "go.mod", Direct: true}
		deps = append(deps, d)
	}
	return deps, nil
}

func (r *Resolver) resolveMaven(ctx context.Context, root string) ([]depcatalog.Dependency, error) {
	out, err := r.command(ctx, root, "mvn", "-q", "dependency:tree", "-DoutputType=text")
	if err != nil {
		return nil, err
	}
	var deps []depcatalog.Dependency
	for _, line := range strings.Split(string(out), "\n") {
		token := strings.TrimSpace(strings.TrimPrefix(line, "[INFO]"))
		token = strings.TrimLeft(token, " +-\\")
		parts := strings.Split(token, ":")
		if len(parts) < 5 {
			continue
		}
		scope := parts[len(parts)-1]
		version := parts[len(parts)-2]
		name := parts[0] + ":" + parts[1]
		deps = append(deps, depcatalog.Dependency{Ecosystem: depcatalog.EcosystemMaven, Name: name, NormalizedName: depcatalog.NormalizeName(depcatalog.EcosystemMaven, name), ResolvedVersion: version, Scope: scope, Source: "mvn dependency:tree", Manifest: "pom.xml", Direct: true})
	}
	return deps, nil
}

var gradleResolvedRe = regexp.MustCompile(`(?:\+---|\---)\s+([^:[:space:]]+):([^:[:space:]]+):([^[:space:]]+)`)

func (r *Resolver) resolveGradle(ctx context.Context, root string) ([]depcatalog.Dependency, error) {
	tool := "gradle"
	if exists(root, "gradlew") {
		tool = "./gradlew"
	}
	out, err := r.command(ctx, root, tool, "dependencies", "--configuration", "runtimeClasspath")
	if err != nil {
		return nil, err
	}
	var deps []depcatalog.Dependency
	for _, match := range gradleResolvedRe.FindAllStringSubmatch(string(out), -1) {
		name := match[1] + ":" + match[2]
		deps = append(deps, depcatalog.Dependency{Ecosystem: depcatalog.EcosystemGradle, Name: name, NormalizedName: depcatalog.NormalizeName(depcatalog.EcosystemGradle, name), ResolvedVersion: match[3], Scope: "runtimeClasspath", Source: "gradle dependencies", Manifest: "build.gradle", Direct: true})
	}
	return deps, nil
}

func (r *Resolver) resolveSwift(ctx context.Context, root string) ([]depcatalog.Dependency, error) {
	out, err := r.command(ctx, root, "swift", "package", "show-dependencies", "--format", "json")
	if err != nil {
		return nil, err
	}
	var tree struct {
		Name         string `json:"name"`
		Dependencies []struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal(out, &tree); err != nil {
		return nil, &ToolError{Tool: "swift package", Err: err}
	}
	var deps []depcatalog.Dependency
	for _, item := range tree.Dependencies {
		if item.Name == "" {
			continue
		}
		deps = append(deps, depcatalog.Dependency{Ecosystem: depcatalog.EcosystemSwift, Name: item.Name, NormalizedName: depcatalog.NormalizeName(depcatalog.EcosystemSwift, item.Name), Scope: "resolved", Source: "swift package show-dependencies", Manifest: "Package.swift", Direct: true})
	}
	return deps, nil
}

func (r *Resolver) resolvePods(ctx context.Context, root string) ([]depcatalog.Dependency, error) {
	if _, err := r.command(ctx, root, "pod", "install", "--no-repo-update"); err != nil {
		return nil, err
	}
	lock := filepath.Join(root, "Podfile.lock")
	data, err := os.ReadFile(filepath.Clean(lock))
	if err != nil {
		return nil, &ToolError{Tool: "pod install", Err: err}
	}
	return depcatalog.ParseManifest("Podfile.lock", data)
}

func (r *Resolver) command(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	out, err := r.run(ctx, dir, name, args...)
	if err != nil {
		return nil, &ToolError{Tool: name, Err: err}
	}
	return out, nil
}
func runCommand(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	// #nosec G204 -- name is selected from the fixed ecosystem tool allowlist; arguments are passed directly, never through a shell.
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}
func exists(root, name string) bool { _, err := os.Stat(filepath.Join(root, name)); return err == nil }
func mergeResolved(deps []depcatalog.Dependency) []depcatalog.Dependency {
	byName := map[string]depcatalog.Dependency{}
	for _, dep := range deps {
		key := string(dep.Ecosystem) + "\x00" + dep.NormalizedName + "\x00" + dep.Scope
		if old, ok := byName[key]; ok && old.ResolvedVersion != "" {
			continue
		}
		byName[key] = dep
	}
	out := make([]depcatalog.Dependency, 0, len(byName))
	for _, dep := range byName {
		out = append(out, dep)
	}
	return out
}
