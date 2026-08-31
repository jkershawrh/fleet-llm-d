#!/usr/bin/env bash
# fleet-llm-d Soak Test
# Runs sustained mixed workload against fleet-controller for a configurable duration.
# Usage: ./run-soak.sh [--url URL] [--duration SECONDS] [--rps RATE] [--token TOKEN]
#
# Default: 2 hours at 10 req/s against localhost:8080
# For 72hr test: ./run-soak.sh --duration 259200 --rps 10

set -euo pipefail

# ---------------------------------------------------------------------------
# Defaults
# ---------------------------------------------------------------------------
URL="http://localhost:8080"
DURATION=7200
RPS=10
TOKEN=""
LEDGER_URL=""
OUTPUT="test/soak/results.json"

# ---------------------------------------------------------------------------
# Parse CLI args
# ---------------------------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    --url)       URL="$2";        shift 2 ;;
    --duration)  DURATION="$2";   shift 2 ;;
    --rps)       RPS="$2";        shift 2 ;;
    --token)     TOKEN="$2";      shift 2 ;;
    --ledger-url) LEDGER_URL="$2"; shift 2 ;;
    --output)    OUTPUT="$2";     shift 2 ;;
    -h|--help)
      head -8 "$0" | tail -6
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      exit 1
      ;;
  esac
done

# ---------------------------------------------------------------------------
# Utility functions
# ---------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMPDIR_SOAK="$(mktemp -d)"
trap 'rm -rf "$TMPDIR_SOAK"' EXIT

timestamp() { date -u +"%Y-%m-%dT%H:%M:%SZ"; }

auth_header() {
  if [[ -n "$TOKEN" ]]; then
    echo "-H" "Authorization: Bearer $TOKEN"
  fi
}

do_curl() {
  local method="$1" path="$2"
  shift 2
  local full_url="${URL}${path}"
  local -a extra_args=()
  if [[ -n "$TOKEN" ]]; then
    extra_args+=(-H "Authorization: Bearer $TOKEN")
  fi
  curl -s -o /dev/null -w '%{http_code} %{time_total}' \
    -X "$method" "${extra_args[@]}" "$@" "$full_url" 2>/dev/null || echo "000 0.000"
}

do_curl_body() {
  local method="$1" path="$2"
  shift 2
  local full_url="${URL}${path}"
  local -a extra_args=()
  if [[ -n "$TOKEN" ]]; then
    extra_args+=(-H "Authorization: Bearer $TOKEN")
  fi
  curl -s -X "$method" "${extra_args[@]}" "$@" "$full_url" 2>/dev/null || echo ""
}

# ---------------------------------------------------------------------------
# Pre-flight checks
# ---------------------------------------------------------------------------
echo "=========================================="
echo "  fleet-llm-d Soak Test"
echo "=========================================="
echo "  URL:       $URL"
echo "  Duration:  ${DURATION}s ($(( DURATION / 3600 ))h $(( (DURATION % 3600) / 60 ))m)"
echo "  RPS:       $RPS"
echo "  Output:    $OUTPUT"
echo "  Ledger:    ${LEDGER_URL:-none}"
echo "=========================================="
echo ""

echo "[preflight] Checking controller health..."
health_resp=$(do_curl GET /healthz)
health_code=$(echo "$health_resp" | awk '{print $1}')
if [[ "$health_code" != "200" ]]; then
  echo "[preflight] FAIL: /healthz returned $health_code (expected 200)" >&2
  exit 1
fi
echo "[preflight] Health check: OK"

if [[ -n "$TOKEN" ]]; then
  echo "[preflight] Verifying auth..."
  auth_resp=$(do_curl GET /api/v1/clusters)
  auth_code=$(echo "$auth_resp" | awk '{print $1}')
  if [[ "$auth_code" == "401" || "$auth_code" == "403" ]]; then
    echo "[preflight] FAIL: Auth token rejected (HTTP $auth_code)" >&2
    exit 1
  fi
  echo "[preflight] Auth check: OK"
fi

# ---------------------------------------------------------------------------
# Counters and state
# ---------------------------------------------------------------------------
TOTAL_REQUESTS=0
TOTAL_ERRORS=0
LATENCIES_FILE="$TMPDIR_SOAK/latencies.txt"
SNAPSHOTS_FILE="$TMPDIR_SOAK/snapshots.json"
REGISTERED_CLUSTERS_FILE="$TMPDIR_SOAK/clusters.txt"
touch "$LATENCIES_FILE" "$SNAPSHOTS_FILE" "$REGISTERED_CLUSTERS_FILE"
echo "[]" > "$SNAPSHOTS_FILE"

START_TIME=$(date +%s)
LAST_SNAPSHOT=0

# Capture initial memory from /metrics
get_memory_bytes() {
  local metrics
  metrics=$(do_curl_body GET /metrics)
  echo "$metrics" | grep -E '^process_resident_memory_bytes ' | awk '{print $2}' || echo "0"
}

MEMORY_START=$(get_memory_bytes)
MEMORY_MAX="$MEMORY_START"

# ---------------------------------------------------------------------------
# Request generators
# ---------------------------------------------------------------------------
random_cluster_name() {
  echo "soak-cluster-$(shuf -i 1000-9999 -n 1 2>/dev/null || echo $RANDOM)"
}

random_registered_cluster() {
  if [[ -s "$REGISTERED_CLUSTERS_FILE" ]]; then
    shuf -n 1 "$REGISTERED_CLUSTERS_FILE" 2>/dev/null || head -1 "$REGISTERED_CLUSTERS_FILE"
  else
    echo ""
  fi
}

send_request() {
  local roll=$(( RANDOM % 100 ))
  local resp code latency

  if (( roll < 40 )); then
    # 40% GET /api/v1/clusters
    resp=$(do_curl GET /api/v1/clusters)
  elif (( roll < 60 )); then
    # 20% POST /api/v1/clusters (register random cluster)
    local cluster_name
    cluster_name=$(random_cluster_name)
    local payload="{\"name\":\"${cluster_name}\",\"region\":\"us-east-1\",\"labels\":{\"env\":\"soak\"}}"
    resp=$(do_curl POST /api/v1/clusters -H "Content-Type: application/json" -d "$payload")
    code=$(echo "$resp" | awk '{print $1}')
    if [[ "$code" == "200" || "$code" == "201" ]]; then
      echo "$cluster_name" >> "$REGISTERED_CLUSTERS_FILE"
    fi
  elif (( roll < 75 )); then
    # 15% GET /api/v1/rollouts
    resp=$(do_curl GET /api/v1/rollouts)
  elif (( roll < 85 )); then
    # 10% GET /healthz
    resp=$(do_curl GET /healthz)
  elif (( roll < 95 )); then
    # 10% GET /metrics (prometheus)
    resp=$(do_curl GET /metrics)
  else
    # 5% DELETE /api/v1/clusters/{random}
    local target
    target=$(random_registered_cluster)
    if [[ -n "$target" ]]; then
      resp=$(do_curl DELETE "/api/v1/clusters/${target}")
    else
      resp=$(do_curl GET /healthz)
    fi
  fi

  code=$(echo "$resp" | awk '{print $1}')
  latency=$(echo "$resp" | awk '{print $2}')

  TOTAL_REQUESTS=$(( TOTAL_REQUESTS + 1 ))

  if [[ "$code" =~ ^[45] ]] || [[ "$code" == "000" ]]; then
    TOTAL_ERRORS=$(( TOTAL_ERRORS + 1 ))
  fi

  echo "$latency" >> "$LATENCIES_FILE"
}

# ---------------------------------------------------------------------------
# Snapshot collection (every 5 minutes)
# ---------------------------------------------------------------------------
collect_snapshot() {
  local now elapsed mem_now err_rate
  now=$(date +%s)
  elapsed=$(( now - START_TIME ))
  mem_now=$(get_memory_bytes)

  # Track max memory
  if command -v bc &>/dev/null; then
    if (( $(echo "$mem_now > $MEMORY_MAX" | bc -l 2>/dev/null || echo 0) )); then
      MEMORY_MAX="$mem_now"
    fi
  else
    # Fallback integer comparison (works for whole numbers)
    local mem_int=${mem_now%%.*}
    local max_int=${MEMORY_MAX%%.*}
    if [[ "$mem_int" -gt "$max_int" ]] 2>/dev/null; then
      MEMORY_MAX="$mem_now"
    fi
  fi

  if [[ "$TOTAL_REQUESTS" -gt 0 ]]; then
    err_rate=$(awk "BEGIN {printf \"%.4f\", $TOTAL_ERRORS / $TOTAL_REQUESTS * 100}")
  else
    err_rate="0.0000"
  fi

  local snapshot
  snapshot=$(cat <<SNAP
{"elapsed_s":${elapsed},"total_requests":${TOTAL_REQUESTS},"total_errors":${TOTAL_ERRORS},"error_rate_pct":${err_rate},"memory_bytes":${mem_now},"timestamp":"$(timestamp)"}
SNAP
)

  echo "[snapshot] ${elapsed}s: requests=${TOTAL_REQUESTS} errors=${TOTAL_ERRORS} err_rate=${err_rate}% mem=${mem_now}"

  # Append to snapshots array
  local current
  current=$(cat "$SNAPSHOTS_FILE")
  echo "$current" | sed "s/]$/,${snapshot}]/" > "$SNAPSHOTS_FILE"
  # Fix leading comma after opening bracket for first entry
  sed -i.bak 's/\[,/[/' "$SNAPSHOTS_FILE" 2>/dev/null || true
  rm -f "${SNAPSHOTS_FILE}.bak"

  # Write ledger checkpoint if configured
  if [[ -n "$LEDGER_URL" ]]; then
    local ledger_payload
    ledger_payload="{\"type\":\"fleet.soak.checkpoint\",\"content\":${snapshot}}"
    curl -s -X POST "$LEDGER_URL" \
      -H "Content-Type: application/json" \
      -d "$ledger_payload" >/dev/null 2>&1 || true
  fi
}

# ---------------------------------------------------------------------------
# Main workload loop
# ---------------------------------------------------------------------------
echo ""
echo "[soak] Starting workload: ${RPS} req/s for ${DURATION}s..."
echo ""

INTERVAL_US=$(awk "BEGIN {printf \"%d\", 1000000 / $RPS}")

END_TIME=$(( START_TIME + DURATION ))

while [[ $(date +%s) -lt $END_TIME ]]; do
  send_request

  # Collect snapshot every 300 seconds (5 minutes)
  now=$(date +%s)
  if (( now - LAST_SNAPSHOT >= 300 )); then
    collect_snapshot
    LAST_SNAPSHOT=$now
  fi

  # Rate limiting: sleep for 1/RPS seconds
  if command -v usleep &>/dev/null; then
    usleep "$INTERVAL_US"
  else
    sleep "$(awk "BEGIN {printf \"%.3f\", 1.0 / $RPS}")"
  fi
done

# Final snapshot
collect_snapshot

# ---------------------------------------------------------------------------
# Compute latency percentiles
# ---------------------------------------------------------------------------
echo ""
echo "[soak] Computing results..."

MEMORY_END=$(get_memory_bytes)

compute_percentile() {
  local pct="$1" file="$2"
  local count
  count=$(wc -l < "$file" | tr -d ' ')
  if [[ "$count" -eq 0 ]]; then
    echo "0.000"
    return
  fi
  local idx
  idx=$(awk "BEGIN {printf \"%d\", ($pct / 100.0) * $count + 0.5}")
  if [[ "$idx" -lt 1 ]]; then idx=1; fi
  if [[ "$idx" -gt "$count" ]]; then idx=$count; fi
  sort -n "$file" | sed -n "${idx}p"
}

SORTED_LAT="$TMPDIR_SOAK/latencies_sorted.txt"
sort -n "$LATENCIES_FILE" > "$SORTED_LAT"

P50=$(compute_percentile 50 "$SORTED_LAT")
P95=$(compute_percentile 95 "$SORTED_LAT")
P99=$(compute_percentile 99 "$SORTED_LAT")

if [[ "$TOTAL_REQUESTS" -gt 0 ]]; then
  ERROR_RATE=$(awk "BEGIN {printf \"%.6f\", $TOTAL_ERRORS / $TOTAL_REQUESTS * 100}")
else
  ERROR_RATE="0.000000"
fi

# Memory growth calculation
if command -v bc &>/dev/null; then
  if [[ "$MEMORY_START" != "0" ]] && [[ -n "$MEMORY_START" ]]; then
    MEMORY_GROWTH=$(echo "scale=2; ($MEMORY_END - $MEMORY_START) / $MEMORY_START * 100" | bc 2>/dev/null || echo "0.00")
  else
    MEMORY_GROWTH="0.00"
  fi
else
  MEMORY_GROWTH="0.00"
fi

# ---------------------------------------------------------------------------
# Pass/fail criteria
# ---------------------------------------------------------------------------
PASS=true

# Error rate < 0.1%
if (( $(awk "BEGIN {print ($ERROR_RATE >= 0.1) ? 1 : 0}") )); then
  echo "[FAIL] Error rate ${ERROR_RATE}% exceeds 0.1% threshold"
  PASS=false
fi

# Memory growth < 50%
if command -v bc &>/dev/null; then
  mem_growth_abs=${MEMORY_GROWTH#-}
  if (( $(echo "$mem_growth_abs >= 50" | bc -l 2>/dev/null || echo 0) )); then
    echo "[FAIL] Memory growth ${MEMORY_GROWTH}% exceeds 50% threshold"
    PASS=false
  fi
fi

# ---------------------------------------------------------------------------
# Print summary
# ---------------------------------------------------------------------------
echo ""
echo "=========================================="
echo "  SOAK TEST SUMMARY"
echo "=========================================="
echo "  Duration:        ${DURATION}s"
echo "  Total Requests:  ${TOTAL_REQUESTS}"
echo "  Total Errors:    ${TOTAL_ERRORS}"
echo "  Error Rate:      ${ERROR_RATE}%"
echo "  Latency p50:     ${P50}s"
echo "  Latency p95:     ${P95}s"
echo "  Latency p99:     ${P99}s"
echo "  Memory Start:    ${MEMORY_START} bytes"
echo "  Memory End:      ${MEMORY_END} bytes"
echo "  Memory Max:      ${MEMORY_MAX} bytes"
echo "  Memory Growth:   ${MEMORY_GROWTH}%"
echo "=========================================="
if $PASS; then
  echo "  Result:          PASS"
else
  echo "  Result:          FAIL"
fi
echo "=========================================="

# ---------------------------------------------------------------------------
# Write results JSON
# ---------------------------------------------------------------------------
OUTPUT_DIR=$(dirname "$OUTPUT")
mkdir -p "$OUTPUT_DIR"

cat > "$OUTPUT" <<RESULTS
{
  "test": "fleet-llm-d-soak",
  "started_at": "$(date -u -r "$START_TIME" +"%Y-%m-%dT%H:%M:%SZ" 2>/dev/null || timestamp)",
  "completed_at": "$(timestamp)",
  "config": {
    "url": "${URL}",
    "duration_s": ${DURATION},
    "target_rps": ${RPS}
  },
  "results": {
    "total_requests": ${TOTAL_REQUESTS},
    "total_errors": ${TOTAL_ERRORS},
    "error_rate_pct": ${ERROR_RATE},
    "latency": {
      "p50_s": ${P50},
      "p95_s": ${P95},
      "p99_s": ${P99}
    },
    "memory": {
      "start_bytes": ${MEMORY_START},
      "end_bytes": ${MEMORY_END},
      "max_bytes": ${MEMORY_MAX},
      "growth_pct": ${MEMORY_GROWTH}
    }
  },
  "snapshots": $(cat "$SNAPSHOTS_FILE"),
  "pass": ${PASS}
}
RESULTS

echo ""
echo "[soak] Results written to: $OUTPUT"

# Exit with appropriate code
if $PASS; then
  exit 0
else
  exit 1
fi
