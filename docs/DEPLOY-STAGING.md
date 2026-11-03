# Staging: gateway, semidx, Postgres and Vault Agent

The dedicated Compose file is `deploy/staging/compose.yml`:

```text
external edge -> gateway (Caddy) -> internal -> ui (semidx :8080)
                                       |-> postgres/pgvector
                                       |-> migrate (one-shot)

external Vault -> vault-agent -> vault_secrets volume (read-only in the app)
```

`ui` is the semidx server; the React UI stays embedded in the binary at
`/admin`. The gateway is the only published port. Postgres has no `ports:` and
`internal` is an internal Docker network.

## Prerequisites

1. Create the ingress network outside Compose:

   ```sh
   docker network create edge
   ```

2. Create the external Docker secrets `semidx-staging-vault-role-id` and
   `semidx-staging-vault-secret-id`. Their values never enter Git.
3. Configure the Vault AppRole to read `secret/data/semidx/staging` with at
   least `db_password`. The value must be URL-safe (`A-Za-z0-9._~-`), because
   the entrypoint builds the application's DSN from it as-is.
4. Replace the placeholder address in `deploy/staging/vault/agent.hcl` (or set
   `VAULT_ADDR`) with a URL the container can reach. Role ID and secret ID stay
   out of the repository.
5. Publish an image to your registry and pin it by digest, for example:

   ```sh
   export SEMIDX_IMAGE=registry.example.invalid/owner/semidx@sha256:<digest>
   ```

## Operation

```sh
./deploy/scripts/preflight.sh
./deploy/scripts/deploy.sh
docker compose -f deploy/staging/compose.yml ps
docker compose -f deploy/staging/compose.yml logs --tail=100 gateway ui vault-agent
```

`preflight.sh` requires the edge network and a digest-pinned image; it changes
nothing. `deploy.sh` only runs `docker compose up -d --no-build`. The gateway
serves plain HTTP to the edge network: TLS is terminated by the external proxy,
and no real certificates live in this repository. A local override can mount
certificates into Caddy.

## Migration

Schema migrations are applied automatically by semidx when it opens Postgres.
The `migrate` service only imports a legacy SQLite index and is disabled by
default:

```sh
export SEMIDX_MIGRATE_ENABLED=true
export SEMIDX_MIGRATE_SOURCE=/migration/index.db
# place index.db only in deploy/staging/migration-input/ (ignored by Git)
./deploy/scripts/preflight.sh
./deploy/scripts/deploy.sh
```

If enabled without `index.db`, the job fails with a configuration error; it
never guesses a source or deletes data. `ui` starts only after the job exits
successfully.

## Resources and hardening

Declared limits add up to 4.875 GiB: gateway 128 MiB, postgres 1536 MiB, ui
2048 MiB, vault-agent 256 MiB and migrate 1024 MiB. Every container runs with
`no-new-privileges`, `cap_drop: ALL` and a read-only root filesystem; volumes
and tmpfs provide the writable paths they need.

## Rollback

Switch explicitly to another digest, without rebuilding and without touching
Postgres:

```sh
export SEMIDX_ROLLBACK_IMAGE=registry.example.invalid/owner/semidx@sha256:<approved-digest>
./deploy/scripts/rollback.sh
```

## Validation

```sh
./deploy/scripts/validate.sh
```

Checks shell syntax (and shellcheck when installed), renders the Compose file,
runs `git diff --check`, and rejects literal passwords or tokens under
`deploy/staging`. It publishes nothing and starts nothing.
