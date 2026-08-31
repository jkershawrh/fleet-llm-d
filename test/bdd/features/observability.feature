Feature: Fleet Observability
  As a platform operator
  I want unified metrics, dashboards, and SLO reporting across the fleet
  So that I can monitor model performance, attribute costs to tenants,
  and identify SLO compliance issues at a glance.

  Background:
    Given a fleet with the following clusters:
      | name          | region     | healthy |
      | us-east-prod  | us-east-1  | true    |
      | eu-west-prod  | eu-west-1  | true    |
      | ap-south-prod | ap-south-1 | true    |
    And the following models are deployed:
      | model                                   | clusters                                    |
      | meta-llama/Llama-3.1-70B-Instruct       | us-east-prod, eu-west-prod, ap-south-prod   |
      | meta-llama/Llama-3.1-8B-Instruct        | us-east-prod, eu-west-prod                  |
      | mistralai/Mixtral-8x7B-Instruct-v0.1    | ap-south-prod                               |
    And the fleet metrics aggregator is running

  Scenario: Fleet metrics aggregation provides a single pane across all clusters
    Given each cluster reports the following metrics:
      | cluster       | model                                 | throughput_tps | ttft_p99_ms | gpu_utilization | replicas |
      | us-east-prod  | meta-llama/Llama-3.1-70B-Instruct     | 5200           | 145         | 78%             | 4        |
      | eu-west-prod  | meta-llama/Llama-3.1-70B-Instruct     | 4800           | 160         | 72%             | 3        |
      | ap-south-prod | meta-llama/Llama-3.1-70B-Instruct     | 3100           | 190         | 65%             | 2        |
      | us-east-prod  | meta-llama/Llama-3.1-8B-Instruct      | 12000          | 55          | 45%             | 2        |
      | eu-west-prod  | meta-llama/Llama-3.1-8B-Instruct      | 9500           | 62          | 40%             | 1        |
      | ap-south-prod | mistralai/Mixtral-8x7B-Instruct-v0.1  | 7200           | 88          | 58%             | 2        |
    When the metrics aggregator collects fleet-wide metrics
    Then the FleetInferencePool "llama-70b" status should show:
      | totalThroughput | avgTTFT | clusterCount |
      | 13100 tokens/s  | 165ms   | 3            |
    And the aggregated metrics should be available at the Prometheus endpoint "/metrics"
    And the metric "fleet_llmd_model_throughput_tokens_per_second" should have labels for each cluster
    And the metric "fleet_llmd_model_ttft_p99_milliseconds" should have labels for each model
    And the metric "fleet_llmd_cluster_gpu_utilization_ratio" should report per-cluster values

  Scenario: Per-tenant usage dashboard shows accurate cost attribution
    Given the following tenants are active:
      | tenant          | priority | monthlyBudget |
      | acme-corp       | 500      | 10000.00      |
      | startup-inc     | 200      | 2000.00       |
      | research-lab    | 800      | 50000.00      |
    And the tenants have consumed resources this month:
      | tenant       | cluster       | model                              | input_tokens | output_tokens | gpu_hours |
      | acme-corp    | us-east-prod  | meta-llama/Llama-3.1-70B-Instruct  | 5000000      | 2500000       | 120.5     |
      | acme-corp    | eu-west-prod  | meta-llama/Llama-3.1-70B-Instruct  | 3000000      | 1200000       | 85.2      |
      | startup-inc  | us-east-prod  | meta-llama/Llama-3.1-8B-Instruct   | 800000       | 400000        | 12.0      |
      | research-lab | ap-south-prod | meta-llama/Llama-3.1-70B-Instruct  | 20000000     | 10000000      | 450.0     |
      | research-lab | us-east-prod  | meta-llama/Llama-3.1-70B-Instruct  | 15000000     | 8000000       | 380.0     |
    When the usage dashboard queries tenant cost attribution
    Then tenant "acme-corp" should show total cost calculated from GPU hours across clusters:
      | cluster      | gpu_hours | cost_per_gpu_hour | cost    |
      | us-east-prod | 120.5     | 3.50              | 421.75  |
      | eu-west-prod | 85.2      | 4.20              | 357.84  |
    And tenant "acme-corp" total cost should be "779.59"
    And tenant "acme-corp" budget utilization should be "7.8%"
    And the TenantProfile "acme-corp" status should show currentMonthCost "779.59"
    And the usage metrics should be available via "fleet_llmd_tenant_cost_dollars" with tenant and cluster labels

  Scenario: SLO compliance heatmap shows status across all model and cluster combinations
    Given the following SLO targets are defined:
      | model                                 | ttft_p99_target_ms | success_rate_target |
      | meta-llama/Llama-3.1-70B-Instruct     | 200                | 0.995               |
      | meta-llama/Llama-3.1-8B-Instruct      | 100                | 0.999               |
      | mistralai/Mixtral-8x7B-Instruct-v0.1  | 150                | 0.997               |
    And the clusters report the following SLO metrics:
      | cluster       | model                                 | ttft_p99_ms | success_rate | slo_met |
      | us-east-prod  | meta-llama/Llama-3.1-70B-Instruct     | 145         | 0.998        | true    |
      | eu-west-prod  | meta-llama/Llama-3.1-70B-Instruct     | 160         | 0.996        | true    |
      | ap-south-prod | meta-llama/Llama-3.1-70B-Instruct     | 220         | 0.992        | false   |
      | us-east-prod  | meta-llama/Llama-3.1-8B-Instruct      | 55          | 0.999        | true    |
      | eu-west-prod  | meta-llama/Llama-3.1-8B-Instruct      | 110         | 0.998        | false   |
      | ap-south-prod | mistralai/Mixtral-8x7B-Instruct-v0.1  | 88          | 0.998        | true    |
    When the SLO compliance heatmap is generated
    Then the heatmap should show 4 cells as "compliant" (green)
    And the heatmap should show 2 cells as "breaching" (red):
      | cluster       | model                              | breach_reason                  |
      | ap-south-prod | meta-llama/Llama-3.1-70B-Instruct  | ttft_p99 220ms > 200ms target  |
      | eu-west-prod  | meta-llama/Llama-3.1-8B-Instruct   | ttft_p99 110ms > 100ms target  |
    And the overall fleet SLO compliance rate should be "66.7%"
    And the metric "fleet_llmd_slo_compliance" should have value 0 or 1 for each model-cluster pair
    And an alert "SLOBreach" should be active for the 2 non-compliant combinations
