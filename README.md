# semantic-indexer-poc

A memory-efficient, secure CLI tool and MCP server for local semantic code search.
[![status: stable](https://img.shields.io/badge/status-stable-green.svg)](#)
[![go: 1.25.0](https://img.shields.io/badge/go-1.25.0-blue.svg)](#)
[![license: Apache--2.0](https://img.shields.io/badge/license-Apache--2.0-lightgrey.svg)](#)

> [!NOTE]
> Este projeto é uma Prova de Conceito (POC) criada para fornecer indexação incremental e busca semântica em repositórios locais sem vazamento de memória e com salvaguardas de privacidade rígidas para dados sensíveis.

## Como Funciona

O indexador varre os arquivos do projeto (ignorando pastas pesadas como `node_modules` e `.git`), divide o código em chunks inteligentes e gera vetores de embedding usando o Gemini (nuvem, veloz) ou Ollama (local, offline). Os vetores são gravados no PostgreSQL com a extensão `pgvector`.

```
                    [ sgrep / MCP Search ]
                               │
                               ▼ (Query)
 ┌──────────────────────────────────────────────────────────┐
 │                  ChainEmbedder (Go CLI)                  │
 └─────────────────────────────┬────────────────────────────┘
                               │
            ┌──────────────────┴──────────────────┐
            ▼ (Se Nuvem & Segredos)              ▼ (Se Local ou Livre)
      [ SQL Fallback ]                     [ Provedor de Embedding ]
      (Pesquisa Textual)                   (Gemini/Ollama/Groq)
            │                                     │
            └──────────────────┬──────────────────┘
                               │
                               ▼
                    [ PostgreSQL + pgvector ]
```

---

## Instalação & Dependências

### 1. Requisitos
- **Go 1.25+**
- **Docker** (para rodar o banco pgvector de forma isolada)
- **Git** (para indexação de histórico)

### 2. Configurar o Banco de Dados
Suba o container do PostgreSQL com suporte a vetores usando o Docker Compose:
```bash
cd /home/lgldsilva/poc-semantic-indexer
docker compose up -d
```
Isso criará a instância `semantic-indexer-pg` escutando na porta `55432`.

### 3. Compilar o Projeto
Para rodar nativamente ou dentro de containers, compile o executável estaticamente:
```bash
CGO_ENABLED=0 GOOS=linux go build -o semantic-indexer-poc .
```

---

## Executando Indexações Seguras (Docker Sandbox)

Para evitar vazamentos de memória e sobrecarga do sistema operacional, **nunca execute a indexação diretamente no host**. Use o script de sandbox que limita o uso de RAM do container a 512MB e monta as pastas de forma segura:

```bash
./index-project.sh /storage/Projetos/jackui [modelo]
```
- **Modelo Padrão**: `gemini-embedding-2` (3072 dimensões, executado em segundos via nuvem).
- **Modelo Local**: `bge-m3` ou `nomic-embed-text` (Ollama local, executado no seu hardware).

---

## Uso & Comandos da CLI

### Comandos Principais
| Comando | Parâmetros | Descrição |
|---|---|---|
| `index` | `-project <path> -model <model> [--git] [--verbose] [--privacy]` | Varrer e indexar o diretório de um projeto |
| `search` | `-project <name> -query <text> [-top-k <num>]` | Busca semântica estruturada com score de cosseno |
| `sgrep` | `-project <name> -query <text> [-top-k <num>]` | Busca semântica com output formatado no padrão clássico do grep |
| `models` | *(nenhum)* | Lista todos os modelos de embedding ativos na chain |
| `drop` | *(nenhum)* | Limpa todo o banco de dados (tabelas e projetos) |
| `mcp` | *(nenhum)* | Inicia o servidor MCP local em stdio (JSON-RPC 2.0) |

### Exemplos de Uso

#### Buscar termos usando Grep Semântico (`sgrep`)
Use o script wrapper `sgrep` para buscar de qualquer pasta do seu projeto. Ele detecta o nome do projeto automaticamente e formata a saída como `caminho:linha:conteudo`:
```bash
cd /storage/Projetos/jackui
sgrep "autenticação JWT" 3
```

#### Buscar diretamente via executável
```bash
./semantic-indexer-poc search -project jackui -query "ffmpeg HLS transcoder" -top-k 3
```

---

## Configuração & Variáveis de Ambiente

As chaves e endpoints são lidos automaticamente do arquivo `/home/lgldsilva/Projetos/immich-classifier/.env` se disponível, ou do ambiente:

| Variável | Padrão | Descrição |
|---|---|---|
| `EMBED_PROVIDER` | `ollama` | Provedor de embedding ativo: `ollama` ou `openai` |
| `EMBED_ENDPOINT` | *(auto)* | Endpoint do provedor (ex: `https://generativelanguage.googleapis.com/v1beta/openai`) |
| `EMBED_API_KEY` | *(vazio)* | Chave de autenticação Bearer do provedor |
| `EMBED_PRIVACY` | `false` | Se `true`, força o uso estrito de provedores locais (Ollama) |
| `OLLAMA_URL` | `http://localhost:11434` | Endpoint do Ollama local |

---

## Segurança & Privacidade

### Proteções Ativas (Nuvem vs. Local)
1. **Filtro de Arquivos Sensíveis**: Arquivos confidenciais (ex: `.env`, `.pem`, ou caminhos contendo `auth`, `secret`, `key`, `password`) são monitorados. Se você rodar a indexação apontando para um modelo na nuvem (como o Gemini), o indexador **não enviará estes arquivos para a API**. Eles serão indexados estritamente na forma de **texto puro no banco local** (sem embedding) para que continuem pesquisáveis apenas no modo de fallback.
2. **Pre-Checks de Sistema**: O indexador recusa indexar pastas raiz do sistema operacional (`/`, `/home`, `/etc`, etc.) para evitar vazamentos e consumo de disco descontrolado.
3. **Sandbox Cgroup**: O script `index-project.sh` garante limite de **512MB RAM** físico, eliminando qualquer risco de OOM/thrashing no homelab.

### O que este projeto NÃO protege
- **Comunicação em Texto Puro**: A API do banco de dados (PostgreSQL) roda em porta exposta na rede local (`55432`). Certifique-se de que a porta está protegida por firewall caso exponha o servidor.
- **Tráfego SSL/TLS local**: A comunicação entre o CLI e o container PostgreSQL na máquina local não usa encriptação SSL por padrão.

---

## Arquitetura & Documentação

Para detalhes de arquitetura de software e decisões de design:
- Veja [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)
- Veja [docs/design-decisions.md](docs/design-decisions.md)

---

## Licença

Este projeto é distribuído sob a licença Apache 2.0. Veja o arquivo LICENSE para detalhes.
