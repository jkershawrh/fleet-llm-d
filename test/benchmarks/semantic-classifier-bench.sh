#!/bin/bash
# llm-d-semantic-classifier benchmark on Oberon
# Tests: classification accuracy, latency profile, throughput, edge cases
#
# Usage:
#   ./test/benchmarks/semantic-classifier-bench.sh [host:port]
#   ./test/benchmarks/semantic-classifier-bench.sh localhost:50051
#
# Prerequisites:
#   - grpcurl installed (brew install grpcurl)
#   - Python 3.12+ with no extra deps required
#   - llm-d-sc running at the specified endpoint

set -euo pipefail

ENDPOINT="${1:-localhost:50051}"
RESULTS_DIR="test/benchmarks/reports"
TIMESTAMP=$(date +%Y%m%d-%H%M)

mkdir -p "$RESULTS_DIR"

echo "═══════════════════════════════════════════════════"
echo "  llm-d-semantic-classifier benchmark"
echo "  Endpoint: ${ENDPOINT}"
echo "  Time:     $(date)"
echo "═══════════════════════════════════════════════════"
echo

# Verify connectivity
echo "Checking connectivity..."
if ! grpcurl -plaintext "${ENDPOINT}" list >/dev/null 2>&1; then
    echo "ERROR: Cannot reach ${ENDPOINT} via gRPC"
    echo "  Ensure llm-d-sc is running and grpcurl is installed"
    echo "  For port-forward: oc port-forward svc/llm-d-semantic-classifier 50051:50051 -n fleet-llm-d"
    exit 1
fi
echo "Connected to ${ENDPOINT}"
echo

# Run the benchmark
python3 test/benchmarks/semantic_classifier_bench.py "${ENDPOINT}"

REPORT=$(ls -t "${RESULTS_DIR}"/semantic-classifier-*.md 2>/dev/null | head -1)
if [ -n "$REPORT" ]; then
    echo
    echo "Report saved to: ${REPORT}"
fi
