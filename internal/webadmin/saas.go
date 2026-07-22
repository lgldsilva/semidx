package webadmin

import (
	"context"
	"errors"
	"fmt"

	"github.com/lgldsilva/semidx/internal/store"
)

var errTenantQuotaExceeded = errors.New("tenant quota exceeded")

func enforceProjectQuota(ctx context.Context, st store.Store) error {
	qs, ok := st.(store.QuotaStore)
	if !ok {
		return nil
	}
	quota, err := qs.GetTenantQuota(ctx)
	if err != nil || quota.MaxProjects <= 0 {
		return err
	}
	usage, err := qs.GetTenantUsage(ctx)
	if err != nil {
		return err
	}
	if usage.Projects >= quota.MaxProjects {
		return fmt.Errorf("%w: project limit %d reached", errTenantQuotaExceeded, quota.MaxProjects)
	}
	return nil
}
