#!/bin/sh
set -eu

case "${SEMIDX_MIGRATE_ENABLED:-false}" in
    1|true|TRUE|yes|YES) ;;
    *)
        echo "semidx migrate: disabled (set SEMIDX_MIGRATE_ENABLED=true to opt in)"
        exit 0
        ;;
esac

source_path=${SEMIDX_MIGRATE_SOURCE:-/migration/index.db}
if [ ! -f "$source_path" ]; then
    echo "semidx migrate: source does not exist: $source_path" >&2
    exit 78
fi

exec /usr/local/bin/semidx migrate --from "$source_path" --to "$SEMIDX_DB_DSN"
