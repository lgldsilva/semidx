#!/bin/sh
# semidx installer — downloads the right release archive for your OS/arch from
# GitHub releases, verifies its SHA-256, and installs the binary.
#
#   curl -fsSL https://raw.githubusercontent.com/lgldsilva/semidx/main/install.sh | sh
#   ./install.sh                              # install latest for this OS/arch
#   ./install.sh --version v0.2.0             # a specific release
#   ./install.sh --os windows --arch arm64 --dest ./dl   # just fetch one archive
#   ./install.sh --all --dest ./dist          # download EVERY artifact + checksums
#
# Env overrides: SEMIDX_API, SEMIDX_DOWNLOAD_BASE, SEMIDX_BIN_DIR, SEMIDX_INSECURE=1
set -eu

API="${SEMIDX_API:-https://api.github.com/repos/lgldsilva/semidx}"
DL_BASE="${SEMIDX_DOWNLOAD_BASE:-https://github.com/lgldsilva/semidx/releases/download}"
BIN_DIR="${SEMIDX_BIN_DIR:-$HOME/.local/bin}"
VERSION="" OS="" ARCH="" DEST="" ALL=0 INSTALL=1

CURL="${CURL:-curl -fsSL}"
[ "${SEMIDX_INSECURE:-}" = "1" ] && CURL="$CURL --insecure"

die() { echo "install: $*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    --os)      OS="$2"; INSTALL=0; shift 2 ;;
    --arch)    ARCH="$2"; INSTALL=0; shift 2 ;;
    --dest)    DEST="$2"; INSTALL=0; shift 2 ;;
    --bin-dir) BIN_DIR="$2"; shift 2 ;;
    --all)     ALL=1; INSTALL=0; shift ;;
    -h|--help) sed -n '2,12p' "$0"; exit 0 ;;
    *) die "unknown flag: $1" ;;
  esac
done

command -v curl >/dev/null 2>&1 || die "curl is required"

detect_os() {
  case "$(uname -s | tr '[:upper:]' '[:lower:]')" in
    linux) echo linux ;; darwin) echo darwin ;;
    mingw*|msys*|cygwin*) echo windows ;;
    *) die "unsupported OS: $(uname -s)" ;;
  esac
}
detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo amd64 ;; aarch64|arm64) echo arm64 ;;
    *) die "unsupported arch: $(uname -m)" ;;
  esac
}

# Resolve the version from the latest release when not pinned. GitHub serves
# /releases/latest with JSON (or 302 to the tag URL); some hosts (e.g. Gitea)
# return 404 there, so fall back to listing releases.
if [ -z "$VERSION" ]; then
  VERSION="$($CURL "$API/releases/latest" 2>/dev/null | grep -o '"tag_name":"[^"]*"' | head -1 | cut -d'"' -f4 || true)"
  if [ -z "$VERSION" ]; then
    VERSION="$($CURL "$API/releases?limit=50" | python3 -c "
import json, re, sys
releases = json.load(sys.stdin)
pat = re.compile(r'^v?(\\d+)\\.(\\d+)\\.(\\d+)')
def key(tag):
    m = pat.match(tag or '')
    return tuple(map(int, m.groups())) if m else (0, 0, 0)
tags = [r['tag_name'] for r in releases if not r.get('draft') and r.get('tag_name')]
if not tags:
    sys.exit(1)
print(max(tags, key=key))
")"
  fi
  [ -n "$VERSION" ] || die "could not resolve the latest release from $API"
fi
VER_NOV="${VERSION#v}"

# --all: pull every asset (all platforms + checksums) and stop.
if [ "$ALL" -eq 1 ]; then
  DEST="${DEST:-.}"; mkdir -p "$DEST"
  echo "Downloading all artifacts for $VERSION into $DEST ..."
  $CURL "$API/releases/tags/$VERSION" \
    | grep -o '"browser_download_url":"[^"]*"' | cut -d'"' -f4 \
    | while read -r url; do echo "  $url"; ( cd "$DEST" && $CURL -O "$url" ); done
  echo "Done."; exit 0
fi

[ -n "$OS" ] || OS="$(detect_os)"
[ -n "$ARCH" ] || ARCH="$(detect_arch)"
EXT=tar.gz; [ "$OS" = windows ] && EXT=zip
ARCHIVE="semidx_${VER_NOV}_${OS}_${ARCH}.${EXT}"
BASE="$DL_BASE/$VERSION"

WORK="$(mktemp -d)"; trap 'rm -rf "$WORK"' EXIT
echo "Fetching $ARCHIVE ($VERSION) ..."
$CURL -o "$WORK/$ARCHIVE" "$BASE/$ARCHIVE" || die "download failed: $BASE/$ARCHIVE"

# Verify SHA-256 against the release checksums.txt.
if $CURL -o "$WORK/checksums.txt" "$BASE/checksums.txt" 2>/dev/null; then
  want="$(grep " $ARCHIVE\$" "$WORK/checksums.txt" | awk '{print $1}')"
  if [ -n "$want" ]; then
    if command -v sha256sum >/dev/null 2>&1; then got="$(sha256sum "$WORK/$ARCHIVE" | awk '{print $1}')"
    else got="$(shasum -a 256 "$WORK/$ARCHIVE" | awk '{print $1}')"; fi
    [ "$want" = "$got" ] || die "checksum mismatch for $ARCHIVE"
    echo "Checksum OK."
  fi
else
  echo "warning: checksums.txt not found — skipping verification" >&2
fi

# If only fetching (--os/--arch/--dest given), leave the archive in DEST.
if [ "$INSTALL" -eq 0 ]; then
  DEST="${DEST:-.}"; mkdir -p "$DEST"; cp "$WORK/$ARCHIVE" "$DEST/"
  echo "Saved $DEST/$ARCHIVE"; exit 0
fi

# Extract and install the binary.
case "$EXT" in
  tar.gz) tar -xzf "$WORK/$ARCHIVE" -C "$WORK" ;;
  zip)    command -v unzip >/dev/null 2>&1 || die "unzip is required for windows archives"; unzip -q "$WORK/$ARCHIVE" -d "$WORK" ;;
esac
BIN="$WORK/semidx"; [ "$OS" = windows ] && BIN="$WORK/semidx.exe"
[ -f "$BIN" ] || die "binary not found inside $ARCHIVE"

mkdir -p "$BIN_DIR"
install -m 0755 "$BIN" "$BIN_DIR/$(basename "$BIN")" 2>/dev/null || { cp "$BIN" "$BIN_DIR/"; chmod 0755 "$BIN_DIR/$(basename "$BIN")"; }
echo "Installed semidx $VERSION to $BIN_DIR/$(basename "$BIN")"
case ":$PATH:" in *":$BIN_DIR:"*) ;; *) echo "note: add $BIN_DIR to your PATH" ;; esac
