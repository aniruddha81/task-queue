-- The gateways' shared Let's Encrypt cache (certificates, account key, challenge tokens),
-- so either gateway can answer a validation and both serve one certificate.

-- +goose Up
CREATE TABLE acme_cache (
  key        text PRIMARY KEY,
  data       bytea NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE acme_cache;
