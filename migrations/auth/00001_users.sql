-- The auth database: its own database and role, separate from jobs.

-- +goose Up
CREATE TABLE users (
  id            uuid PRIMARY KEY DEFAULT uuidv7(), -- becomes owner_id on jobs
  email         text NOT NULL UNIQUE CHECK (email = lower(email) AND length(email) BETWEEN 3 AND 254),
  password_hash text NOT NULL,                      -- argon2id, PHC string format
  admin         boolean NOT NULL DEFAULT false,
  created_at    timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE users;
