# Security Policy

## Supported versions

semidx is in early productization (`v0.x`). Security fixes land on `main` and in
the latest tagged release. Older `v0.x` tags are not separately patched — track
the latest release.

| Version | Supported |
|---|---|
| `main` / latest `v0.x` tag | Yes |
| older `v0.x` tags | No |

## Reporting a vulnerability

Please report security issues privately — do **not** open a public issue for an
unpatched vulnerability.

- Email: `security@example.com` *(replace with the project's real contact)*.
- Include: affected version/commit, a description, and reproduction steps or a
  proof of concept.

We aim to acknowledge a report promptly and will coordinate a fix and disclosure
timeline with you. Please give us reasonable time to release a fix before any
public disclosure.

## Security model

semidx is designed to be self-hosted, so your code, documents and embedding
credentials stay within infrastructure you control.

### No secrets in the index

- **Privacy routing.** Files that look sensitive — `.env`, `.pem`, `.key`,
  `.conf`, `.config`, or any path containing segments like `secret`, `key`,
  `token`, `password`, `credential`, `auth`, `private`, `cert` — are **never
  sent to a cloud embedding provider**. They are embedded by a local provider if
  one can serve the model, or stored as keyword-only text (with a `NULL`
  embedding) so they remain searchable without ever leaving the machine.
- **Privacy mode.** `EMBED_PRIVACY=true` (or `--privacy`) restricts the entire
  run to local providers.
- **Your server, your data.** Search queries and file contents are sent to the
  semidx server you configured, not to any third party. Embedding provider API
  keys live only on the server (or only on your machine in standalone mode).
- **System-directory guard.** The CLI refuses to index root system directories
  (`/`, `/home`, `/etc`, `/usr`, `/var`, …) to avoid runaway scans.

### Credentials and authentication

- **Passwords** for web-UI users are hashed with **argon2id** (memory-hard KDF;
  ~64 MB / 3 passes). Only the encoded hash is stored.
- **Opaque API keys** are random `semidx_<hex>` tokens; only their SHA-256 hash
  is stored, the plaintext is shown once, and each carries a scope set
  (`read` / `write` / `admin`). Keys are revocable.
- **JWT control tokens** are HS256, signed with `SEMIDX_JWT_SECRET`. Each carries
  a unique `jti` recorded server-side, so a token can be **revoked even if it
  never expires**; verification checks signature, algorithm and expiry.
- **Web sessions** are server-side and cookie-backed: only the session token's
  hash is stored, cookies are `HttpOnly` and `Secure` (when
  `SEMIDX_COOKIE_SECURE=true`), every mutating request carries a CSRF token
  bound to the session, and login attempts are rate-limited.

### Network exposure

- In the reference deployment (`deploy/docker-compose.yml`) the **PostgreSQL
  port is not published** — the database is reachable only on the internal
  compose network. The only external surface is the authenticated HTTP API.
- Run the server behind a TLS-terminating reverse proxy and keep
  `SEMIDX_COOKIE_SECURE=true` so session cookies are only sent over HTTPS.

### Supply-chain and code-quality gates

CI enforces the following on every pull request and push to `main`
(`.gitea/workflows/ci.yml`):

- **`govulncheck`** — fails on known CVEs in dependencies or the Go stdlib that
  reach reachable code paths.
- **`gosec`** — static security analysis (SAST); documented exceptions carry an
  inline `#nosec RULE -- reason` justification.
- **`gitleaks`** — secret scanning (also run pre-commit via lefthook,
  `gitleaks protect --staged`).
- **`golangci-lint`** plus `go vet` and the race-enabled test suite.

The tagged release pipeline (`.gitea/workflows/release.yml`) additionally runs:

- **SonarQube** quality gate.
- **CycloneDX SBOM** generation, uploaded to **Dependency-Track**.
- **Trivy** image scan that fails on a fixable `CRITICAL` vulnerability before
  the image is pushed.

*(These homelab/CI integrations are gated on their secrets; they skip cleanly
where those secrets are absent.)*
