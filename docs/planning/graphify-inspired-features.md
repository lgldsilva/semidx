# Plano: Features inspiradas no Graphify

> **Handoff para outro agente/harness.** Este documento é autossuficiente: descreve
> o contexto, as decisões tomadas, e o roadmap de implementação. Leia a seção
> **Verificações feitas no código** antes de codar — algumas afirmações "óbvias"
> foram checadas e uma foi corrigida.

- **Status:** planejado, nada implementado ainda
- **Data:** 2026-07-21
- **Origem:** avaliação do repo [Graphify-Labs/graphify](https://github.com/Graphify-Labs/graphify)
  como fonte de ideias para o semidx.

---

## Contexto — por que este trabalho existe

O Graphify resolve o mesmo problema do semidx (dar contexto de código a agentes de
IA) por um caminho diferente: constrói um **grafo de conhecimento determinístico**
via tree-sitter (AST, custo zero de LLM) e usa **algoritmos de grafo**
(pathfinding, community detection, centralidade) em vez de depender só de
similaridade vetorial. Nos benchmarks dele, retrieval por grafo bate BM25 e
sistemas de memória (mem0) em recall, a ~1/10 do custo de ingestão, com **zero
créditos de LLM** para construir o índice.

O ponto-chave para o semidx: **grande parte dessa fundação já existe no nosso
código** (grafo de dependências, extração AST, BFS de grafo, filtro por worktree,
dedup content-addressed). As ideias do Graphify são, em sua maioria, **expor e
compor** o que já temos — não construir do zero. O resultado pretendido é um
conjunto de "graph primitives" no MCP que o agente compõe (`trace de A→B`,
`quem chama X`, `símbolos de um arquivo`), mais tags de confiança e, num horizonte
maior, detecção de comunidades / god nodes.

Diferenças de escopo (o que **não** vamos copiar): o Graphify é multimodal
(PDF/imagem/vídeo) e usa NetworkX/Python + Leiden. Aqui é Go, e o foco é
reaproveitar a infra existente, não trazer um runtime de grafo pesado.

---

## Verificações feitas no código (importante)

Estas foram confirmadas lendo o código atual (`main` @ commit eb52bad), não
assumidas:

1. **Grafo de dependências já existe e é persistido.**
   - Tabela `file_dependencies (project_id, source_file, target_file)` —
     migração `internal/store/migrations/00014_graph_edges.sql`.
   - `IndexStore.InsertFileDependencies(ctx, projectID, sourceFile, targets)` —
     `internal/store/store.go:137` (interface), `:1263` (PgStore).
   - `FetchGraphNeighbors(ctx, projectID) map[string][]string` e
     `FetchGraphPathsBFS(ctx, projectID, seedPaths, maxDepth)` — já usados por
     `search.Service.expandByGraph` (graph-RAG por BFS com decay 0.85, floor 0.3,
     depth default 2, guard de 100 paths).
   - Extração de imports multi-linguagem (Go/Java/TS/JS/Py/Rust/C/Ruby/C#/Markdown)
     vive em `internal/imports/` (via `si.Analyze` no indexer).

2. **Extração AST de símbolos já existe** — `internal/analyzer/analyzer.go`,
   `analyzer.Symbols(path, content) []Symbol` com `Symbol{Name,Kind,StartLine,EndLine}`.
   Tree-sitter via `github.com/odvcencio/gotreesitter`. Cobre
   .go/.java/.kt/.scala/.js/.jsx/.ts/.tsx/.py/.tf. Chunks de código já recebem
   prefixo `"[kind] name\n"` (`internal/chunker/chunker_ast.go`).

3. **Filtro por worktree já implementado** — `SearchSimilarWorktree` e
   `SearchSimilarKeywordsWorktree` (`internal/store/store.go:124-125`), e
   `search.Request.Worktree` (`internal/search/service.go:48-50`).

4. **⚠️ CORREÇÃO ao plano inicial:** a CLI **já resolve o worktree automaticamente**
   pela cwd — `internal/searchtargets/targets.go:119`
   (`one.Worktree = cwdGit.Toplevel`), com testes em `targets_test.go` e
   `targets_variants_test.go`. Portanto **não há a lacuna** que o rascunho sugeria.
   O que falta é apenas um **override explícito** `--worktree <path>` para buscar
   um checkout diferente do atual. Isso rebaixa a feature #3 de "gap" para
   "nice-to-have de baixo valor". Não priorizar.

5. **Modo sem embeddings já existe parcialmente** — `Indexer.SetKeywordOnly(true)`
   (armazena texto sem embeddings, busca por FTS) e `chunker.ChunkFileAST`.
   O `--ast-only` seria uma variante que ativa símbolos + keyword-only juntos.

6. **Padrão de registro de tool MCP** (para a feature #1):
   - `internal/mcpserver/mcpserver.go` → `New(b Backend)` chama `mcp.AddTool(...)`.
   - Tools atuais: `semantic_search`, `semantic_projects`, `semantic_reindex`,
     `semantic_status`, e `semantic_ask` (condicional, só se o backend implementa
     `AskBackend` — padrão de type-assertion a seguir para tools opcionais).
   - Backend é interface com implementações `localBackend` (local.go) e
     `clientBackend` (remote.go). Novas capacidades entram como interface
     estendida (ex.: `GraphBackend`) + registro condicional via type-assert,
     espelhando `AskBackend`.

---

## Decisões tomadas

- **D1 — Começar só pela feature #1 (graph primitives MCP).** Maior retorno pelo
  menor esforço; é o diferencial central do Graphify e reaproveita
  `FetchGraphNeighbors`/`FetchGraphPathsBFS`/`analyzer.Symbols`, que já existem e
  estão testados. Custo de indexação continua zero (AST é determinístico e já roda).
- **D2 — Confidence tags (#2) só depois de #1.** Exige migração de schema; fazer
  com `DEFAULT 'AMBIGUOUS'` para não quebrar linhas existentes.
- **D3 — Community detection (#4) é o último.** Único item com código novo real
  (algoritmo Leiden/Louvain) e o de maior risco. Só encarar depois de validar que
  os primitives (#1) são usados na prática.
- **D4 — Worktree override (#3) despriorizado** (ver verificação #4).
- **D5 — Tudo aditivo e config-gated.** Nenhum breaking change no fluxo atual.
  Tools novas registradas condicionalmente (padrão `AskBackend`), modos novos atrás
  de flag.
- **D6 — Sonar só roda em `main` (Community edition)** e o gate de cobertura ≥90%
  vale para `internal/**` e `pkg/**`. `cmd/**` é excluído do denominador. Código
  novo em `internal/graph/` precisará de testes.

---

## Roadmap

### v1 — Quick wins (aditivo, ~1 semana) ← COMEÇAR AQUI

**Feature #1: Graph primitives no MCP.**

Novas tools (nomes seguindo o prefixo `semantic_`):

| Tool | Faz | Reaproveita |
|------|-----|-------------|
| `semantic_neighbors` | vizinhos de import/export de um arquivo | `FetchGraphNeighbors` |
| `semantic_trace` | caminho de dependência entre arquivos (A→B) | `FetchGraphPathsBFS` |
| `semantic_symbols` | símbolos definidos num arquivo | `analyzer.Symbols` |

Arquivos a mexer:
- `internal/mcpserver/mcpserver.go` — registrar tools + handlers (input struct com
  tags `jsonschema`, handler `mcp.ToolHandlerFor[in, any]`, formatação via
  `textResult`/`errorResult`).
- `internal/mcpserver/local.go` — implementar métodos no `localBackend`
  (acesso direto ao store + `analyzer`).
- `internal/mcpserver/remote.go` — implementar via `pkg/client` (exige endpoints
  HTTP correspondentes no `internal/server/` **se** quisermos paridade remota;
  decidir se v1 é local-only para reduzir escopo).
- Definir a interface estendida (ex.: `GraphBackend`) e registrar as tools
  condicionalmente (type-assert, como `AskBackend`).

Notas de implementação:
- `semantic_symbols` precisa do conteúdo do arquivo. No standalone o store tem os
  chunks; avaliar reconstruir do índice vs. reler do disco. Símbolos já foram
  extraídos no index — considerar persistir símbolos (ligado à #2) para não
  reprocessar. Para v1, reler do disco (mais simples) é aceitável.
- Respeitar o guard de DoS do BFS (maxDepth clamp, limite de paths).
- MCP stdio: logs vão para stderr; stdout é do protocolo.

**Também em v1 (opcional, baixo custo):** `--ast-only` no `semidx index`
(feature #5) — `Indexer.SetASTOnly()` que liga símbolos + keyword-only, flag em
`cmd/semidx/commands.go`.

Entregável: 3 tools novas + docs. Sem migração de schema.

### v2 — Confidence tags (~2 semanas)

**Feature #2.** Adicionar a cada resultado de busca um rótulo de confiança:
- `EXTRACTED` — chunk começa com prefixo de símbolo (`[func] `, `[class] `…):
  menção explícita.
- `INFERRED` — chunk contém nome de símbolo do arquivo, mas não é a declaração.
- `AMBIGUOUS` — sem match de símbolo (co-ocorrência vetorial pura).

Mudanças:
- `store.SearchResult` ganha `Confidence` e `Symbol` (novos campos).
- Migração: colunas `confidence TEXT DEFAULT 'AMBIGUOUS'` e `symbol TEXT` nas
  tabelas `chunks_<dims>` (Postgres) e `chunks` (SQLite). Atualizar
  `EnsureChunksTable` e `InsertChunks`.
- Classificação em index-time (`indexer.indexUnit`) reaproveitando os símbolos já
  extraídos (evita recomputar). Alternativamente em search-time como fallback.
- Propagar até o MCP: incluir `confidence`/`symbol` nos formatos `structured` e
  `minimal` (`internal/mcpserver/mcpserver.go`).

Risco: migração de schema — mitigar com default e backfill preguiçoso.

### v3 — Community detection + god nodes (~3-4 semanas)

**Feature #4.** Único item com código novo significativo.
- Novo pacote `internal/graph/` — Leiden/Louvain sobre `file_dependencies`, +
  centralidade (degree/betweenness) para "god nodes" (símbolos/arquivos mais
  conectados).
- Avaliar lib Go pura (ex.: `gonum.org/v1/gonum/graph` para centralidade;
  Leiden pode exigir implementação própria) vs. implementação caseira. Registrar
  a escolha como ADR em `docs/design-decisions.md`.
- Nova tabela `file_communities (project_id, file_path, community_id, centrality)`.
- Hook assíncrono em `indexer.finalizeProject` (falhar graciosamente, não bloquear
  o index).
- Tool MCP `semantic_communities`.
- Precisa de testes (≥90% no pacote novo) e teste de performance em repo grande.

---

## Como validar (end-to-end)

Toolchain e gates (do AGENTS.md do projeto):

```sh
export GOTOOLCHAIN=go1.25.12
go build ./...
go test -race -shuffle=on ./...        # testcontainers pulam sem Docker
gofmt -l .                             # deve ser vazio
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run ./...
go run github.com/securego/gosec/v2/cmd/gosec@v2.27.1 -quiet ./...
```

Validação funcional das tools MCP novas (v1):
- Harness de integração MCP: `deploy/agentics-test/run.sh standalone`
  (ver `deploy/agentics-test/README.md`).
- Manual: indexar um repo Go pequeno (`semidx index . --local`), subir
  `semidx mcp` e chamar `semantic_neighbors`/`semantic_trace`/`semantic_symbols`
  contra arquivos conhecidos, conferindo que os edges batem com os imports reais.

Fluxo de PR (nunca commitar direto na `main`):
- Branch a partir de `main` atualizada; Conventional Commits (scope único, sem
  vírgula — ex. `feat(mcp)`), trailer `Claude-Session:`.
- Gitea não tem `gh`; abrir/mergear PR via REST API (skill `gitea-pr`).

---

## Arquivos-chave (mapa rápido)

| Camada | Arquivo | Papel |
|--------|---------|-------|
| MCP | `internal/mcpserver/mcpserver.go` | registro de tools, handlers, formatação |
| MCP | `internal/mcpserver/local.go` / `remote.go` | backends standalone / HTTP |
| Search | `internal/search/service.go` | `Search`, `expandByGraph`, `Request/Response` |
| Store | `internal/store/store.go` | interface `IndexStore`, `SearchResult`, grafo |
| Store | `internal/store/migrations/*.sql` | schema (14 = graph edges, 7 = worktree) |
| Index | `internal/indexing/indexer.go` | pipeline; `SetKeywordOnly`, `finalizeProject` |
| AST | `internal/analyzer/analyzer.go` | `Symbols()` via tree-sitter |
| Imports | `internal/imports/` | extração de deps por linguagem |
| Chunk | `internal/chunker/chunker_ast.go` | chunking AST-aware, prefixo `[kind] name` |
| CLI | `cmd/semidx/commands.go` | flags de `search`/`index`; resolução de worktree |

---

## Referências

- Graphify: <https://github.com/Graphify-Labs/graphify>
  (ARCHITECTURE.md, BENCHMARKS.md na branch `v8`)
- Plano detalhado com exemplos de código (gerado nesta sessão, fora do repo):
  `~/.claude/plans/snuggly-frolicking-swan-agent-a650d87a396acf73d.md`
- Docs internos relevantes: `docs/architecture.md`, `docs/design-decisions.md`
  (ADRs), `AGENTS.md` (gates/convenções).
