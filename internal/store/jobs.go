package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/lgldsilva/semidx/internal/tenant"
)

// Job is a durable indexing job.
type Job struct {
	ID            int
	ProjectID     int
	TenantID      int
	Type          string // "full" | "git_history" | "batch"
	Status        string // queued | running | succeeded | failed
	Error         string
	FilesIndexed  int
	ChunksCreated int
	Payload       string // JSON payload for batch jobs
	DeletedFiles  int
	ErrorCount    int
	ProgressTotal int
	ProgressDone  int
}

// EnqueueJob queues an indexing job for a project.
func (s *PgStore) EnqueueJob(ctx context.Context, projectID int, jobType string) (int, error) {
	var id int
	err := s.pool.QueryRow(ctx,
		`INSERT INTO index_jobs (project_id, type)
		 SELECT id, $2 FROM projects WHERE id = $1 AND tenant_id = $3
		 RETURNING id`, projectID, jobType, tenant.ID(ctx)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// EnqueueBatchJob queues a batch indexing job with a JSON payload.
func (s *PgStore) EnqueueBatchJob(ctx context.Context, projectID int, payload string) (int, error) {
	var id int
	err := s.pool.QueryRow(ctx,
		`INSERT INTO index_jobs (project_id, type, payload)
		 SELECT id, 'batch', $2 FROM projects WHERE id = $1 AND tenant_id = $3
		 RETURNING id`, projectID, payload, tenant.ID(ctx)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// ClaimJob atomically claims the oldest queued job and marks it running, using
// FOR UPDATE SKIP LOCKED so concurrent workers never grab the same job. Returns
// (nil, nil) when nothing is queued.
func (s *PgStore) ClaimJob(ctx context.Context) (*Job, error) {
	var j Job
	err := s.pool.QueryRow(ctx, `
		WITH next_job AS (
			SELECT id FROM index_jobs WHERE status = 'queued'
			ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1
		)
		UPDATE index_jobs AS j SET status = 'running', started_at = NOW()
		FROM next_job
		WHERE j.id = next_job.id
		RETURNING j.id, j.project_id,
			(SELECT p.tenant_id FROM projects p WHERE p.id = j.project_id),
			j.type, j.status, COALESCE(j.payload, '')
	`).Scan(&j.ID, &j.ProjectID, &j.TenantID, &j.Type, &j.Status, &j.Payload)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// ListenJobInsert acquires a dedicated connection and listens for
// job_inserted notifications. Each notification's payload is the inserted
// job ID as a decimal string. The returned channel is closed when ctx is
// cancelled or the connection drops.
//
// Only one LISTEN connection should be active at a time per PgStore; callers
// should not call ListenJobInsert concurrently.
func (s *PgStore) ListenJobInsert(ctx context.Context) (<-chan string, error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, "LISTEN job_inserted"); err != nil {
		conn.Release()
		return nil, err
	}
	ch := make(chan string, 10)
	go func() {
		defer conn.Release()
		defer close(ch)
		for {
			n, err := conn.Conn().WaitForNotification(ctx)
			if err != nil {
				return // context cancelled or connection closed
			}
			select {
			case ch <- n.Payload:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

// UpdateJobProgress updates live counters while a job is running.
func (s *PgStore) UpdateJobProgress(ctx context.Context, id, progressDone, progressTotal, filesIndexed, chunksCreated, errorCount int) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE index_jobs SET
			progress_done = $2,
			progress_total = CASE WHEN $3 > 0 THEN $3 ELSE progress_total END,
			files_indexed = $4,
			chunks_created = $5,
			error_count = $6
		WHERE id = $1 AND status = 'running'
	`, id, progressDone, progressTotal, filesIndexed, chunksCreated, errorCount)
	return err
}

// CompleteJob marks a job succeeded with its result counts.
func (s *PgStore) CompleteJob(ctx context.Context, id, filesIndexed, chunksCreated, deletedFiles, errorCount int) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE index_jobs SET status = 'succeeded', finished_at = NOW(),
			files_indexed = $2, chunks_created = $3, deleted_files = $4, error_count = $5
		WHERE id = $1
	`, id, filesIndexed, chunksCreated, deletedFiles, errorCount)
	return err
}

// FailJob marks a job failed with an error message.
func (s *PgStore) FailJob(ctx context.Context, id int, errMsg string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE index_jobs SET status = 'failed', finished_at = NOW(), error = $2 WHERE id = $1`,
		id, errMsg)
	return err
}

// GetJob returns a job by id, or ErrNotFound.
func (s *PgStore) GetJob(ctx context.Context, id int) (*Job, error) {
	var j Job
	err := s.pool.QueryRow(ctx, `
		SELECT j.id, j.project_id, p.tenant_id, j.type, j.status, COALESCE(j.error, ''), j.files_indexed, j.chunks_created,
			COALESCE(payload, ''), deleted_files, error_count,
			progress_total, progress_done
		FROM index_jobs j JOIN projects p ON p.id = j.project_id
		WHERE j.id = $1 AND p.tenant_id = $2
	`, id, tenant.ID(ctx)).Scan(&j.ID, &j.ProjectID, &j.TenantID, &j.Type, &j.Status, &j.Error, &j.FilesIndexed, &j.ChunksCreated,
		&j.Payload, &j.DeletedFiles, &j.ErrorCount, &j.ProgressTotal, &j.ProgressDone)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// ListRecentJobs returns the newest jobs for a project (highest id first).
// limit defaults to 10 and is capped at 50. projectID 0 lists across all projects.
func (s *PgStore) ListRecentJobs(ctx context.Context, projectID, limit int) ([]Job, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	var (
		rows pgx.Rows
		err  error
	)
	if projectID > 0 {
		rows, err = s.pool.Query(ctx, `SELECT j.id, j.project_id, p.tenant_id, j.type, j.status, COALESCE(j.error, ''), j.files_indexed, j.chunks_created,
			COALESCE(j.payload, ''), j.deleted_files, j.error_count, j.progress_total, j.progress_done
			FROM index_jobs j JOIN projects p ON p.id = j.project_id
			WHERE j.project_id = $1 AND p.tenant_id = $2 ORDER BY j.id DESC LIMIT $3`, projectID, tenant.ID(ctx), limit)
	} else {
		rows, err = s.pool.Query(ctx, `SELECT j.id, j.project_id, p.tenant_id, j.type, j.status, COALESCE(j.error, ''), j.files_indexed, j.chunks_created,
			COALESCE(j.payload, ''), j.deleted_files, j.error_count, j.progress_total, j.progress_done
			FROM index_jobs j JOIN projects p ON p.id = j.project_id
			WHERE p.tenant_id = $1 ORDER BY j.id DESC LIMIT $2`, tenant.ID(ctx), limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.ID, &j.ProjectID, &j.TenantID, &j.Type, &j.Status, &j.Error, &j.FilesIndexed, &j.ChunksCreated,
			&j.Payload, &j.DeletedFiles, &j.ErrorCount, &j.ProgressTotal, &j.ProgressDone); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// GetProjectByID returns a project by id, or ErrNotFound.
func (s *PgStore) GetProjectByID(ctx context.Context, id int) (*Project, error) {
	p, err := scanProject(s.pool.QueryRow(ctx, `SELECT `+projectColumns+` FROM projects WHERE tenant_id = $1 AND ($2 = 0 OR workspace_id = $2) AND id = $3`, tenant.ID(ctx), activeWorkspaceID(ctx), id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}
