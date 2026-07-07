// Package webchat provides the ChatRAG web chat UI. It is server-rendered
// (html/template, no external JS) and embedded in the binary.
package webchat

import (
	"context"
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	"time"

	"github.com/lgldsilva/semidx/internal/chat"
	"github.com/lgldsilva/semidx/internal/rag"
)

//go:embed templates/*.html
var templatesFS embed.FS

// ChatPipeline is the interface the web chat server uses to answer questions.
// Implemented by rag.Pipeline in production, or a test mock.
type ChatPipeline interface {
	Ask(ctx context.Context, question, project string, history []chat.Message) (*rag.Answer, error)

	// StreamAsk returns a channel of streaming chunks along with sources and
	// metadata. The channel is closed when streaming completes.
	StreamAsk(ctx context.Context, question, project string, history []chat.Message) (<-chan chat.StreamChunk, []rag.Source, string, bool, error)
}

// Server serves the web chat HTTP endpoints.
type Server struct {
	pipeline   ChatPipeline
	project    string
	tmpl       *template.Template
	log        *slog.Logger
	listenAddr string
}

// New creates a web chat server. pipeline is the RAG pipeline to use, project is
// the default project name (can be empty), and listenAddr is the HTTP listen
// address (e.g. ":8976").
func New(pipeline ChatPipeline, project string, listenAddr string) (*Server, error) {
	log := slog.With("component", "webchat")
	tmpl, err := template.New("").ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &Server{
		pipeline:   pipeline,
		project:    project,
		tmpl:       tmpl,
		log:        log,
		listenAddr: listenAddr,
	}, nil
}

// ListenAndServe registers routes and starts the HTTP server.
func (s *Server) ListenAndServe() error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/chat", http.StatusFound)
	})
	mux.HandleFunc("GET /chat", s.handleChatPage)
	mux.HandleFunc("POST /api/chat", s.handleChat)
	mux.HandleFunc("POST /api/chat/stream", s.handleChatStream)
	mux.HandleFunc("GET /api/health", s.handleHealth)

	srv := &http.Server{
		Addr:         s.listenAddr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second, // chat responses can be long
		IdleTimeout:  60 * time.Second,
	}
	s.log.Info("starting web chat server", "addr", s.listenAddr)
	return srv.ListenAndServe()
}

// pageData is the data passed to the chat page template.
type pageData struct {
	Project string
}
