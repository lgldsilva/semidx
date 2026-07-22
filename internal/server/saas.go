package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/lgldsilva/semidx/internal/store"
)

var errQuotaExceeded = errors.New("tenant quota exceeded")

func (s *Server) enforceProjectQuota(ctx context.Context) error {
	qs, ok := s.store.(store.QuotaStore)
	if !ok {
		return nil
	}
	quota, err := qs.GetTenantQuota(ctx)
	if err != nil {
		return err
	}
	if quota.MaxProjects <= 0 {
		return nil
	}
	usage, err := qs.GetTenantUsage(ctx)
	if err != nil {
		return err
	}
	if usage.Projects >= quota.MaxProjects {
		return fmt.Errorf("%w: project limit %d reached", errQuotaExceeded, quota.MaxProjects)
	}
	return nil
}

func (s *Server) enforceRuntimeEdgeQuota(ctx context.Context, count int) error {
	qs, ok := s.store.(store.QuotaStore)
	if !ok || count <= 0 {
		return nil
	}
	quota, err := qs.GetTenantQuota(ctx)
	if err != nil {
		return err
	}
	if quota.MaxRuntimeEdges <= 0 {
		return nil
	}
	usage, err := qs.GetTenantUsage(ctx)
	if err != nil {
		return err
	}
	if usage.RuntimeEdges+int64(count) > quota.MaxRuntimeEdges {
		return fmt.Errorf("%w: runtime edge limit %d reached", errQuotaExceeded, quota.MaxRuntimeEdges)
	}
	return nil
}

func (s *Server) handleTenantUsage(w http.ResponseWriter, r *http.Request) {
	qs, ok := s.store.(store.QuotaStore)
	if !ok {
		writeJSONError(w, http.StatusNotImplemented, "tenant usage is unavailable")
		return
	}
	quota, err := qs.GetTenantQuota(r.Context())
	if err != nil {
		s.log.Error("tenant quota lookup", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not load tenant quota")
		return
	}
	usage, err := qs.GetTenantUsage(r.Context())
	if err != nil {
		s.log.Error("tenant usage lookup", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "could not load tenant usage")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"quota": quota, "usage": usage})
}
