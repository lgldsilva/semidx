# CI/CD

semidx currently lives in the homelab Gitea and is built by Jenkins. Two CI
definitions exist in the repo, active at different stages of the roadmap:

| File | Runs on | Status |
|---|---|---|
| `Jenkinsfile` | Jenkins (Gitea) | **active now** |
| `.github/workflows/ci.yml` | GitHub Actions | dormant until the public GitHub migration (roadmap F7) |

## Flow (current)

```
Gitea push ──webhook──► Jenkins @ oracle-desktop (job: semidx)
   gofmt · go vet · go build · go test -race + coverage · golangci-lint · gitleaks
```

Validation gates only. There is deliberately **no** SonarQube quality gate,
image build, Trivy scan or deploy yet:

- Test coverage currently exists only in `internal/config`; a hard coverage gate
  would be red by design. It gets wired in once the package restructure (F2)
  brings real coverage up.
- There is no server or container image to ship until the `serve` subcommand
  lands (F4). Image build + Trivy + registry push + Watchtower deploy are added
  to this `Jenkinsfile` then, following the `jackui` pattern.

The pipeline runs entirely inside `golang:1.25`; lint and secret-scan tools are
pinned via `go run <pkg>@<version>`, so nothing extra needs installing on the
agent.

## One-time setup (already done)

- **Jenkins job** `semidx`: `WorkflowJob` reading `Jenkinsfile` from
  `https://gitea.raspberrypi.lan/lgldsilva/semidx.git` (credential
  `gitea-git-creds`), all branches, `<lightweight>false</lightweight>` (avoids
  the stale-Jenkinsfile checkout gotcha), poll trigger `H/2 * * * *` as a
  backstop.
- **Gitea webhook** → `http://jenkins.raspberrypi.lan:8091/gitea-webhook/post`
  (push events) for immediate builds; polling covers any missed webhook.

## Gotcha: root-owned workspace leftovers

Stages run as `-u root` inside the container, so files they create
(`coverage.out`, caches) are root-owned. Left behind, the next build's
`git clean` fails with "Operation not permitted". The `post { always }` step
chowns the workspace back to the jenkins uid (1000). Go caches are redirected to
`/tmp` so they never touch the workspace in the first place.
