# Install & update guide

This page is the exhaustive install/update reference for semidx. The
[README](../README.md#install) is the short version.

## Availability (roll-out)

Channels go live in phases. Until a channel is marked **live**, use the
universal installers or `go install`.

| Channel | Status | Notes |
|---|---|---|
| GitHub Releases (binaries) | **live** | `semidx` × 6 platforms + checksums |
| GHCR (`ghcr.io/lgldsilva/semidx`) | **live** | Public Docker image (`latest` + version tags) |
| `install.sh` (Unix) | live | SHA-256 verified |
| `install.ps1` (Windows) | live | SHA-256 verified |
| `go install` | live | needs Go 1.26.5+ |
| Homebrew | pending | tap `lgldsilva/homebrew-tap` |
| Scoop | pending | bucket `lgldsilva/scoop-bucket` |
| winget | pending | PR to `microsoft/winget-pkgs` |
| Chocolatey | pending | package review |
| AUR | pending | PKGBUILD |
| Snap | pending | Snap Store review |
| Flatpak | pending | Flathub review |

The pending channels below are roadmap entries, not working install methods.
Track progress in
[Packaging status #16](https://github.com/lgldsilva/semidx/issues/16).

## Universal installers

### Unix — `install.sh`

```sh
curl -fsSL https://raw.githubusercontent.com/lgldsilva/semidx/main/install.sh | sh
```

| Flag | Meaning |
|---|---|
| `--version vX.Y.Z` | install a specific tag |
| `--os linux\|darwin\|windows` | download only (no install) |
| `--arch amd64\|arm64` | download only (no install) |
| `--dest DIR` | write archive(s) to DIR |
| `--all` | download every artifact + checksums |
| `--bin-dir DIR` | install binary to DIR (default `~/.local/bin`) |

Env overrides: `SEMIDX_API`, `SEMIDX_DOWNLOAD_BASE`, `SEMIDX_BIN_DIR`,
`SEMIDX_INSECURE=1`, `CURL=…`.

### Windows — `install.ps1`

```powershell
irm https://raw.githubusercontent.com/lgldsilva/semidx/main/install.ps1 | iex
```

| Parameter | Meaning |
|---|---|
| `-Version vX.Y.Z` | install a specific tag |
| `-Destination DIR` | write archive(s) to DIR |
| `-All` | download every artifact + checksums |
| `-NoInstall` | download only |

Default install path: `%LOCALAPPDATA%\semidx\bin\` (added to the user PATH).

Env overrides: `$env:SEMIDX_API`, `$env:SEMIDX_DOWNLOAD_BASE`, `$env:SEMIDX_BIN_DIR`.

## Package managers

Homebrew, Scoop, winget, Chocolatey, AUR, Snap and Flatpak are **not published
yet**. Commands for those channels will be documented only after a package has
passed its registry review and a clean-machine install/update smoke test.

Until then, use the universal installers, GitHub Releases, GHCR or `go install`.

### Docker / GHCR

```sh
docker pull ghcr.io/lgldsilva/semidx:latest
docker pull ghcr.io/lgldsilva/semidx:v0.44.9
```

Reference compose files live under `deploy/`.

### Go

```sh
go install github.com/lgldsilva/semidx/cmd/semidx@latest
go install github.com/lgldsilva/semidx/cmd/chatrag@latest
```

## Updating

| How you installed | How to update |
|---|---|
| `install.sh` / `install.ps1` | `semidx upgrade` (or re-run the installer) |
| Docker | pull a newer tag |
| `go install` | re-run with `@latest` |

`semidx upgrade --check` reports whether a newer release exists without
installing. If the binary was installed by a package manager, `semidx upgrade`
detects that and refuses to overwrite it — use the package manager instead.

## Private / self-hosted release mirrors

```sh
export SEMIDX_API=https://gitea.example.com/api/v1/repos/owner/semidx
export SEMIDX_DOWNLOAD_BASE=https://gitea.example.com/owner/semidx/releases/download
export SEMIDX_UPDATE_API=$SEMIDX_API
export SEMIDX_UPDATE_URL=$SEMIDX_DOWNLOAD_BASE
export SEMIDX_UPDATE_TOKEN=…   # optional
```

## Verify

```sh
semidx version
semidx --help
```

Checksums for every release artifact are published as `checksums.txt` next to
the archives on the GitHub Release.
