#!/bin/sh
set -eu

SECRETS_DIR=${SEMIDX_SECRETS_DIR:-/run/secrets}

read_secret() {
    secret_name=$1
    secret_file="$SECRETS_DIR/$secret_name"
    if [ -r "$secret_file" ]; then
        tr -d '\r\n' < "$secret_file"
    fi
}

import_if_unset() {
    variable_name=$1
    secret_name=$2
    current_value=$3
    if [ -z "$current_value" ]; then
        secret_value=$(read_secret "$secret_name")
        if [ -n "$secret_value" ]; then
            export "$variable_name=$secret_value"
        fi
    fi
}

import_if_unset SEMIDX_JWT_SECRET jwt_secret "${SEMIDX_JWT_SECRET:-}"
import_if_unset SEMIDX_SECRET_KEY secret_key "${SEMIDX_SECRET_KEY:-}"
import_if_unset SEMIDX_CSRF_KEY csrf_key "${SEMIDX_CSRF_KEY:-}"
import_if_unset SEMIDX_BOOTSTRAP_ADMIN_PASSWORD bootstrap_admin_password "${SEMIDX_BOOTSTRAP_ADMIN_PASSWORD:-}"

# The current application accepts a DSN, not a *_FILE variant. Vault therefore
# supplies a URL-safe generated password and the DSN is assembled here.
if [ -z "${SEMIDX_DB_DSN:-}" ]; then
    db_password=$(read_secret db_password)
    if [ -z "$db_password" ]; then
        echo "semidx: missing db_password in $SECRETS_DIR (or SEMIDX_DB_DSN)" >&2
        exit 78
    fi
    case "$db_password" in
        *[!A-Za-z0-9._~-]*)
            echo "semidx: db_password contains unsupported DSN characters" >&2
            exit 78
            ;;
    esac
    export SEMIDX_DB_DSN="postgres://semidx:${db_password}@postgres:5432/semidx?sslmode=disable"
fi

if [ "$#" -eq 0 ]; then
    set -- /usr/local/bin/semidx serve
elif [ "${1#-}" != "$1" ] || [ "$1" = "serve" ] || [ "$1" = "index" ] || [ "$1" = "search" ] || [ "$1" = "migrate" ] || [ "$1" = "config" ] || [ "$1" = "login" ] || [ "$1" = "push" ] || [ "$1" = "repo" ]; then
    set -- /usr/local/bin/semidx "$@"
fi

exec "$@"
