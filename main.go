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
	"github.com/lgldsilva/semidx/internal/embed"
	"github.com/lgldsilva/semidx/internal/search"
	"github.com/lgldsilva/semidx/internal/store"
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
	db, err := store.NewPgStore(ctx, cfg.DatabaseURL)
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

func indexCmd(ctx context.Context, cfg *config.Config, db store.Store, emb embed.Embedder) {
	fs := flag.NewFlagSet("index", flag.ExitOnError)
	projectPath := fs.String("project", "", "Path to project directory")
	model := fs.String("model", "bge-m3", "Embedding model name")
	maxFiles := fs.Int("max-files", 0, "Limit number of files to index (0 = all)")
	gitMode := fs.Bool("git", false, "Also index git history (git log -p)")
	gitSince := fs.String("git-since", "30.days", "Git log --since duration (e.g. 7.days, 1.month)")
	verbose := fs.Bool("verbose", false, "Show detailed progress and errors")
	privacy := fs.Bool("privacy", false, "Force local-only providers (Ollama)")
	_ = fs.Parse(os.Args[2:]) // ExitOnError: Parse exits on failure

	if ce, ok := emb.(*embed.ChainEmbedder); ok {
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

func searchCmd(ctx context.Context, cfg *config.Config, db store.Store, emb embed.Embedder) {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	projectName := fs.String("project", "", "Project name")
	query := fs.String("query", "", "Search query")
	topK := fs.Int("top-k", 5, "Number of results")
	model := fs.String("model", "", "Override embedding model (default: project model)")
	privacy := fs.Bool("privacy", false, "Force local-only providers (Ollama)")
	asJSON := fs.Bool("json", false, "Output results as JSON")
	_ = fs.Parse(os.Args[2:]) // ExitOnError: Parse exits on failure

	if ce, ok := emb.(*embed.ChainEmbedder); ok {
		ce.SetPrivacy(*privacy || cfg.Privacy)
	}

	if *projectName == "" || *query == "" {
		fs.PrintDefaults()
		os.Exit(1)
	}

	svc := search.NewService(db, emb)
	start := time.Now()
	resp, err := svc.Search(ctx, search.Request{Project: *projectName, Query: *query, Model: *model, TopK: *topK})
	if err != nil {
		log.Fatalf("failed to search: %v", err)
	}
	elapsed := time.Since(start)

	if *asJSON {
		if err := (search.JSONFormatter{}).Format(os.Stdout, resp); err != nil {
			log.Fatalf("failed to format results: %v", err)
		}
		return
	}

	fmt.Printf("Searching project: %s (model: %s)\nQuery: %s\n\n", resp.Project.Name, resp.Model, *query)
	if resp.Fallback {
		fmt.Print("[warn] embedding unavailable — used keyword search\n\n")
	}
	fmt.Printf("Found %d results in %v\n\n", len(resp.Results), elapsed)
	_ = (search.HumanFormatter{}).Format(os.Stdout, resp)
}

func modelsCmd(emb embed.Embedder) {
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

func dropCmd(ctx context.Context, db store.Store) {
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

func sgrepCmd(ctx context.Context, cfg *config.Config, db store.Store, emb embed.Embedder) {
	fs := flag.NewFlagSet("sgrep", flag.ExitOnError)
	projectName := fs.String("project", "", "Project name")
	query := fs.String("query", "", "Search query")
	topK := fs.Int("top-k", 5, "Number of results")
	model := fs.String("model", "", "Override embedding model (default: project model)")
	privacy := fs.Bool("privacy", false, "Force local-only providers (Ollama)")
	asJSON := fs.Bool("json", false, "Output results as JSON")
	_ = fs.Parse(os.Args[2:]) // ExitOnError: Parse exits on failure

	if ce, ok := emb.(*embed.ChainEmbedder); ok {
		ce.SetPrivacy(*privacy || cfg.Privacy)
	}

	if *projectName == "" || *query == "" {
		fs.PrintDefaults()
		os.Exit(1)
	}

	svc := search.NewService(db, emb)
	resp, err := svc.Search(ctx, search.Request{Project: *projectName, Query: *query, Model: *model, TopK: *topK})
	if err != nil {
		log.Fatalf("failed to search: %v", err)
	}

	var f search.Formatter = search.GrepFormatter{ProjectPath: resp.Project.Path}
	if *asJSON {
		f = search.JSONFormatter{}
	}
	if err := f.Format(os.Stdout, resp); err != nil {
		log.Fatalf("failed to format results: %v", err)
	}
}

func buildChain(cfg *config.Config) embed.Embedder {
	var providers []embed.ProviderInstance

	// 1. Gemini
	if cfg.GeminiAPIKey != "" {
		providers = append(providers, embed.ProviderInstance{
			Name:     "gemini",
			Embedder: embed.NewOpenAIClient("https://generativelanguage.googleapis.com/v1beta/openai", cfg.GeminiAPIKey),
			Local:    false,
		})
	}

	// 2. Groq
	if cfg.GroqAPIKey != "" {
		providers = append(providers, embed.ProviderInstance{
			Name:     "groq",
			Embedder: embed.NewOpenAIClient("https://api.groq.com/openai/v1", cfg.GroqAPIKey),
			Local:    false,
		})
	}

	// 3. OpenRouter
	if cfg.OpenRouterAPIKey != "" {
		providers = append(providers, embed.ProviderInstance{
			Name:     "openrouter",
			Embedder: embed.NewOpenAIClient("https://openrouter.ai/api/v1", cfg.OpenRouterAPIKey),
			Local:    false,
		})
	}

	// 4. Ollama Cloud
	if cfg.OllamaCloudAPIKey != "" {
		providers = append(providers, embed.ProviderInstance{
			Name:     "ollama-cloud",
			Embedder: embed.NewOpenAIClient("https://ollama.com/v1", cfg.OllamaCloudAPIKey),
			Local:    false,
		})
	}

	// 5. Ollama Local (sempre disponível como fallback)
	providers = append(providers, embed.ProviderInstance{
		Name:     "ollama",
		Embedder: embed.NewOllamaClient(cfg.OllamaURL),
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

		var emb embed.Embedder
		if cfg.Provider == "openai" {
			emb = embed.NewOpenAIClient(endpoint, cfg.APIKey)
		} else {
			emb = embed.NewOllamaClient(endpoint)
		}

		providers = append([]embed.ProviderInstance{{
			Name:     "custom",
			Embedder: emb,
			Local:    cfg.Provider == "ollama",
		}}, providers...)
	}

	return embed.NewChainEmbedder(providers, cfg.Privacy)
}
