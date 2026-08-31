Feature: Fleet Autoscaling Policy
  As a fleet operator
  I want the autoscaler to adjust replicas based on SLO metrics and cost constraints
  So that inference workloads maintain performance targets without exceeding GPU budgets.

  Background:
    Given a fleet with the following clusters:
      | name          | region     | gpu_type    | gpu_count | cost_per_gpu_hour | healthy |
      | us-east-prod  | us-east-1  | nvidia-h100 | 64        | 3.50              | true    |
      | eu-west-prod  | eu-west-1  | nvidia-h100 | 48        | 4.20              | true    |
      | ap-south-prod | ap-south-1 | nvidia-a100 | 32        | 2.10              | true    |
    And a FleetInferencePool "llama-70b" deployed with the following replicas:
      | cluster       | replicas | gpus_per_replica |
      | us-east-prod  | 4        | 4                |
      | eu-west-prod  | 2        | 4                |
      | ap-south-prod | 2        | 4                |
    And a FleetScalingPolicy "llama-scaling" with strategy "balanced"

  Scenario: Scale up on SLO breach increases replicas when TTFT exceeds target
    Given the scaling policy has objectives:
      | metric   | target | tolerancePercent |
      | ttft-p99 | 200ms  | 10               |
    And the scaling policy has constraints:
      | globalMaxGPUs | stabilizationWindowSeconds |
      | 128           | 300                        |
    And cluster "us-east-prod" reports the following metrics for 5 minutes:
      | metric   | value |
      | ttft-p99 | 280ms |
    When the autoscaler evaluates the scaling policy
    Then the autoscaler should propose scaling up cluster "us-east-prod"
    And the replica count on "us-east-prod" should increase from 4 to at least 5
    And the total GPU consumption should not exceed 128
    And the FleetScalingPolicy should emit event "ScaleUp" with reason "SLOBreach" for metric "ttft-p99"

  Scenario: Scale down on underutilization decreases replicas when GPU util is below threshold
    Given the scaling policy has objectives:
      | metric          | target | tolerancePercent |
      | gpu-utilization | 70%    | 10               |
    And the scaling policy has constraints:
      | stabilizationWindowSeconds |
      | 300                        |
    And cluster "eu-west-prod" reports the following metrics for 10 minutes:
      | metric          | value |
      | gpu-utilization | 25%   |
    And cluster "eu-west-prod" ttft-p99 is within SLO at 150ms
    When the autoscaler evaluates the scaling policy
    Then the autoscaler should propose scaling down cluster "eu-west-prod"
    And the replica count on "eu-west-prod" should decrease from 2 to 1
    And the autoscaler should not scale below 1 replica on any active cluster
    And the FleetScalingPolicy should emit event "ScaleDown" with reason "Underutilized" for metric "gpu-utilization"

  Scenario: Cross-cluster migration moves replicas from expensive to cheaper cluster
    Given the scaling policy has crossCluster configuration:
      | enableMigration | migrationThreshold | migrationCooldownSeconds |
      | true            | 0.3                | 600                      |
    And the scaling policy has objectives:
      | metric          | target | tolerancePercent |
      | gpu-utilization | 70%    | 10               |
    And cluster utilization is:
      | cluster       | gpu_utilization | cost_per_gpu_hour |
      | eu-west-prod  | 30%             | 4.20              |
      | ap-south-prod | 85%             | 2.10              |
    And the utilization gap between "eu-west-prod" and "ap-south-prod" exceeds threshold 0.3
    When the autoscaler evaluates cross-cluster migration
    Then the autoscaler should propose migrating 1 replica from "eu-west-prod" to "ap-south-prod"
    And the estimated cost savings should be reported in the scaling event
    And no migration should occur again within the next 600 seconds
    And the FleetScalingPolicy should emit event "CrossClusterMigration" with source "eu-west-prod" and target "ap-south-prod"

  Scenario: Global GPU constraint prevents scaling beyond the fleet GPU budget
    Given the scaling policy has objectives:
      | metric   | target | tolerancePercent |
      | ttft-p99 | 200ms  | 10               |
    And the scaling policy has constraints:
      | globalMaxGPUs | maxScaleUpRate |
      | 40            | 4/5m           |
    And the current total GPU consumption across all clusters is 32
    And all clusters report ttft-p99 above the SLO target:
      | cluster       | ttft-p99 |
      | us-east-prod  | 350ms    |
      | eu-west-prod  | 290ms    |
      | ap-south-prod | 310ms    |
    When the autoscaler evaluates the scaling policy
    Then the autoscaler should propose adding at most 2 replicas total (8 GPUs to reach the 40 GPU cap)
    And the total GPU consumption after scaling should not exceed 40
    And the autoscaler should not add more than 4 replicas within any 5-minute window
    And a condition "GPUBudgetConstrained" should be set with status "True"
    And the condition message should contain "global GPU budget of 40 reached"
