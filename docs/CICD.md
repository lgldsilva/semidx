# CI/CD

semidx lives in the homelab Gitea and is built by **Gitea Actions** on
self-hosted `act_runner`s. The workflows, each with different jobs:

| Workflow | File | Triggers | Purpose |
|---|---|---|---|
| **CI (gates)** | `.gitea/workflows/ci.yml` | every push to `main`, every pull request | **quality/security gates** — build, test, lint, gitleaks, govulncheck, gosec, Trivy image scan (all on PR + main); SonarQube **on main only** |
| **Auto-tag (semver)** | `.gitea/workflows/autotag.yml` | every push to `main` | **cuts the version** — computes the next semver from Conventional Commits and pushes a `v*` tag, which triggers the release |
| **Release (deploy)** | `.gitea/workflows/release.yml` | version tags (`v*`) and manual dispatch | **publishes + deploys** the release — pushes the image, uploads the SBOM to Dependency-Track, builds GoReleaser artifacts, and redeploys via Watchtower |

The split is deliberate: **a PR runs every gate that predicts what may enter
`main`; auto-tag turns a merge into a version; the release does the deploy**
(image push + SBOM publish + artifacts). The release commit is already gated by
the PR it came from, so the gates are not re-run at release time.

> **Continuous delivery.** A merge to `main` with a `feat`/`fix` (etc.) commit
> auto-tags a new version and ships it end-to-end — no manual `git tag`. Merges
> with only `docs`/`chore`/`ci`/`test` commits bump nothing and cut no release.

> **SonarQube is main-only.** The homelab SonarQube is **Community edition**,
> which analyses a single branch and has no PR/branch analysis. So the `sonar`
> job runs only on push to `main` — a post-merge quality gate on main, not a PR
> gate. PR-time prediction comes from the other gates (a red gate there blocks
> the merge before it ever reaches main).

All workflows live in **`.gitea/workflows/`**. Gitea scans only that directory
when it exists and ignores `.github/workflows/`, so keeping the gates under
`.github/` silently stopped them from running on PRs once the release workflow
was added under `.gitea/`. The gate YAML stays GitHub-Actions compatible (only
`actions/checkout` + `actions/setup-go`), so it can move to `.github/workflows/`
unchanged for the public migration (roadmap F7). The release workflow targets
homelab infrastructure (SonarQube, Dependency-Track, the Gitea registry) reachable
only behind the WireGuard network.

## CI gates (`.gitea/workflows/ci.yml`)

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

## Auto-tag / versioning (`.gitea/workflows/autotag.yml`)

On every push to `main`, one job computes the next version with **svu**
(`go run github.com/caarlos0/svu/v3@v3.2.1`, pinned like the other tools) from the
Conventional Commits since the last tag — `feat` → minor, `fix`/others → patch,
`!`/`BREAKING CHANGE` → major. If it bumps, it pushes an annotated `v*` tag; that
tag is what triggers the release pipeline below. A merge whose commits warrant no
bump (only `docs`/`chore`/`ci`/`test`) tags nothing, and the job is idempotent
(it skips a tag that already exists).

> **Requires a PAT — `RELEASE_TAG_TOKEN`.** A tag pushed by the auto-provided
> Actions token (`secrets.GITEA_TOKEN`) does **not** re-trigger another workflow
> (loop guard, same as GitHub), so `release.yml` would never fire. The push
> therefore uses a real Personal Access Token (repo **write** scope) stored in the
> repo secret `RELEASE_TAG_TOKEN`. It cannot be named `GITEA_TOKEN` — Gitea
> reserves the `GITEA_`/`GITHUB_` secret prefixes. **Without the secret the job
> skips cleanly** (so forks and a not-yet-provisioned repo stay green, but no
> release is cut until the PAT is set).

## Release / deploy pipeline (`.gitea/workflows/release.yml`)

Triggers **only** on `push: tags: ['v*']` and on `workflow_dispatch` — this is
the DEPLOY, not a gate. Jobs run on the generic `[self-hosted]` runner pool.

```
build-test ──┬──► sbom              (CycloneDX ──► Dependency-Track, continue-on-error)
             ├──► image ──► deploy   (push image → trigger Watchtower redeploy)
             └──► release-artifacts  (GoReleaser: multi-arch tar.gz/zip + checksums + changelog)
                       notify-failure  (Telegram, only if a real job failed)
```

Job graph (`needs` edges):

- `build-test` — pre-publish sanity: `go build` + `go test -race ./...` on the
  tagged commit. The full gate set already ran on the PR that merged it.
- `sbom` — **needs** `build-test`, `continue-on-error: true`. Generates a
  CycloneDX SBOM (`scripts/sbom-upload.sh`) and uploads it to Dependency-Track.
  Never fails the pipeline.
- `image` — **needs** `build-test`. Builds the Docker image, runs **Trivy on the
  LOCAL image before any push** (fails on a fixable CRITICAL), then pushes it to
  the Gitea registry.
- `deploy` — **needs** `image`. Triggers the homelab instance to roll out the new
  image (see **Deploy** below). Skips cleanly when the trigger isn't configured.
- `release-artifacts` — **needs** `build-test`. Runs **GoReleaser** (`.goreleaser.yaml`)
  to cross-build linux/darwin/windows × amd64/arm64, package tar.gz/zip with
  SHA-256 checksums + a grouped changelog, and publish them to the Gitea release.
  goreleaser is pinned to **v2.13.0** (the newest that builds with go.mod's Go —
  `@latest` needs Go 1.26+, which `GOTOOLCHAIN=local` blocks). Without `GITEA_TOKEN`
  it builds a snapshot to validate the config without publishing. `install.sh`
  consumes these assets.
- `notify-failure` — **needs** `[build-test, sbom, image, deploy, release-artifacts]`,
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
| `REGISTRY_TOKEN` | push step of `image` | Token/password for the Gitea container registry (`gitea.raspberrypi.lan`), scope `write:package`. Without it the image is built + scanned but not pushed (and nothing to deploy). |
| `WATCHTOWER_TOKEN` | `deploy` job | Bearer token for Watchtower's HTTP API (`WATCHTOWER_HTTP_API_TOKEN` on the host). Triggers an immediate redeploy. |
| `GITEA_TOKEN` | `release-artifacts` job | Gitea token with repo write scope, used by GoReleaser to publish the release + upload the artifacts. Without it, artifacts are built as a snapshot but not published. |
| `RELEASE_TAG_TOKEN` | `autotag` job in `autotag.yml` | **PAT** (repo **write** scope) used to push the computed `v*` tag so the tag event re-triggers `release.yml` (the auto-provided `GITEA_TOKEN` cannot re-trigger a workflow, and cannot be renamed — Gitea reserves the `GITEA_`/`GITHUB_` prefixes). Without it, auto-tag skips and no release is cut. |
| `TELEGRAM_BOT_TOKEN` | `notify-failure` | Telegram bot token. |
| `TELEGRAM_CHAT_ID` | `notify-failure` | Telegram chat id. Both Telegram secrets must be set for a notification. |

### Variables (optional)

| Variable | Used by | Default if unset |
|---|---|---|
| `REGISTRY_USER` | `image` push | `lgldsilva` |
| `SONAR_HOST_IP` | `sonar` | `192.168.0.100` — when the runner can't resolve `sonar.raspberrypi.lan` (the usual act_runner case), it appends `<ip> sonar.raspberrypi.lan` to `/etc/hosts` before scanning. Override only if the server moves. |
| `WATCHTOWER_URL` | `deploy` | (none) — Watchtower HTTP-API base URL, e.g. `https://watchtower.raspberrypi.lan`. Set (with `WATCHTOWER_TOKEN`) to redeploy immediately on release. |
| `SEMIDX_HEALTH_URL` | `deploy` | (none) — optional health URL polled after redeploy, e.g. `https://semidx.raspberrypi.lan/readyz`. |

The internal CA is handled without extra config: the Node-based artifact and
Sonar steps set `NODE_TLS_REJECT_UNAUTHORIZED=0`, and `scripts/sbom-upload.sh`
calls `curl --insecure` against Dependency-Track.

## Deploy

Deploy is **pull-based**, matching the rest of the homelab: the release pipeline
publishes the image and the target host's **Watchtower** rolls it out — the CI
never needs SSH or a deploy runner. The `deploy` job just asks Watchtower to do
it **immediately** instead of waiting for its poll interval.

Flow: `image` pushes `…/semidx:latest` → `deploy` calls Watchtower's HTTP API on
the host → Watchtower pulls `:latest` and recreates the `semidx` container →
(optional) the job polls `SEMIDX_HEALTH_URL` until it is serving.

One-time host setup (raspberrypi-srv):

1. `docker login gitea.raspberrypi.lan` so Watchtower can pull the private image.
2. Put `deploy/homelab/docker-compose.yml` + a filled `.env` (from
   `.env.example`) on the host and `docker compose up -d`. The `semidx` service
   carries the `com.centurylinklabs.watchtower.enable=true` label.
3. Run Watchtower with its HTTP API enabled
   (`--http-api-update` + `WATCHTOWER_HTTP_API_TOKEN=<token>`), reachable over
   WireGuard, and set the repo variable `WATCHTOWER_URL` + secret
   `WATCHTOWER_TOKEN` (below).

Without `WATCHTOWER_URL`/`WATCHTOWER_TOKEN` the `deploy` job is a no-op that
notes the image was published and Watchtower will pick it up on its own interval.
Watchtower only swaps the image — env/port/volume changes still require editing
the host compose + `docker compose up -d`.

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

## Concurrency policy

All workflows carry a top-level `concurrency` block to prevent redundant gate
runs when multiple commits land on the same PR in quick succession.

### Rules per workflow

| Workflow | group | cancel-in-progress | Effect |
|---|---|---|---|
| `ci.yml` | `CI-{PR number}` (PR) / `CI-refs/heads/main` (push/dispatch) | `true` on PR, `false` otherwise | Cancels stale runs of the same PR; never cancels main or dispatch runs |
| `release.yml` | `Release` (fixed) | `false` | Serializes all releases regardless of tag; never aborts a deploy in progress |
| `mutation.yml` | `Mutation-refs/heads/main` | `false` | Serializes nightly runs; does not interrupt mutation in progress |
| `autotag.yml` | `autotag-main` (string literal) | `false` (implicit) | Serializes tag computation; unchanged from original |

### How the dynamic group in ci.yml works

`ci.yml` triggers on three events (`pull_request`, `push: [main]`,
`workflow_dispatch`). A single `concurrency` block covers all three:

```yaml
concurrency:
  group: ${{ github.workflow }}-${{ github.event.pull_request.number || github.ref }}
  cancel-in-progress: ${{ github.event_name == 'pull_request' }}
```

- **PR**: `github.event.pull_request.number` is the PR number (truthy) → group
  `CI-42`; `cancel-in-progress` evaluates to `true` → stale runs of the same PR
  are cancelled.
- **push to main / dispatch**: `github.event.pull_request.number` is null (falsy)
  → `|| github.ref` wins → group `CI-refs/heads/main`; `cancel-in-progress`
  evaluates to `false` → runs queue, never cancel.

### Why release.yml uses a fixed group

`release.yml` triggers on `push: tags: ['v*']` and `workflow_dispatch`. Using
`${{ github.ref }}` as the group would give each tag its own group
(`Release-refs/tags/v1.2.3` vs `Release-refs/tags/v1.2.4`), allowing two
releases to deploy concurrently. A fixed group (`${{ github.workflow }}`)
serializes all releases regardless of tag — the second waits for the first to
finish.

### Operational checklist — stuck queue

1. Check that the runner is `Online` in **Settings → Actions → Runners**.
2. If offline: restart the `act_runner` container on the affected node.
3. Stale PR runs (same PR, outdated commit) are cancelled automatically by
   `concurrency` on the next push — no manual cancellation needed.
4. Queued `release.yml` runs wait for the previous one to finish
   (`cancel-in-progress: false`). If the previous run is stuck, cancel it
   manually in Gitea before re-triggering.
5. Rollback: if a misconfigured group causes unexpected cancellations, remove
   only the `concurrency` block from the affected workflow and re-run — other
   workflows are unaffected.
