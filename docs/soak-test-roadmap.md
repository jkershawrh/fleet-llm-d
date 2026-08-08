# Soak Test Roadmap — Next Phase

## Completed

| Test | Ops | Result | What it proved |
|------|-----|--------|----------------|
| Cascade (smoke→pressure) | 9,778 | 100%, 9/9 SLO | All 17 phases work across 3 clusters |
| Stress (10x, 16 min) | 6,522 | 100%, 9/9 SLO | Control plane holds at 10x short bursts |
| 24hr-variable (ongoing) | ~1,100+ | 99.5% | Variable intensity over hours, metrics-driven routing |
| SNO crash test | 55,957 | 90.2% before crash | 10x sustained crashes SNO, limit is 8x |

## Next: Targeted Soaks

### 1. Inference Saturation Test
**Goal:** Find the inference ceiling — how many concurrent requests before OVMS/vLLM degrade.

Pure inference load through Praxis, no control plane ops. Ramp from 1 to 100 concurrent requests. Measure:
- Requests/sec at each concurrency level
- TTFT p50/p99 degradation curve
- First error threshold
- GPU utilization at saturation
- CPU OVMS vs GPU vLLM comparison

**Why:** We know the control plane handles ~150 clusters. We don't know how many inference requests per cluster.

### 2. Pen Test Soak
**Goal:** Validate security under sustained attack patterns.

Run `test/security/pen_test.py` against the live cluster for 2 hours:
- Token replay and expiry abuse
- Privilege escalation (viewer → admin)
- Malformed GCL CloudEvents (bad signatures, expired packages, wrong key IDs)
- Oversized payloads (1MB+ intent bodies)
- Model name injection through Praxis
- Rapid auth token generation to exhaust HMAC computation

**Why:** The pen test suite exists but has never run against the live deployment.

### 3. Metric Poisoning Test
**Goal:** Prove the system handles compromised agent data.

A rogue agent reports:
- 0% GPU utilization (attract all traffic)
- 100% GPU utilization (avoid all traffic)
- Negative queue depth
- Impossible throughput (1M tps)
- Rapid oscillation between healthy/unhealthy

Verify the controller doesn't crash, routing doesn't send all traffic to the rogue, and the ledger records the anomalous metrics.

**Why:** No validation that agent-reported metrics are plausible. A compromised spoke could manipulate fleet routing.

### 4. Controller HA Failover Soak
**Goal:** Prove leader election handoff works under load.

Run 2 controller replicas. During sustained soak:
- Kill the leader every 30 minutes
- Measure: time to failover, requests lost during handoff, state consistency after recovery
- Verify no duplicate placement decisions during split-brain window

**Why:** Leader election code exists and passes unit tests. Never tested under real load with real state.

### 5. Ledger Durability Soak
**Goal:** Prove the ledger survives adverse conditions.

During sustained soak:
- Kill the ledger-gateway pod every hour (simulating crashes)
- Verify chain integrity after every restart
- Measure recovery time and entries lost (should be zero — fail-closed)
- Run the full verify chain across all entry types after each recovery

**Why:** The ledger is the compliance backbone. If it loses entries or breaks chains under adverse conditions, the audit trail is unreliable.

### 6. Cross-Cluster Network Partition Test
**Goal:** Prove graceful degradation when spokes go unreachable.

During sustained soak:
- Cut Brutus (delete the ExternalName service) — verify GPU inference fails gracefully, CPU inference continues
- Cut Arena — verify Oberon handles all CPU inference alone
- Restore both — verify routing rebalances
- Measure: error rate during partition, recovery time, routing distribution after heal

**Why:** NodePort bridges are single points of failure. The system needs to degrade gracefully, not cascade-fail.

### 7. Long-Duration Stability (72hr)
**Goal:** Prove no memory leaks, connection pool exhaustion, or state drift over 3 days.

Calm profile (1x, 30s) for 72 hours. Monitor:
- Controller memory and CPU usage trend (should be flat, not growing)
- Postgres connection count (should stay at pool size)
- Ledger entry count growth (linear, not exponential)
- Praxis connection pool health
- Pod restart count (should be zero)

**Why:** 24hr soaks miss slow leaks. Production systems run for weeks.

### 8. Multi-Tenant Isolation Soak
**Goal:** Prove tenant quotas hold under concurrent multi-tenant load.

Create 5 tenants with different quotas. Run concurrent inference from each tenant through Praxis. Verify:
- Token metering is accurate per tenant
- Quota enforcement triggers at the right threshold
- One tenant's burst doesn't starve another
- Cost attribution in chargeback reports is correct

**Why:** Tenant governance is a capability claim. Never tested with real concurrent tenants.

## Priority Order

| # | Test | Effort | Blocked by | Evidence value |
|---|------|--------|-----------|----------------|
| 1 | Inference saturation | 2hr build + 1hr run | Nothing | Highest — answers "how many requests" |
| 2 | Pen test | 30min setup + 2hr run | Nothing | High — security validation |
| 3 | Metric poisoning | 2hr build + 1hr run | Nothing | High — trust boundary validation |
| 4 | Controller HA | 4hr (need 2-replica deploy) | Nothing | Medium — HA proof |
| 5 | Ledger durability | 2hr build + 4hr run | Nothing | Medium — compliance proof |
| 6 | Network partition | 2hr build + 2hr run | Nothing | Medium — resilience proof |
| 7 | 72hr stability | Trivial (change profile) | Time | Medium — leak detection |
| 8 | Multi-tenant | 4hr build + 2hr run | Nothing | Medium — governance proof |
