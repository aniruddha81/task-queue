-- 00004 overflowed the interval range from about attempt 44 (2^43 seconds), making Fail and
-- the reaper error out for long-retrying jobs. Cap the exponent first: 2^20 s is far past
-- the 10-minute cap, so delays are unchanged for every attempt that worked before.

-- +goose Up
CREATE OR REPLACE FUNCTION retry_backoff(attempt int) RETURNS interval LANGUAGE sql VOLATILE AS $$
  SELECT LEAST(interval '10 minutes', interval '1 second' * power(2, LEAST(GREATEST(attempt, 1) - 1, 20))) * random()
$$;

-- +goose Down
CREATE OR REPLACE FUNCTION retry_backoff(attempt int) RETURNS interval LANGUAGE sql VOLATILE AS $$
  SELECT LEAST(interval '10 minutes', interval '1 second' * power(2, GREATEST(attempt, 1) - 1)) * random()
$$;
