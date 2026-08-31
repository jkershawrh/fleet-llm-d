Feature: Fleet Routing Policy
  As a fleet gateway operator
  I want inference requests to be routed to the optimal cluster
  So that latency, cost, availability, and tenant requirements are met.

  Background:
    Given a fleet with the following clusters:
      | name          | region     | healthy | avg_latency_ms | kv_cache_hit_rate | cost_per_1k_tokens |
      | us-east-prod  | us-east-1  | true    | 45             | 0.72              | 0.012              |
      | eu-west-prod  | eu-west-1  | true    | 38             | 0.65              | 0.018              |
      | ap-south-prod | ap-south-1 | true    | 62             | 0.41              | 0.008              |
    And a FleetInferencePool "llama-70b" deployed on all clusters
    And the fleet gateway is running with health check interval "10s"

  Scenario: Latency-based routing prefers the local cluster for realtime requests
    Given a FleetRoutingPolicy "latency-first" with strategy "geographic"
    And the policy has a rule:
      | name          | action_preferLocal | action_maxLatencyMs |
      | prefer-local  | true               | 100                 |
    When a request arrives from region "us-east-1" with header "X-Request-Priority: realtime"
    Then the request should be routed to cluster "us-east-prod"
    And the routing decision should include reason "geographic-proximity"
    And the observed latency should be below 100ms

  Scenario: Failover routing reroutes when primary cluster becomes unhealthy
    Given a FleetRoutingPolicy "ha-routing" with strategy "failover"
    And the policy has a rule with failover clusters:
      | name     | primary      | failover_order                  |
      | ha-rule  | us-east-prod | eu-west-prod, ap-south-prod     |
    And health check unhealthyThreshold is 3
    When cluster "us-east-prod" fails 3 consecutive health checks
    Then cluster "us-east-prod" should be marked unhealthy
    And subsequent requests should be routed to cluster "eu-west-prod"
    And a fleet event "ClusterFailover" should be emitted with source "us-east-prod" and target "eu-west-prod"
    When cluster "us-east-prod" passes 3 consecutive health checks
    Then cluster "us-east-prod" should be marked healthy
    And requests should resume routing to cluster "us-east-prod"

  Scenario: KV cache affinity routing prefers cluster with highest cache hit rate
    Given a FleetRoutingPolicy "cache-aware" with strategy "weighted"
    And the policy has a rule:
      | name         | action_kvCacheAffinity |
      | kv-affinity  | true                   |
    And the request prefix hash matches cached entries on:
      | cluster       | prefix_match_score |
      | us-east-prod  | 0.72               |
      | eu-west-prod  | 0.65               |
      | ap-south-prod | 0.41               |
    When a request arrives with a conversational prefix of 2048 tokens
    Then the request should be routed to cluster "us-east-prod"
    And the routing decision should include reason "kv-cache-affinity"
    And the response header "X-KV-Cache-Hit" should be "true"

  Scenario: Cost-optimized routing directs batch workloads to cheapest cluster
    Given a FleetRoutingPolicy "cost-first" with strategy "weighted"
    And the policy has a rule:
      | name          | match_header_X-Workload-Type | action_preferCheapest |
      | batch-cheap   | batch                        | true                  |
    When a request arrives with header "X-Workload-Type: batch"
    Then the request should be routed to cluster "ap-south-prod"
    And the routing decision should include reason "cost-optimized"
    And cluster "ap-south-prod" should have the lowest cost_per_1k_tokens

  Scenario: Tenant-specific routing respects the tenant's allowed cluster list
    Given a TenantProfile "acme-corp" with allowed clusters:
      | cluster      |
      | us-east-prod |
      | eu-west-prod |
    And a FleetRoutingPolicy "tenant-aware" with strategy "geographic"
    And the policy has a rule:
      | name          | match_source | action_preferLocal |
      | tenant-route  | acme-corp    | true               |
    When a request arrives from tenant "acme-corp" from region "ap-south-1"
    Then the request should NOT be routed to cluster "ap-south-prod"
    And the request should be routed to one of:
      | cluster      |
      | us-east-prod |
      | eu-west-prod |
    And the routing decision should include reason "tenant-cluster-restriction"
