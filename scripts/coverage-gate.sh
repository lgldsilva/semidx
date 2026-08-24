#!/usr/bin/env bash
# Statement-weighted coverage gate for the business packages.
#
# AGENTS.md policy: "Coverage target >=90% on business packages (internal/**,
# pkg/**); cmd/** and deploy/** are excluded from the Sonar coverage
# denominator." This mirrors that denominator: internal/** and pkg/** minus the
# paths listed in sonar.coverage.exclusions (sonar-project.properties).
#
# The math is statement-weighted, read straight from the coverage profile:
# every block line is "file.go:startLine.col,endLine.col numStatements count",
# so coverage = sum(numStatements where count>0) / sum(numStatements). Averaging
# the per-function percentages of `go tool cover -func` instead (the previous
# gate) weighs a 1-statement function the same as a 1000-statement one and
# overstates the result by several points.
#
# Usage: scripts/coverage-gate.sh [coverage-profile] [threshold]
set -euo pipefail

profile="${1:-coverage.out}"
threshold="${2:-90}"
module="github.com/lgldsilva/semidx"

if [ ! -f "$profile" ]; then
  echo "coverage profile not found: $profile" >&2
  exit 1
fi

# Mirrors sonar.coverage.exclusions; cmd/** and deploy/** are already outside
# the internal/** + pkg/** filter below.
excluded="internal/webadmin/templates/,internal/webchat/templates/,internal/webui/,internal/indexstoretest/,internal/mcpinstall/,internal/skills/,internal/store/migrations/,internal/store/migrations.go"

total=$(awk -v module="$module" -v excluded="$excluded" '
  BEGIN {
    n = split(excluded, list, ",")
    prefix = module "/"
  }
  NR == 1 && $1 == "mode:" { next }
  {
    # "<import-path>/<file>.go:<start>,<end> <statements> <count>"
    file = $1
    sub(/:[0-9]+\.[0-9]+,[0-9]+\.[0-9]+$/, "", file)
    if (index(file, prefix) != 1) next
    rel = substr(file, length(prefix) + 1)
    if (rel !~ /^(internal|pkg)\//) next
    for (i = 1; i <= n; i++) {
      if (list[i] != "" && index(rel, list[i]) == 1) next
    }
    statements += $2
    if ($3 > 0) covered += $2
  }
  END {
    if (statements > 0) printf "%.2f", 100 * covered / statements
    else print "0"
  }
' "$profile")

echo "business-package coverage (internal+pkg, statement-weighted): ${total}%"
awk -v t="$total" -v threshold="$threshold" 'BEGIN {
  if (t + 0 < threshold + 0) {
    print "business coverage below " threshold "%: " t
    exit 1
  }
}'
