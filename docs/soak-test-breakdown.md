# Ecosystem Capability Soak Test

## Overview

The capability soak exercises 12 fleet-llm-d capabilities every 30 seconds against live infrastructure for 72 hours. It proves the full ecosystem -- fleet-llm-d + GCL + DeepField + ARE Ledger + real inference -- holds up under sustained load.

**Script:** `test/soak/capability_soak.py`

## Capabilities Tested

| # | Capability | What the soak does each cycle | What it proves |
|---|-----------|------------------------------|----------------|
| 1 | **Clusters** | Registers clusters, lists them back | Fleet controller accepts and stores cluster state |
| 2 | **Agent Heartbeats** | POSTs agent status + metrics for each cluster | Per-cluster agents can report health continuously |
| 3 | **Tenants** | Creates tenants with quotas, lists them | Multi-tenant governance persists |
| 4 | **Placement** | Submits models, verifies placement decisions | Constraint solver assigns models to clusters correctly |
| 5 | **Lifecycle** | Creates canary rollouts, promotes/rollbacks | Rollout state machine works under sustained load |
| 6 | **Inference** | Sends real prompt to OVMS granite-2b-cpu, gets completion | Real model inference stays responsive over time |
| 7 | **Observability** | Queries fleet metrics, model metrics, platform metrics | Metrics federation returns live data, not stale |
| 8 | **Ledger** | Calls `/api/verify` on ARE Immutable Ledger | Hash chains maintain integrity as entries grow |
| 9 | **GCL** | Health check on Governed Cognitive Loop | Governance pipeline stays available |
| 10 | **DeepField** | Health check on DeepField | Observation pipeline stays available |
| 11 | **Health** | Hits `/healthz` on fleet-controller | Controller stays responsive under load |
| 12 | **Recovery** | Detects degradation, verifies self-healing | System recovers from transient failures |

## What Hits Real Infrastructure

| Component | What's running | Where |
|-----------|---------------|-------|
| Fleet-controller | Go control plane with PostgreSQL persistence | fleet-llm-d namespace |
| PostgreSQL | State store -- survives controller restarts | fleet-llm-d namespace |
| OVMS inference | Granite 2B on Intel Xeon, sub-second responses | triforce namespace |
| ARE Immutable Ledger | Rust core + Python REST gateway, PostgreSQL-backed | immutable-ledger namespace |
| GCL | Governed Cognitive Loop, prompt classification | governed-cognitive-loop namespace |
| DeepField | Observation and forecast pipeline | fleet-llm-d namespace |
| Praxis AI | Programmable inference gateway, 6 models | fleet-llm-d namespace |

## SLO Gates (72-hour pass criteria)

| Gate | Threshold |
|------|-----------|
| Per-capability success rate | >= 95% |
| Inference success rate | >= 90% |
| Ledger integrity | 100% |
| Availability | >= 99.5% |

## Output Format

The soak prints a rolling table every cycle:

```
Cycle  Time  Clust Agent Tenant Place Life Infer Obs Ledgr Hlth  Err  Status
```

All counts are cumulative successes. `Err` is cumulative errors across all capabilities. `Status` is UP or DOWN per cycle.

## Running the Soak

```bash
# Launch on OpenShift as a Job
make test-soak-capability

# Or run directly
python3 test/soak/capability_soak.py \
  --fleet-url http://fleet-controller.fleet-llm-d.svc:8080 \
  --ledger-url http://192.168.1.123:30099 \
  --inference-url http://ovms-granite-2b.triforce.svc:8080/v3 \
  --inference-model granite-2b-cpu \
  --gcl-url http://gcl-app.governed-cognitive-loop.svc:8000 \
  --deepfield-url http://deepfield-fleet.fleet-llm-d.svc:8000 \
  --profile 72hr
```

## Profiles

| Profile | Duration | Cycle interval |
|---------|----------|---------------|
| `quick` | 10 minutes | 30s |
| `1hr` | 1 hour | 30s |
| `8hr` | 8 hours | 30s |
| `72hr` | 72 hours (3 days) | 30s |

## Current Soak (July 2026)

**Cluster:** Oberon SNO (256 CPUs, Intel Xeon, 512GB RAM)

**Events:**
- **0-8h:** Clean run, 907 cycles, all capabilities UP. Ledger gateway OOM fix applied at cycle ~165 (readiness probe changed from 12MB `GET /api/entries` to TCP socket check, memory limit raised to 1Gi).
- **~8h:** Node rebooted. Soak pod restarted (1 restart). Fleet-controller hit ImagePullBackOff due to internal registry auth expiry.
- **Recovery:** Internal registry pull secret refreshed, controller pod restarted. Soak self-healed at cycle 141 (post-restart count), all capabilities resumed.
- **8h+:** Running clean, all capabilities UP.

**Resilience evidence:** The soak survived a full node reboot, detected the controller outage (reported DOWN), and self-recovered when the controller came back -- without manual intervention on the soak itself.
