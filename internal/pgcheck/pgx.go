package pgcheck

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// connectTimeout bounds the probe: doctor must report a slow or unreachable
// database quickly instead of hanging on a wrong DSN.
const connectTimeout = 5 * time.Second

type pgxQueryer struct{ conn *pgx.Conn }

func (p pgxQueryer) QueryRow(ctx context.Context, sql string, args ...any) Row {
	return p.conn.QueryRow(ctx, sql, args...)
}

// InspectDSN opens a single short-lived connection to dsn and probes it. Unlike
// opening the store, it applies no migrations and creates nothing.
func InspectDSN(ctx context.Context, dsn string) (*Report, error) {
	ctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()

	return Inspect(ctx, pgxQueryer{conn: conn})
}
