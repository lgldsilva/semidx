# Search excellence

Este documento registra a primeira entrega de usabilidade e contrato para a
busca semântica. Ele é deliberadamente operacional: cada indicador abaixo deve
ser observável no CLI, no painel web e no MCP.

## O que mudou

- A resposta de busca agora informa `route` (`keyword`, `vector`, `hybrid` ou
  `fallback`) e `took_ms`. O JSON também preserva `keyword`, linhas inicial e
  final, origem do hit, símbolo, confiança e staleness.
- O CLI remoto e multi-projeto mantém o caminho real do projeto e os metadados
  de cada hit. `sgrep` não ancora um documento em um checkout git sem relação.
- `semidx init` não anuncia um projeto como pronto quando o usuário recusou a
  indexação. O resumo distingue modo keyword de modo semântico e exibe o modelo
  efetivamente selecionado.
- O painel web exibe rota, modelo, latência e quantidade de resultados; cancela
  requisições obsoletas; explica que o score é ordinal; sinaliza conteúdo stale;
  oferece copiar a localização e adiciona skip-link, foco visível e navegação
  por teclado nas abas e na paleta de comandos.
- O `semantic_search` do MCP publica saída tipada e estruturada, erros com
  código/ação, anotações de somente leitura e metadados de rota/modelo. Perfis
  `search`, `workspace`, `code-intel` e `all` reduzem o contexto inicial do
  agente; use `semidx mcp --tool-profile search` ou `SEMIDX_MCP_PROFILE`.
  O campo `format` controla apenas o bloco textual legado; clientes MCP novos
  devem consumir `structuredContent`, cujo contrato é sempre o envelope
  completo da busca.

## Contrato de evidência

Uma resposta não deve sugerir que um score é probabilidade. Para comparar
mudanças de recuperação, registre sempre:

```text
query, project, route, model, top_k, fallback, degraded,
result path + line range, score, source, stale, took_ms
```

`fallback=true` e `degraded=true` são estados de produto, não detalhes de
implementação. O fallback precisa permanecer explícito para que um agente
possa decidir entre editar com cautela, reler o arquivo ou pedir reindexação.

## Gate de relevância lexical concluído

O baseline keyword versionado em `testdata/eval/baselines/` continua sendo uma
referência, não uma meta de qualidade. O recuperador lexical agora ordena
candidatos por BM25/FTS (SQLite) ou cobertura + `ts_rank_cd` (PostgreSQL), sem
score constante. O benchmark versionado também registra a rota efetivamente
observada por consulta e sua distribuição agregada. O próximo ciclo deve:

1. preservar o baseline e a distribuição de rotas;
2. adicionar um conjunto pequeno de consultas rotuladas, com arquivo e linha
   esperados;
3. medir MRR, nDCG@10, Recall@10, P@5 e p50/p95 por rota;
4. só então comparar embeddings, fusão híbrida e reranker com o mesmo conjunto;
5. publicar os resultados como artefato reprodutível, incluindo modelo,
   dimensões, backend e commit.

## Limites atuais

Esta entrega não inventa uma melhora de recall: ela torna a recuperação
explicável e comparável em todas as interfaces. BM25/FTS lexical, filtros
faceted persistentes, feedback de relevância e testes visuais E2E continuam
sendo etapas separadas, pois exigem benchmark e dados de uso reais.
