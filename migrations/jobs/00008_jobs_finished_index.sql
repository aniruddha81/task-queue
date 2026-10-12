-- Retention: the leader deletes finished jobs older than the retention period, oldest first.
-- Built CONCURRENTLY, so the live jobs table keeps taking writes while it builds.

-- +goose NO TRANSACTION
-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS jobs_finished ON jobs (finished_at) WHERE finished_at IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS jobs_finished;
