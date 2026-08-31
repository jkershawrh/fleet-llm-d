#!/usr/bin/env bash
set -euo pipefail

# fleet-llm-d Production Test Suite
# Runs all test types in order with pass/fail gating.
# Usage: ./run-all.sh --url http://localhost:8080 [--secret changeme] [--skip-soak]

URL="${HARNESS_URL:-http://localhost:8080}"
METRICS="${HARNESS_METRICS:-http://localhost:9091}"
SECRET="${HARNESS_SECRET:-}"
TOKEN="${HARNESS_TOKEN:-}"
SKIP_SOAK=false
SOAK_PROFILE="quick"
OUTPUT_DIR="test/production/results"
REPORT=""

while [[ $# -gt 0 ]]; do
  case $1 in
    --url) URL="$2"; shift 2 ;;
    --metrics) METRICS="$2"; shift 2 ;;
    --secret) SECRET="$2"; shift 2 ;;
    --token) TOKEN="$2"; shift 2 ;;
    --skip-soak) SKIP_SOAK=true; shift ;;
    --soak-profile) SOAK_PROFILE="$2"; shift 2 ;;
    --output) OUTPUT_DIR="$2"; shift 2 ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

mkdir -p "$OUTPUT_DIR"

HARNESS_FLAGS="--url=$URL --metrics=$METRICS --output=$OUTPUT_DIR/harness.json"
if [ -n "$TOKEN" ]; then HARNESS_FLAGS="$HARNESS_FLAGS --token=$TOKEN"; fi
if [ -n "$SECRET" ]; then HARNESS_FLAGS="$HARNESS_FLAGS --secret=$SECRET"; fi

PASSED=0
FAILED=0
SKIPPED=0
RESULTS=()

run_test() {
  local name="$1"
  local cmd="$2"
  local gate="${3:-false}"

  echo ""
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "  $name"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

  local start=$(date +%s)
  if eval "$cmd" > "$OUTPUT_DIR/${name// /_}.log" 2>&1; then
    local duration=$(($(date +%s) - start))
    echo "  PASS (${duration}s)"
    RESULTS+=("PASS  $name  (${duration}s)")
    PASSED=$((PASSED + 1))
  else
    local duration=$(($(date +%s) - start))
    echo "  FAIL (${duration}s)"
    RESULTS+=("FAIL  $name  (${duration}s)")
    FAILED=$((FAILED + 1))
    if [ "$gate" = "true" ]; then
      echo ""
      echo "  GATE FAILURE: $name failed. Stopping test suite."
      print_report
      exit 1
    fi
  fi
}

print_report() {
  echo ""
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "  PRODUCTION TEST SUITE REPORT"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo ""
  for result in "${RESULTS[@]}"; do
    echo "  $result"
  done
  echo ""
  echo "  Total: $((PASSED + FAILED + SKIPPED)) tests"
  echo "  Passed: $PASSED"
  echo "  Failed: $FAILED"
  echo "  Skipped: $SKIPPED"
  echo ""
  if [ "$FAILED" -eq 0 ]; then
    echo "  RESULT: ALL TESTS PASSED"
  else
    echo "  RESULT: $FAILED TEST(S) FAILED"
  fi
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

  # Write JSON summary
  cat > "$OUTPUT_DIR/summary.json" << JSONEOF
{
  "timestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "target": "$URL",
  "passed": $PASSED,
  "failed": $FAILED,
  "skipped": $SKIPPED,
  "result": "$([ "$FAILED" -eq 0 ] && echo "PASS" || echo "FAIL")"
}
JSONEOF
}

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  fleet-llm-d Production Test Suite"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Target: $URL"
echo "  Output: $OUTPUT_DIR"
echo "  Soak:   $( [ "$SKIP_SOAK" = true ] && echo "skipped" || echo "$SOAK_PROFILE" )"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# Build harness
echo ""
echo "Building test harness..."
go build -o bin/fleet-harness ./test/harness 2>&1

# ── Phase 1: Smoke (GATE) ──
run_test "Smoke" "./bin/fleet-harness --suite=smoke $HARNESS_FLAGS" true

# ── Phase 2: Security ──
run_test "Security (auth)" "go test -tags=security ./test/security/..."
if [ -n "$SECRET" ]; then
  run_test "Security (pen test)" "python3 test/security/pen_test.py --fleet-url=$URL --auth-secret=$SECRET"
  run_test "Security (redteam)" "./bin/fleet-harness --suite=redteam $HARNESS_FLAGS"
fi

# ── Phase 3: Pressure + Chaos ──
run_test "Pressure" "./bin/fleet-harness --suite=pressure $HARNESS_FLAGS"
run_test "Chaos" "./bin/fleet-harness --suite=chaos $HARNESS_FLAGS"
run_test "Chaos Recovery" "./bin/fleet-harness --suite=chaos-recovery $HARNESS_FLAGS"

# ── Phase 4: Performance ──
run_test "Latency" "./bin/fleet-harness --suite=latency $HARNESS_FLAGS"
run_test "Throughput" "./bin/fleet-harness --suite=throughput $HARNESS_FLAGS"
run_test "Stress" "./bin/fleet-harness --suite=stress $HARNESS_FLAGS"

# ── Phase 5: Scale ──
run_test "Scale" "./bin/fleet-harness --suite=scale $HARNESS_FLAGS"

# ── Phase 6: Soak ──
if [ "$SKIP_SOAK" = false ]; then
  run_test "Capability Soak ($SOAK_PROFILE)" \
    "python3 test/soak/capability_soak.py --fleet-url=$URL --profile=$SOAK_PROFILE --timeout=120"
else
  SKIPPED=$((SKIPPED + 1))
  RESULTS+=("SKIP  Capability Soak")
fi

print_report
