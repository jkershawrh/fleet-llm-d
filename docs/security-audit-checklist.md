# Fleet-LLM-D Security Audit Checklist

| Field | Value |
|-------|-------|
| **Project** | fleet-llm-d |
| **Version** | 1.0 |
| **Date** | July 2026 |
| **Scope** | Full-stack security audit across control plane, data plane, supply chain, and compliance |
| **Frameworks** | EU AI Act, NIST AI RMF, SOC 2 Type II, NIST 800-53, OCC SR 11-7 |

This checklist covers the security posture of the fleet-llm-d inference orchestration platform across all deployment modes (Hub, Standalone, Federated). Each item should be evaluated against the target compliance frameworks and validated for all customer segments: telco edge, financial services, and sovereign cloud (air-gapped). Items are organized by security domain. All statuses begin at "Not Started" and should be updated as the audit progresses.

---

## Authentication

| Requirement | Status | Notes |
|-------------|--------|-------|
| JWT bearer token issuance and validation on all 15 REST endpoints | Not Started | Verify token signing algorithm (RS256 minimum), expiration enforcement, and refresh flow. Confirm no endpoint is accessible without a valid token. |
| gRPC mTLS between fleet-controller and fleet-agent | Not Started | Validate certificate rotation strategy. Ensure control-plane-to-data-plane channels use mutual TLS with pinned CA, not system trust store. |
| Cluster registration authentication (fleet-agent bootstrap) | Not Started | Audit the initial trust establishment when a new fleet-agent registers with fleet-controller. Verify one-time bootstrap tokens are single-use and time-bounded. |
| Dashboard authentication and session management | Not Started | Confirm session tokens are HttpOnly, Secure, SameSite=Strict. Validate logout invalidates server-side session state. Check idle timeout policy. |
| Service account credential rotation for PostgreSQL, Kafka, Redis | Not Started | Ensure credentials are not embedded in container images or environment variables in plaintext. Verify rotation automation and zero-downtime rollover. |
| ARE Immutable Ledger gRPC authentication | Not Started | Validate that fleet-llm-d authenticates to the ARE Ledger over its separate network using dedicated service credentials. Confirm credentials are scoped to write-only for event submission. |
| fleetctl CLI authentication flow | Not Started | Verify CLI uses short-lived tokens (not long-lived API keys). Confirm token storage on disk is encrypted or uses OS keychain. Audit token revocation path. |

## Authorization

| Requirement | Status | Notes |
|-------------|--------|-------|
| RBAC enforcement for all 7 CRDs | Not Started | Map each CRD (FleetInferencePool, PlacementPolicy, FleetRoutingPolicy, TenantProfile, FleetScalingPolicy, ModelLifecycle, KVCacheTransferPolicy) to required Kubernetes RBAC roles. Verify least-privilege for each controller. |
| Tenant isolation enforcement via TenantProfile | Not Started | Confirm that TenantProfile CRDs enforce hard boundaries on compute quota, model access, and cost limits. Verify no cross-tenant data leakage in shared PostgreSQL tables. |
| Multi-cluster access control scoping | Not Started | Validate that users/service accounts can only access clusters explicitly granted by their role bindings. Test cross-cluster escalation paths in Federated mode. |
| API endpoint authorization matrix | Not Started | Document and verify the authorization policy for each of the 15 REST endpoints. Confirm role-based access (admin, operator, viewer) is enforced server-side, not just in UI. |
| fleetctl CLI authorization scoping | Not Started | Ensure CLI commands respect the authenticated user's RBAC permissions. Verify that cluster-admin operations require explicit elevated credentials. |
| Namespace-level isolation in Hub mode | Not Started | Confirm that the single active fleet-controller restricts operations to the fleet namespace and tenant-scoped sub-namespaces. Leader election is required before any multi-replica HA claim. Audit for namespace escape vectors. |
| Gateway routing authorization | Not Started | Verify fleet-gateway enforces authorization on cross-cluster routing decisions. Confirm that RoutingPolicy CRDs cannot be modified by non-admin tenants. |
| Admission webhook enforcement | Not Started | Validate that a validating admission webhook rejects CRD mutations from unauthorized service accounts. Test bypass scenarios (dry-run, server-side apply). |

## Data Protection

| Requirement | Status | Notes |
|-------------|--------|-------|
| TLS in transit for all communication channels | Not Started | Audit every network path: REST API, gRPC, Kafka producer/consumer, Redis client, PostgreSQL client, inter-cluster gateway, kv-transfer. Minimum TLS 1.2; prefer TLS 1.3. |
| Secrets management architecture | Not Started | Verify PostgreSQL, Kafka (AMQ Streams), Redis, and ARE Ledger credentials are stored in Kubernetes Secrets (or external vault). Audit for secrets in ConfigMaps, env vars, or source code. |
| KV cache encryption during NIXL-based transfer | Not Started | Confirm kv-transfer encrypts KV cache data in transit between clusters. Validate that encryption keys are per-transfer or per-tenant, not shared globally. |
| Data at rest encryption for PostgreSQL fleet state | Not Started | Verify PostgreSQL uses encrypted storage (dm-crypt/LUKS or cloud-provider encryption). Confirm backup encryption. Audit key management for rotation. |
| Kafka message encryption and access control | Not Started | Validate that Kafka (AMQ Streams) topics carrying fleet events use TLS for transport and SASL for authentication. Audit topic-level ACLs for tenant isolation. |
| Redis cache data sensitivity classification | Not Started | Classify data stored in Redis cache. If model metadata or tenant data is cached, verify encryption at rest and access control. Confirm TTL policies prevent stale sensitive data. |
| Model weight and artifact protection | Not Started | Verify that OCI model artifacts pulled via ModelPack are stored with appropriate filesystem permissions. Confirm no model weights are logged or exposed via API responses. |
| PII and sensitive data handling in inference logs | Not Started | Audit inference request/response logging for PII leakage. Confirm log scrubbing or redaction is in place for financial services deployments. Verify log retention policies. |

## Supply Chain

| Requirement | Status | Notes |
|-------------|--------|-------|
| Container image signing with cosign/Sigstore | Not Started | Implement and verify cosign signatures for fleet-controller, fleet-agent, and fleet-gateway images. Configure admission policies to reject unsigned images in all deployment modes. |
| ModelPack OCI provenance verification | Not Started | Validate that ModelPack integration verifies OCI signatures and provenance metadata (SLSA) before deploying models. Confirm signature verification in air-gapped sovereign deployments. |
| SBOM generation for fleet-llm-d components | Not Started | Generate SBOMs (SPDX or CycloneDX) for Go control plane and Rust data plane binaries. Include transitive dependencies. Publish SBOMs alongside container images. |
| Go dependency pinning and integrity verification | Not Started | Verify go.sum integrity checks are enforced in CI. Audit go.mod for unpinned dependencies or replace directives pointing to forks. Run govulncheck against dependency tree. |
| Rust dependency pinning via Cargo.lock | Not Started | Confirm Cargo.lock is committed and enforced in CI (--locked builds). Audit for yanked crates or dependencies with known advisories via cargo audit. |
| Build pipeline integrity (SLSA Level 2+) | Not Started | Verify CI/CD pipeline produces signed provenance attestations. Confirm build environment is ephemeral and reproducible. Audit for secret injection in build steps. |
| Dashboard (npm) dependency audit | Not Started | Run npm audit on dashboard dependencies. Pin versions in package-lock.json. Evaluate and remediate high/critical findings. Remove unused dependencies. |
| Base image provenance and update cadence | Not Started | Verify Docker base images use trusted sources (Red Hat UBI preferred). Confirm base images are rebuilt on a regular cadence (at least monthly) to pick up OS-level security patches. |

## Compliance

| Requirement | Status | Notes |
|-------------|--------|-------|
| ARE Ledger hash chain verification | Not Started | Implement periodic verification that the ARE Ledger hash chain is intact and tamper-evident. Confirm fleet-llm-d can detect and alert on hash chain breaks. Document verification procedure for auditors. |
| Audit trail completeness for all 11 event types | Not Started | Map each of the 11 event types to their trigger points in the codebase. Verify no code path can perform a state-changing action without emitting the corresponding audit event to the ARE Ledger. |
| EU AI Act Article 12 (record-keeping) mapping | Not Started | Document how fleet-llm-d's audit trail satisfies Article 12 requirements for high-risk AI system logging. Map each required record type to the specific event type and data fields captured. |
| NIST AI RMF mapping (Govern, Map, Measure, Manage) | Not Started | Produce a crosswalk document mapping fleet-llm-d capabilities to NIST AI RMF functions. Identify gaps in risk measurement and bias monitoring that require additional tooling. |
| SOC 2 Type II evidence collection | Not Started | Identify SOC 2 trust service criteria (Security, Availability, Confidentiality) relevant to fleet-llm-d. Establish continuous evidence collection for access reviews, change management, and incident response. |
| NIST 800-53 control mapping | Not Started | Map fleet-llm-d security controls to NIST 800-53 rev5 control families (AC, AU, CM, IA, SC, SI). Identify residual risk for controls delegated to the underlying platform (OpenShift, cloud provider). |
| OCC SR 11-7 model risk management alignment | Not Started | Document how fleet-llm-d supports model lifecycle governance (validation, monitoring, inventory) as required by OCC SR 11-7 for financial services customers. Identify gaps in model performance tracking. |
| Air-gapped compliance evidence export | Not Started | Verify that sovereign cloud (air-gapped) deployments can export compliance evidence and audit logs without network connectivity. Confirm offline verification of ARE Ledger hash chains. |

## Network

| Requirement | Status | Notes |
|-------------|--------|-------|
| Network policies for fleet namespace | Not Started | Define and apply Kubernetes NetworkPolicy resources restricting ingress/egress for fleet-controller, fleet-agent, and fleet-gateway pods. Default-deny with explicit allowlists. |
| Fleet-to-cluster mTLS enforcement | Not Started | Verify that all communication between the hub fleet-controller and spoke cluster fleet-agents uses mTLS. Confirm certificate validation includes SAN/hostname checks. |
| ARE Ledger network isolation verification | Not Started | Confirm the ARE Immutable Ledger runs on a separate network segment from fleet-llm-d. Validate that only the fleet-controller's gRPC client can reach the ledger endpoint. Audit firewall rules. |
| Kafka (AMQ Streams) TLS and network segmentation | Not Started | Verify Kafka brokers accept only TLS connections. Confirm Kafka is on a dedicated network segment or uses NetworkPolicy to restrict access to fleet components only. |
| Inter-cluster communication security (Federated mode) | Not Started | Audit peer-to-peer communication in Federated deployment mode. Verify that cross-cluster traffic is encrypted and authenticated. Confirm no cluster can impersonate another. |
| Sovereign zone air-gap enforcement | Not Started | Validate that air-gapped deployments have no egress to external networks. Confirm model pulls, updates, and telemetry operate entirely within the air-gapped boundary. Test for DNS/NTP leaks. |
| Ingress and load balancer hardening | Not Started | Audit ingress controller configuration for TLS termination, rate limiting, and WAF rules. Verify no debug or admin endpoints are exposed through the ingress. |
| Edge site network resilience (telco 30+ sites) | Not Started | Validate that fleet-agents at edge sites operate correctly during network partitions. Confirm that reconnection uses re-authentication and does not trust stale sessions. |

## Vulnerability Management

| Requirement | Status | Notes |
|-------------|--------|-------|
| govulncheck integration for Go control plane | Not Started | Add govulncheck to CI pipeline for fleet-controller. Triage and remediate findings. Establish policy for blocking merges on high/critical vulnerabilities. |
| cargo audit integration for Rust data plane | Not Started | Add cargo audit to CI pipeline for fleet-agent, fleet-gateway, and kv-transfer. Configure advisory database updates. Block releases on unresolved advisories. |
| npm audit for dashboard frontend | Not Started | Integrate npm audit into dashboard CI pipeline. Establish remediation SLAs: critical within 48 hours, high within 7 days. Track audit findings over time. |
| Trivy container image scanning | Not Started | Scan fleet-controller, fleet-agent, and fleet-gateway images with Trivy in CI. Configure severity thresholds to block image promotion on critical/high findings. Include OS package and language-specific scanning. |
| CVE response process documentation | Not Started | Document the end-to-end CVE response process: triage, impact assessment, patching, customer notification, and post-incident review. Define SLAs by severity for each customer segment. |
| Dependency update cadence policy | Not Started | Establish a policy for regular dependency updates (e.g., weekly automated PRs via Dependabot/Renovate). Define criteria for accepting, deferring, or rejecting updates. Track dependency age. |
| Penetration testing schedule | Not Started | Schedule annual (minimum) penetration testing covering all deployment modes. Include API fuzzing of the 15 REST endpoints and gRPC interfaces. Ensure findings feed back into this checklist. |
| Runtime vulnerability monitoring | Not Started | Evaluate and deploy runtime security monitoring (Falco, StackRox, or equivalent) for fleet namespace workloads. Configure alerts for anomalous process execution, network connections, and file access. |
