-- Workers seen by dispatch, and wake-ups for long-polling dispatchers.

-- +goose Up
CREATE TABLE nodes (
  id           uuid PRIMARY KEY,
  name         text NOT NULL,
  cloud        text NOT NULL CHECK (cloud IN ('aws', 'azure')),
  queues       text[] NOT NULL,
  types        text[] NOT NULL,
  version      text NOT NULL,
  last_seen_at timestamptz NOT NULL DEFAULT now()
);

-- A hint only: dispatchers also poll every second, so a lost notification costs latency,
-- never correctness. Postgres folds identical notifications within one transaction.
-- +goose StatementBegin
CREATE FUNCTION notify_jobs_available() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_notify('jobs_available', '');
  RETURN NULL;
END $$;
-- +goose StatementEnd

CREATE TRIGGER jobs_available
  AFTER INSERT OR UPDATE OF state ON jobs
  FOR EACH ROW WHEN (NEW.state = 'available')
  EXECUTE FUNCTION notify_jobs_available();

-- +goose Down
DROP TRIGGER jobs_available ON jobs;
DROP FUNCTION notify_jobs_available();
DROP TABLE nodes;
