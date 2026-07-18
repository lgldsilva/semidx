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

## Snap

The generated Snap manifest is intentionally `classic`. semidx needs access to
user-selected repositories and document directories, which strict confinement
cannot provide without making ordinary indexing fail. Build and publish it only
from the Snap Store publisher account after the `semidx` name has been
registered and classic confinement approved:

```sh
snapcraft pack
snapcraft upload --release=stable semidx_*.snap
```

## Flatpak

Flatpak is not a supported semidx distribution channel. It is designed for
sandboxed desktop applications, while semidx is a CLI/server that must index
arbitrary repositories and can host an HTTP API. Granting broad host filesystem
access would defeat the sandbox and would not meet Flathub expectations.

Use a native package, the verified installer, Go, or GHCR instead. A future
desktop client could be evaluated for Flathub separately; do not publish the
server/CLI as a misleading Flatpak wrapper.
