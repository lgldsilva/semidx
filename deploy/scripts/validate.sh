#!/bin/sh
set -eu

ROOT_DIR=$(cd -- "$(dirname -- "$0")/../.." && pwd)
COMPOSE_FILE="$ROOT_DIR/deploy/staging/compose.yml"

for script in "$ROOT_DIR"/deploy/scripts/*.sh "$ROOT_DIR"/deploy/docker/*.sh; do
    [ -f "$script" ] || continue
    sh -n "$script"
done

if command -v shellcheck >/dev/null 2>&1; then
    shellcheck --shell=sh --severity=style \
        "$ROOT_DIR"/deploy/scripts/*.sh "$ROOT_DIR"/deploy/docker/*.sh
else
    echo "validate: shellcheck unavailable; sh -n passed" >&2
fi

if command -v docker >/dev/null 2>&1; then
    docker compose -f "$COMPOSE_FILE" config --quiet
else
    echo "validate: docker unavailable; skipped Compose validation" >&2
fi

git -C "$ROOT_DIR" diff --check
forbidden=$(git -C "$ROOT_DIR" diff --name-only | \
    grep -E '^(cmd/|internal/|web/src/)' || true)
if [ -n "$forbidden" ]; then
    echo "validate: ownership violation:" >&2
    printf '%s\n' "$forbidden" >&2
    exit 1
fi

if grep -RniE 'jenkins|password[=:][^$ {<?]|token[=:][^$ {<?]' \
    "$ROOT_DIR/.gitea" "$ROOT_DIR/deploy/staging" >/dev/null 2>&1; then
    echo "validate: possible Jenkins reference or hardcoded secret" >&2
    exit 1
fi

echo "validate: deployment artifacts passed syntax, Compose, diff and ownership checks"
