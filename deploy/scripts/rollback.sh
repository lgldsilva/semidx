#!/bin/sh
set -eu

ROOT_DIR=$(cd -- "$(dirname -- "$0")/../.." && pwd)
COMPOSE_FILE="$ROOT_DIR/deploy/staging/compose.yml"

: "${SEMIDX_ROLLBACK_IMAGE:?set SEMIDX_ROLLBACK_IMAGE to an approved digest}"
if ! printf '%s\n' "$SEMIDX_ROLLBACK_IMAGE" | grep -Eq '@sha256:[0-9a-fA-F]{64}$'; then
    echo "rollback: SEMIDX_ROLLBACK_IMAGE must be pinned by digest" >&2
    exit 78
fi

export SEMIDX_IMAGE="$SEMIDX_ROLLBACK_IMAGE"
"$ROOT_DIR/deploy/scripts/preflight.sh"
docker compose -f "$COMPOSE_FILE" up -d --no-build ui migrate gateway
docker compose -f "$COMPOSE_FILE" ps
