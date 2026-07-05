-- +goose Up
-- Notify workers when a new job is inserted, so they can claim it immediately
-- instead of waiting for the next 2s poll cycle.
CREATE OR REPLACE FUNCTION notify_job_insert() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('job_inserted', NEW.id::text);
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_job_insert ON index_jobs;
CREATE TRIGGER trg_job_insert AFTER INSERT ON index_jobs
    FOR EACH ROW EXECUTE FUNCTION notify_job_insert();

-- +goose Down
DROP TRIGGER IF EXISTS trg_job_insert ON index_jobs;
DROP FUNCTION IF EXISTS notify_job_insert();
