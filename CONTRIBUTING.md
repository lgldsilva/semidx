# Contributing to semidx

Thanks for your interest! This project is in early productization (v0.x) — the
API and internals are still moving. Small, focused PRs are the easiest to review.

## Development setup

- Go 1.25+
- Docker (for the pgvector database and integration tests)

```bash
docker compose up -d           # PostgreSQL + pgvector on port 55432
go build ./...
go test ./...
```

## Ground rules

- **Never commit to `main`** — open a PR from a feature branch.
- **Conventional Commits** for every commit and PR title
  (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`, `chore:`).
- **No secrets in the tree** — `gitleaks` runs on every commit via lefthook
  (`lefthook install` after cloning) and again in CI.
- New behavior needs tests. Bug fixes need a regression test.

## License

By contributing, you agree that your contributions will be licensed under the
Apache License 2.0.
