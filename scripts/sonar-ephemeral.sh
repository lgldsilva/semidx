#!/usr/bin/env bash
# sonar-ephemeral.sh — Community Edition PR analysis via temporary project.
#
# CE has no PR/branch analysis. On PRs we:
#   1. analyze into a temp projectKey (auto-created)
#   2. wait for quality gate + print measures report
#   3. delete the temp project (always)
# On main (EPHEMERAL=0): analyze permanent projectKey=semidx.
#
# Uses npx sonarqube-scanner (matches existing semidx CI) so Node trusts the
# homelab Development CA via NODE_EXTRA_CA_CERTS.
set -euo pipefail

SONAR_HOST_URL="${SONAR_HOST_URL:-${SONAR_URL:-https://sonar.raspberrypi.lan}}"
SONAR_TOKEN="${SONAR_TOKEN:-}"
PROJECT_KEY="${PROJECT_KEY:-semidx-tmp-$(date +%s)}"
PROJECT_NAME="${PROJECT_NAME:-$PROJECT_KEY}"
EPHEMERAL="${EPHEMERAL:-1}"
COVERAGE_FILE="${COVERAGE_FILE:-coverage.out}"
REPORT_FILE="${REPORT_FILE:-/tmp/sonar-ephemeral-report.txt}"
NODE_EXTRA_CA_CERTS="${NODE_EXTRA_CA_CERTS:-/usr/local/share/ca-certificates/gitea-ca.crt}"

if [ -z "$SONAR_TOKEN" ]; then
  echo "✘ SONAR_TOKEN not set"
  exit 1
fi

api() {
  local method="$1" path="$2"
  shift 2
  # Prefer Bearer so secret scanners do not flag curl -u patterns.
  curl -sk -X "$method" -H "Authorization: Bearer ${SONAR_TOKEN}" "${SONAR_HOST_URL}${path}" "$@"
}

cleanup() {
  local rc=$?
  if [ "$EPHEMERAL" = "1" ]; then
    echo "→ deleting temporary Sonar project: $PROJECT_KEY"
    api POST "/api/projects/delete" --data-urlencode "project=${PROJECT_KEY}" >/dev/null 2>&1 \
      || echo "  (delete non-zero — may already be gone)"
  fi
  exit "$rc"
}
trap cleanup EXIT

if [ ! -s "$COVERAGE_FILE" ]; then
  echo "→ generating $COVERAGE_FILE"
  go test -count=1 -coverprofile="$COVERAGE_FILE" ./...
fi

if [ -f "$NODE_EXTRA_CA_CERTS" ]; then
  export NODE_EXTRA_CA_CERTS
else
  echo "  (NODE_EXTRA_CA_CERTS not found at $NODE_EXTRA_CA_CERTS — Node may fail TLS)"
fi

echo "→ SonarScanner projectKey=$PROJECT_KEY ephemeral=$EPHEMERAL url=$SONAR_HOST_URL"

set +e
npx -y sonarqube-scanner \
  -Dsonar.host.url="$SONAR_HOST_URL" \
  -Dsonar.token="$SONAR_TOKEN" \
  -Dsonar.projectKey="$PROJECT_KEY" \
  -Dsonar.projectName="$PROJECT_NAME" \
  -Dsonar.go.coverage.reportPaths="$COVERAGE_FILE" \
  -Dsonar.qualitygate.wait=true \
  2>&1 | tee /tmp/sonar-ephemeral-scan.log
SCAN_RC=${PIPESTATUS[0]}
set -e

echo "→ fetching quality gate + measures"
QG_JSON="$(api GET "/api/qualitygates/project_status?projectKey=${PROJECT_KEY}" || true)"
MEAS_JSON="$(api GET "/api/measures/component?component=${PROJECT_KEY}&metricKeys=bugs,vulnerabilities,code_smells,coverage,duplicated_lines_density,ncloc,security_hotspots" || true)"

{
  echo "======== Sonar ephemeral report ========"
  echo "projectKey: $PROJECT_KEY"
  echo "url: ${SONAR_HOST_URL}/dashboard?id=${PROJECT_KEY}"
  echo "scanner_exit: $SCAN_RC"
  echo ""
  echo "--- quality gate ---"
  printf '%s' "$QG_JSON" | python3 -c '
import json,sys
raw=sys.stdin.read().strip()
if not raw:
    print("(empty)"); raise SystemExit
try: d=json.loads(raw)
except Exception: print(raw[:400]); raise SystemExit
ps=d.get("projectStatus") or {}
print("status:", ps.get("status","?"))
for c in ps.get("conditions") or []:
    print("  - %s: %s actual=%s threshold=%s" % (c.get("metricKey"), c.get("status"), c.get("actualValue"), c.get("errorThreshold")))
' 2>/dev/null || echo "$QG_JSON"
  echo ""
  echo "--- measures ---"
  printf '%s' "$MEAS_JSON" | python3 -c '
import json,sys
raw=sys.stdin.read().strip()
if not raw:
    print("(empty)"); raise SystemExit
try: d=json.loads(raw)
except Exception: print(raw[:400]); raise SystemExit
for m in (d.get("component") or {}).get("measures") or []:
    print("  %s: %s" % (m.get("metric"), m.get("value")))
' 2>/dev/null || echo "$MEAS_JSON"
  echo "======================================="
} | tee "$REPORT_FILE"

QG_STATUS="$(printf '%s' "$QG_JSON" | python3 -c 'import json,sys
try:
 d=json.load(sys.stdin); print((d.get("projectStatus") or {}).get("status") or "UNKNOWN")
except Exception: print("UNKNOWN")' 2>/dev/null || echo UNKNOWN)"

echo "quality_gate_status=$QG_STATUS"
if [ "$QG_STATUS" = "ERROR" ] || [ "$QG_STATUS" = "FAILED" ]; then
  echo "✘ Quality gate FAILED"
  exit 1
fi
if [ "$SCAN_RC" -ne 0 ] && [ "$QG_STATUS" = "UNKNOWN" ]; then
  echo "✘ Scanner failed and QG unknown"
  exit 1
fi
echo "✓ Quality gate acceptable ($QG_STATUS)"
exit 0
