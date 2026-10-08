-- PostgreSQL 18+ (uuidv7). Migrations are expand-only: never edit a released file;
-- renames and drops take two releases (see deployment_plan.md).

-- +goose Up
CREATE TABLE jobs (
  id               uuid PRIMARY KEY DEFAULT uuidv7(),
  owner_id         uuid NOT NULL,                       -- from the verified JWT, never the body
  idempotency_key  text NOT NULL,                       -- required; 'cron:' prefix reserved
  request          jsonb NOT NULL,                      -- original request, for 409 on key reuse
  queue            text NOT NULL,
  type             text NOT NULL,
  payload          jsonb NOT NULL,
  priority         int  NOT NULL DEFAULT 0,
  run_at           timestamptz NOT NULL DEFAULT now(),
  state            text NOT NULL DEFAULT 'available'
                   CHECK (state IN ('available', 'running', 'succeeded', 'dead', 'cancelled')),
  cancel_requested boolean NOT NULL DEFAULT false,
  attempt          int  NOT NULL DEFAULT 0,
  max_attempts     int  NOT NULL DEFAULT 5 CHECK (max_attempts >= 1),
  timeout_seconds  int  NOT NULL DEFAULT 60 CHECK (timeout_seconds BETWEEN 1 AND 3600),
  affinity         text CHECK (affinity IN ('aws', 'azure')),
  lease_token      uuid,
  lease_expires_at timestamptz,
  deadline_at      timestamptz,
  node_id          uuid,
  last_error       text,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  finished_at      timestamptz,
  UNIQUE (owner_id, idempotency_key),
  -- a lease exists exactly while the job is running
  CHECK ((state = 'running') = (lease_token IS NOT NULL AND lease_expires_at IS NOT NULL))
);
CREATE INDEX jobs_claim  ON jobs (queue, priority DESC, run_at) WHERE state = 'available';
CREATE INDEX jobs_leases ON jobs (lease_expires_at)             WHERE state = 'running';

CREATE TABLE job_attempts (
  job_id      uuid NOT NULL REFERENCES jobs (id),
  attempt     int  NOT NULL,                 -- never reused: redrive raises max_attempts
  lease_token uuid NOT NULL UNIQUE,
  node_id     uuid NOT NULL,
  started_at  timestamptz NOT NULL,
  finished_at timestamptz,
  outcome     text CHECK (outcome IN ('succeeded', 'failed', 'lease_expired', 'cancelled')),
  error       text,
  PRIMARY KEY (job_id, attempt)
);

-- +goose Down
DROP TABLE job_attempts;
DROP TABLE jobs;
