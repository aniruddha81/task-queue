-- Runs once, when the cluster is first bootstrapped. One role per service, each owning only
-- its own database. Passwords come from init-dbs.sh.
CREATE ROLE jobs_svc LOGIN PASSWORD :'jobs_pw';
CREATE ROLE auth_svc LOGIN PASSWORD :'auth_pw';
CREATE DATABASE jobs OWNER jobs_svc;
CREATE DATABASE auth OWNER auth_svc;
REVOKE ALL ON DATABASE jobs FROM PUBLIC;
REVOKE ALL ON DATABASE auth FROM PUBLIC;
