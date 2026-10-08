-- The sinks database: stand-ins for external systems that check the effect key (G4).
-- Each table's dedupe_key is the effect key; a mutant build replaces it with a random one,
-- and effect_key keeps the real key so the checker can still count per job.

-- +goose Up
CREATE TABLE ledger_accounts (
  account text PRIMARY KEY,
  balance bigint NOT NULL DEFAULT 0
);
CREATE TABLE ledger_entries (
  dedupe_key   text PRIMARY KEY,
  effect_key   text NOT NULL,
  from_account text NOT NULL,
  to_account   text NOT NULL,
  amount       bigint NOT NULL CHECK (amount > 0),
  applied_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ledger_entries_effect ON ledger_entries (effect_key);

CREATE TABLE webhook_deliveries (
  dedupe_key  text PRIMARY KEY,
  effect_key  text NOT NULL,
  body        jsonb NOT NULL,
  received_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX webhook_deliveries_effect ON webhook_deliveries (effect_key);

CREATE TABLE digests (
  dedupe_key text PRIMARY KEY,
  effect_key text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX digests_effect ON digests (effect_key);

-- Every call, including duplicates absorbed and deliberate failures.
CREATE TABLE sink_calls (
  id         bigserial PRIMARY KEY,
  sink       text NOT NULL,
  effect_key text NOT NULL,
  result     text NOT NULL CHECK (result IN ('applied', 'duplicate', 'failed_before', 'failed_after')),
  at         timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE sink_calls;
DROP TABLE digests;
DROP TABLE webhook_deliveries;
DROP TABLE ledger_entries;
DROP TABLE ledger_accounts;
