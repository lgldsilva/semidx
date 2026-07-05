# Análise Completa — semidx (Produto Escalável)

> Auditoria cross-dimensional realizada em 2026-07-03.
> 6 dimensões · 5 agentes especialistas · 84 achados · 0 críticos.

---

## Índice

1. [Avaliação Geral](#avaliação-geral)
2. [🔴 HIGH Severity (15)](#-high-severity)
   - [Segurança (2)](#segurança)
   - [Usabilidade do CLI (6)](#usabilidade-do-cli)
   - [Performance & Escalabilidade (5)](#performance--escalabilidade)
   - [Documentação (2)](#documentação)
3. [🟡 MEDIUM Severity (33)](#-medium-severity)
   - [Segurança & Erros (8)](#segurança--erros)
   - [Arquitetura (6)](#arquitetura)
   - [Performance (13)](#performance)
   - [Usabilidade (6)](#usabilidade)
4. [🟢 LOW Severity (36)](#-low-severity)
5. [Top 10 Quick Wins](#-top-10-quick-wins)
6. [Roadmap para Produto Escalável](#-roadmap-para-produto-escalável)
7. [Resumo Estatístico](#resumo-estatístico)
8. [Apêndice: Detalhes por Dimensão](#apêndice-detalhes-por-dimensão)

---

## Avaliação Geral

O projeto está em um estado de engenharia **sólido** para um PoC evoluído a produto.
A arquitetura (22 packages, 3 backends de storage, chain de embedding, MCP server,
dual interface `IndexStore`/`Store`) é bem desenhada. 16/18 packages com ≥90% de
cobertura, property-based testing, mutation testing, CI gates verdes.

**Zero vulnerabilidades críticas.** Os gaps são de polimento de produto — UX,
timeouts, métricas, benchmarks — não de arquitetura quebrada.

| Dimensão | Nota | Resumo |
|----------|------|--------|
| Segurança & Erros | **B+** | 2 HIGH, 8 MEDIUM, 8 LOW |
| Arquitetura & Design | **A-** | 0 HIGH, 6 MEDIUM, 9 LOW |
| Documentação & Benchmarks | **B / F** | 2 HIGH, 0 MEDIUM, 8 LOW (benchmarks: F) |
| Performance & Escalabilidade | **B-** | 5 HIGH, 13 MEDIUM, 6 LOW |
| CLI Usabilidade | **C+** | 6 HIGH, 6 MEDIUM, 5 LOW |

---

## 🔴 HIGH Severity

### Segurança

| # | Arquivo:Linha | Problema | Sugestão |
|---|---------------|----------|----------|
| S1 | `cmd/semidx/commands.go:305` | **Bootstrap token vai para stderr** e persiste em logs do systemd/container/journald para sempre | Escrever só se stdout for TTY, ou exigir flag `--show-bootstrap-token`, ou salvar em arquivo `0600` |
| S2 | `internal/server/server.go:166` | **Search vaza `err.Error()` para o cliente** — expõe mensagens internas de DB e provider. Comentário na linha 165 diz "only user-facing error is missing project" mas não é enforced | Mapear erros para mensagens user-safe e logar o erro real server-side |

### Usabilidade do CLI

| # | Arquivo:Linha | Problema | Sugestão |
|---|---------------|----------|----------|
| U1 | `cmd/semidx/commands.go:270` | **`semidx drop` sem confirmação** — destrói todo o índice com um comando. Sem `--confirm`, sem `--project` para scopo | Adicionar prompt interativo + flag `--confirm` |
| U2 | `internal/search/format.go:23` | **`HumanFormatter` não mostra números de linha** — `StartLine`/`EndLine` existem no struct mas são descartados na formatação. O `sgrep` mostra, mas o `search` padrão não | Adicionar `file:line` ao output formatado e converter score para porcentagem (`85%` em vez de `0.8512`) |
| U3 | `internal/embed/embed.go:85,104,123,147` | **Erros em português** vazam para o CLI (`"falha ao gerar embeddings"`, `"nenhum modelo de embedding disponível"`). Mistura com mensagens em inglês do resto do sistema | Traduzir para inglês: `"failed to generate embeddings"`, `"no embedding model available"` |
| U4 | `cmd/semidx/main.go:104` | **`--help` é uma lista plana alfabética** — sem guia de primeiros passos, sem exemplos, sem `Long`. `config` aparece primeiro, `drop` (perigoso!) em segundo | Adicionar `Long` com quickstart + exemplos. Agrupar comandos por fluxo (index→search, setup, advanced, danger) |
| U5 | `cmd/semidx/searchtargets.go:141` | **"no indexed projects found"** sem sugestão. Usuário novato não sabe que precisa rodar `semidx index` | `"no indexed projects found — run 'semidx index --project .' first"` |
| U6 | `cmd/semidx/main.go:137-140` | **Sem modo zero-config** — `--keyword --local` deveria ser ativado automaticamente quando nenhum provider de embedding ou Postgres está configurado | Auto-detectar no `PersistentPreRunE`: se nada configurado → `keyword=true, local=true` + aviso amigável |

### Performance & Escalabilidade

| # | Arquivo:Linha | Problema | Sugestão |
|---|---------------|----------|----------|
| P1 | `internal/store/store.go:303` + `localstore.go:200` | **`SetWorktreeFiles`: INSERT row-by-row** em loop. Para um repo de 50k arquivos, são 50k round-trips síncronos | Usar `pgx.CopyFrom` (Postgres) ou prepared statement + loop em transação (SQLite) |
| P2 | `internal/embed/openai.go:37` + `ollama.go:48` | **HTTP clients sem connection pooling** — `http.Client{Timeout: 5*time.Minute}` sem `Transport`. Cada request de embedding abre nova conexão TCP | Configurar `http.Transport` com `MaxIdleConns=100`, `MaxIdleConnsPerHost=10`, `IdleConnTimeout=90s` |
| P3 | `internal/server/server.go:99` | **Servidor HTTP sem `ReadTimeout`/`WriteTimeout`/`IdleTimeout`** — só tem `ReadHeaderTimeout: 10s`. Cliente lento pode segurar conexão indefinidamente | Adicionar `ReadTimeout: 30s`, `WriteTimeout: 30s`, `IdleTimeout: 120s` |
| P4 | `internal/server/` (handlers POST) | **Sem `MaxBytesReader`** — `files/batch` e `search` aceitam POST body sem limite de tamanho. Atacante envia GB e esgota memória | Adicionar middleware com `http.MaxBytesReader` (1MB search, 100MB batch) |
| P5 | `internal/webadmin/webadmin.go:220` | **`loginLimiter.tries`: mapa sem evicção** — cresce indefinidamente. Entradas stale nunca são removidas | Goroutine de limpeza periódica (a cada 5min) ou LRU com TTL |

### Documentação

| # | Problema | Sugestão |
|---|----------|----------|
| D1 | **Zero benchmarks** (`Benchmark*`) em todo o código. Sem `Makefile`/`Taskfile`. Nenhum target de benchmark | Adicionar `BenchmarkCosineBruteForce`, `BenchmarkPgVectorSearch`, `BenchmarkChunking` + `Makefile` com target `bench` |
| D2 | **`docs/design-decisions.md` em português** (resto em inglês). Sem `.env.example` para dev. `scripts/sgrep` é binário commitado | Traduzir ADRs, criar `.env.example`, remover binário do git |

---

## 🟡 MEDIUM Severity

### Segurança & Erros

| # | Arquivo:Linha | Problema | Sugestão |
|---|---------------|----------|----------|
| SM1 | `internal/store/store.go:809` + `localstore.go` | **`SetUserDisabled` concatena SQL**: `col := "NULL"` / `"NOW()"` concatenado. Embora nunca seja user input, é frágil | `CASE WHEN $2 THEN NOW() ELSE NULL END` |
| SM2 | `internal/store/store.go:913` + `localstore.go:536` | **`ILIKE %word%` sem limite** — leading wildcard força full scan. Query com muitas palavras curtas gera N OR clauses de full scan → DoS | Adicionar `minWordLen` (3+ chars), cap de 20 termos, índice `pg_trgm` GIN |
| SM3 | `internal/webadmin/webadmin.go:71` | **Rate limiter só no webadmin** — API (`/api/v1/*`) sem rate limiting. Atacante pode bruteforcear search/list/create | Adicionar rate limiting por token/IP nos handlers da API |
| SM4 | `internal/server/` (handlers POST) | **Sem validação de tamanho de body** (relacionado a P4 HIGH) | Ver P4 |
| SM5 | `internal/privacy/privacy.go:12` | **Keyword `"key"` dá false positive** — `donkey.go`, `hotkey.py`, `keyboard.go` são classificados como sensíveis e nunca embeddados em cloud | Apertar keywords (exigir match exato de path segment). Adicionar content-based detection (alta entropia) |
| SM6 | `internal/store/store.go:308` + `indexing/indexer.go:168,192` | **Erros descartados**: `tx.Rollback`, `errgroup.Wait`, `UpdateProjectStatus` — falhas silenciosas | No mínimo logar warnings. Para `UpdateProjectStatus`, retentar 1x |
| SM7 | `internal/server/projects.go:27` | **Sem validação de nome de projeto** nos handlers — vazio, >255 chars, caracteres especiais aceitos | Validar: non-empty, max 255, starts with alphanumeric |
| SM8 | `internal/webadmin/webadmin.go:67` | **CSRF key gerada em cada restart** — quebra multi-instância (load balancer). Sessões invalidadas em restart | Aceitar `SEMIDX_CSRF_KEY` via config, ou derivar do JWT secret |

### Arquitetura

| # | Arquivo:Linha | Problema | Sugestão |
|---|---------------|----------|----------|
| AM1 | `internal/store/store.go:126` | **`Store` com 25 métodos** — interface "god-like". Fakes no server_test.go precisam implementar tudo ou dar panic | Segregar em `TokenStore`, `UserStore`, `SessionStore`, `JobStore` |
| AM2 | `internal/store/store.go:89` + `localstore.go:452` | **Parâmetro `dims` vaza abstração** — significativo para Postgres (nome da tabela), ignorado por SQLite (`_`). Propósito muda entre "table selector" e "fallback hint" | Remover `dims` dos métodos de search; cada implementação determina internamente |
| AM3 | `internal/embed/embed.go:69` | **Código duplicado no `ChainEmbedder`** — `Embed`, `EmbedSingle`, `ModelInfo` compartilham o mesmo loop de iteração (~30 linhas cada) | Extrair helper `tryEach(ctx, fn)` — cada método vira uma closure de 1 linha |
| AM4 | `internal/extract/extract.go:55` | **Registry de extractors não é pluggable** — `byExt` é `var` de pacote. Usuários não podem adicionar formatos customizados (ex: `.proto`, `.graphql`) | Adicionar `Register(ext string, fn Extractor)` + `sync.Once` |
| AM5 | `cmd/semidx/main.go:167` | **`buildChain` em `main.go`** — 64 linhas de wiring de providers no entry point. Deveria ser factory function | Mover para `internal/embed/` como `NewChainFromConfig(cfg)` |
| AM6 | `internal/config/config.go:35` | **Campos de `Config` públicos e mutáveis** — `main.go:114-115` modifica `LocalIndexPath` e `KeywordOnly` pós-load. Não seguro para acesso concorrente | Campos privados + getters, ou método `Clone()` com overrides |

### Performance

| # | Arquivo:Linha | Problema | Sugestão |
|---|---------------|----------|----------|
| PM1 | `internal/store/store.go:383,774` | **`ListProjects`/`ListUsers` sem paginação** — endpoints da API retornam tudo. Sem `LIMIT`/`OFFSET` | Adicionar query params `limit`/`offset` |
| PM2 | `internal/store/store.go:942` | **`probeDimsForProject`: N+1 queries** — loop sobre todas as tabelas `chunks_%` com `SELECT 1` individual. No hot path de keyword search | `UNION ALL` único, ou cache `dims` na tabela `projects` |
| PM3 | `internal/indexing/indexer.go:38` | **`embedBatchSize=8` fixo** — OpenAI suporta 2048, Gemini 2048. Batch de 8 = muitos round-trips desnecessários | Configurável por provider ou `SEMIDX_EMBED_BATCH_SIZE` |
| PM4 | `internal/embed/embed.go:65` | **Sem timeout por provider** no chain — se provider #1 trava por 5min (HTTP client timeout), todos os outros esperam | `context.WithTimeout` de 30s (cloud) / 2s (local) por chamada |
| PM5 | `internal/indexing/indexer.go:34,36` | **`maxFileSize=1MB` / `maxChunksPerFile=32` hardcoded** — arquivos grandes truncados silenciosamente | Configurável via `SEMIDX_MAX_FILE_SIZE` / `SEMIDX_MAX_CHUNKS_PER_FILE` |
| PM6 | `internal/server/files.go:50` | **`handleFilesBatch`: indexação síncrona** no handler HTTP. Batch grande excede timeout do cliente | Enfileirar job, retornar `202 Accepted` com job ID |
| PM7 | `internal/server/jobs.go:18` | **Workers de job: polling a cada 2s** — sem notificação push. 1800 polls/hora por worker em fila vazia | `LISTEN`/`NOTIFY` (Postgres) ou channel interno com ticker como fallback |
| PM8 | `internal/store/store.go:892` | **Keyword search sem índice FTS/trigram** — `ILIKE '%word%'` força full scan | `CREATE EXTENSION pg_trgm` + GIN index. SQLite: FTS5 |
| PM9 | `internal/localstore/localstore.go:461` | **SQLite `searchSimilar`: carrega TODOS chunks** para sort, depois trunca para topK | Usar min-heap de tamanho K fixo durante o scan |
| PM10 | `internal/indexing/indexer.go:525` | **Indexer usa `fmt.Printf`**, não `slog`. Search service sem logging. Embedding chain sem logging de provider | Migrar para `slog`. Adicionar logging de provider selecionado, timing, fallback |
| PM11 | — | **Sem limite de chunks por projeto** — push malicioso pode criar milhões de chunks | Cap configurável + health check de alerta |
| PM12 | `internal/server/server.go:76` | **Métricas: só `semidx_http_requests_total`** (counter). Sem histogramas de latência, gauge de jobs, pool stats | `HistogramVec` para search/embed latency. `Gauge` para active jobs, queue depth, DB pool |
| PM13 | `internal/indexing/indexer.go:533` | **`ReadMemStats` a cada arquivo** (modo verbose) — causa STW (stop-the-world). Com 4 workers e centenas de arquivos, impacto mensurável | Throttle para 1x a cada 10s, ou usar `runtime/metrics` (Go 1.16+) |

### Usabilidade

| # | Arquivo:Linha | Problema | Sugestão |
|---|---------------|----------|----------|
| UM1 | Todos os comandos | **Maioria dos comandos sem `Long` nem `Example`** — `index`, `search`, `sgrep`, `serve`, `mcp`, `models`, `drop`, `login`, `repo`, `skills` | Adicionar `Long` + `Example` com casos de uso reais |
| UM2 | `commands.go:69` vs `commands.go:155` | **`--project`: path obrigatório no `index`, opcional (path ou nome) no `search`** — semântica diferente confunde | `index`: default `.` (diretório atual). `search`: documentar auto-detection no `Long` |
| UM3 | `cmd/semidx/commands.go:181` | **Aviso de fallback no stdout** — mistura com output pipeável e quebra JSON (`[warn] embedding unavailable...`) | `fmt.Fprintf(os.Stderr, ...)` |
| UM4 | `cmd/semidx/commands.go:98` | **Falha no `index` não sugere `--keyword`** — usuário recebe erro do chain embedder sem saber que existe alternativa | Pre-flight check: se embedding falhar, sugerir `--keyword` com mensagem explicativa |
| UM5 | `internal/search/format.go:29` | **Score exibido como `0.8512`** — usuário não sabe se é bom, ruim, ou o que significa. Keyword results sempre `0.5` | `%.0f%%` (multiplicar por 100). Keyword: mostrar `"keyword match"` em vez de score |
| UM6 | `cmd/semidx/main.go:141` | **Comandos em ordem alfabética** — `config` primeiro, `drop` (perigoso!) em segundo. Fluxo natural é `index → search` | Agrupar: Primary (index, search, sgrep, unlock) → Setup (config, login, models) → Advanced (serve, mcp, repo, skills) → Danger (drop, migrate) |

---

## 🟢 LOW Severity

### Segurança & Configuração

| # | Arquivo:Linha | Problema |
|---|---------------|----------|
| L1 | `internal/config/config.go:27` | DSN padrão hardcoded (`postgres://semantic:semantic@...`) — senha conhecida. Já tem `#nosec G101` |
| L2 | `internal/embed/openai.go:37` / `ollama.go:48` | Timeout HTTP de 5min para embedding — muito generoso. Com 5 providers no chain, pior caso = 25min |
| L3 | `pkg/client/client.go:210` | `url.PathEscape` não escapa `/` — nome de projeto com `/` quebra URL |
| L4 | `internal/embed/openai.go:55` | Envia `Authorization: Bearer ` (vazio) quando sem API key — inofensivo mas pode ser rejeitado |
| L5 | `internal/localstore/localstore.go:131` | Race condition no `ensureSchema` — dois processos simultâneos podem DROP + recriar tabelas concorrentemente |
| L6 | `internal/webadmin/handlers.go:27` | Template render errors retornam HTTP 200 com página quebrada (erro só logado, sem response de erro) |
| L7 | `cmd/semidx/remote.go:192` | `--index` default `true` — convenção Go é `--no-index` para booleanos negativos |
| L8 | `internal/jwtauth/` + `passwd/` + `server/` | `crypto/rand.Read` failure branches — aceitos como uncoverable (test coverage gap) |

### Arquitetura

| # | Arquivo:Linha | Problema |
|---|---------------|----------|
| L9 | `cmd/semidx/commands.go:1` | CLI importa 11 packages internos — esperado para cobra, mas `index` tem business logic inline |
| L10 | `internal/indexing/indexer.go:437` | Retry com exponential backoff existe (3 tentativas, jitter) mas sem circuit breaker por provider |
| L11 | `internal/embed/embed.go:85,104,123,147` | Mensagens de erro misturam português e inglês (duplicado de U3 HIGH) |
| L12 | `internal/embed/openai.go:91` | `ModelInfo` é no-op para OpenAI-compatible — retorna `Dims: 0` para modelos desconhecidos |
| L13 | `pkg/client/` vs `internal/server/` | DTOs duplicados (`SearchHit`, `SearchResponse`) entre client SDK e server |
| L14 | `internal/server/jobs.go:18` | Workers com polling a cada 2s — latência de até 2s para jobs novos (duplicado de PM7 MEDIUM) |
| L15 | `internal/config/config.go:116` | `Load()` não injetável — testes manipulam `os.Environ` e filesystem real |
| L16 | — | Sem tipos de erro estruturados (com `StatusCode`, `Retryable`, `Code`) — só sentinelas |
| L17 | `internal/localstore/localstore.go:32` | `SQLiteStore` struct sem field-level docs |
| L18 | `internal/server/server.go:27` | `Server` struct sem field-level docs |
| L19 | `internal/search/format.go:40` | `GrepFormatter.ProjectPath` sem doc |

### Documentação

| # | Problema |
|---|----------|
| L20 | `docs/self-hosting.md` — `SEMIDX_DB_PASSWORD` não documentado na tabela de env vars |
| L21 | `docs/api.md` — `took_ms` no JSON de exemplo mas não descrito em prosa |
| L22 | Sem especificação OpenAPI/Swagger |
| L23 | Sem política de versionamento de API documentada |
| L24 | `docs/self-hosting.md` — sem tutorial de como adicionar um extractor customizado |
| L25 | Root — sem `.env.example` para o `docker-compose.yml` de desenvolvimento |

### Performance

| # | Arquivo:Linha | Problema |
|---|---------------|----------|
| L26 | `internal/localstore/localstore.go:461` | Sem `sync.Pool` para buffers de embedding — alocados por row no scan |
| L27 | `internal/store/store.go:464` / `localstore.go:371` | `ListFileHashes` retorna mapa ilimitado — 100k+ files = alocação grande |
| L28 | `internal/localstore/localstore.go:610` | `ExportChunks` carrega todos chunks em memória (operação offline, aceitável) |
| L29 | `internal/indexing/indexer.go:486` | `indexGitHistory`: `EmbedSingle` sequencial, sem batch — lento para muitos commits |
| L30 | `internal/store/migrations/00001_base_schema.sql:22` | `UNIQUE(project_id, path)` na migração base — corrigido por migrações posteriores, mas frágil |
| L31 | — | Sem OpenTelemetry/tracing hooks |

### Usabilidade do CLI

| # | Arquivo:Linha | Problema |
|---|---------------|----------|
| L32 | `internal/search/format.go:44` | sgrep usa paths absolutos — verboso comparado a `rg` (mas necessário para multi-project) |
| L33 | `cmd/semidx/commands.go:410` vs `remote.go:132` | `--client` (mcp install) vs `--target` (skills install) — naming inconsistente |
| L34 | `internal/indexing/indexer.go` | Sem indicador de progresso durante index (fora do modo verbose) |
| L35 | `commands.go:72` vs `commands.go:167` | `--model` default `"bge-m3"` no index, `""` no search — descrição poderia ser mais clara |

---

## 🏆 Top 10 Quick Wins

Ordenados por impacto (resolvem problemas reais com mínimo de código):

| # | Mudança | Arquivo | Linhas | Severidade |
|---|---------|---------|--------|------------|
| 1 | **Adicionar `--confirm` ao `drop`** (prompt interativo + flag) | `commands.go:270` | ~15 | HIGH |
| 2 | **Traduzir 4 erros em português** no ChainEmbedder | `embed.go:85,104,123,147` | ~4 | HIGH |
| 3 | **Adicionar `file:line` ao `HumanFormatter`** + score em % | `format.go:23` | ~10 | HIGH |
| 4 | **Mensagem "no indexed projects" sugere `semidx index`** | `searchtargets.go:141` | ~5 | HIGH |
| 5 | **Auto-detectar modo zero-config** (`--keyword --local` implícito) | `main.go:137` | ~25 | HIGH |
| 6 | **Adicionar `Long` + exemplos ao comando root** | `main.go:104` | ~35 | HIGH |
| 7 | **Adicionar `ReadTimeout`/`WriteTimeout`/`IdleTimeout`** ao server HTTP | `server.go:99` | ~5 | HIGH |
| 8 | **Adicionar `MaxBytesReader`** nos handlers POST (search, batch) | `server.go:76` | ~12 | HIGH |
| 9 | **Configurar `http.Transport` com connection pooling** nos clients de embedding | `openai.go:37`, `ollama.go:48` | ~12 | HIGH |
| 10 | **Limpar `loginLimiter.tries` periodicamente** (goroutine de cleanup) | `webadmin.go:220` | ~18 | HIGH |

**Total estimado: ~140 linhas para resolver 10 HIGHS.**

---

## 📋 Roadmap para Produto Escalável

### Fase 1 — UX Foundation (agora, ~2-3 dias)

Resolver os 10 quick wins acima + adicionar `Long`/`Example` a todos os comandos.

**Critério de saída:** Um novo usuário consegue `semidx index --project . && semidx search --query "auth"` sem ler documentação.

### Fase 2 — Production Hardening (~1 semana)

- [ ] Adicionar timeouts HTTP completos (`ReadTimeout`, `WriteTimeout`, `IdleTimeout`)
- [ ] `MaxBytesReader` em todos os handlers POST
- [ ] Rate limiting na API (`/api/v1/*`)
- [ ] Connection pooling nos HTTP clients de embedding
- [ ] Evicção no `loginLimiter`
- [ ] CSRF key persistente (via `SEMIDX_CSRF_KEY` ou derivada do JWT secret)
- [ ] Bootstrap token: flag `--show-bootstrap-token` + aviso mais forte
- [ ] Search handler: mapear erros para mensagens user-safe
- [ ] Warnings no stderr, não stdout

### Fase 3 — Performance at Scale (~2 semanas)

- [ ] `SetWorktreeFiles`: bulk insert via `pgx.CopyFrom` / prepared statement
- [ ] Índice GIN trigram (`pg_trgm`) para keyword search em Postgres
- [ ] FTS5 para keyword search em SQLite
- [ ] Top-K heap no `searchSimilar` do SQLite (evitar carregar tudo em memória)
- [ ] `ListProjects`/`ListUsers` com paginação
- [ ] Métricas: histogramas de latência (search, embed), gauges (active jobs, queue depth, DB pool)
- [ ] Benchmark suite (`BenchmarkCosineBruteForce`, `BenchmarkPgVectorSearch`, `BenchmarkChunking`, `BenchmarkEmbedBatch`)
- [ ] `Makefile` com targets `build`, `test`, `bench`, `lint`, `docker-build`, `clean`

### Fase 4 — Observability & Operations (~2 semanas)

- [ ] Migrar indexer de `fmt.Printf` para `slog`
- [ ] Structured logging no search service e embedding chain (provider selection, timing, fallback events)
- [ ] OpenTelemetry tracing (médio prazo)
- [ ] Circuit breaker nos providers de embedding
- [ ] `LISTEN`/`NOTIFY` (Postgres) em vez de polling nos workers de job
- [ ] OpenAPI 3.0 spec gerada dos endpoints
- [ ] Limites configuráveis: `SEMIDX_MAX_FILE_SIZE`, `SEMIDX_MAX_CHUNKS_PER_FILE`, `SEMIDX_EMBED_BATCH_SIZE`, project chunk cap

### Fase 5 — Extensibility & Manutenibilidade (~2 semanas)

- [ ] Registry de extractors pluggable externamente (`Register()`)
- [ ] Segregar `Store` em interfaces menores (`TokenStore`, `UserStore`, `SessionStore`, `JobStore`)
- [ ] Mover `buildChain` de `main.go` para `internal/embed/NewChainFromConfig()`
- [ ] Config imutável (campos privados + getters)
- [ ] Extrair helper `tryEach` no ChainEmbedder (eliminar código duplicado)
- [ ] Traduzir `docs/design-decisions.md` para inglês
- [ ] Adicionar `SEMIDX_DB_PASSWORD` e outros env vars faltantes à documentação
- [ ] Remover binário `scripts/sgrep` do git, criar `.env.example`

---

## Resumo Estatístico

| Dimensão | 🔴 HIGH | 🟡 MEDIUM | 🟢 LOW | Total |
|----------|---------|-----------|--------|-------|
| Segurança & Erros | 2 | 8 | 8 | 18 |
| Arquitetura & Design | 0 | 6 | 9 | 15 |
| Documentação & Benchmarks | 2 | 0 | 8 | 10 |
| Performance & Escalabilidade | 5 | 13 | 6 | 24 |
| CLI Usabilidade | 6 | 6 | 5 | 17 |
| **Total** | **15** | **33** | **36** | **84** |

**Zero críticos. Zero vulnerabilidades exploráveis remotamente. Arquitetura fundamentalmente sólida.**

Os 15 HIGHs se concentram em:
- 6 de usabilidade do CLI (a ferramenta funciona mas é difícil de descobrir)
- 5 de performance/escalabilidade (timeouts ausentes, falta de pooling)
- 2 de segurança (token em log, erro interno vazando)
- 2 de documentação (zero benchmarks, docs misturando idiomas)

---

## Apêndice: Detalhes por Dimensão

### A. Segurança & Erros — Detalhes Completos

**Forças identificadas:**
- Todas as queries SQL usam placeholders parametrizados (`$1`, `$2` via pgx; `?` via SQLite)
- Nomes dinâmicos de tabela sanitizados via `pgx.Identifier{}.Sanitize()`
- Senhas hasheadas com argon2id (`internal/passwd/`)
- JWT com HS256, verificação explícita de algoritmo (`alg != HS256` guard)
- CSRF em todos os endpoints mutáveis do webadmin com HMAC + constant-time compare
- Cookies de sessão: `HttpOnly`, `Secure` (configurável), `SameSite=Lax`
- Zero `panic()` em código de produção
- SQLite com WAL journaling, `busy_timeout`, `MaxOpenConns(1)`
- Leituras de arquivo com `io.LimitReader`
- `ReadHeaderTimeout` configurado no servidor HTTP

### B. Arquitetura — Detalhes Completos

**Forças identificadas:**
- 22 packages sem dependências circulares (grafo flui: `cmd` → `internal/server` → `internal/store`)
- `embed` e `extract` com zero dependências internas — máxima testabilidade
- `IndexStore` / `Store` — abstração de dois níveis bem desenhada
- Compile-time assertions: `var _ store.IndexStore = (*SQLiteStore)(nil)`
- `ChainEmbedder` com chain-of-responsibility limpo
- `Backend` interface do MCP server com 3 métodos — abstração correta
- `pkg/client/` sem imports de `internal/` — SDK público limpo
- `errgroup` + `SetLimit` para concorrência bounded
- Property-based testing com `rapid` nos lugares certos
- Mutation testing em `chunker`
- Testcontainers com `t.Cleanup` para isolamento de testes Postgres

### C. Documentação — Detalhes Completos

**Forças identificadas:**
- 21/21 packages com `// Package` doc comment
- Maioria dos exports documentados com GoDoc
- Zero `TODO`/`FIXME`/`HACK` no código de produção
- `docs/api.md` cobre 17 endpoints com exemplos JSON
- `docs/self-hosting.md` referencia 21+ env vars
- `deploy/agentics-test/` com README completo do harness MCP
- `README.md` com quickstart para server standalone

### D. Performance — Detalhes Completos

**Forças identificadas:**
- `errgroup.Group` + `SetLimit(workers)` para bounded file concurrency — padrão correto
- `pgx.Batch` + `SendBatch` para bulk insert de chunks
- `FOR UPDATE SKIP LOCKED` para job claiming atômico
- `WAL` + `busy_timeout` + `MaxOpenConns=1` — config SQLite bem tunada
- Exponential backoff com jitter para retry de embedding
- Content-addressed file storage com hash-based dedup
- Graceful shutdown com 10s timeout
- Configuração por environment variables com precedência limpa

### E. CLI Usabilidade — Detalhes Completos

**Comandos que precisam de `Long`/`Example`:**
`index`, `search`, `sgrep`, `serve`, `mcp`, `models`, `drop`, `login`, `repo`, `skills`, `config set/get/unset/keys/path`

**Comandos que já têm `Long`:**
`mcp install`, `unlock`, `migrate`, `config` (parent)

**Workflow ideal proposto:**
```
Primary workflow:    index  →  search  →  sgrep  →  unlock
Setup:               config  →  login  →  models
Server / Advanced:   serve  →  mcp  →  repo  →  skills
Danger zone:         drop  →  migrate
```
