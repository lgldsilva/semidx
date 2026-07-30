# Auditoria de adoção e indicadores do semidx

**Data:** 2026-07-30  
**Escopo:** todos os harnesses locais detectáveis + indicadores completos
(uso, cobertura de integração, frequência, sucesso/erros, latência, relevância).  
**Modo:** predominantemente somente leitura; sem reindexação e sem alteração de
configs de clientes.

## Veredito

O semidx está **amplamente instalado** nos harnesses principais do operador, mas
**não está amplamente utilizado**.

- Instalação MCP: forte (Cursor, Codex, OpenCode, Crush, Gemini/Antigravity, Pi,
  Kimi/MiMo).
- Uso real: baixo (~40 buscas / 30 dias no backend remoto), concentrado no próprio
  projeto `semidx`, com atribuição fraca (`unknown` ≈ 67%) e taxa de erro elevada
  (10%).
- Harness de integração (`deploy/agentics-test`) cobre bem a fiação MCP, mas
  **não está no CI** apesar de `docs/requirements.md` marcar REQ-MCP-05 como done.

Separar sempre: **instalado ≠ conectado ≠ chamado ≠ útil**.

---

## 1. Cobertura dos harnesses locais

Inventário feito na máquina do operador (user-level). O workspace do repositório
não define MCP próprio; a configuração vem do home do usuário.

| Harness | MCP `semidx` | Skills semidx | Evidência de uso | Classificação |
|---|---|---|---|---|
| Cursor | configurado (`~/.cursor/mcp.json`) | `semantic-search` | tools MCP presentes; transcripts ruidosos neste repo | configurado / uso inconclusivo |
| Codex | configurado (`~/.codex/config.toml`) | `semantic-search` | logs: ~29× `semantic_search`; conexão MCP | usado recentemente |
| OpenCode | configurado (`~/.config/opencode/opencode.json`) | — | tool calls tipados: search 24, projects 14, status 1, reindex 1 | usado recentemente |
| Crush | configurado | — | só config | configurado |
| Gemini CLI | configurado | skill `semidx` | só config / schemas | configurado |
| Antigravity | espelhado no Gemini | — | só config | configurado |
| Pi | configurado | — | só config | configurado |
| Kimi Code | configurado | — | só config | configurado |
| MiMo | importado do OpenCode | — | só config | configurado |
| Claude Code | **ausente** (só `ai-memory`) | `semantic-search` instalada | `skillUsage` não mostra a skill | skill sem MCP |
| Claude Desktop | ausente | — | — | ausente |
| GitHub Copilot CLI | ausente | — | — | ausente |
| VS Code | ausente | — | — | ausente |
| Windsurf | não instalado na máquina | — | — | ausente |
| Continue / Qwen / cagent | sem MCP semidx | — | — | ausente |

### Skills bundled vs instaladas

Bundled (6): `semantic-search`, `semantic-graph`, `code-intel`,
`impact-before-refactor`, `auto-index`, `workspace-agent`.

Na prática, só `semantic-search` aparece de forma consistente nos dirs de skills
dos clientes. As outras cinco estão no produto, mas não na adoção local.

### Matriz produto (suporte) vs harness agentics

`internal/mcpinstall` suporta 15 clientes. O harness
`deploy/agentics-test/lib.sh` instala/asserta:

- JSON apply: claude-code, claude-desktop, cursor, windsurf, gemini-cli,
  antigravity, copilot, vscode, opencode, crush
- TOML: codex
- print-only: cagent
- **Não harnessa:** `pi`, `kimi`, `mimo`

Skills no harness: só `semantic-search` e `workspace-agent` (2/6).

`semidx doctor` lista todos os clients MCP, mas para skills só olha
`~/.claude`, `~/.agents`, `~/.cursor` e dirs de projeto — não cobre Codex,
OpenCode, Kimi, Pi, Crush, MiMo, Windsurf, Antigravity.

---

## 2. Indicadores de uso e confiabilidade

### Snapshot remoto (30 dias, backend homelab)

Fonte: API de analytics do servidor remoto durante a sessão de planejamento
(rota observada no deploy: `GET /api/v1/usage`; no working tree a rota de produto
é `GET /api/v1/search-usage`). Valores arredondados a partir do agregado.

| Indicador | Valor |
|---|---|
| Total de buscas | **40** (~1,3/dia) |
| Outcome ok | **50%** |
| Outcome empty | **8%** |
| Outcome fallback | **32%** |
| Outcome error | **10%** (`elevated_error_rate`) |
| Source unknown | **27** (~67%) |
| Source mcp | **7** (~18%) |
| Source cli | **6** (~15%) |
| Projetos indexados ready | **43** (modelo `bge-m3`) |
| Top projeto | `semidx` = 30; depois `ai-memory-setup` 5, `ai-launcher` 4, `jackui` 1 |

### Proxy client-side (OpenCode DB)

Chamadas tipadas `semidx_*` (não confundir com menções textuais no repo):

| Tool | Count |
|---|---|
| `semidx_semantic_search` | 24 |
| `semidx_semantic_projects` | 14 |
| `semidx_semantic_status` | 1 |
| `semidx_semantic_reindex` | 1 |

Isso confirma uso MCP real em OpenCode, mas em volume baixo e não prova valor
de retrieval (só execução da ferramenta).

### Latência

- Eventos gravam `latency_ms`, mas o relatório agregado (`semidx usage` /
  `usage.Report`) **não** publica p50/p95.
- Prometheus no working tree tem `semidx_search_duration_seconds` e
  `semidx_search_total{project,source,outcome}`; o deploy observado ainda era
  incompleto vs o tree (séries de search ausentes ou parciais).
- Baseline keyword de lab: p50 ≈ 28 ms, p95 ≈ 33 ms, p99 ≈ 37 ms
  (`testdata/eval/baselines/96d1c46-keyword.json`) — latência de lab, não de
  produção.

### Saúde observada nesta sessão (cloud agent)

- MCP `user-semidx` falhou TLS: certificado válido para
  `*.internal.lgldsilva.com.br`, host configurado `semidx.raspberrypi.lan`.
- Servidor inacessível a partir do ambiente cloud (egress livre, mas host
  privado).
- Binário PATH da máquina local estava atrás do working tree (`doctor` /
  `usage` ausentes no snapshot anterior).

---

## 3. Qualidade e harness de integração

### Agentics harness

Prova: escrita de configs, merge/backup, skills parciais, handshake MCP stdio,
`semantic_projects` / `semantic_search` com fixture, Claude nativo best-effort.

Limitações:

| Item | Estado |
|---|---|
| `all` inclui `server`? | **Não** — só `standalone` + `keyword` |
| Clientes pi/kimi/mimo | não exercitados |
| Transporte HTTP MCP | não coberto |
| `tool-test.mjs` | `initialize` + `tools/list`; não chama tools extras |
| Real agent call | só Claude Code; SKIP sem `ANTHROPIC_API_KEY` |
| Persistência de métricas | stdout + exit code; sem JSON histórico |
| CI | **não gated** em `.github/workflows/ci.yml` |

Inconsistência documental: `docs/requirements.md` REQ-MCP-05 marca o gate
keyword como **done**, mas o workflow atual não executa
`deploy/agentics-test/run.sh`.

### Relevância (lab)

Gold set: 50 queries (`testdata/eval/semidx-retrieval-v1.json`).  
Baseline keyword `96d1c46`:

| Métrica | Valor |
|---|---|
| nDCG@10 | 0.058 |
| MRR | 0.038 |
| Precision@5 | 0.021 |
| Recall@10 | 0.12 |
| failed / fallback | 0 / 0 |

Documentado como referência de medição, **não** alvo de qualidade. Thresholds de
regressão em `testdata/eval/thresholds/retrieval-default.json` (queda máx. 0.01
em nDCG/MRR/P@5/R@10; +50 ms p95). `semidx bench` / compare **não** rodam no CI.

Proxy de relevância em produção: só `ok` / `empty` / `fallback` / `hit_count`.
Sem grades de relevância online.

### Gates de qualidade que existem

CI: build, `go test -race`, coverage ≥90% em `internal/**`+`pkg/**`, gofmt, vet,
golangci-lint, gitleaks, govulncheck, gosec, Trivy.  
Não medem adoção de agentes nem relevância de retrieval em produção.

---

## 4. Funil de adoção (leitura)

```text
Instalação MCP ampla ──► Handshake/config OK (parcial)
        │
        ▼
Chamadas MCP reais (OpenCode/Codex, dezenas) ──► Buscas atribuídas mcp=7/40
        │
        ▼
Sucesso útil?  ok=50%, fallback=32%, error=10% ──► Valor concentrado no próprio semidx
```

Conclusão do funil: a superfície de instalação está madura; o gargalo é
**ativação + atribuição + confiabilidade do path remoto**, não falta de
clientes suportados no código.

---

## 5. Lacunas metodológicas

1. Config presente ≠ uso (doctor/install são inventário estático).
2. Transcripts do projeto `semidx` têm falso positivo massivo (código/docs).
3. `source=unknown` domina — clientes sem `X-Semidx-Client`.
4. Latência e hit_count gravados, não agregados no report público.
5. Fallback alto mistura keyword intencional e embed down.
6. Version skew CLI/server/API (`/usage` vs `/search-usage`).
7. Ambiente cloud desta auditoria não alcança o homelab; números de uso vêm do
   snapshot da sessão de planejamento na máquina local.
8. Skills e `mcp install` não geram eventos de analytics.

---

## 6. Recomendações priorizadas

| Prioridade | Ação | Por quê |
|---|---|---|
| P0 | Alinhar hostname TLS (`semidx.internal…` vs `raspberrypi.lan`) | MCP quebrado em paths com verificação de certificado |
| P0 | Atualizar CLI local ao working tree (`doctor`, `usage`) | Inventário e analytics inacessíveis no PATH antigo |
| P0 | Corrigir atribuição `X-Semidx-Client` / source desconhecido | 67% unknown impede medir adoção por canal |
| P1 | Ligar MCP no Claude Code (doctor já alerta) | Skill instalada sem canal de busca |
| P1 | Adicionar `deploy/agentics-test/run.sh keyword` ao CI (ou corrigir REQ-MCP-05) | Gate documentado como done, ausente |
| P1 | Investigar error 10% + fallback 32% | Confiabilidade percepida baixa |
| P2 | Estender harness a `pi`/`kimi`/`mimo` e assertar as 6 skills | Matriz de instalação ≠ matriz de prova |
| P2 | Agregar p50/p95 de `latency_ms` em `semidx usage` | Campo já existe nos eventos |
| P2 | Instalar skills restantes nos targets ativos | Produto tem 6; local tem ~1 |
| P3 | Nightly `semidx bench compare` contra baseline | Relevância só existe como lab manual |

---

## Fontes

- `deploy/agentics-test/{README.md,run.sh,lib.sh,mcp-probe,tool-test.mjs}`
- `internal/mcpinstall/mcpinstall.go`
- `cmd/semidx/doctor.go`, `cmd/semidx/usage.go`, `cmd/semidx/skills_targets.go`
- `internal/usage/{usage.go,report.go}`, `internal/store/usage.go`, `internal/localstore/usage.go`
- `internal/server/server.go` (Prometheus)
- `internal/mcpserver` (`semantic_usage`)
- `testdata/eval/{semidx-retrieval-v1.json,baselines/96d1c46-keyword.json,thresholds/retrieval-default.json}`
- `docs/usage.md`, `docs/evaluation.md`, `docs/requirements.md`
- `.github/workflows/ci.yml`
- Inventário local de configs/logs (Cursor, Codex, OpenCode, …) na sessão de planejamento
- Snapshot remoto de analytics (30d) na mesma sessão
