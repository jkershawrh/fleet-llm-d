#!/usr/bin/env bash
set -euo pipefail

echo "=== Tearing down fleet-llm-d spoke on Arena ==="

echo "--- Deleting fleet-agent ---"
oc delete deploy/fleet-agent -n fleet-llm-d --ignore-not-found
oc delete svc/fleet-agent -n fleet-llm-d --ignore-not-found

echo "--- Deleting mock-inference ---"
oc delete deploy/mock-inference -n fleet-llm-d --ignore-not-found
oc delete svc/mock-inference -n fleet-llm-d --ignore-not-found

echo "--- Deleting network policies ---"
oc delete networkpolicy default-deny-all -n fleet-llm-d --ignore-not-found
oc delete networkpolicy allow-fleet-agent-egress -n fleet-llm-d --ignore-not-found
oc delete networkpolicy allow-fleet-agent-ingress -n fleet-llm-d --ignore-not-found
oc delete networkpolicy allow-mock-inference-ingress -n fleet-llm-d --ignore-not-found

echo ""
echo "=== Arena spoke teardown complete ==="
