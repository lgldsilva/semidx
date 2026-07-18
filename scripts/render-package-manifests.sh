#!/usr/bin/env bash
# Render package-manager manifests from a verified GitHub Release checksum file.
#
# Usage:
#   scripts/render-package-manifests.sh v0.44.9 checksums.txt out-dir
#
# The generated directory is deliberately not committed: each release has a
# different immutable version and checksum.  Review the output, then submit it
# to the package manager's repository/store using its normal review process.
set -euo pipefail

if [ "$#" -ne 3 ]; then
  echo "usage: $0 vX.Y.Z checksums.txt output-dir" >&2
  exit 64
fi

tag="$1"
checksums="$2"
output_dir="$3"

if [[ ! "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.-]+)?$ ]]; then
  echo "version must be a v-prefixed semantic version, got: $tag" >&2
  exit 64
fi
if [ ! -r "$checksums" ]; then
  echo "checksum file is not readable: $checksums" >&2
  exit 66
fi

version="${tag#v}"

for template in packaging/aur/PKGBUILD.in packaging/snap/snapcraft.yaml.in; do
  if [ ! -r "$template" ]; then
    echo "package template is not readable: $template" >&2
    exit 66
  fi
done

checksum_for() {
  local file="$1"
  local checksum
  checksum="$(awk -v file="$file" '$2 == file { print $1 }' "$checksums")"
  if [[ ! "$checksum" =~ ^[a-fA-F0-9]{64}$ ]]; then
    echo "missing or invalid SHA-256 for $file in $checksums" >&2
    exit 65
  fi
  printf '%s' "$checksum"
}

linux_amd64="$(checksum_for "semidx_${version}_linux_amd64.tar.gz")"
linux_arm64="$(checksum_for "semidx_${version}_linux_arm64.tar.gz")"

mkdir -p "$output_dir/aur" "$output_dir/snap"

sed \
  -e "s|@VERSION@|$version|g" \
  -e "s|@LINUX_AMD64_SHA256@|$linux_amd64|g" \
  -e "s|@LINUX_ARM64_SHA256@|$linux_arm64|g" \
  packaging/aur/PKGBUILD.in >"$output_dir/aur/PKGBUILD"

sed \
  -e "s|@VERSION@|$version|g" \
  -e "s|@LINUX_AMD64_SHA256@|$linux_amd64|g" \
  packaging/snap/snapcraft.yaml.in >"$output_dir/snap/snapcraft.yaml"

echo "Rendered AUR and Snap manifests for $tag in $output_dir."
echo "AUR: review, generate .SRCINFO with makepkg --printsrcinfo, then push to aur.archlinux.org."
echo "Snap: run snapcraft pack and publish only with the Snap Store publisher account."
