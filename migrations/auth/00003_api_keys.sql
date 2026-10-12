-- API keys: a service exchanges one for a short-lived token (POST /v1/auth/token) instead of
-- signing in with a person's password. Only the key's SHA-256 is stored: it is 256 random
-- bits, so a fast hash is enough, and a leaked table reveals no usable key.

-- +goose Up
CREATE TABLE api_keys (
  id           uuid PRIMARY KEY DEFAULT uuidv7(),
  user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  name         text NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
  hash         bytea NOT NULL UNIQUE,
  created_at   timestamptz NOT NULL DEFAULT now(),
  last_used_at timestamptz
);
CREATE INDEX api_keys_user ON api_keys (user_id);

-- +goose Down
DROP TABLE api_keys;
