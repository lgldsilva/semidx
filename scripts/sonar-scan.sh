#!/usr/bin/env bash
# Run the REAL SonarQube quality gate locally — the same gate the Gitea release
# pipeline enforces. sonar-project.properties is the single source of config
# (projectKey, coverage paths, exclusions, sonar.qualitygate.wait=true), so this
# only supplies the host, the token and the coverage report.
#
# Because the properties file sets sonar.qualitygate.wait=true, `sonar-scanner`
# exits non-zero when the gate fails — so this script fails too (no false-green).
#
#   SONAR_TOKEN=xxxxx ./scripts/sonar-scan.sh
#
# Requires `sonar-scanner` on PATH (brew install sonar-scanner) and go.
set -eu

: "${SONAR_TOKEN:?set SONAR_TOKEN (a SonarQube user/analysis token)}"
SONAR_HOST_URL="${SONAR_HOST_URL:-https://sonar.raspberrypi.lan}"

command -v sonar-scanner >/dev/null 2>&1 || {
  echo "sonar-scanner not found — install it (brew install sonar-scanner) or use the CI job" >&2
  exit 1
}

echo "==> generating coverage (go test ./...)"
go test -coverprofile=coverage.out ./... >/dev/null || {
  echo "tests failed — fix them before scanning" >&2
  exit 1
}

echo "==> running SonarQube analysis (waits for the quality-gate verdict)"
# The homelab server uses an internal CA; accept it for the Node-based scanner.
NODE_TLS_REJECT_UNAUTHORIZED=0 sonar-scanner \
  -Dsonar.host.url="$SONAR_HOST_URL" \
  -Dsonar.token="$SONAR_TOKEN"

echo "==> quality gate PASSED (same gate as CI)"
rm -f coverage.out
