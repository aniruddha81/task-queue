#!/usr/bin/env bash
# Testing the tester: build each mutant (one safety mechanism removed), run the chaos test
# against it, and require the checker to FAIL. Finally rebuild the clean stack.
#
#   bash deploy/local/mutants.sh [duration]     # default 3m per mutant
set -u
cd "$(dirname "$0")/../.."
compose="docker compose -f deploy/local/compose.yml"
duration="${1:-3m}"
caught=0
missed=()

for m in mutant_nofence mutant_nokey mutant_noeffectkey mutant_noepoch mutant_ackfirst; do
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
echo "caught $caught of 5; missed: ${missed[*]:-none}"
[ ${#missed[@]} -eq 0 ]
