// Package webchat provides the ChatRAG web chat UI. It is server-rendered
// (html/template, no external JS) and embedded in the binary.
package webchat

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/lgldsilva/semidx/internal/chat"
	"github.com/lgldsilva/semidx/internal/rag"
	"github.com/lgldsilva/semidx/internal/store"
)

// allowPublicEnv, when set to "1", lets the chat server bind to a non-loopback
// address despite having no authentication (operator is fronting it with an
// authenticating proxy). Otherwise such a bind is refused.
const allowPublicEnv = "SEMIDX_CHATRAG_ALLOW_PUBLIC"

//go:embed templates/*.html
var templatesFS embed.FS

// ChatPipeline is the interface the web chat server uses to answer questions.
// Implemented by rag.FantasyPipeline in production, or a test mock.
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

	// convs, when non-nil, persists chat conversations (the local SQLite store
	// implements it). Nil disables the /api/conversations endpoints (501) and
	// the UI hides its sidebar.
	//
	// TODO(follow-up): in remote mode (CLI logged into a semidx server) these
	// endpoints should proxy the server's /admin/api/conversations API instead
	// of a local store; nothing would consume that today, so remote backends
	// simply leave convs nil.
	convs store.ConversationStore
}

// SetConversationStore enables persistent conversations, backed by cs. The web
// chat is single-user, so every conversation is stored under user id 0.
func (s *Server) SetConversationStore(cs store.ConversationStore) { s.convs = cs }

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

// checkBindSafety refuses to expose the unauthenticated chat on a non-loopback
// address, where it would leak indexed code and LLM keys to the network. The
// operator can override with SEMIDX_CHATRAG_ALLOW_PUBLIC=1 (e.g. behind an
// authenticating reverse proxy).
func (s *Server) checkBindSafety() error {
	if isLoopbackListen(s.listenAddr) {
		return nil
	}
	if os.Getenv(allowPublicEnv) == "1" {
		s.log.Warn("web chat bound to a non-loopback address without authentication", "addr", s.listenAddr)
		return nil
	}
	return fmt.Errorf("refusing to bind web chat to non-loopback address %q: the chat has no "+
		"authentication and would expose indexed code and LLM keys on the network — bind to "+
		"127.0.0.1, or set %s=1 if it is behind an authenticating proxy", s.listenAddr, allowPublicEnv)
}

// isLoopbackListen reports whether addr binds only to the loopback interface.
// An empty host (e.g. ":8976") binds to all interfaces and is NOT loopback.
func isLoopbackListen(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ListenAndServe registers routes and starts the HTTP server.
func (s *Server) ListenAndServe() error {
	if err := s.checkBindSafety(); err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/chat", http.StatusFound)
	})
	mux.HandleFunc("GET /chat", s.handleChatPage)
	mux.HandleFunc("POST /api/chat", s.handleChat)
	mux.HandleFunc("POST /api/chat/stream", s.handleChatStream)
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/conversations", s.handleListConversations)
	mux.HandleFunc("POST /api/conversations", s.handleCreateConversation)
	mux.HandleFunc("GET /api/conversations/{id}", s.handleGetConversation)
	mux.HandleFunc("DELETE /api/conversations/{id}", s.handleDeleteConversation)
	mux.HandleFunc("POST /api/conversations/{id}/messages", s.handleAddConversationMessage)

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
