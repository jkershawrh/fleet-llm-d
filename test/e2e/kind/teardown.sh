#!/usr/bin/env bash
# Tear down the three-cluster Kind environment.
set -euo pipefail

for cluster in fleet-hub fleet-spoke-1 fleet-spoke-2; do
  if kind get clusters 2>/dev/null | grep -q "^${cluster}$"; then
    kind delete cluster --name "$cluster"
    echo "Deleted $cluster"
  fi
done
echo "Kind clusters cleaned up"
