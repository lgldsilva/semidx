#!/bin/sh
set -eu

ROOT_DIR=$(cd -- "$(dirname -- "$0")/../.." && pwd)
COMPOSE_FILE="$ROOT_DIR/deploy/staging/compose.yml"

"$ROOT_DIR/deploy/scripts/preflight.sh"
docker compose -f "$COMPOSE_FILE" up -d --no-build
docker compose -f "$COMPOSE_FILE" ps
