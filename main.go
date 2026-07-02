package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/lgldsilva/semidx/internal/config"
)

func main() {
	// Limita a memória do runtime do Go a 2GB
	debug.SetMemoryLimit(2 * 1024 * 1024 * 1024)

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cfg := config.Load()
	embedder := buildChain(cfg)

	ctx := context.Background()
	db, err := NewDB(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("failed to connect to db: %v", err)
	}
	defer db.Close()

	switch os.Args[1] {
	case "index":
		indexCmd(ctx, cfg, db, embedder)
	case "search":
		searchCmd(ctx, cfg, db, embedder)
	case "sgrep":
		sgrepCmd(ctx, cfg, db, embedder)
	case "models":
		modelsCmd(embedder)
	case "drop":
		dropCmd(ctx, db)
	case "mcp":
		runMCPServer(db, embedder)
	default:
		printUsage()
	}
}

func printUsage() {
	fmt.Print(`Usage: semantic-indexer-poc <command> [args]

Commands:
  index  -project <path> -model <name>  Index a project directory
  search -project <name> -query <text>  Search indexed project
  sgrep  -project <name> -query <text>  Search and output in classic grep format (file:line:content)
  models                                List available embedding models
  drop                                  Drop all indexed data
`)
}

func indexCmd(ctx context.Context, cfg *config.Config, db *DB, emb Embedder) {
	fs := flag.NewFlagSet("index", flag.ExitOnError)
	projectPath := fs.String("project", "", "Path to project directory")
	model := fs.String("model", "bge-m3", "Embedding model name")
	maxFiles := fs.Int("max-files", 0, "Limit number of files to index (0 = all)")
	gitMode := fs.Bool("git", false, "Also index git history (git log -p)")
	gitSince := fs.String("git-since", "30.days", "Git log --since duration (e.g. 7.days, 1.month)")
	verbose := fs.Bool("verbose", false, "Show detailed progress and errors")
	privacy := fs.Bool("privacy", false, "Force local-only providers (Ollama)")
	_ = fs.Parse(os.Args[2:]) // ExitOnError: Parse exits on failure

	if ce, ok := emb.(*ChainEmbedder); ok {
		ce.SetPrivacy(*privacy || cfg.Privacy)
	}

	if *projectPath == "" {
		fs.PrintDefaults()
		os.Exit(1)
	}

	cleanPath := filepath.Clean(*projectPath)
	forbidden := map[string]bool{
		"/": true, "/home": true, "/etc": true, "/usr": true, "/var": true,
		"/opt": true, "/bin": true, "/sbin": true, "/lib": true,
	}
	if forbidden[cleanPath] {
		log.Fatalf("Erro de segurança: Não é permitido indexar o diretório do sistema: %s", cleanPath)
	}

	name := projectNameFromPath(*projectPath)

	info, err := emb.ModelInfo(ctx, *model)
	if err != nil {
		log.Fatalf("failed to get model info for %s: %v", *model, err)
	}
	fmt.Printf("Indexing project: %s\n", name)
	fmt.Printf("Path: %s\n", *projectPath)
	fmt.Printf("Model: %s (dims=%d)\n", *model, info.Dims)

	if err := db.EnsureChunksTable(ctx, info.Dims); err != nil {
		log.Fatalf("failed to ensure chunks table: %v", err)
	}

	projectID, err := db.UpsertProject(ctx, name, *projectPath, *model)
	if err != nil {
		log.Fatalf("failed to upsert project: %v", err)
	}

	indexer := NewIndexer(db, emb, info.Dims, *verbose, *gitMode, *gitSince)

	start := time.Now()
	stats, err := indexer.IndexProject(ctx, projectID, *projectPath, *model, *maxFiles)
	if err != nil {
		log.Fatalf("failed to index project: %v", err)
	}
	elapsed := time.Since(start)

	fmt.Printf("\nDone in %v\n", elapsed)
	fmt.Printf("Files scanned: %d\n", stats.FilesScanned)
	fmt.Printf("Files indexed: %d\n", stats.FilesIndexed)
	fmt.Printf("Chunks created: %d\n", stats.ChunksCreated)
	fmt.Printf("Errors: %d\n", stats.Errors)
}

func searchCmd(ctx context.Context, cfg *config.Config, db *DB, emb Embedder) {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	projectName := fs.String("project", "", "Project name")
	query := fs.String("query", "", "Search query")
	topK := fs.Int("top-k", 5, "Number of results")
	model := fs.String("model", "", "Override embedding model (default: project model)")
	privacy := fs.Bool("privacy", false, "Force local-only providers (Ollama)")
	_ = fs.Parse(os.Args[2:]) // ExitOnError: Parse exits on failure

	if ce, ok := emb.(*ChainEmbedder); ok {
		ce.SetPrivacy(*privacy || cfg.Privacy)
	}

	if *projectName == "" || *query == "" {
		fs.PrintDefaults()
		os.Exit(1)
	}

	project, err := db.GetProject(ctx, *projectName)
	if err != nil {
		log.Fatalf("failed to get project: %v", err)
	}

	searchModel := project.Model
	if *model != "" {
		searchModel = *model
	}

	var dims int
	info, err := emb.ModelInfo(ctx, searchModel)
	if err != nil {
		dims = inferDims(searchModel)
	} else {
		dims = info.Dims
	}

	fmt.Printf("Searching project: %s (model: %s)\n", project.Name, searchModel)
	fmt.Printf("Query: %s\n\n", *query)

	start := time.Now()
	var results []SearchResult
	embedding, err := emb.EmbedSingle(ctx, searchModel, *query)
	if err != nil {
		fmt.Printf("[warn] embedding failed (%v), falling back to keyword search...\n\n", err)
		results, err = db.SearchSimilarKeywords(ctx, project.ID, *query, dims, *topK)
		if err != nil {
			log.Fatalf("failed to search: %v", err)
		}
	} else {
		results, err = db.SearchSimilar(ctx, project.ID, embedding, dims, *topK)
		if err != nil {
			log.Fatalf("failed to search: %v", err)
		}
	}
	elapsed := time.Since(start)

	fmt.Printf("Found %d results in %v\n\n", len(results), elapsed)
	for i, r := range results {
		fmt.Printf("--- Result %d (score: %.4f) ---\n", i+1, r.Score)
		fmt.Printf("File: %s\n", r.FilePath)
		preview := strings.TrimSpace(r.Content)
		if len(preview) > 500 {
			preview = preview[:500] + "..."
		}
		fmt.Printf("%s\n\n", preview)
	}
}

func modelsCmd(emb Embedder) {
	ctx := context.Background()
	models, err := emb.ListModels(ctx)
	if err != nil {
		log.Fatalf("failed to list models: %v", err)
	}

	fmt.Println("Available embedding models:")
	for _, m := range models {
		fmt.Printf("  - %s\n", m)
	}
}

func dropCmd(ctx context.Context, db *DB) {
	if err := db.DropAll(ctx); err != nil {
		log.Fatalf("failed to drop data: %v", err)
	}
	fmt.Println("All indexed data dropped.")
}

func projectNameFromPath(path string) string {
	path = strings.TrimRight(path, "/")
	idx := strings.LastIndex(path, "/")
	if idx >= 0 {
		return path[idx+1:]
	}
	return path
}

func sgrepCmd(ctx context.Context, cfg *config.Config, db *DB, emb Embedder) {
	fs := flag.NewFlagSet("sgrep", flag.ExitOnError)
	projectName := fs.String("project", "", "Project name")
	query := fs.String("query", "", "Search query")
	topK := fs.Int("top-k", 5, "Number of results")
	model := fs.String("model", "", "Override embedding model (default: project model)")
	privacy := fs.Bool("privacy", false, "Force local-only providers (Ollama)")
	_ = fs.Parse(os.Args[2:]) // ExitOnError: Parse exits on failure

	if ce, ok := emb.(*ChainEmbedder); ok {
		ce.SetPrivacy(*privacy || cfg.Privacy)
	}

	if *projectName == "" || *query == "" {
		fs.PrintDefaults()
		os.Exit(1)
	}

	project, err := db.GetProject(ctx, *projectName)
	if err != nil {
		log.Fatalf("failed to get project: %v", err)
	}

	searchModel := project.Model
	if *model != "" {
		searchModel = *model
	}

	var dims int
	info, err := emb.ModelInfo(ctx, searchModel)
	if err != nil {
		dims = inferDims(searchModel)
	} else {
		dims = info.Dims
	}

	var results []SearchResult
	embedding, err := emb.EmbedSingle(ctx, searchModel, *query)
	if err != nil {
		// Fallback silencioso no sgrep para manter a compatibilidade
		results, err = db.SearchSimilarKeywords(ctx, project.ID, *query, dims, *topK)
		if err != nil {
			log.Fatalf("failed to search: %v", err)
		}
	} else {
		results, err = db.SearchSimilar(ctx, project.ID, embedding, dims, *topK)
		if err != nil {
			log.Fatalf("failed to search: %v", err)
		}
	}

	for _, r := range results {
		lineNum := findLineNumber(project.Path, r.FilePath, r.Content)
		lines := strings.Split(r.Content, "\n")
		firstLine := ""
		for _, line := range lines {
			if strings.TrimSpace(line) != "" {
				firstLine = strings.TrimSpace(line)
				break
			}
		}
		fmt.Printf("%s:%d:%s\n", filepath.Join(project.Path, r.FilePath), lineNum, firstLine)
	}
}

func findLineNumber(projectPath, filePath, content string) int {
	fullPath := filepath.Join(projectPath, filePath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return 1
	}

	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return 1
	}
	firstLine := ""
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			firstLine = strings.TrimSpace(l)
			break
		}
	}
	if firstLine == "" {
		return 1
	}

	fileContent := string(data)
	idx := strings.Index(fileContent, firstLine)
	if idx < 0 {
		return 1
	}

	lineNum := 1
	for i := 0; i < idx; i++ {
		if fileContent[i] == '\n' {
			lineNum++
		}
	}
	return lineNum
}

func buildChain(cfg *config.Config) Embedder {
	var providers []ProviderInstance

	// 1. Gemini
	if cfg.GeminiAPIKey != "" {
		providers = append(providers, ProviderInstance{
			Name:     "gemini",
			Embedder: NewOpenAIClient("https://generativelanguage.googleapis.com/v1beta/openai", cfg.GeminiAPIKey),
			Local:    false,
		})
	}

	// 2. Groq
	if cfg.GroqAPIKey != "" {
		providers = append(providers, ProviderInstance{
			Name:     "groq",
			Embedder: NewOpenAIClient("https://api.groq.com/openai/v1", cfg.GroqAPIKey),
			Local:    false,
		})
	}

	// 3. OpenRouter
	if cfg.OpenRouterAPIKey != "" {
		providers = append(providers, ProviderInstance{
			Name:     "openrouter",
			Embedder: NewOpenAIClient("https://openrouter.ai/api/v1", cfg.OpenRouterAPIKey),
			Local:    false,
		})
	}

	// 4. Ollama Cloud
	if cfg.OllamaCloudAPIKey != "" {
		providers = append(providers, ProviderInstance{
			Name:     "ollama-cloud",
			Embedder: NewOpenAIClient("https://ollama.com/v1", cfg.OllamaCloudAPIKey),
			Local:    false,
		})
	}

	// 5. Ollama Local (sempre disponível como fallback)
	providers = append(providers, ProviderInstance{
		Name:     "ollama",
		Embedder: NewOllamaClient(cfg.OllamaURL),
		Local:    true,
	})

	// Prepend override customizado se as variáveis principais de EMBED_* estiverem setadas
	if cfg.Provider != "" {
		endpoint := cfg.Endpoint
		if endpoint == "" {
			if cfg.Provider == "ollama" {
				endpoint = cfg.OllamaURL
			} else {
				endpoint = "https://api.openai.com/v1"
			}
		}

		var emb Embedder
		if cfg.Provider == "openai" {
			emb = NewOpenAIClient(endpoint, cfg.APIKey)
		} else {
			emb = NewOllamaClient(endpoint)
		}

		providers = append([]ProviderInstance{{
			Name:     "custom",
			Embedder: emb,
			Local:    cfg.Provider == "ollama",
		}}, providers...)
	}

	return NewChainEmbedder(providers, cfg.Privacy)
}

func inferDims(model string) int {
	model = strings.ToLower(model)
	switch {
	case strings.Contains(model, "nomic"):
		return 768
	case strings.Contains(model, "bge-m3"), strings.Contains(model, "mxbai"), strings.Contains(model, "qwen3"):
		return 1024
	case strings.Contains(model, "gemini-embedding-2"), strings.Contains(model, "text-embedding-3-large"):
		return 3072
	default:
		return 0
	}
}
