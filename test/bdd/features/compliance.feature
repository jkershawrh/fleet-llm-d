Feature: Compliance & Audit Trail
  As a compliance officer
  I want all fleet decisions recorded in a tamper-evident immutable ledger
  So that regulatory audits can verify placement rationale, decision integrity,
  and non-repudiation of fleet actions.

  Background:
    Given a fleet with the following clusters:
      | name          | region     | healthy |
      | us-east-prod  | us-east-1  | true    |
      | eu-west-prod  | eu-west-1  | true    |
      | ap-south-prod | ap-south-1 | true    |
    And the ARE immutable ledger is available and healthy
    And a FleetInferencePool "llama-70b" deployed on all clusters

  Scenario: All fleet decisions are recorded in immutable ledger
    Given a placement decision for model "llama-70b" targeting cluster "us-east-prod"
    And a scaling decision increasing replicas from 2 to 4 on cluster "eu-west-prod"
    And a routing decision shifting 30% traffic from "us-east-prod" to "ap-south-prod"
    When each decision is executed by the fleet controller
    Then a ledger entry of type "fleet.decision.placement" should exist for the placement
    And a ledger entry of type "fleet.decision.scaling" should exist for the scaling
    And a ledger entry of type "fleet.decision.routing" should exist for the routing
    And each entry should contain a timestamp, actor, and rationale field
    And the entries should be ordered by their chain position

  Scenario: Decision chains verify integrity end-to-end
    Given 100 fleet decisions have been recorded in the ledger
    When the chain integrity is verified from genesis to the latest entry
    Then all hash links should be valid
    And no gaps should exist in the chain sequence
    And the verification should complete within 5 seconds
    And the verifier should return a signed attestation of chain validity

  Scenario: Regulatory audit trail includes placement rationale
    Given a PlacementPolicy "gdpr-compliant" with regulatory constraints
    And the placement engine places "llama-70b" on cluster "eu-west-prod"
    When an auditor queries the ledger for placement decisions on "llama-70b"
    Then the ledger entry should include the policy name "gdpr-compliant"
    And the entry should include the list of evaluated clusters
    And the entry should include the reason each excluded cluster was rejected
    And the entry should include the constraint expressions that were evaluated
    And the audit trail should be exportable in a standard compliance format

  Scenario: Ledger entries include writer signatures for non-repudiation
    Given a fleet controller with identity "controller-us-east-1"
    When the controller records a decision in the ledger
    Then the ledger entry should include a cryptographic signature from the writer
    And the signature should be verifiable against the controller's public key
    And attempting to modify the entry content should invalidate the signature
    And the entry should include the writer identity "controller-us-east-1"
