-- Leader election (lease + epoch), the evidence table for G7, and cron schedules (G6).

-- +goose Up
CREATE TABLE leader_leases (
  name       text PRIMARY KEY,
  holder     uuid,
  epoch      bigint NOT NULL DEFAULT 0,
  expires_at timestamptz NOT NULL
);
INSERT INTO leader_leases (name, expires_at) VALUES ('scheduler', '-infinity');

-- One row per chore transaction that changed something, written inside that transaction.
CREATE TABLE chore_runs (
  epoch        bigint NOT NULL,
  chore        text NOT NULL,
  started_at   timestamptz NOT NULL,
  committed_at timestamptz NOT NULL
);

CREATE TABLE schedules (
  id           uuid PRIMARY KEY DEFAULT uuidv7(),
  owner_id     uuid NOT NULL,
  name         text NOT NULL,
  cron         text NOT NULL,          -- fixed at creation: a new cadence is a new schedule
  timezone     text NOT NULL,          -- IANA name, e.g. Asia/Kolkata
  template     jsonb NOT NULL,         -- the job to submit, in the POST /v1/jobs body format
  paused       boolean NOT NULL DEFAULT false,
  next_tick_at timestamptz NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (owner_id, name)
);
CREATE INDEX schedules_due ON schedules (next_tick_at) WHERE NOT paused;

-- Exactly one job per tick: a second scheduler can only collide here, never duplicate.
CREATE TABLE schedule_ticks (
  schedule_id uuid NOT NULL REFERENCES schedules (id) ON DELETE CASCADE,
  tick_at     timestamptz NOT NULL,
  job_id      uuid NOT NULL REFERENCES jobs (id),
  PRIMARY KEY (schedule_id, tick_at)
);

-- +goose Down
DROP TABLE schedule_ticks;
DROP TABLE schedules;
DROP TABLE chore_runs;
DROP TABLE leader_leases;
