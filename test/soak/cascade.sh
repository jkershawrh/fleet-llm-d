#!/usr/bin/env bash
set -euo pipefail

# Cascading soak test runner for fleet-llm-d 3-cluster ecosystem.
#
# Runs progressively harder test profiles, stopping on first failure.
# Each level must pass before advancing to the next.
#
# Usage:
#   ./test/soak/cascade.sh                          # run all levels up to standard
#   ./test/soak/cascade.sh --level smoke            # smoke only
#   ./test/soak/cascade.sh --level pressure         # smoke → short → pressure
#   ./test/soak/cascade.sh --level stress           # smoke → short → pressure → stress
#   ./test/soak/cascade.sh --level full              # smoke → short → pressure → stress → standard
#   ./test/soak/cascade.sh --level overnight         # all the way to overnight
#   ./test/soak/cascade.sh --skip-to pressure       # jump straight to pressure (skip smoke/short)

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

FLEET_URL="${FLEET_URL:-https://fleet-controller-fleet-llm-d.apps.oberon.fm2aihpcsed.com}"
LEDGER_URL="${LEDGER_URL:-http://ledger-gateway.immutable-ledger.svc:28099}"
GCL_SIGNING_KEY="${GCL_SIGNING_KEY:-}"
TARGET_LEVEL="${1:-}"
SKIP_TO=""

# Parse args
while [[ $# -gt 0 ]]; do
  case $1 in
    --level) TARGET_LEVEL="$2"; shift 2 ;;
    --skip-to) SKIP_TO="$2"; shift 2 ;;
    --fleet-url) FLEET_URL="$2"; shift 2 ;;
    --ledger-url) LEDGER_URL="$2"; shift 2 ;;
    *) shift ;;
  esac
done

# Cascade levels in order
LEVELS=(smoke short pressure stress standard overnight)

# Map target level to stop index
case "${TARGET_LEVEL:-standard}" in
  smoke)     STOP=0 ;;
  short)     STOP=1 ;;
  pressure)  STOP=2 ;;
  stress)    STOP=3 ;;
  standard|full) STOP=4 ;;
  overnight) STOP=5 ;;
  *)         STOP=4 ;;
esac

# Map skip-to level to start index
START=0
if [[ -n "$SKIP_TO" ]]; then
  for i in "${!LEVELS[@]}"; do
    if [[ "${LEVELS[$i]}" == "$SKIP_TO" ]]; then
      START=$i
      break
    fi
  done
fi

echo "================================================"
echo "  fleet-llm-d Cascading Soak Test"
echo "================================================"
echo ""
echo "  Fleet URL:    $FLEET_URL"
echo "  Ledger URL:   $LEDGER_URL"
echo "  Levels:       ${LEVELS[*]:$START:$((STOP - START + 1))}"
echo "  Clusters:     oberon-sno, arena-xeon6, brutus-h100"
echo ""
echo "  Level descriptions:"
echo "    smoke     — single pass, all 14 phases, no sustained load"
echo "    short     — 5 min sustained, 15s intervals, 2 concurrent"
echo "    pressure  — 10 min sustained, 5s intervals, 5 concurrent"
echo "    stress    — 15 min sustained, 3s intervals, 10 concurrent"
echo "    standard  — 2 hr sustained, 30s intervals, 2 concurrent"
echo "    overnight — 8 hr sustained, 30s intervals, 2 concurrent"
echo ""

RESULTS_DIR="${PROJECT_ROOT}/test/soak/results/$(date +%Y%m%d-%H%M%S)"
mkdir -p "$RESULTS_DIR"

PASS_COUNT=0
FAIL_COUNT=0

for i in $(seq $START $STOP); do
  level="${LEVELS[$i]}"
  echo "──────────────────────────────────────────────"
  echo "  Level $((i+1))/$((STOP+1)): $level"
  echo "──────────────────────────────────────────────"
  echo ""

  OUTFILE="${RESULTS_DIR}/${level}.json"

  python3 "$SCRIPT_DIR/multi_cluster_test.py" \
    --fleet-url "$FLEET_URL" \
    --ledger-url "$LEDGER_URL" \
    --profile "$level" \
    --json \
    ${GCL_SIGNING_KEY:+--gcl-signing-key "$GCL_SIGNING_KEY"} \
    2>&1 | tee "${RESULTS_DIR}/${level}.log"

  EXIT_CODE=${PIPESTATUS[0]}

  if [[ $EXIT_CODE -eq 0 ]]; then
    echo ""
    echo "  ✓ $level PASSED"
    echo ""
    PASS_COUNT=$((PASS_COUNT + 1))
  else
    echo ""
    echo "  ✗ $level FAILED (exit code $EXIT_CODE)"
    echo ""
    echo "  Stopping cascade. Fix issues before advancing."
    echo "  Results: $RESULTS_DIR"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    break
  fi
done

echo ""
echo "================================================"
echo "  Cascade Results"
echo "================================================"
echo ""
echo "  Passed: $PASS_COUNT"
echo "  Failed: $FAIL_COUNT"
echo "  Results: $RESULTS_DIR"
echo ""

if [[ $FAIL_COUNT -eq 0 ]]; then
  echo "  All levels passed. Ready for next level or full soak."
  exit 0
else
  echo "  Fix failures before advancing."
  exit 1
fi
