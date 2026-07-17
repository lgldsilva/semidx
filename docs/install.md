# Install & update guide

This page is the exhaustive install/update reference for semidx. The
[README](../README.md#install) is the short version.

## Availability (roll-out)

Channels go live in phases. Until a channel is marked **live**, use the
universal installers or `go install`.

| Channel | Status | Notes |
|---|---|---|
| GitHub Releases (binaries) | live (on first public release) | `semidx` + `chatrag` × 6 platforms |
| GHCR (`ghcr.io/lgldsilva/semidx`) | live (on first public release) | Docker image |
| `install.sh` (Unix) | live | SHA-256 verified |
| `install.ps1` (Windows) | live | SHA-256 verified |
| `go install` | live | needs Go 1.25+ |
| Homebrew | pending | tap `lgldsilva/homebrew-tap` |
| Scoop | pending | bucket `lgldsilva/scoop-bucket` |
| winget | pending | PR to `microsoft/winget-pkgs` |
| Chocolatey | pending | package review |
| AUR | pending | PKGBUILD |
| Snap | pending | Snap Store review |
| Flatpak | pending | Flathub review |

Track progress on the pinned **Packaging status** GitHub issue and the
Packaging project board.

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

### Homebrew (macOS)

```sh
brew install lgldsilva/tap/semidx
brew upgrade semidx
```

### Scoop (Windows)

```powershell
scoop bucket add lgldsilva https://github.com/lgldsilva/scoop-bucket
scoop install semidx
scoop update semidx
```

### winget (Windows 10/11)

```powershell
winget install lgldsilva.semidx
winget upgrade lgldsilva.semidx
```

### Chocolatey (Windows enterprise)

```powershell
choco install semidx
choco upgrade semidx
```

### AUR (Arch)

```sh
yay -S semidx
yay -Syu semidx
```

### Snap

```sh
sudo snap install semidx
sudo snap refresh semidx
```

### Flatpak

```sh
flatpak install flathub com.github.lgldsilva.semidx
flatpak update com.github.lgldsilva.semidx
```

### Docker / GHCR

```sh
docker pull ghcr.io/lgldsilva/semidx:latest
docker pull ghcr.io/lgldsilva/semidx:v0.43.1
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
| Homebrew | `brew upgrade semidx` |
| Scoop | `scoop update semidx` |
| winget | `winget upgrade lgldsilva.semidx` |
| Chocolatey | `choco upgrade semidx` |
| AUR | `yay -Syu semidx` |
| Snap | `sudo snap refresh semidx` |
| Flatpak | `flatpak update com.github.lgldsilva.semidx` |
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
