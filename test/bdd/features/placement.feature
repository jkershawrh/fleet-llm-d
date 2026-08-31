Feature: Model Placement Policy
  As a fleet operator
  I want the placement controller to enforce constraints and affinities
  So that models are deployed only on clusters that satisfy regulatory,
  hardware, cost, and topology requirements.

  Background:
    Given a fleet with the following clusters:
      | name          | region     | gpu_type     | gpu_count | cost_per_gpu_hour | healthy |
      | us-east-prod  | us-east-1  | nvidia-h100  | 64        | 3.50              | true    |
      | eu-west-prod  | eu-west-1  | nvidia-h100  | 48        | 4.20              | true    |
      | ap-south-prod | ap-south-1 | nvidia-a100  | 32        | 2.10              | true    |
      | us-west-dev   | us-west-2  | nvidia-l40s  | 16        | 1.80              | true    |
      | eu-central    | eu-central-1 | nvidia-h100 | 24       | 3.90              | true    |
    And a FleetInferencePool "llama-70b" for model "meta-llama/Llama-3.1-70B-Instruct"

  Scenario: Regulatory constraint enforcement restricts placement to allowed regions
    Given a PlacementPolicy "gdpr-compliant" with constraints:
      | type       | rule                                                |
      | regulatory | cluster.region in ['eu-west-1', 'eu-central-1']     |
    When the placement controller evaluates "llama-70b" with policy "gdpr-compliant"
    Then the model should be placed only on clusters:
      | cluster      |
      | eu-west-prod |
      | eu-central   |
    And clusters "us-east-prod", "ap-south-prod", "us-west-dev" should be excluded
    And the PlacementPolicy status should show constraintCount 1

  Scenario: Hardware constraint enforcement restricts placement to matching GPU types
    Given a PlacementPolicy "h100-required" with constraints:
      | type     | rule                             |
      | hardware | cluster.gpu.type == 'nvidia-h100' |
    When the placement controller evaluates "llama-70b" with policy "h100-required"
    Then the model should be placed only on clusters:
      | cluster       |
      | us-east-prod  |
      | eu-west-prod  |
      | eu-central    |
    And clusters "ap-south-prod", "us-west-dev" should be excluded
    And each selected cluster should have gpu_type "nvidia-h100"

  Scenario: Cost constraint enforcement places model on cheapest qualifying clusters
    Given a PlacementPolicy "budget-tier" with constraints:
      | type | rule                                |
      | cost | cluster.costPerGPUHour <= 2.50      |
    And the policy has affinity:
      | type           | weight |
      | costEfficiency | 0.9    |
    When the placement controller evaluates "llama-70b" with policy "budget-tier"
    Then the model should be placed only on clusters:
      | cluster       |
      | us-west-dev   |
      | ap-south-prod |
    And the cluster "us-west-dev" should be ranked first due to lowest cost
    And no cluster with costPerGPUHour above 2.50 should be selected

  Scenario: Multi-cluster spreading distributes replicas across regions with maxSkew
    Given a PlacementPolicy "spread-regions" with spreading:
      | topologyKey                    | maxSkew |
      | topology.kubernetes.io/region  | 1       |
    And the policy has constraints:
      | type     | rule                             |
      | hardware | cluster.gpu.type == 'nvidia-h100' |
    And the FleetInferencePool "llama-70b" requests 6 replicas across minClusters 3
    When the placement controller evaluates "llama-70b" with policy "spread-regions"
    Then the replicas should be distributed as:
      | cluster      | replicas |
      | us-east-prod | 2        |
      | eu-west-prod | 2        |
      | eu-central   | 2        |
    And the difference in replica count between any two clusters should not exceed 1
    And the FleetInferencePool status should show clusterCount 3

  Scenario: No feasible placement returns error when no cluster meets constraints
    Given a PlacementPolicy "impossible" with constraints:
      | type       | rule                                           |
      | regulatory | cluster.region in ['ap-northeast-1']            |
      | hardware   | cluster.gpu.type == 'nvidia-h200'               |
    When the placement controller evaluates "llama-70b" with policy "impossible"
    Then the placement should fail with reason "NoFeasibleCluster"
    And the FleetInferencePool "llama-70b" status phase should be "Failed"
    And a condition of type "Placed" with status "False" should be set
    And the condition message should contain "no cluster satisfies all placement constraints"

  Scenario: ModelPack auto-resolves GPU requirements from OCI reference
    Given a FleetInferencePool with ociRef "registry.example.com/models/llama-3-70b:v1"
    When the placement engine resolves the ModelPack config
    Then GPU memory requirement should be computed as approximately 168 GB
    And the placement should select clusters with H200 or B200 GPUs
