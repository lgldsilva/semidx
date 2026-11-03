#!/bin/sh
set -eu

ROOT_DIR=$(cd -- "$(dirname -- "$0")/../.." && pwd)
COMPOSE_FILE="$ROOT_DIR/deploy/staging/compose.yml"

command -v docker >/dev/null 2>&1 || {
    echo "preflight: docker is required" >&2
    exit 127
}

EDGE_NETWORK_NAME=${EDGE_NETWORK_NAME:-edge}
if ! docker network inspect "$EDGE_NETWORK_NAME" >/dev/null 2>&1; then
    echo "preflight: external edge network does not exist: $EDGE_NETWORK_NAME" >&2
    exit 78
fi

: "${SEMIDX_IMAGE:?set SEMIDX_IMAGE to a registry image pinned by digest}"
if ! printf '%s\n' "$SEMIDX_IMAGE" | grep -Eq '@sha256:[0-9a-fA-F]{64}$'; then
    echo "preflight: SEMIDX_IMAGE must be registry/image@sha256:<64 hex chars>" >&2
    exit 78
fi

if [ "${SEMIDX_MIGRATE_ENABLED:-false}" = "true" ] &&
    [ ! -f "$ROOT_DIR/deploy/staging/migration-input/index.db" ]; then
    echo "preflight: migration enabled but index.db is absent" >&2
    exit 78
fi

docker compose -f "$COMPOSE_FILE" config --quiet
echo "preflight: staging Compose is valid and image is digest-pinned"
