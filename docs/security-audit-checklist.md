# Fleet-LLM-D Security Audit Checklist

| Field | Value |
|-------|-------|
| **Project** | fleet-llm-d |
| **Version** | 1.0 |
| **Date** | August 2026 |
| **Scope** | Full-stack security audit across control plane, data plane, supply chain, and compliance |
| **Frameworks** | EU AI Act, NIST AI RMF, SOC 2 Type II, NIST 800-53, OCC SR 11-7 |

This checklist covers the security posture of the fleet-llm-d inference orchestration platform across all deployment modes (Hub, Standalone, Federated). Each item should be evaluated against the target compliance frameworks and validated for all customer segments: telco edge, financial services, and sovereign cloud (air-gapped). Items are organized by security domain. All statuses begin at "Not Started" and should be updated as the audit progresses.

---

## Authentication

| Requirement | Status | Notes |
|-------------|--------|-------|
| JWT bearer token issuance and validation on all 15 REST endpoints | Done | `pkg/auth/token.go` GenerateToken/ValidateToken (HMAC-SHA256, not RS256); refresh via `handleRefreshToken` in routes.go; `AuthMiddleware` exempts healthz/readyz/metrics; 10 tests in `test/security/auth_test.go` |
| gRPC mTLS between fleet-controller and fleet-agent | Not Started | Validate certificate rotation strategy. Ensure control-plane-to-data-plane channels use mutual TLS with pinned CA, not system trust store. |
| Cluster registration authentication (fleet-agent bootstrap) | Not Started | Audit the initial trust establishment when a new fleet-agent registers with fleet-controller. Verify one-time bootstrap tokens are single-use and time-bounded. |
| Dashboard authentication and session management | In Progress | The dashboard now uses live controller data and mutations through a same-origin Next.js proxy. The proxy attaches the server-only `FLEET_API_TOKEN`, so the controller credential is not exposed to the browser. Remaining gap: the dashboard still needs end-user authentication, session expiry/logout, and CSRF protection before it is exposed beyond a trusted operator network. |
| Service account credential rotation for PostgreSQL, Kafka, Redis | Done | **All tracks now use K8s Secrets.** Kustomize production-track uses `secretKeyRef` for all sensitive values. Oberon (dev/soak) credentials moved from inline plaintext to `deploy/oberon/secrets.yaml` Secret templates: `fleet-postgres-credentials` (PG password + PG_URL), `ledger-postgres-credentials` (ledger password + DATABASE_URL), `fleet-identity` (HMAC secret + GCL signing key). `fleet-controller.yaml` PG_URL now references `secretKeyRef`. `fleet-postgres.yaml` POSTGRESQL_PASSWORD references `secretKeyRef`. `ledger.yaml` POSTGRESQL_PASSWORD and DATABASE_URL reference `secretKeyRef`. `optional: true` removed from `FLEET_AUTH_SECRET` and `GCL_DECISION_SIGNING_KEY` secretKeyRef so missing secrets fail loudly. Remaining gaps: (1) No automated credential rotation (External Secrets Operator or cert-manager CronJob recommended); (2) Redis has no auth; (3) Placeholder values still committed to git (should migrate to SealedSecrets for production). |
| ARE Immutable Ledger gRPC authentication | Done | **Fail-closed enforcement**: configured-ledger failures now compensate cluster registration/deregistration, tenant creation, drain/activate, and intent admission mutations before returning an error. REST and JSON-RPC cluster registration share the same guarded mutation path. Memory/disabled modes remain non-fatal for development. **Remaining gaps**: (1) Auth token is operator-configured (`LEDGER_GATEWAY_API_TOKEN`); (2) No gRPC client for the canonical protocol; (3) No read-only vs write-only token scoping. |
| fleetctl CLI authentication flow | Done | HMAC-SHA256 bearer tokens with configurable TTL via `fleetctl login`. Custom CA support via --tls-ca. Token sent as Authorization: Bearer header. Gap: tokens stored in memory only (no disk persistence or keychain). |

## Authorization

| Requirement | Status | Notes |
|-------------|--------|-------|
| RBAC enforcement for all 7 CRDs | Done | 4 ClusterRoles in `deploy/kustomize/base/rbac.yaml` (controller, agent, viewer, tenant-admin) |
| Tenant isolation enforcement via TenantProfile | Done | QuotaEnforcer in `pkg/tenant/quota/enforcer.go`; TestTenantCannotAccessOtherTenantUsage in `test/security/auth_test.go` |
| Multi-cluster access control scoping | Done | **K8s RBAC (properly scoped)**: controller RBAC includes the fleet CRDs and Praxis Grid resources; `fleet-agent` remains read-only. Agent status reports must reference a pre-registered cluster. JSON-RPC now requires a valid bearer token with the correct role and TLS, and controller-to-cluster clients always verify certificates using a configured or Kubernetes CA. **Remaining gaps**: (1) Federated peer identity is not cluster-scoped; (2) application RBAC is role-only; (3) JWT claims do not include ClusterID. |
| API endpoint authorization matrix | Done | AuthorizationMiddleware checks roles against HTTP methods; rate limiting in `pkg/auth/ratelimit.go` (per-key token bucket) |
| fleetctl CLI authorization scoping | Done | All CLI commands go through `FleetClient.doRequest` (client.go:179-212) which attaches `Authorization: Bearer` header when token is set. Server-side RBAC enforces role permissions (admin=all, operator=no DELETE, viewer/tenant=GET only). `warnIfNoToken()` in main.go now emits a stderr warning before destructive operations (cluster deregister, rollout promote, rollout rollback) when no `--token` is provided. Remaining gaps: (1) `login` command mints arbitrary-role tokens locally via shared HMAC secret with no server-side audit trail of token issuance; (2) No disk persistence or keychain integration for tokens. |
| Namespace-level isolation in Hub mode | Done | `--namespace` flag in main.go scopes CRDWatcher (watcher.go:166-167) and KubernetesRepository to namespace-scoped API paths. Leader election uses same namespace (leader.go:46-48, fallback `fleet-llm-d`). `WatchEndpoint` (reconciler.go) now validates the `namespace` field on incoming watch events against the configured namespace and rejects mismatches with 403 Forbidden. Namespace is wired into the Reconciler via `SetNamespace()` during controller construction. Remaining gap: WebhookHandler validates CRD specs but does not verify the admission request namespace matches the configured namespace. |
| Gateway routing authorization | Done | Praxis config (`deploy/praxis/praxis-ai-config.yaml`) is pure L7 routing with no auth filters. Authorization enforced at CRD admission: v1beta1 `FleetRoutingPolicy` requires `authorizationRef` with mandatory fields (grantId, subject, audience, expiresAt); CEL validates audience=`fleet-llm-d` and breakGlass requires incidentRef. RBAC: only `fleet-controller` has write access to `fleetroutingpolicies`; `fleet-tenant-admin` and `fleet-viewer` are read-only. `TenantProfile.spec.clusters.allowed/denied` restrictions are now enforced at routing evaluation time: `filterClustersByTenant()` in `pkg/routing/policy/evaluator.go` filters candidate clusters using `AllowedClusters`/`DeniedClusters` fields on `RoutingRequest` before any routing logic runs. Backward compatible: empty restrictions allow all clusters. |
| Admission webhook enforcement | Done | `pkg/controller/webhook.go` validates FleetInferencePool, TenantProfile, PlacementPolicy via K8s AdmissionReview protocol. WebhookHandler dispatches by Kind. Tests in webhook_test.go. |

## Data Protection

| Requirement | Status | Notes |
|-------------|--------|-------|
| TLS in transit for all communication channels | Done | Controller supports `--tls-cert`/`--tls-key` for HTTPS. fleetctl supports `--tls-ca` for custom CA. PostgreSQL connection uses `sslmode=prefer`. **fleet-agent now supports proper CA certificate verification**: `--tls-ca-cert` / `FLEET_TLS_CA_CERT` flag accepts a PEM-encoded CA certificate path. When set, the certificate is loaded via `reqwest::Certificate::from_pem()` and added as a trusted root certificate for both the MetricsReporter and PolicyEnforcer HTTP clients. `danger_accept_invalid_certs` is only used when `--tls-insecure` is set AND no CA cert is configured. Remaining gap: KV transfer (TCP) has no TLS. |
| Secrets management architecture | Done | All deployment tracks now use K8s Secrets via `secretKeyRef`. Kustomize production-track already used `secretKeyRef` for auth secrets. Oberon dev/soak track: `deploy/oberon/secrets.yaml` created with Secret templates for `fleet-identity` (HMAC + GCL signing key), `fleet-postgres-credentials` (PG password + PG_URL), and `ledger-postgres-credentials` (ledger password + DATABASE_URL). All hardcoded credentials removed from `fleet-controller.yaml`, `fleet-postgres.yaml`, and `ledger.yaml` and replaced with `secretKeyRef`. `optional: true` removed from auth secret references so missing secrets fail loudly rather than silently degrading to unauthenticated mode. Remaining gaps: (1) Placeholder values committed to git (should use SealedSecrets or External Secrets Operator); (2) No automated rotation. |
| KV cache encryption during NIXL-based transfer | Not Started | Confirm kv-transfer encrypts KV cache data in transit between clusters. Validate that encryption keys are per-transfer or per-tenant, not shared globally. |
| Data at rest encryption for PostgreSQL fleet state | Not Started | Verify PostgreSQL uses encrypted storage (dm-crypt/LUKS or cloud-provider encryption). Confirm backup encryption. Audit key management for rotation. |
| Kafka message encryption and access control | Not Started | Validate that Kafka (AMQ Streams) topics carrying fleet events use TLS for transport and SASL for authentication. Audit topic-level ACLs for tenant isolation. |
| Redis cache data sensitivity classification | Done | No application code connects to Redis -- zero Redis client imports in Go (`pkg/`, `cmd/`), Rust (`crates/`), or TypeScript (`web/`). Redis exists only as a standalone overlay convenience (`deploy/kustomize/overlays/standalone/redis.yaml`) using stock `redis:7-alpine` with no custom config. All caching uses in-memory Go structs. No sensitive data is cached. ClusterIP-only access, non-root (UID 999). Note: if Redis integration is added in the future, TLS, AUTH, ACLs, and TTL policies must be established before any client code lands. |
| Model weight and artifact protection | Done | `pkg/modelpack/resolver.go` fetches only OCI manifest and config blob (metadata: name, format, precision, param size) -- never weight layers. Zero logging in `pkg/modelpack/`. All Dockerfiles run non-root. **Cosign signature verification now supports cryptographic key verification**: `WithCosignPublicKey(pemData)` option accepts a PEM-encoded ECDSA or RSA public key. When configured alongside `WithRequireSignature(true)`, `verifySignature()` fetches the `.sig` tag manifest, extracts `dev.cosignproject.cosign/signature` annotations from layers, decodes the base64 signature, and verifies it against the configured public key using `ecdsa.VerifyASN1` or `rsa.VerifyPKCS1v15`. Without a key, tag-existence check remains as fallback. **`requireSignature` defaults to true in production overlays**: Hub and Federated kustomize overlays now set `FLEET_MODELPACK_REQUIRE_SIGNATURE=true`. Standalone/dev overlays keep the default (false). Remaining gap: no registry authentication in `doGet()` -- private registries will fail. |
| PII and sensitive data handling in inference logs | Done | Go control plane (`pkg/server/`) does not handle inference content directly -- logs only operational metadata via `log/slog`. `RequestLoggingMiddleware` in `pkg/server/middleware.go` explicitly restricts logged fields to method, path, status, and latency with a security guard comment block prohibiting body/header/query-param logging. The middleware is wired into the handler chain in `run.go`. Rust proxy (`crates/fleet-agent/src/proxy.rs`) reads request bodies (up to 16MB) to forward upstream but only logs upstream URL and body byte count, never body content. Remaining gaps: (1) `RUST_LOG` can be overridden at runtime to enable debug on the Rust side; (2) No application-level log retention policy. Posture is now safe by design on the Go side, safe by omission on the Rust side. |

## Supply Chain

| Requirement | Status | Notes |
|-------------|--------|-------|
| Container image signing with cosign/Sigstore | Done | cosign signing in `.github/workflows/release.yaml` |
| ModelPack OCI provenance verification | Not Started | Validate that ModelPack integration verifies OCI signatures and provenance metadata (SLSA) before deploying models. Confirm signature verification in air-gapped sovereign deployments. |
| SBOM generation for fleet-llm-d components | Done | CycloneDX SBOM generated in `.github/workflows/security.yaml` |
| Go dependency pinning and integrity verification | Done | govulncheck in security.yaml; dependency-review-action@v4 on PRs |
| Rust dependency pinning via Cargo.lock | Done | cargo audit in security.yaml; dependency-review-action@v4 on PRs |
| Build pipeline integrity (SLSA Level 2+) | Not Started | Verify CI/CD pipeline produces signed provenance attestations. Confirm build environment is ephemeral and reproducible. Audit for secret injection in build steps. |
| Dashboard (npm) dependency audit | Done | npm audit in security.yaml |
| Base image provenance and update cadence | Done | UBI base images from registry.access.redhat.com; non-root USER 65534:65534; readOnlyRootFilesystem + drop ALL caps in kustomize |

## Compliance

| Requirement | Status | Notes |
|-------------|--------|-------|
| ARE Ledger hash chain verification | Done | `FleetRecorder.VerifyAllChains()` verifies 5 chain types. `fleetctl verify chains` CLI command. Soak test verified 253 chains over 30 hours. Benchmarks at 10/100/1000 entries. |
| Audit trail completeness for all 11 event types | Done | FleetRecorder records 13 event types: placement, routing, scaling, tenant usage, tenant created, tenant deleted, lifecycle (deploy/promote/rollback), KV cache transfer, cluster registered, cluster deregistered, cluster drain, cluster activated, auth failure, RBAC denial. VerifyAllChains covers all 11 chain types. |
| EU AI Act Article 12 (record-keeping) mapping | Done | See `docs/security/eu-ai-act-article12-mapping.md`. Maps Art. 12(1)-(4) to FleetRecorder event types and ARE Ledger hash chains. Gaps identified: cluster reg/dereg events, tenant CRUD events, OCI signature verification, structured export format. |
| NIST AI RMF mapping (Govern, Map, Measure, Manage) | Done | See `docs/security/nist-ai-rmf-mapping.md`. Crosswalk maps fleet-llm-d to all 4 functions: Govern (GCL + RBAC + PlacementPolicy), Map (placement solver + ModelLifecycle), Measure (observability + soak tests), Manage (autoscaling + rollout). Gaps: bias monitoring (MS-5), operator training (GV-3). |
| SOC 2 Type II evidence collection | Done | See `docs/security/soc2-evidence-collection.md`. Maps Security (CC6/CC7/CC8), Availability (A1), Confidentiality (C1), and Processing Integrity (PI1) criteria to fleet-llm-d controls. Continuous evidence: RBAC audits, ARE Ledger events, CI scan artifacts, cosign signatures, soak test reports. |
| NIST 800-53 control mapping | Done | See `docs/security/nist-800-53-control-mapping.md`. Maps AC (RBAC, rate limiting), AU (FleetRecorder, ARE Ledger), CM (CRDs, kustomize, cosign), IA (HMAC tokens, fleet-agent), SC (NetworkPolicy, TLS), SI (govulncheck, Trivy, SBOM). Delegated controls (PE, MP, CP) documented. |
| OCC SR 11-7 model risk management alignment | Done | See `docs/security/occ-sr-11-7-alignment.md`. Maps ModelLifecycle CRD (validation/canary/stable), FleetInferencePool (inventory), observability metrics (performance monitoring), canary rollout (ongoing validation). Gaps: bias monitoring, model drift detection, model card metadata. |
| Air-gapped compliance evidence export | Done | `fleetctl verify export [filename]` exports chain verification, cluster state, tenant state, rollout state, and fleet metrics as a self-contained JSON file for offline audit. No network connectivity required after export. |

## Network

| Requirement | Status | Notes |
|-------------|--------|-------|
| Network policies for fleet namespace | Done | default-deny-all in kustomize base; per-component ingress/egress allow policies; namespace isolation partial (some ingress allows any namespace) |
| Fleet-to-cluster mTLS enforcement | Not Started | Verify that all communication between the hub fleet-controller and spoke cluster fleet-agents uses mTLS. Confirm certificate validation includes SAN/hostname checks. |
| ARE Ledger network isolation verification | Not Started | Confirm the ARE Immutable Ledger runs on a separate network segment from fleet-llm-d. Validate that only the fleet-controller's gRPC client can reach the ledger endpoint. Audit firewall rules. |
| Kafka (AMQ Streams) TLS and network segmentation | Not Started | Verify Kafka brokers accept only TLS connections. Confirm Kafka is on a dedicated network segment or uses NetworkPolicy to restrict access to fleet components only. |
| Inter-cluster communication security (Federated mode) | Not Started | Audit peer-to-peer communication in Federated deployment mode. Verify that cross-cluster traffic is encrypted and authenticated. Confirm no cluster can impersonate another. |
| Sovereign zone air-gap enforcement | Done | `deploy/kustomize/overlays/airgap/` provides a kustomize overlay that replaces all 6 external container image references with a configurable local mirror registry. `README.md` documents `oc image mirror` and `skopeo copy` commands for all images, plus `ImageContentSourcePolicy` for OpenShift transparent redirect. Remaining gaps: (1) no offline ModelPack mode; (2) DNS egress unscoped (should restrict to kube-dns); (3) HTTPS egress unscoped; (4) HuggingFace library requires pre-populated cache for air-gap. |
| Ingress and load balancer hardening | Not Started | Audit ingress controller configuration for TLS termination, rate limiting, and WAF rules. Verify no debug or admin endpoints are exposed through the ingress. |
| Edge site network resilience (telco 30+ sites) | Not Started | Validate that fleet-agents at edge sites operate correctly during network partitions. Confirm that reconnection uses re-authentication and does not trust stale sessions. |

## Vulnerability Management

| Requirement | Status | Notes |
|-------------|--------|-------|
| govulncheck integration for Go control plane | Done | govulncheck in `.github/workflows/security.yaml` |
| cargo audit integration for Rust data plane | Done | cargo audit in `.github/workflows/security.yaml` |
| npm audit for dashboard frontend | Done | npm audit in `.github/workflows/security.yaml` |
| Trivy container image scanning | Done | Trivy in `.github/workflows/security.yaml` with CRITICAL/HIGH fail threshold |
| CVE response process documentation | Done | See `docs/security/cve-response-process.md`. Triage within 24hr, SLAs by severity (Critical: 48hr, High: 7d, Medium: 30d, Low: 90d), customer notification templates, post-incident review process. References govulncheck, cargo audit, Trivy in `.github/workflows/security.yaml`. |
| Dependency update cadence policy | Done | See `docs/security/dependency-update-policy.md`. Weekly updates for Go/Rust/npm (gated by security scans), monthly for base images. Accept/defer/reject criteria defined. Dependency age SLO: <90 days for direct deps. |
| Penetration testing schedule | Done | See `docs/security/penetration-testing-schedule.md`. Annual third-party pen test covering API fuzzing (15 REST + gRPC), AuthN/AuthZ bypass, tenant isolation escape, cross-cluster escalation. Quarterly automated scans via `test/security/pen_test.py`. |
| Runtime vulnerability monitoring | Not Started | Evaluate and deploy runtime security monitoring (Falco, StackRox, or equivalent) for fleet namespace workloads. Configure alerts for anomalous process execution, network connections, and file access. |
