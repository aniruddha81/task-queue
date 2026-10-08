-- Runs once, on a fresh volume. One role per service, each owning only its own database.
-- Local development passwords only.
CREATE ROLE jobs_svc LOGIN PASSWORD 'jobs-local-only';
CREATE ROLE auth_svc LOGIN PASSWORD 'auth-local-only';
CREATE DATABASE jobs OWNER jobs_svc;
CREATE DATABASE auth OWNER auth_svc;
REVOKE ALL ON DATABASE jobs FROM PUBLIC;
REVOKE ALL ON DATABASE auth FROM PUBLIC;
