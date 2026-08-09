Feature: Grid Integration (Praxis CRD Translation & SWIM Health Sync)
  As a fleet controller operator
  I want fleet cluster and pool state to be translated into Praxis Grid CRDs
  And Grid health status to be synced back to fleet cluster records
  So that cross-cluster inference routing is driven by the Grid data plane.

  Background:
    Given a fleet with the following clusters:
      | name         | region     | zone           | egress_address          |
      | us-east-prod | us-east-1  | us-east-1a     | egress.us-east.fleet.io |
      | eu-west-prod | eu-west-1  | eu-west-1b     | egress.eu-west.fleet.io |
      | ap-south-prod| ap-south-1 | ap-south-1a    | egress.ap-south.fleet.io|
    And a FleetInferencePool "llama-70b" with model "meta-llama/Llama-3.1-70B-Instruct" on cluster "us-east-prod" port 8080
    And the Grid network is "fleet-mesh"

  Scenario: Cluster state translates to GridSite CRDs
    When the fleet controller syncs cluster state to Grid CRDs
    Then 3 GridSite CRDs should be applied
    And each GridSite should reference gridNetworkRef "fleet-mesh"
    And GridSite "us-east-prod" should have region "us-east-1" and egress address "egress.us-east.fleet.io"

  Scenario: Pool state translates to InferenceProvider CRDs
    When the fleet controller syncs pool state to Grid CRDs
    Then 1 InferenceProvider CRD should be applied
    And the InferenceProvider "llama-70b" should have endpoint containing "llama-70b"
    And the InferenceProvider "llama-70b" should list model "meta-llama/Llama-3.1-70B-Instruct"

  Scenario: SWIM health sync updates cluster status
    Given cluster "us-east-prod" has fleet status "Running"
    And the GridSite "us-east-prod" reports phase "Unreachable"
    When the SWIM sync adapter runs
    Then cluster "us-east-prod" fleet status should be "Degraded"

  Scenario: SWIM sync skips unchanged phases
    Given cluster "us-east-prod" has fleet status "Running"
    And the GridSite "us-east-prod" reports phase "Active"
    And the SWIM sync adapter has already observed phase "Active" for "us-east-prod"
    When the SWIM sync adapter runs
    Then no cluster updates should be issued
