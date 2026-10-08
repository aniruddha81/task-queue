#!/bin/sh
# Patroni's post_bootstrap hook: runs once, on the first primary, with a superuser URL.
set -e
psql "$1" -v ON_ERROR_STOP=1 -f /init.sql
