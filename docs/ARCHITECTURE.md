# Arquitetura do Sistema

Este documento descreve a estrutura de software, fluxo de dados e decisões de design do indexador semântico.

---

## Fluxo de Dados e Componentes

O sistema é modular e utiliza uma arquitetura baseada em comandos CLI e ferramentas integradas via protocolo MCP.

```
       CLI (index/search/sgrep)              MCP Server (stdio)
                │                                    │
                ├─────────────────┬──────────────────┘
                │                 │
                ▼                 ▼
          [ main.go ]        [ mcp.go ]
                │                 │
                └────────┬────────┘
                         │
                         ▼
        ┌──────────────────────────────────┐
        │       ChainEmbedder (Go)         │ (embedder.go, openai.go, ollama.go)
        └────────────────┬─────────────────┘
                         │
         ┌───────────────┴───────────────┐
         ▼ (Se arquivo sensível/local)   ▼ (Se arquivo normal/nuvem)
   [ Ollama (Local) ]             [ Gemini (Nuvem) ]
         │                               │
         └───────────────┬───────────────┘
                         │
                         ▼
        ┌──────────────────────────────────┐
        │          Indexer (Go)            │ (indexer.go, chunker.go)
        └────────────────┬─────────────────┘
                         │
                         ▼
        ┌──────────────────────────────────┐
        │            DB (pgx)              │ (db.go)
        └────────────────┬─────────────────┘
                         │
                         ▼
              [ PostgreSQL + pgvector ]
```

---

## Mapa de Componentes

| Arquivo | Responsabilidade | Dependências |
|---|---|---|
| `main.go` | Entrypoint da CLI, parses de flags, inicialização da chain de embedders e conexão com o banco. | `db.go`, `embedder.go`, `indexer.go` |
| `mcp.go` | Servidor JSON-RPC 2.0 via StdIO que implementa o protocolo MCP para expor ferramentas ao agente de IA. | `db.go`, `embedder.go`, `indexer.go` |
| `db.go` | Interface com PostgreSQL e pgvector (upserts de arquivos/projetos, inserts em batches, busca cosseno e FTS fallback). | `github.com/pgvector/pgvector-go` |
| `embedder.go` | Abstração da interface `Embedder` e implementação da `ChainEmbedder` com suporte a fallback e restrição de privacidade. | `context` |
| `ollama.go` | Cliente HTTP nativo para o Ollama local (usando `/api/chat` com keep_alive e `/api/show` para obter dimensões). | `net/http` |
| `openai.go` | Cliente HTTP compatível com OpenAI (usado para APIs externas como Gemini Pessoal, Groq e OpenRouter). | `net/http` |
| `indexer.go` | Orquestrador da varredura, leitura limitada de arquivos, hashing e execução de sub-batches de embedding. | `db.go`, `embedder.go`, `chunker.go` |
| `chunker.go` | Lógica de fatiamento (chunking) de código por quebra de linhas e texto com sliding window e overlapping. | `strings`, `path/filepath` |

---

## Modelo de Armazenamento (PostgreSQL)

O banco de dados utiliza a extensão `vector` para busca cosseno. Para acomodar diferentes modelos simultaneamente no mesmo banco, as tabelas de vetores são **criadas dinamicamente** baseadas na dimensão do modelo:

### 1. Tabela `projects`
Armazena os metadados dos repositórios indexados.
```sql
CREATE TABLE projects (
    id SERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    path TEXT NOT NULL,
    model TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'indexing',
    created_at TIMESTAMP DEFAULT NOW()
);
```

### 2. Tabela `files`
Armazena a assinatura hash (SHA-256) dos arquivos para suportar a indexação incremental rápida.
```sql
CREATE TABLE files (
    id SERIAL PRIMARY KEY,
    project_id INTEGER REFERENCES projects(id) ON DELETE CASCADE,
    path TEXT NOT NULL,
    hash TEXT NOT NULL,
    size_bytes INTEGER NOT NULL,
    indexed_at TIMESTAMP DEFAULT NOW(),
    UNIQUE(project_id, path)
);
```

### 3. Tabelas Dinâmicas `chunks_<dims>`
Exemplo: `chunks_1024` ou `chunks_3072`. Criadas dinamicamente no startup da indexação.
```sql
CREATE TABLE chunks_3072 (
    id SERIAL PRIMARY KEY,
    project_id INTEGER REFERENCES projects(id) ON DELETE CASCADE,
    file_id INTEGER REFERENCES files(id) ON DELETE CASCADE,
    chunk_index INTEGER NOT NULL,
    content TEXT NOT NULL,
    embedding vector(3072), -- Dims dinâmicas baseadas no modelo
    created_at TIMESTAMP DEFAULT NOW(),
    UNIQUE(project_id, file_id, chunk_index)
);
```

---

## Ordem de Leitura Recomendada para Contribuidores

Caso queira estudar ou estender este codebase, leia os arquivos nesta sequência:

1. **`embedder.go`**: Entenda a interface que abstrai a geração de embeddings e a chain de fallback.
2. **`openai.go`** e **`ollama.go`**: Veja como implementamos os clientes de rede específicos.
3. **`db.go`**: Compreenda as consultas SQL, busca por cosseno e a lógica de fallback por palavra-chave (`SearchSimilarKeywords`).
4. **`chunker.go`**: Estude as regras de skipping de diretórios e o algoritmo de chunking.
5. **`indexer.go`**: Veja o loop principal do indexador, o controle de memória, a integração com o Git history e a proteção a arquivos sensíveis.
6. **`main.go`** e **`mcp.go`**: Veja como expomos essas funcionalidades na CLI e na API MCP.
