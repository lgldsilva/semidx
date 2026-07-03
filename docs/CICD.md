# CI/CD

semidx lives in the homelab Gitea and is built by **Gitea Actions** on
self-hosted `act_runner`s. There are **two workflows** with different jobs:

| Workflow | File | Triggers | Purpose |
|---|---|---|---|
| **CI** | `.github/workflows/ci.yml` | every push to `main`, every pull request | fast validation gate — no infra needed |
| **Release** | `.gitea/workflows/release.yml` | version tags (`v*`) and manual dispatch | Sonar + SBOM + Trivy + image build/push |

The CI workflow is intentionally left in `.github/workflows/` so the same YAML
also runs unchanged on GitHub Actions after the public migration (roadmap F7).
The release workflow lives in `.gitea/workflows/` because it targets homelab
infrastructure (SonarQube, Dependency-Track, the Gitea registry) that only
exists behind the WireGuard network.

## CI gate (`.github/workflows/ci.yml`)

Runs on `ubuntu-latest` runners; needs no secrets or homelab services.

```
Gitea push / PR ──► Gitea Actions ──► self-hosted act_runner
   test:       go vet · go build · go test -race -shuffle=on
   lint:       golangci-lint (via `go run …@v2.12.2`)
   gitleaks:   secret scan (via `go run …gitleaks@latest`)
   govulncheck: CVE scan of reachable deps + stdlib (via `go run …`)
```

Uses only `actions/checkout` and `actions/setup-go`; every tool runs as a plain
`go run <tool>@<version>` step, so the workflow is portable to GitHub Actions.
This file is the sole PR gate and is **not** touched by the release pipeline.

## Release pipeline (`.gitea/workflows/release.yml`)

Triggers **only** on `push: tags: ['v*']` and on `workflow_dispatch` — never on
ordinary pushes to `main`, so it does not duplicate the CI gate. Jobs run on the
generic `[self-hosted]` runner pool.

```
build-test ──┬──► sonar          (SonarQube quality gate)
             ├──► sbom            (CycloneDX ──► Dependency-Track, continue-on-error)
             └──► image           (docker build ──► Trivy CRITICAL gate ──► push)
                       notify-failure  (Telegram, only if a real job failed)
```

Job graph (`needs` edges):

- `build-test` — go build, `go test -race -coverprofile=coverage.out ./...`,
  uploads `coverage.out` as an artifact. Always runs.
- `sonar` — **needs** `build-test`. Downloads the coverage artifact and runs
  `sonarqube-scanner` against SonarQube; waits on the server-side quality gate.
- `sbom` — **needs** `build-test`, `continue-on-error: true`. Generates a
  CycloneDX SBOM (`scripts/sbom-upload.sh`) and uploads it to Dependency-Track.
  Never fails the pipeline.
- `image` — **needs** `build-test`. Builds the Docker image, runs **Trivy on the
  LOCAL image before any push** (fails on a fixable CRITICAL), then pushes to the
  Gitea registry.
- `notify-failure` — **needs** `[build-test, sonar, sbom, image]`,
  `if: failure()`. Sends a Telegram message. Because `sbom` is
  continue-on-error, an SBOM failure alone does not trigger it.

### Graceful skipping (no secret ⇒ no failure)

The semidx deploy instance and its homelab wiring do not exist yet, so the
release pipeline is built to **stay green when the infra/secrets are absent**.
Job-level `if:` cannot read secrets, so each secret is mapped into an `env:` and
the dependent step is guarded with `if: ${{ env.THESECRET != '' }}`. When the
secret is empty the step is skipped and the job succeeds:

- no `SONAR_TOKEN` ⇒ the SonarQube analysis step is skipped;
- no `DT_API` ⇒ the SBOM upload is skipped (and it is continue-on-error anyway);
- no `REGISTRY_TOKEN` ⇒ the image is still built **and Trivy-scanned locally**,
  but not pushed;
- no Telegram secrets ⇒ the failure notification is skipped.

## Operator setup — Gitea repo secrets & variables

Set these in the Gitea repository under **Settings → Actions → Secrets** (and
**Variables** where noted). Each one lights up one optional stage; leave any of
them unset and that stage is skipped cleanly.

### Secrets

| Secret | Lights up | Notes |
|---|---|---|
| `SONAR_TOKEN` | `sonar` job | SonarQube analysis token (server URL is hard-coded to `https://sonar.raspberrypi.lan`). |
| `DT_API` | `sbom` job | Dependency-Track base URL, e.g. `https://dtrack.raspberrypi.lan`. Presence of `DT_API` gates the job. |
| `DT_USER` | `sbom` job | Dependency-Track user with `BOM_UPLOAD` permission. |
| `DT_PASS` | `sbom` job | password for `DT_USER`. |
| `REGISTRY_TOKEN` | push step of `image` | Token/password for the Gitea container registry (`gitea.raspberrypi.lan`). Without it the image is built + scanned but not pushed. |
| `TELEGRAM_BOT_TOKEN` | `notify-failure` | Telegram bot token. |
| `TELEGRAM_CHAT_ID` | `notify-failure` | Telegram chat id. Both Telegram secrets must be set for a notification. |

### Variables (optional)

| Variable | Used by | Default if unset |
|---|---|---|
| `REGISTRY_USER` | `image` push | `lgldsilva` |
| `SONAR_HOST_IP` | `sonar` | (none) — when set and DNS has no record, the runner appends `<ip> sonar.raspberrypi.lan` to `/etc/hosts` before scanning. |

The internal CA is handled without extra config: the Node-based artifact and
Sonar steps set `NODE_TLS_REJECT_UNAUTHORIZED=0`, and `scripts/sbom-upload.sh`
calls `curl --insecure` against Dependency-Track.

## Deploy is deferred

There is **no running semidx instance to deploy to yet**. The `image` job
therefore stops at build + scan + (optional) push; it does **not** run a compose
deploy, create a release git tag, prune old registry packages, or send a success
notification. When a semidx host is provisioned, add a `deploy` job (ideally on a
dedicated `deploy` runner label, which also needs provisioning) that runs
`docker compose -f deploy/docker-compose.yml up -d` on that host, gated on
`REGISTRY_TOKEN` the same way as the push step.

## Runners

CI runs on self-hosted `act_runner`s (label `ubuntu-latest`) in the
`act-ubuntu:ca` image (catthehacker + the internal CA baked in). The release
pipeline uses the generic `[self-hosted]` pool; its `image` job additionally
requires Docker (the daemon socket) on the runner to build and to run Trivy.
Store integration tests skip cleanly when no Docker provider is reachable
(`testcontainers.SkipIfProviderIsNotHealthy`), so `build-test` stays green
either way.

> Historical note: an earlier iteration used a Jenkins pipeline; the project
> pivoted to Gitea Actions because the CI YAML is portable to GitHub. The old
> `Jenkinsfile` has been removed.
