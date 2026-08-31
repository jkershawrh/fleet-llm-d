Feature: Model Lifecycle Management
  As a model operator
  I want fleet-wide rollout strategies with SLO gates
  So that model updates are deployed safely across clusters with automatic rollback on regression.

  Background:
    Given a fleet with the following clusters:
      | name          | region     | healthy |
      | us-east-prod  | us-east-1  | true    |
      | eu-west-prod  | eu-west-1  | true    |
      | ap-south-prod | ap-south-1 | true    |
    And a FleetInferencePool "llama-70b" serving model "meta-llama/Llama-3.1-70B-Instruct" version "v1.0"
    And the pool is deployed on all clusters with status "Running"

  Scenario: Canary rollout with gradual weight increase gated by SLO checks
    Given a ModelLifecycle "llama-canary-v2" for model "meta-llama/Llama-3.1-70B-Instruct" version "v2.0"
    And the strategy is "canary" with configuration:
      | initialWeight | weightIncrement | interval | rollbackOnFailure | maxFailedChecks |
      | 5             | 10              | 15m      | true              | 3               |
    And the SLO gate requires:
      | maxLatencyP99Ms | minSuccessRate | maxTTFTMs |
      | 500             | 0.995          | 250       |
    When the lifecycle controller starts the rollout
    Then the ModelLifecycle status phase should be "Progressing"
    And the initial canary weight should be 5%
    And 5% of traffic should be routed to version "v2.0"
    When 15 minutes elapse and the canary meets all SLO gates:
      | metric          | observed_value |
      | latency-p99     | 420ms          |
      | success-rate    | 0.998          |
      | ttft            | 180ms          |
    Then the canary weight should increase to 15%
    And a condition "SLOGatePassed" should be set with status "True"
    When the canary weight reaches 100%
    Then the ModelLifecycle status phase should be "Complete"
    And all traffic should be routed to version "v2.0"
    And version "v1.0" replicas should be scaled down

  Scenario: Automatic rollback when SLO regression is detected during canary
    Given a ModelLifecycle "llama-canary-v3" for model "meta-llama/Llama-3.1-70B-Instruct" version "v3.0"
    And the strategy is "canary" with configuration:
      | initialWeight | weightIncrement | interval | rollbackOnFailure | maxFailedChecks |
      | 5             | 10              | 15m      | true              | 3               |
    And the SLO gate requires:
      | maxLatencyP99Ms | minSuccessRate |
      | 500             | 0.995          |
    And the rollout is in progress at canary weight 25%
    When the canary reports SLO violations for 3 consecutive checks:
      | check | latency_p99 | success_rate |
      | 1     | 620ms       | 0.991        |
      | 2     | 750ms       | 0.988        |
      | 3     | 810ms       | 0.985        |
    Then the lifecycle controller should trigger an automatic rollback
    And the ModelLifecycle status phase should be "RolledBack"
    And all traffic should be routed back to version "v1.0"
    And version "v3.0" replicas should be scaled to 0
    And a condition "RollbackTriggered" should be set with status "True"
    And the condition reason should be "SLOGateFailed"
    And the condition message should contain "3 consecutive SLO failures: latency p99 810ms > 500ms, success rate 0.985 < 0.995"
    And a fleet event "ModelRollback" should be emitted

  Scenario: Staged cluster rollout deploys to clusters in a specified order
    Given a ModelLifecycle "llama-staged-v2" for model "meta-llama/Llama-3.1-70B-Instruct" version "v2.0"
    And the strategy is "rolling" with cluster order:
      | order |
      | us-east-prod  |
      | eu-west-prod  |
      | ap-south-prod |
    When the lifecycle controller starts the rollout
    Then the rollout should begin on cluster "us-east-prod" first
    And clusters "eu-west-prod" and "ap-south-prod" should remain on version "v1.0"
    And the per-cluster status for "us-east-prod" should be "Progressing"
    When cluster "us-east-prod" completes the rollout successfully
    Then the per-cluster status for "us-east-prod" should be "Complete"
    And the rollout should proceed to cluster "eu-west-prod"
    And the per-cluster status for "eu-west-prod" should be "Progressing"
    And cluster "ap-south-prod" should still remain on version "v1.0"
    When all clusters complete the rollout
    Then the ModelLifecycle status phase should be "Complete"
    And all per-cluster statuses should be "Complete"

  Scenario: Blue-green deployment performs an atomic traffic switch
    Given a ModelLifecycle "llama-bluegreen-v2" for model "meta-llama/Llama-3.1-70B-Instruct" version "v2.0"
    And the strategy is "blue-green"
    When the lifecycle controller starts the rollout
    Then version "v2.0" should be deployed alongside version "v1.0" on all clusters
    And all traffic should continue to route to version "v1.0" (blue)
    And the ModelLifecycle status phase should be "Progressing"
    When version "v2.0" is healthy on all clusters and the SLO gate passes
    Then the lifecycle controller should perform an atomic traffic switch
    And all traffic should be routed to version "v2.0" (green)
    And the traffic switch should occur within a single reconciliation cycle
    And version "v1.0" replicas should remain available for rollback
    When the rollback window of 30 minutes elapses without issues
    Then version "v1.0" replicas should be scaled down
    And the ModelLifecycle status phase should be "Complete"

  Scenario: Model deployment is recorded in the immutable ledger
    Given a FleetInferencePool "gpt-oss-120b" deployed to cluster "us-east-1"
    When the deployment completes successfully
    Then a ledger entry of type "fleet.model.deployed" should exist
    And the entry should have a valid chain position
    And the entry content should include the model name and cluster
