# CI/CD

semidx currently lives in the homelab Gitea and is built by **Gitea Actions**,
which runs the GitHub-Actions-compatible workflow in `.github/workflows/ci.yml`.
The same workflow will run unchanged on GitHub Actions after the public
migration (roadmap F7).

## Flow (current)

```
Gitea push / PR ──► Gitea Actions ──► self-hosted act_runner (homelab)
   test:     go vet · go build · go test -race
   lint:     golangci-lint (via `go run …@v2.12.2`)
   gitleaks: secret scan (via `go run …gitleaks@latest`)
```

Validation gates only — no image build, scan or deploy yet (there is no server
image to ship until the `serve` command lands, F4).

## Portability note

The workflow uses **only** `actions/checkout` and `actions/setup-go`, which work
on both Gitea Actions and GitHub Actions. Lint and secret-scan run as plain
`go run <tool>@<version>` steps rather than marketplace actions
(`golangci-lint-action`, `gitleaks-action`, `commitlint-action`), which are
GitHub-specific and do not run reliably on Gitea Actions. This keeps one
workflow working on both platforms.

## Runners

CI runs on self-hosted `act_runner`s registered in the homelab Gitea (labels
include `ubuntu-latest`). Jobs execute in the `act-ubuntu:ca` image (catthehacker
+ the internal CA baked in). The store integration tests require a Docker
provider inside the job; when none is reachable they skip cleanly
(`testcontainers.SkipIfProviderIsNotHealthy`), so CI stays green either way and
those tests run in full locally (the pre-push hook) where Docker is present.

> Historical note: an earlier iteration used a Jenkins pipeline; the project
> pivoted to Gitea Actions because the same YAML is portable to GitHub. The old
> `Jenkinsfile` has been removed.
