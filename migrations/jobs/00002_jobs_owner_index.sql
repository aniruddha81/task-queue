-- Listing a user's jobs, newest first (UUIDv7 ids sort by creation time).

-- +goose Up
CREATE INDEX jobs_owner ON jobs (owner_id, id DESC);

-- +goose Down
DROP INDEX jobs_owner;
