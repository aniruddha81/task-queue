#!/usr/bin/env bash
# changes.sh <deployed-sha> <sha>: what release <sha> has to do, from the files that differ
# from the running release:
#   docs   only Markdown changed: nothing reaches a server, so no chaos run, build or deploy
#   web    only web/ (and Markdown) changed: no chaos run (the dashboard can't break a job
#          guarantee), and only the gateways, the one role that serves it, are rolled out
#   full   anything else, or anything it can't work out (no running release, unknown commit)
# It checks its own rule first, so a broken rule fails the release instead of quietly
# skipping the chaos run (docs/decisions/0012-every-check-must-be-shown-to-fail.md).
set -euo pipefail

classify() { # changed paths on stdin -> docs | web | full
  local kind=docs f
  while IFS= read -r f; do
    case $f in
    "") ;;
    web/*) kind=web ;;
    *.md) ;;
    *) echo full && return ;;
    esac
  done
  echo $kind
}

expect() { # expect <kind> <path>...
  [ "$(printf '%s\n' "${@:2}" | classify)" = "$1" ] || { echo "changes.sh: rule broken: [${*:2}] is not $1" >&2; exit 1; }
}
expect full internal/queue/store.go
expect full web/app/page.tsx internal/gateway/gateway.go
expect full Dockerfile docs/usage.md
expect full deploy/vm/gateway.compose.yml
expect full .github/workflows/release.yml
expect full migrations/jobs/00008_x.sql
expect web web/app/new/page.tsx docs/usage.md
expect web web/package-lock.json
expect docs README.md docs/design.md
expect docs

# --no-renames: a file moved into docs/ also lists the path it left.
names=$(git diff --no-renames --name-only "$1" "$2" 2>/dev/null) || { echo full; exit 0; }
classify <<<"$names"
