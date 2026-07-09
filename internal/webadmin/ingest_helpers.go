package webadmin

import (
	"context"
	"errors"
	"net/http"

	"github.com/lgldsilva/semidx/internal/indexing"
	"github.com/lgldsilva/semidx/internal/store"
)

type ingestSession struct {
	proj  *store.Project
	model string
	dims  int
	idx   *indexing.Indexer
}

// loadIngestSession resolves the project, embedder, and indexer for browser ingest.
func (a *Admin) loadIngestSession(ctx context.Context, w http.ResponseWriter, name string) (*ingestSession, bool) {
	proj, err := a.store.GetProject(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		writeJSONErr(w, http.StatusNotFound, spaErrProjectNotFound)
		return nil, false
	}
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, msgInternalError)
		return nil, false
	}
	if a.emb == nil {
		writeJSONErr(w, http.StatusServiceUnavailable, "no embedder configured on server")
		return nil, false
	}
	model := proj.Model
	if model == "" {
		model = "bge-m3"
	}
	info, err := a.emb.ModelInfo(ctx, model)
	if err != nil {
		a.log.Warn("model info for ingest", "err", err)
		writeJSONErr(w, http.StatusBadGateway, "embedding model unavailable — configure a provider or use keyword mode on CLI")
		return nil, false
	}
	dims := info.Dims
	if dims <= 0 {
		dims = 1024
	}
	if err := a.store.EnsureChunksTable(ctx, dims); err != nil {
		writeJSONErr(w, http.StatusInternalServerError, "could not prepare storage")
		return nil, false
	}
	idx := indexing.NewIndexer(a.store, a.emb, dims, indexing.IndexerOpts{})
	return &ingestSession{proj: proj, model: model, dims: dims, idx: idx}, true
}
