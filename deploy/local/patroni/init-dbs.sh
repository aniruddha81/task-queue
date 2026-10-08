#!/bin/sh
# Patroni's post_bootstrap hook: runs once, on the first primary, with a superuser URL.
# The cloud passes the service passwords in; local development uses fixed ones.
set -e
psql "$1" -v ON_ERROR_STOP=1 \
  -v jobs_pw="${JOBS_DB_PASSWORD:-jobs-local-only}" \
  -v auth_pw="${AUTH_DB_PASSWORD:-auth-local-only}" \
  -f /init.sql
