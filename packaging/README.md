# semidx package-manager sources

This directory contains source templates, not published packages. Render them
from the `checksums.txt` asset of the release being published:

```sh
scripts/render-package-manifests.sh vX.Y.Z checksums.txt /tmp/semidx-packages
```

The renderer refuses missing or malformed SHA-256 values. Review the generated
files before publishing; a package manager is never allowed to consume an
unverified archive.

## AUR (`yay` / `paru`)

`yay` and `paru` are AUR clients, not registries. Publish the generated
`aur/PKGBUILD` and its generated `.SRCINFO` to the `semidx` repository at
`aur.archlinux.org` using the maintainer's AUR SSH account. Example consumer
command after the package passes AUR review:

```sh
yay -S semidx
```

The template uses the immutable Linux release archives and per-architecture
SHA-256 values; it does not download a moving `latest` artifact.

Every stable release validates the rendered PKGBUILD in CI (Arch container:
`makepkg --printsrcinfo` + `namcap`, failing on namcap errors) before the
release is finished. Publishing still requires the maintainer's AUR SSH
account:

```sh
cd /tmp/semidx-packages/aur
makepkg --printsrcinfo > .SRCINFO
git -C <aur-semidx-clone> add PKGBUILD .SRCINFO
git -C <aur-semidx-clone> commit -m "semidx vX.Y.Z"
git -C <aur-semidx-clone> push
```

## Snap

The generated Snap manifest is intentionally `classic`. semidx needs access to
user-selected repositories and document directories, which strict confinement
cannot provide without making ordinary indexing fail. Build and publish it only
from the Snap Store publisher account after the `semidx` name has been
registered and classic confinement approved:

```sh
cd /tmp/semidx-packages/snap/amd64  # or arm64, on a matching build host
snapcraft pack
snapcraft upload --release=stable semidx_*.snap
```

The renderer produces separate amd64 and arm64 manifests, each restricted to
its matching build/run platform and release-asset checksum.

## Flatpak

Flatpak is not a supported semidx distribution channel. It is designed for
sandboxed desktop applications, while semidx is a CLI/server that must index
arbitrary repositories and can host an HTTP API. Granting broad host filesystem
access would defeat the sandbox and would not meet Flathub expectations.

Use a native package, the verified installer, Go, or GHCR instead. A future
desktop client could be evaluated for Flathub separately; do not publish the
server/CLI as a misleading Flatpak wrapper.

## winget

The renderer produces the three-file winget manifest set
(`Semidx.semidx.version.yaml`, `Semidx.semidx.installer.yaml`,
`Semidx.semidx.yaml` = default locale) using schema 1.6.0 and the immutable
per-release Windows zip archives with SHA-256 pins. The proposed package
identifier is `Semidx.semidx`; adjust Publisher/identifier at submission if
the winget-pkgs review asks. Submission is a manual PR to
[`microsoft/winget-pkgs`](https://github.com/microsoft/winget-pkgs):

```sh
cd /tmp/semidx-packages/winget
# fork microsoft/winget-pkgs, then copy the manifests under:
#   manifests/s/Semidx/semidx/<version>/
git add manifests && git commit -m "Add Semidx.semidx version <version>"
git push && gh pr create ...
```

The `winget-pkgs` pipeline validates manifests on the PR; after merge, the
channel is considered live only after a `winget install Semidx.semidx` smoke
test on a clean Windows machine.

## Homebrew and Scoop taps (auto-update)

`.goreleaser.yaml` publishes `homebrew_casks:` (Casks/) and `scoops:` on every
stable tag when the `TAP_GITHUB_TOKEN` repository secret is valid. The release
workflow verifies the token against `lgldsilva/homebrew-tap` before running
GoReleaser and silently strips the tap sections (with a `::warning::` notice)
when the secret is empty or rejected — a broken secret must never abort a
release. The token must be a fine-grained PAT with `contents:write` on
`lgldsilva/homebrew-tap` and `lgldsilva/scoop-bucket` only; do not reuse a
broad personal token. If a release completes while the taps stay on an old
version, check the release run log for the "TAP_GITHUB_TOKEN" warning first.
