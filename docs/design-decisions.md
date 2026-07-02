# Decisões de Design (ADRs)

Este documento registra as principais decisões arquiteturais tomadas durante a evolução deste POC, motivadas por incidentes reais ocorridos no homelab.

---

## 1. Abstração e Chain de Provedores com Fallback

- **Decisão**: Criar a interface `Embedder` e a implementação `ChainEmbedder` permitindo que múltiplos provedores de embedding sejam varridos em ordem de preferência.
- **Why**: 
  - *Incidente*: Chaves de API externas (ou o Ollama local) ocasionalmente falham por timeout, queda de rede ou estouro de cota (rate limits no free tier). Deixar o CLI amarrado a um único provedor causava interrupção completa da indexação.
- **How**: 
  - Implementado em `embedder.go`. O `ChainEmbedder` recebe uma lista de provedores ordenados. Qualquer falha de rede ou timeout na geração do embedding chaveia automaticamente para o próximo da lista (ex: Gemini -> OpenRouter -> Ollama Local).
- **Trade-offs**: 
  - Diferentes modelos geram vetores de dimensões diferentes (ex: bge-m3 1024d vs Gemini 3072d). A busca semântica **não funciona** se misturarmos vetores de modelos diferentes no mesmo índice (gerará lixo matemático). Por isso, a chain só pode fazer fallback entre endpoints servindo o *mesmo* modelo ou modelos com dimensões equivalentes, ou o projeto deve ser reindexado do zero se houver troca de modelo.

---

## 2. Tabelas Dinâmicas de Vetores baseadas em Dimensão

- **Decisão**: Abandonar a tabela única `chunks` com tamanho fixo `vector(1024)` em favor de tabelas dinâmicas como `chunks_768`, `chunks_1024` e `chunks_3072`.
- **Why**: 
  - *Incidente*: O pgvector exige tamanho fixo na declaração da coluna (ex: `vector(1024)`). Modelos mais leves (como `nomic-embed-text` de 768d) ou mais precisos (como `gemini-embedding-2` de 3072d) quebravam a inserção SQL com erro de sintaxe e violação de restrição.
- **How**: 
  - Implementado em `db.go`. O método `EnsureChunksTable(ctx, dims)` cria dinamicamente a tabela correspondente (`chunks_X`) baseada nas dimensões reportadas pelo modelo durante a inicialização.
- **Trade-offs**: 
  - Adiciona complexidade ao SQL (usando concatenação de strings para os nomes das tabelas). Porém, mantém o banco de dados limpo e flexível para aceitar qualquer modelo do mercado sem migrações manuais de schema.

---

## 3. Sandboxing de Indexação via Docker com Capping de RAM (512MB)

- **Decisão**: Executar a indexação pesada estritamente dentro de containers Docker com limites rígidos de memória (`--memory 512m`).
- **Why**: 
  - *Incidente*: O processo de indexação do `opencode` convencional (baseado em Bun) e as primeiras execuções do Go POC inflaram a memória RAM do homelab de 20GB a 23GB, esgotando o swap de 4GB e causando travamento total (thrashing/freezing) da máquina física de 31GB antes de serem mortos pelo OOM-killer.
- **How**: 
  - Criado o script `/home/lgldsilva/poc-semantic-indexer/index-project.sh`. Ele compila a aplicação estaticamente (`CGO_ENABLED=0`) e a executa em um container `alpine` leve com limites de cgroup aplicados no próprio runtime do Docker.
- **Trade-offs**: 
  - Requer que o Docker esteja instalado e rodando no host. No entanto, protege a máquina física contra qualquer vazamento de memória ou picos de processamento, tornando a execução 100% segura.

---

## 4. Roteamento e Proteção de Arquivos Sensíveis

- **Decisão**: Detectar arquivos contendo segredos ou informações confidenciais (`.env`, `auth`, `secret`, `key`) e forçar a geração de embeddings estritamente de forma **local** (via Ollama). Se o projeto estiver usando um modelo de nuvem (como o Gemini), o arquivo sensível é indexado apenas como **texto puro local** (sem enviar para a nuvem).
- **Why**: 
  - *Incidente*: Enviar arquivos de configuração, credenciais de banco ou chaves privadas para APIs na nuvem (Google Gemini/OpenRouter) viola regras básicas de segurança e expõe segredos do homelab.
- **How**: 
  - Implementado em `chunker.go` (`IsSensitive`) e `indexer.go`. O indexador usa a flag `WithForceLocal` via contexto Go. Se a geração local do modelo falhar ou não for suportada (ex: Gemini na nuvem), o indexador salva o chunk de texto com o vetor `embedding` setado como `NULL`.
- **Trade-offs**: 
  - Chunks sensíveis indexados sem embedding não aparecem na busca semântica pura. Porém, eles continuam localizados pelo mecanismo de **Search Fallback por palavra-chave (FTS)**, garantindo segurança sem perder a capacidade de busca.

---

## 5. Fallback por Palavras-Chave (SQL ILIKE) e Auto-Detecção de Tabela

- **Decisão**: Implementar busca textual clássica com suporte a varredura automática de tabelas do banco caso a API de embeddings esteja completamente offline.
- **Why**: 
  - *Incidente*: Se o Ollama local estiver desligado e a internet cair, a busca semântica quebra porque não consegue converter a query em vetor.
- **How**: 
  - Implementado em `db.go` (`SearchSimilarKeywords`). O método divide a query em palavras e executa buscas combinadas por `ILIKE` no banco. Se não soubermos o modelo ou a dimensão, o Go POC consulta a tabela do sistema `pg_tables` para achar qual tabela `chunks_*` tem registros gravados para aquele projeto.
- **Trade-offs**: 
  - A busca textual não entende sinônimos (ex: buscar "usuário" não achará "user"), mas mantém a busca operacional e resiliente sob qualquer circunstância de falha de infraestrutura.

---

## 6. Índice ANN (HNSW/halfvec) e Números de Linha no Banco

- **Decisão**: Criar um índice **HNSW** de cosseno em cada tabela `chunks_<dims>` e persistir `start_line`/`end_line` de cada chunk no banco (revertendo a decisão original de calcular a linha lendo o arquivo em tempo de busca).
- **Why**:
  - Sem índice ANN a busca vetorial fazia *sequential scan* — inaceitável conforme os projetos crescem.
  - O cálculo de linha lendo o arquivo (`findLineInFile`) exige que o arquivo esteja acessível no host da busca — impossível na arquitetura cliente-servidor (o servidor não tem os arquivos) — e o algoritmo achava a *primeira* ocorrência da linha em qualquer lugar do arquivo (frágil).
- **How**:
  - `EnsureChunksTable` cria `CREATE INDEX ... USING hnsw`. **Gotcha do pgvector**: HNSW sobre o tipo `vector` limita a **2000 dimensões**; modelos maiores (Gemini 3072) indexam o cast `halfvec` (`(embedding::halfvec(N)) halfvec_cosine_ops`), e `SearchSimilar` consulta a expressão equivalente para que o índice seja usado.
  - `Chunk` carrega `StartLine`/`EndLine` (calculados no `chunker`), persistidos em `chunks_<dims>` e retornados no `SearchResult`. O `GrepFormatter` usa a linha do banco; `findLineInFile` foi removido.
- **Trade-offs**:
  - `halfvec` reduz a precisão do índice (meia precisão) para modelos >2000d — aceitável para recall aproximado; a distância exata ainda usa o `vector`. Tabelas antigas recebem as colunas via `ALTER TABLE ADD COLUMN IF NOT EXISTS` (upgrade sem `drop`), mas dados pré-migração ficam com linha nula (reindexar para popular).

---

## 🚫 O que NÃO faremos (por enquanto)

- **Tabela `models` no banco**: `InferDims` (mapa nome→dimensão) já é fonte única em `internal/embed`; mover para uma tabela no banco acoplaria `embed`→`store` por benefício marginal. Reavaliar se/quando precisar de config por-modelo (provider/local) sem recompilar.
- **Indexação de Arquivos Grandes (>1MB)**: Arquivos gigantes são ignorados ou truncados. Este projeto é otimizado para código-fonte e documentação estruturada em markdown.

---

## ⚠️ Checklist de Erros a Evitar

- [ ] **Não use `os.ReadFile` puro**: Ele aloca o arquivo inteiro em memória antes de fatiar. Sempre use `os.Open` + `io.LimitReader` para arquivos de tamanho incerto.
- [ ] **Não use `\r` em logs de background**: Caracteres de retorno de carro quebram o buffer de visualização de TUIs de agentes, causando lentidão e travamentos de renderização. Use logs estruturados por linhas com `\n`.
- [ ] **Não misture modelos no mesmo projeto**: Mantenha o mesmo modelo (ex: `bge-m3`) em todas as indexações de um projeto, ou chame `drop` antes de mudar.
