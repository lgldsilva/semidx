# CI/CD

semidx lives in the homelab Gitea and is built by **Gitea Actions** on
self-hosted `act_runner`s. There are **two workflows** with different jobs:

| Workflow | File | Triggers | Purpose |
|---|---|---|---|
| **CI (gates)** | `.github/workflows/ci.yml` | every push to `main`, every pull request | **quality/security gates** — build, test, lint, gitleaks, govulncheck, gosec, Trivy image scan (all on PR + main); SonarQube **on main only** |
| **Release (deploy)** | `.gitea/workflows/release.yml` | version tags (`v*`) and manual dispatch | **publishes** the release — pushes the image and uploads the SBOM to Dependency-Track |

The split is deliberate: **a PR runs every gate that predicts what may enter
`main`; the only thing the release adds is the deploy** (image push + SBOM
publish). The release commit is already gated by the PR it came from, so the
gates are not re-run at release time.

> **SonarQube is main-only.** The homelab SonarQube is **Community edition**,
> which analyses a single branch and has no PR/branch analysis. So the `sonar`
> job runs only on push to `main` — a post-merge quality gate on main, not a PR
> gate. PR-time prediction comes from the other gates (a red gate there blocks
> the merge before it ever reaches main).

The CI workflow is intentionally left in `.github/workflows/` so the same YAML
also runs unchanged on GitHub Actions after the public migration (roadmap F7).
The release workflow lives in `.gitea/workflows/` because it targets homelab
infrastructure (SonarQube, Dependency-Track, the Gitea registry) that only
exists behind the WireGuard network.

## CI gates (`.github/workflows/ci.yml`)

The gate pipeline. Runs on every PR and every push to `main`.

```
Gitea push / PR ──► Gitea Actions ──► self-hosted act_runner
   test:        go vet · go build · go test -race -shuffle=on
   lint:        golangci-lint (via `go run …@v2.12.2`)
   gitleaks:    secret scan (via `go run …gitleaks@latest`)
   govulncheck: CVE scan of reachable deps + stdlib (via `go run …`)
   gosec:       SAST (via `go run …gosec@v2.27.1`)
   sonar:       SonarQube quality gate — MAIN ONLY (Community edition has no PR
                analysis). Runs only on push to main; sonar.qualitygate.wait=true
                fails the job when main's gate fails. Skips without SONAR_TOKEN.
   image-scan:  docker build + Trivy CRITICAL gate on the local image (no push).
                Skips if the runner has no Docker.
```

The Go tool gates use only `actions/checkout` and `actions/setup-go` and run
each tool as a plain `go run <tool>@<version>` step, so they stay portable to
GitHub Actions. The `sonar` gate needs the homelab `SONAR_TOKEN` and pins the
Sonar host IP into `/etc/hosts` (act_runner has no LAN DNS); it degrades to a
skip when the secret is absent, so forked/secret-less runs stay green.

## Release / deploy pipeline (`.gitea/workflows/release.yml`)

Triggers **only** on `push: tags: ['v*']` and on `workflow_dispatch` — this is
the DEPLOY, not a gate. Jobs run on the generic `[self-hosted]` runner pool.

```
build-test ──┬──► sbom            (CycloneDX ──► Dependency-Track, continue-on-error)
             └──► image           (docker build ──► Trivy CRITICAL ──► push = deploy)
                       notify-failure  (Telegram, only if a real job failed)
```

Job graph (`needs` edges):

- `build-test` — pre-publish sanity: `go build` + `go test -race ./...` on the
  tagged commit. The full gate set already ran on the PR that merged it.
- `sbom` — **needs** `build-test`, `continue-on-error: true`. Generates a
  CycloneDX SBOM (`scripts/sbom-upload.sh`) and uploads it to Dependency-Track.
  Never fails the pipeline.
- `image` — **needs** `build-test`. Builds the Docker image, runs **Trivy on the
  LOCAL image before any push** (fails on a fixable CRITICAL), then pushes to the
  Gitea registry — the actual publish/deploy step.
- `notify-failure` — **needs** `[build-test, sbom, image]`,
  `if: failure()`. Sends a Telegram message. Because `sbom` is
  continue-on-error, an SBOM failure alone does not trigger it.

### Graceful skipping (no secret ⇒ no failure)

The semidx deploy instance and its homelab wiring do not exist yet, so the
release pipeline is built to **stay green when the infra/secrets are absent**.
Job-level `if:` cannot read secrets, so each secret is mapped into an `env:` and
the dependent step is guarded with `if: ${{ env.THESECRET != '' }}`. When the
secret is empty the step is skipped and the job succeeds:

- no `SONAR_TOKEN` ⇒ the SonarQube gate (in `ci.yml`) is skipped;
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
| `SONAR_TOKEN` | `sonar` gate in `ci.yml` (PR + push) | SonarQube analysis token (server URL is hard-coded to `https://sonar.raspberrypi.lan`). |
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
| `SONAR_HOST_IP` | `sonar` | `192.168.0.100` — when the runner can't resolve `sonar.raspberrypi.lan` (the usual act_runner case), it appends `<ip> sonar.raspberrypi.lan` to `/etc/hosts` before scanning. Override only if the server moves. |

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
