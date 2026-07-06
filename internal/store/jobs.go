package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// Job is a durable indexing job.
type Job struct {
	ID            int
	ProjectID     int
	Type          string // "full" | "git_history" | "batch"
	Status        string // queued | running | succeeded | failed
	Error         string
	FilesIndexed  int
	ChunksCreated int
	Payload       string // JSON payload for batch jobs
	DeletedFiles  int
	ErrorCount    int
}

// EnqueueJob queues an indexing job for a project.
func (s *PgStore) EnqueueJob(ctx context.Context, projectID int, jobType string) (int, error) {
	var id int
	err := s.pool.QueryRow(ctx,
		`INSERT INTO index_jobs (project_id, type) VALUES ($1, $2) RETURNING id`,
		projectID, jobType).Scan(&id)
	return id, err
}

// EnqueueBatchJob queues a batch indexing job with a JSON payload.
func (s *PgStore) EnqueueBatchJob(ctx context.Context, projectID int, payload string) (int, error) {
	var id int
	err := s.pool.QueryRow(ctx,
		`INSERT INTO index_jobs (project_id, type, payload) VALUES ($1, 'batch', $2) RETURNING id`,
		projectID, payload).Scan(&id)
	return id, err
}

// ClaimJob atomically claims the oldest queued job and marks it running, using
// FOR UPDATE SKIP LOCKED so concurrent workers never grab the same job. Returns
// (nil, nil) when nothing is queued.
func (s *PgStore) ClaimJob(ctx context.Context) (*Job, error) {
	var j Job
	err := s.pool.QueryRow(ctx, `
		UPDATE index_jobs SET status = 'running', started_at = NOW()
		WHERE id = (
			SELECT id FROM index_jobs WHERE status = 'queued'
			ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1
		)
		RETURNING id, project_id, type, status, COALESCE(payload, '')
	`).Scan(&j.ID, &j.ProjectID, &j.Type, &j.Status, &j.Payload)
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
		SELECT id, project_id, type, status, COALESCE(error, ''), files_indexed, chunks_created,
			COALESCE(payload, ''), deleted_files, error_count
		FROM index_jobs WHERE id = $1
	`, id).Scan(&j.ID, &j.ProjectID, &j.Type, &j.Status, &j.Error, &j.FilesIndexed, &j.ChunksCreated,
		&j.Payload, &j.DeletedFiles, &j.ErrorCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// GetProjectByID returns a project by id, or ErrNotFound.
func (s *PgStore) GetProjectByID(ctx context.Context, id int) (*Project, error) {
	p, err := scanProject(s.pool.QueryRow(ctx, `SELECT `+projectColumns+` FROM projects WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}
