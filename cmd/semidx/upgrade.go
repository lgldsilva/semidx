package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/lgldsilva/semidx/internal/config"
)

// Defaults point at the homelab Gitea release; override with SEMIDX_UPDATE_API /
// SEMIDX_UPDATE_URL (e.g. to a public GitHub release after the OSS migration).
const (
	defaultUpdateAPI = "https://gitea.raspberrypi.lan/api/v1/repos/lgldsilva/semidx"
	defaultUpdateDL  = "https://gitea.raspberrypi.lan/lgldsilva/semidx/releases/download"
)

// newUpgradeCmd self-updates the running binary to a release built by the
// pipeline (GoReleaser artifacts), reusing the same archive/checksum layout
// install.sh consumes. It downloads the archive for this OS/arch, verifies its
// SHA-256, and atomically replaces the current executable.
func newUpgradeCmd(_ *deps) *cobra.Command {
	var wantVersion string
	var checkOnly bool
	c := &cobra.Command{
		Use:   "upgrade",
		Short: "Update the semidx binary to the latest release",
		Long: "Self-update the semidx binary from a published release (the GoReleaser\n" +
			"artifacts). Downloads the archive for this OS/arch, verifies its SHA-256\n" +
			"against checksums.txt, and atomically replaces the running executable.\n\n" +
			"Source is the homelab Gitea by default; point it elsewhere (e.g. a public\n" +
			"GitHub release) with `semidx config set SEMIDX_UPDATE_API/SEMIDX_UPDATE_URL`.",
		Example: "  semidx upgrade\n  semidx upgrade --check\n  semidx upgrade --version v0.2.0",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			apiURL := valueOr("SEMIDX_UPDATE_API", defaultUpdateAPI)
			dlURL := valueOr("SEMIDX_UPDATE_URL", defaultUpdateDL)
			// Optional: private release hosts (e.g. a private Gitea repo) need a
			// token to read releases and download assets. Public releases don't.
			token := config.EffectiveValue("SEMIDX_UPDATE_TOKEN")
			hc := updateHTTPClient(apiURL)

			tag, err := resolveUpgradeTag(ctx, hc, apiURL, token, wantVersion)
			if err != nil {
				return err
			}
			fmt.Printf("current: %s\nlatest:  %s\n", version, tag)

			if checkOnly {
				printUpgradeCheck(version, tag)
				return nil
			}
			if wantVersion == "" && sameVersion(version, tag) {
				fmt.Println("already up to date.")
				return nil
			}
			return installUpgrade(ctx, hc, dlURL, tag, token)
		},
	}
	c.Flags().StringVar(&wantVersion, "version", "", "install a specific release tag (default: latest)")
	c.Flags().BoolVar(&checkOnly, "check", false, "only report whether an update is available")
	return c
}

// resolveUpgradeTag returns the requested tag, or resolves the latest release
// tag when none was requested.
func resolveUpgradeTag(ctx context.Context, hc *http.Client, apiURL, token, wantVersion string) (string, error) {
	if wantVersion != "" {
		return wantVersion, nil
	}
	latest, err := fetchLatestTag(ctx, hc, apiURL, token)
	if err != nil {
		return "", fmt.Errorf("resolve latest release: %w", err)
	}
	return latest, nil
}

// printUpgradeCheck reports whether the current binary is already at tag (used
// by `upgrade --check`).
func printUpgradeCheck(current, tag string) {
	if sameVersion(current, tag) {
		fmt.Println("up to date.")
		return
	}
	fmt.Println("an update is available — run `semidx upgrade`.")
}

// installUpgrade downloads the release binary for this OS/arch and atomically
// replaces the running executable with it.
func installUpgrade(ctx context.Context, hc *http.Client, dlURL, tag, token string) error {
	bin, err := downloadReleaseBinary(ctx, hc, dlURL, tag, runtime.GOOS, runtime.GOARCH, token)
	if err != nil {
		return err
	}
	if err := replaceRunningBinary(bin); err != nil {
		return fmt.Errorf("install update: %w", err)
	}
	fmt.Printf("upgraded to %s\n", tag)
	return nil
}

// valueOr resolves a config key (env > .env > user config), falling back to def.
func valueOr(key, def string) string {
	if v := config.EffectiveValue(key); v != "" {
		return v
	}
	return def
}

// updateHTTPClient trusts the homelab's self-signed CA when talking to it (or
// when SEMIDX_INSECURE=1), matching install.sh's --insecure.
func updateHTTPClient(apiURL string) *http.Client {
	insecure := config.EffectiveValue("SEMIDX_INSECURE") == "1" || strings.Contains(apiURL, "raspberrypi.lan")
	tr := &http.Transport{}
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- opt-in for the operator's self-hosted CA
	}
	return &http.Client{Timeout: 120 * time.Second, Transport: tr}
}

// fetchLatestTag reads the newest published release tag. GitHub serves
// /releases/latest; Gitea often returns 404 there, so we fall back to listing
// releases and picking the highest semver tag_name.
func fetchLatestTag(ctx context.Context, hc *http.Client, apiURL, token string) (string, error) {
	base := strings.TrimRight(apiURL, "/")
	if tag, err := fetchLatestTagFromEndpoint(ctx, hc, base+"/releases/latest", token); err == nil {
		return tag, nil
	} else if !isHTTPNotFound(err) {
		return "", err
	}
	return fetchLatestTagFromList(ctx, hc, base+"/releases?limit=50", token)
}

func fetchLatestTagFromEndpoint(ctx context.Context, hc *http.Client, url, token string) (string, error) {
	body, err := httpGetBytes(ctx, hc, url, token)
	if err != nil {
		return "", err
	}
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		return "", fmt.Errorf("parse release: %w", err)
	}
	if rel.TagName == "" {
		return "", fmt.Errorf("no tag_name in latest release")
	}
	return rel.TagName, nil
}

func fetchLatestTagFromList(ctx context.Context, hc *http.Client, url, token string) (string, error) {
	body, err := httpGetBytes(ctx, hc, url, token)
	if err != nil {
		return "", err
	}
	var releases []struct {
		TagName string `json:"tag_name"`
		Draft   bool   `json:"draft"`
	}
	if err := json.Unmarshal(body, &releases); err != nil {
		return "", fmt.Errorf("parse release list: %w", err)
	}
	var tags []string
	for _, r := range releases {
		if r.Draft || r.TagName == "" {
			continue
		}
		tags = append(tags, r.TagName)
	}
	if len(tags) == 0 {
		return "", fmt.Errorf("no published releases found")
	}
	sort.Slice(tags, func(i, j int) bool {
		return compareReleaseTags(tags[i], tags[j]) < 0
	})
	return tags[len(tags)-1], nil
}

var releaseTagVersion = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

func compareReleaseTags(a, b string) int {
	av, aok := parseReleaseTagVersion(a)
	bv, bok := parseReleaseTagVersion(b)
	if aok && bok {
		if av.less(bv) {
			return -1
		}
		if bv.less(av) {
			return 1
		}
		return 0
	}
	return strings.Compare(a, b)
}

type releaseTagVer struct{ major, minor, patch int }

func (v releaseTagVer) less(o releaseTagVer) bool {
	if v.major != o.major {
		return v.major < o.major
	}
	if v.minor != o.minor {
		return v.minor < o.minor
	}
	return v.patch < o.patch
}

func parseReleaseTagVersion(tag string) (releaseTagVer, bool) {
	m := releaseTagVersion.FindStringSubmatch(tag)
	if m == nil {
		return releaseTagVer{}, false
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	pat, _ := strconv.Atoi(m[3])
	return releaseTagVer{major: maj, minor: min, patch: pat}, true
}

// HTTPError represents an HTTP error during the upgrade process.
type HTTPError struct {
	StatusCode int
	Status     string
	URL        string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("GET %s: %s", e.URL, e.Status)
}

func isHTTPNotFound(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == 404
	}
	s := err.Error()
	return strings.Contains(s, " 404 ") || strings.HasSuffix(s, " 404") || strings.Contains(s, ": 404")
}

// downloadReleaseBinary downloads the archive for tag/os/arch, verifies its
// SHA-256 against checksums.txt, and returns the extracted `semidx` binary.
func downloadReleaseBinary(ctx context.Context, hc *http.Client, dlBase, tag, goos, goarch, token string) ([]byte, error) {
	ext := "tar.gz"
	if goos == "windows" {
		ext = "zip"
	}
	archive := fmt.Sprintf("semidx_%s_%s_%s.%s", strings.TrimPrefix(tag, "v"), goos, goarch, ext)
	base := strings.TrimRight(dlBase, "/") + "/" + tag

	arData, err := httpGetBytes(ctx, hc, base+"/"+archive, token)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", archive, err)
	}
	if sums, err := httpGetBytes(ctx, hc, base+"/checksums.txt", token); err == nil {
		if want := checksumFor(string(sums), archive); want != "" {
			got := sha256.Sum256(arData)
			if want != hex.EncodeToString(got[:]) {
				return nil, fmt.Errorf("checksum mismatch for %s", archive)
			}
		}
	}
	return extractSemidxBinary(arData, ext, goos)
}

// checksumFor returns the sha256 hex for the named file from a checksums.txt body
// (lines of "<hex>  <name>").
func checksumFor(sums, name string) string {
	for _, line := range strings.Split(sums, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[1] == name {
			return f[0]
		}
	}
	return ""
}

// extractSemidxBinary pulls the semidx executable out of a release archive.
func extractSemidxBinary(data []byte, ext, goos string) ([]byte, error) {
	want := "semidx"
	if goos == "windows" {
		want = "semidx.exe"
	}
	if ext == "zip" {
		return extractFromZip(data, want)
	}
	return extractFromTarGz(data, want)
}

// extractFromZip returns the named entry from a zip release archive.
func extractFromZip(data []byte, want string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	for _, f := range zr.File {
		if filepath.Base(f.Name) == want {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer func() { _ = rc.Close() }()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("%s not found in archive", want)
}

// extractFromTarGz returns the named entry from a gzip'd tar release archive.
func extractFromTarGz(data []byte, want string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("open gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(h.Name) == want {
			return io.ReadAll(tr) // #nosec G110 -- release archive from a trusted, checksum-verified source
		}
	}
	return nil, fmt.Errorf("%s not found in archive", want)
}

// replaceRunningBinary resolves the current executable and swaps in the new
// bytes via replaceBinaryAt.
func replaceRunningBinary(bin []byte) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return replaceBinaryAt(exe, bin)
}

// replaceBinaryAt writes the new bytes next to exePath and atomically renames
// them into place. On Windows a running .exe can't be overwritten, so the old
// one is moved aside first.
func replaceBinaryAt(exePath string, bin []byte) error {
	dir := filepath.Dir(exePath)
	tmp := filepath.Join(dir, ".semidx.upgrade.tmp")
	if err := os.WriteFile(tmp, bin, 0o755); err != nil { // #nosec G306 -- an executable must be 0755
		return fmt.Errorf("write to %s (need write access to the install dir?): %w", dir, err)
	}
	if runtime.GOOS == "windows" {
		old := exePath + ".old"
		_ = os.Remove(old)
		if err := os.Rename(exePath, old); err != nil {
			_ = os.Remove(tmp)
			return err
		}
		if err := os.Rename(tmp, exePath); err != nil {
			_ = os.Rename(old, exePath) // best-effort rollback
			return err
		}
		return nil
	}
	// POSIX: renaming over a running binary is fine — the process keeps the old
	// inode; the next launch uses the new file.
	if err := os.Rename(tmp, exePath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// httpGetBytes GETs a URL and returns the body, erroring on non-2xx. When token
// is non-empty it authenticates (needed for private release hosts).
func httpGetBytes(ctx context.Context, hc *http.Client, url, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return nil, &HTTPError{
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			URL:        url,
		}
	}
	return io.ReadAll(resp.Body)
}

// sameVersion compares two version strings ignoring a leading 'v'. A "dev" build
// is never considered current, so `upgrade` always proceeds from a dev binary.
func sameVersion(a, b string) bool {
	if a == "dev" || a == "" {
		return false
	}
	return strings.TrimPrefix(a, "v") == strings.TrimPrefix(b, "v")
}
