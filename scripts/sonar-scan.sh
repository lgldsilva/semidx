#!/usr/bin/env bash
# Run the REAL SonarCloud quality gate locally — the same gate the GitHub
# `sonar` workflow enforces. sonar-project.properties is the single source of
# config (coverage paths, exclusions, sonar.qualitygate.wait=true); this script
# only supplies the host, the token and the coverage report.
#
# Because the properties file sets sonar.qualitygate.wait=true, `sonar-scanner`
# exits non-zero when the gate fails — so this script fails too (no false-green).
#
#   SONAR_TOKEN=xxxxx ./scripts/sonar-scan.sh
#
# Requires `sonar-scanner` on PATH (brew install sonar-scanner) and go.
set -eu

: "${SONAR_TOKEN:?set SONAR_TOKEN (a SonarCloud analysis token)}"
SONAR_HOST_URL="${SONAR_HOST_URL:-https://sonarcloud.io}"
SONAR_ORGANIZATION="${SONAR_ORGANIZATION:-lgldsilva}"
SONAR_PROJECT_KEY="${SONAR_PROJECT_KEY:-lgldsilva_semidx}"

command -v sonar-scanner >/dev/null 2>&1 || {
  echo "sonar-scanner not found — install it (brew install sonar-scanner) or use the CI job" >&2
  exit 1
}

echo "==> generating coverage (go test ./...)"
go test -coverprofile=coverage.out ./... >/dev/null || {
  echo "tests failed — fix them before scanning" >&2
  exit 1
}

echo "==> running SonarCloud analysis (waits for the quality-gate verdict)"
sonar-scanner \
  -Dsonar.host.url="$SONAR_HOST_URL" \
  -Dsonar.organization="$SONAR_ORGANIZATION" \
  -Dsonar.projectKey="$SONAR_PROJECT_KEY" \
  -Dsonar.token="$SONAR_TOKEN"

echo "==> quality gate PASSED (same gate as CI)"
rm -f coverage.out
