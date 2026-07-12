#!/bin/sh
# Print a version string for the current commit, used to tag the Docker image.
#
#   - HEAD is exactly on a tag        -> that tag            (e.g. v1.2.3)
#   - HEAD is after the latest tag    -> git describe        (e.g. v1.2.3-4-gabc1234)
#   - no tags exist in the repository -> 0.0.0-<short-sha>
#
# Requires a full-history checkout (fetch-depth: 0) so tags are available.
set -eu

if tag="$(git describe --tags --exact-match 2>/dev/null)"; then
  printf '%s\n' "$tag"
elif desc="$(git describe --tags 2>/dev/null)"; then
  printf '%s\n' "$desc"
else
  printf '0.0.0-%s\n' "$(git rev-parse --short HEAD)"
fi
