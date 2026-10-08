-- Evidence for G3: a result that arrived after its lease was lost (and was discarded)
-- is recorded on its own attempt. The chaos checker counts these; the dashboard shows them.

-- +goose Up
ALTER TABLE job_attempts ADD COLUMN late_result text CHECK (late_result IN ('complete', 'fail'));
ALTER TABLE job_attempts ADD COLUMN late_at timestamptz;

-- +goose Down
ALTER TABLE job_attempts DROP COLUMN late_at;
ALTER TABLE job_attempts DROP COLUMN late_result;
