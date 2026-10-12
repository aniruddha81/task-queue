#!/usr/bin/env bash
# Testing the tester: build each mutant (one safety mechanism removed), run the chaos test
# against it, and require the checker to FAIL. Finally rebuild the clean stack.
# Two mechanisms guard gaps a random fault almost never hits (docs/bugs.md), so their
# mutants must fail a deterministic test instead, run against the local stack's server.
#
#   bash deploy/local/mutants.sh [duration]     # default 3m per mutant
set -u
cd "$(dirname "$0")/../.."
compose="docker compose -f deploy/local/compose.yml"
duration="${1:-3m}"
caught=0
missed=()

for m in mutant_nofence mutant_nokey mutant_noeffectkey mutant_ackfirst; do
  echo "=== $m"
  MUTANT=$m $compose up --build -d --wait >/dev/null 2>&1 || MUTANT=$m $compose up --build -d >/dev/null
  sleep 5
  if MUTANT=$m go run ./cmd/torture -duration "$duration" -quiesce 6m > "/tmp/chaos-$m.log" 2>&1; then
    echo "    MISSED: the checker passed a build without this mechanism"
    missed+=("$m")
  else
    echo "    caught: $(grep -E '^\| G[0-9] .*FAIL' "/tmp/chaos-$m.log" | cut -d'|' -f2 | tr -d ' ' | tr '\n' ' ')"
    caught=$((caught + 1))
  fi
done

echo "=== restoring the clean build"
$compose up --build -d >/dev/null

export TEST_DATABASE_URL="${TEST_DATABASE_URL:-postgres://postgres:superuser-local-only@localhost:5432,localhost:5434/postgres?sslmode=require&target_session_attrs=read-write}"
# mutant, test, and the failure that test reports when it catches the mutant (not, say, a
# database it couldn't reach)
for mt in "mutant_noepoch TestPausedLeaderIsFenced stale.chore" \
          "mutant_notick TestTickSchedulesConcurrentlyNoDuplicates want.all.equal"; do
  set -- $mt
  echo "=== $1 ($2)"
  if go test -tags "$1" -count=1 -run "^$2\$" ./internal/scheduler/ > "/tmp/test-$1.log" 2>&1; then
    echo "    MISSED: $2 passed a build without this mechanism"
    missed+=("$1")
  elif grep -q "$3" "/tmp/test-$1.log"; then
    echo "    caught by $2"
    caught=$((caught + 1))
  else
    echo "    ERROR: $2 didn't run (see /tmp/test-$1.log)"
    missed+=("$1")
  fi
done

echo "caught $caught of 6; missed: ${missed[*]:-none}"
[ ${#missed[@]} -eq 0 ]
