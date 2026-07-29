Feature: Immutable Ledger Evidence Chain
  As a fleet operator
  I want all fleet decisions recorded in the immutable ledger
  So that I have a tamper-evident audit trail for compliance

  Background:
    Given the following clusters are registered:
      | cluster_id    | region     | gpu_type    | gpus | cost_per_gpu_hr | healthy |
      | us-east-prod  | us-east-1  | nvidia-h100 | 64   | 3.50            | true    |
      | eu-west-prod  | eu-west-1  | nvidia-h100 | 48   | 4.20            | true    |

  Scenario: Record placement decision in ledger
    When I record a placement decision for "llama-70b" on "us-east-prod"
    Then the ledger should contain an entry of type "fleet.placement.assigned"
    And the entry should have a valid timestamp

  Scenario: Record tenant usage in ledger
    When I record tenant usage for "tenant-alpha" on "us-east-prod" consuming 5000 tokens
    Then the ledger should contain an entry of type "fleet.tenant.usage"

  Scenario: Multiple entries maintain chain ordering
    When I record a placement decision for "llama-70b" on "us-east-prod"
    And I record a placement decision for "granite-3b" on "eu-west-prod"
    And I record tenant usage for "tenant-alpha" on "us-east-prod" consuming 1000 tokens
    Then the ledger chain should be valid
    And the ledger should contain 3 entries

  Scenario: Chain verification detects all entry types
    When I record a placement decision for "llama-70b" on "us-east-prod"
    And I record a scaling decision for "us-east-prod" from 2 to 4 replicas
    And I record a routing decision shifting traffic from "us-east-prod" to "eu-west-prod"
    Then the ledger chain should be valid
    And all entries should have valid timestamps
