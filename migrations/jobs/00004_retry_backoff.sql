-- One definition of retry delay, used by both Fail and the lease reaper:
-- exponential (1 s, 2 s, 4 s, ...) capped at 10 minutes, with full jitter.

-- +goose Up
CREATE FUNCTION retry_backoff(attempt int) RETURNS interval LANGUAGE sql VOLATILE AS $$
  SELECT LEAST(interval '10 minutes', interval '1 second' * power(2, GREATEST(attempt, 1) - 1)) * random()
$$;

-- +goose Down
DROP FUNCTION retry_backoff(int);
