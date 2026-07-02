package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
)

// JSON-RPC types
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MCP types
type mcpTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type mcpToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// runMCPServer serves the MCP protocol over stdio, reusing the database
// pool and embedder chain built in main.
func runMCPServer(db store.Store, emb embed.Embedder) {
	server := &mcpServer{db: db, emb: emb}

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer

	for scanner.Scan() {
		var req jsonRPCRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			writeError(nil, -32700, "Parse error")
			continue
		}

		switch req.Method {
		case "initialize":
			server.handleInitialize(req.ID)
		case "tools/list":
			server.handleToolsList(req.ID)
		case "tools/call":
			server.handleToolsCall(req.ID, req.Params)
		case "resources/list":
			server.handleResourcesList(req.ID)
		case "resources/read":
			server.handleResourcesRead(req.ID, req.Params)
		default:
			writeError(req.ID, -32601, fmt.Sprintf("Method not found: %s", req.Method))
		}
	}
}

type mcpServer struct {
	db  store.Store
	emb embed.Embedder
	mu  sync.Mutex
}

func (s *mcpServer) handleInitialize(id interface{}) {
	result := map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]interface{}{
			"tools":     map[string]interface{}{},
			"resources": map[string]interface{}{},
		},
		"serverInfo": map[string]interface{}{
			"name":    "semantic-indexer",
			"version": "0.1.0",
		},
	}
	writeResult(id, result)
}

func (s *mcpServer) handleToolsList(id interface{}) {
	tools := []mcpTool{
		{
			Name:        "semantic_search",
			Description: "Search indexed code semantically using natural language queries",
			InputSchema: mustMarshal(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"project": map[string]interface{}{"type": "string", "description": "Project name"},
					"query":   map[string]interface{}{"type": "string", "description": "Natural language search query"},
					"model":   map[string]interface{}{"type": "string", "description": "Embedding model (default: project default)"},
					"top_k":   map[string]interface{}{"type": "integer", "description": "Number of results (default: 5)"},
				},
				"required": []string{"project", "query"},
			}),
		},
		{
			Name:        "semantic_index",
			Description: "Index a project directory for semantic search",
			InputSchema: mustMarshal(map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"project":   map[string]interface{}{"type": "string", "description": "Project directory path"},
					"model":     map[string]interface{}{"type": "string", "description": "Embedding model (default: bge-m3)"},
					"max_files": map[string]interface{}{"type": "integer", "description": "Max files to index (0=all)"},
					"git":       map[string]interface{}{"type": "boolean", "description": "Also index git history"},
					"git_since": map[string]interface{}{"type": "string", "description": "Git log --since (default: 30.days)"},
				},
				"required": []string{"project"},
			}),
		},
		{
			Name:        "semantic_models",
			Description: "List available embedding models",
			InputSchema: mustMarshal(map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			}),
		},
	}
	writeResult(id, map[string]interface{}{"tools": tools})
}

func (s *mcpServer) handleToolsCall(id interface{}, paramsRaw json.RawMessage) {
	var params mcpToolCallParams
	if err := json.Unmarshal(paramsRaw, &params); err != nil {
		writeError(id, -32602, "Invalid params")
		return
	}

	ctx := context.Background()
	s.mu.Lock()
	defer s.mu.Unlock()

	var result interface{}
	switch params.Name {
	case "semantic_search":
		result = s.doSearch(ctx, params.Arguments)
	case "semantic_index":
		result = s.doIndex(ctx, params.Arguments)
	case "semantic_models":
		models, err := s.emb.ListModels(ctx)
		if err != nil {
			writeError(id, -32000, err.Error())
			return
		}
		result = map[string]interface{}{"models": models}
	default:
		writeError(id, -32601, fmt.Sprintf("Unknown tool: %s", params.Name))
		return
	}

	// MCP tools/call response wraps result in content array
	content := []map[string]interface{}{{"type": "text", "text": fmt.Sprintf("%v", result)}}
	writeResult(id, map[string]interface{}{"content": content})
}

type searchArgs struct {
	Project string `json:"project"`
	Query   string `json:"query"`
	Model   string `json:"model"`
	TopK    int    `json:"top_k"`
}

func (s *mcpServer) doSearch(ctx context.Context, argsRaw json.RawMessage) string {
	var args searchArgs
	if err := json.Unmarshal(argsRaw, &args); err != nil {
		return fmt.Sprintf("error: invalid arguments: %v", err)
	}

	// Guard against searching a half-indexed project (unique to the MCP flow).
	project, err := s.db.GetProject(ctx, args.Project)
	if err != nil {
		return fmt.Sprintf("error: project not found: %s", args.Project)
	}
	if project.Status == "indexing" {
		return fmt.Sprintf("warning: O projeto '%s' ainda está sendo indexado em background. Por favor, use a busca/grep convencional enquanto a indexação semântica é concluída.", args.Project)
	}

	resp, err := search.NewService(s.db, s.emb).Search(ctx, search.Request{
		Project: args.Project, Query: args.Query, Model: args.Model, TopK: args.TopK,
	})
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}

	var buf strings.Builder
	if err := (search.HumanFormatter{Preview: 300}).Format(&buf, resp); err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	return buf.String()
}

type indexArgs struct {
	Project  string `json:"project"`
	Model    string `json:"model"`
	MaxFiles int    `json:"max_files"`
	Git      bool   `json:"git"`
	GitSince string `json:"git_since"`
}

func (s *mcpServer) doIndex(ctx context.Context, argsRaw json.RawMessage) string {
	var args indexArgs
	if err := json.Unmarshal(argsRaw, &args); err != nil {
		return fmt.Sprintf("error: invalid arguments: %v", err)
	}
	if args.Model == "" {
		args.Model = "bge-m3"
	}
	if args.GitSince == "" {
		args.GitSince = "30.days"
	}

	cleanPath := filepath.Clean(args.Project)
	forbidden := map[string]bool{
		"/": true, "/home": true, "/etc": true, "/usr": true, "/var": true,
		"/opt": true, "/bin": true, "/sbin": true, "/lib": true,
	}
	if forbidden[cleanPath] {
		return fmt.Sprintf("error: segurança: não é permitido indexar diretório do sistema %s", cleanPath)
	}

	name := projectNameFromPath(args.Project)

	info, err := s.emb.ModelInfo(ctx, args.Model)
	if err != nil {
		return fmt.Sprintf("error: model info: %v", err)
	}

	if err := s.db.EnsureChunksTable(ctx, info.Dims); err != nil {
		return fmt.Sprintf("error: ensure table: %v", err)
	}

	projectID, err := s.db.UpsertProject(ctx, name, args.Project, args.Model)
	if err != nil {
		return fmt.Sprintf("error: upsert project: %v", err)
	}

	indexer := indexing.NewIndexer(s.db, s.emb, info.Dims, 0, false, args.Git, args.GitSince)
	stats, err := indexer.IndexProject(ctx, projectID, args.Project, args.Model, args.MaxFiles)
	if err != nil {
		return fmt.Sprintf("error: index: %v", err)
	}

	return fmt.Sprintf("Indexed %d/%d files, %d chunks, %d errors",
		stats.FilesIndexed, stats.FilesScanned, stats.ChunksCreated, stats.Errors)
}

func (s *mcpServer) handleResourcesList(id interface{}) {
	writeResult(id, map[string]interface{}{"resources": []interface{}{}})
}

func (s *mcpServer) handleResourcesRead(id interface{}, params json.RawMessage) {
	writeResult(id, map[string]interface{}{"contents": []interface{}{}})
}

func mustMarshal(v interface{}) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func writeResult(id interface{}, result interface{}) {
	resp := jsonRPCResponse{JSONRPC: "2.0", ID: id, Result: result}
	if err := json.NewEncoder(os.Stdout).Encode(resp); err != nil {
		fmt.Fprintf(os.Stderr, "mcp: encode response: %v\n", err)
	}
}

func writeError(id interface{}, code int, message string) {
	resp := jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}}
	if err := json.NewEncoder(os.Stdout).Encode(resp); err != nil {
		fmt.Fprintf(os.Stderr, "mcp: encode response: %v\n", err)
	}
}
