#!/bin/sh
# Generate a CycloneDX SBOM for the Go module and upload it to Dependency-Track.
#
# Invoked by the `sbom` job in .github/workflows/release.yml, which is
# continue-on-error, so any failure here is non-fatal to the release.
#
# Required env (all provided as Gitea secrets):
#   DT_API   Dependency-Track base URL, e.g. https://dtrack.example.com
#   DT_USER  Dependency-Track username with BOM_UPLOAD permission
#   DT_PASS  password for DT_USER
# Optional env:
#   DT_PROJECT_NAME     project name in Dependency-Track (default: semidx)
#   DT_PROJECT_VERSION  project version               (default: latest)
#   BOM_FILE            output path for the SBOM       (default: bom.json)
set -eu

: "${DT_API:?DT_API (Dependency-Track base URL) is required}"
: "${DT_USER:?DT_USER is required}"
: "${DT_PASS:?DT_PASS is required}"

PROJECT_NAME="${DT_PROJECT_NAME:-semidx}"
PROJECT_VERSION="${DT_PROJECT_VERSION:-latest}"
BOM_FILE="${BOM_FILE:-bom.json}"

# Homelab Dependency-Track is served with the internal CA; talk to it insecurely.
dt_curl() {
  curl -sS --insecure "$@"
}

# 1) Generate the CycloneDX SBOM for the Go project.
npx -y @cyclonedx/cdxgen -t go -o "$BOM_FILE" .

# 2) Exchange username/password for a short-lived bearer token.
TOKEN="$(dt_curl -X POST "$DT_API/api/v1/user/login" \
  --data-urlencode "username=$DT_USER" \
  --data-urlencode "password=$DT_PASS")"
if [ -z "$TOKEN" ]; then
  echo "sbom-upload: Dependency-Track login returned no token" >&2
  exit 1
fi

# 3) Upload the BOM, auto-creating the project/version if it does not exist.
BOM_B64="$(base64 "$BOM_FILE" | tr -d '\n')"
PAYLOAD="$(printf '{"projectName":"%s","projectVersion":"%s","autoCreate":true,"bom":"%s"}' \
  "$PROJECT_NAME" "$PROJECT_VERSION" "$BOM_B64")"

dt_curl -X PUT "$DT_API/api/v1/bom" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "$PAYLOAD"

echo "sbom-upload: uploaded $BOM_FILE for $PROJECT_NAME@$PROJECT_VERSION"
