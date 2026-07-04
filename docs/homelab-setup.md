# semidx — Homelab Setup Guide

How to install the CLI on any machine and connect it to the homelab semidx server.

## Prerequisites

- Network access to the homelab (LAN or WireGuard)
- DNS resolving `*.raspberrypi.lan` (via AdGuard or `/etc/hosts`)

## 1. Install the CLI

### Option A: Download from Gitea (recommended)

```bash
curl -fsSL https://gitea.raspberrypi.lan/lgldsilva/semidx/raw/branch/main/install.sh | sh
```

Or a specific version:

```bash
./install.sh --version v0.2.0
```

If the install script can't reach Gitea (e.g., from outside the LAN), download
the binary directly from the [Gitea releases page](https://gitea.raspberrypi.lan/lgldsilva/semidx/releases).

### Option B: Build from source

```bash
go install github.com/lgldsilva/semidx/cmd/semidx@latest
```

### Verify

```bash
semidx --version
```

## 2. Connect to the homelab server

```bash
semidx login https://semidx.raspberrypi.lan --token <your-token>
```

This saves the server URL and token to `~/.config/semidx/config.yaml`. All
subsequent commands (`search`, `repo`, `mcp`) will use this server.

> **macOS note:** if the homelab uses a self-signed/internal CA, you may need:
> ```bash
> export SEMIDX_INSECURE=1  # only if the cert is not trusted
> ```
> Alternatively, install the homelab CA cert into your system trust store.

## 3. Index a project (server-side)

The server clones and indexes git repos. Nothing is uploaded from your machine.

```bash
# Add a repo — the server clones + indexes it
semidx repo add https://github.com/user/myproject.git --name myproject

# Re-index after changes
semidx repo reindex myproject
```

Check the index status in the admin UI: https://semidx.raspberrypi.lan/admin/

## 4. Search

```bash
# Semantic search (by meaning)
semidx search --project myproject --query "how are auth tokens validated"

# Classic grep-style output
semidx sgrep --project myproject --query "retry backoff"

# JSON output for scripting
semidx sgrep --project myproject --query "error handling" --json
```

Without `--project`, semidx resolves the project enclosing your current directory
(git worktree), then falls back to searching all projects.

## 5. MCP setup (AI agents)

The CLI includes an MCP server so AI assistants can search semantically.

### Claude Code / Claude Desktop

```json
{
  "mcpServers": {
    "semidx": {
      "command": "semidx",
      "args": ["mcp"]
    }
  }
}
```

Config file locations:
- Claude Code: `.mcp.json` (project) or `~/.claude/.mcp.json` (global)
- Claude Desktop: `~/Library/Application Support/Claude/claude_desktop_config.json` (macOS)

### OpenCode

```json
{
  "mcp": {
    "semidx": {
      "type": "local",
      "command": "semidx",
      "args": ["mcp"],
      "enabled": true
    }
  }
}
```

### Auto-install for other clients

```bash
semidx mcp install --client cursor --apply
semidx mcp install --client windsurf --apply
semidx mcp install --client vscode --apply
semidx mcp install --client gemini-cli --apply
```

Use `semidx mcp install --list` to see all supported clients.

### Install agent skills

Skills teach the AI assistant when semantic search beats grep:

```bash
semidx skills install
```

## 6. Standalone mode (no server)

If you prefer to run semidx locally without the server:

```bash
# Index a directory into a local SQLite file
semidx --local index --project .

# Search without any embedding model (keyword-only)
semidx --local --keyword sgrep --project . --query "database migration"

# With local Ollama embeddings
export SEMIDX_OLLAMA_URL=http://localhost:11434
semidx --local index --project . --model bge-m3
semidx --local sgrep --project . --query "retry logic"
```

## Troubleshooting

### "semidx: 404: project not found"

You haven't indexed any projects yet. Use `semidx repo add` or index from the
admin UI.

### "certificate signed by unknown authority"

The homelab uses an internal CA. Either:
- Install the CA cert: `sudo cp homelab-ca.crt /usr/local/share/ca-certificates/ && sudo update-ca-certificates`
- Skip verification: `export SEMIDX_INSECURE=1`

### "connect: network is unreachable" from MCP

The MCP server proxies through the CLI's login session. Make sure you ran
`semidx login` first.

### Server not accessible

Check the server is running:
```bash
curl -s https://semidx.raspberrypi.lan/api/v1/projects -H "Authorization: Bearer <your-token>"
```

Expected: `{"projects":[...]}`
