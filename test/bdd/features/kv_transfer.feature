Feature: KV Cache Transfer Policy
  As a fleet operator
  I want cross-cluster KV cache migration to be triggered automatically
  So that cache state is preserved during failovers, pre-populated before
  traffic shifts, and transferred without saturating the network.

  Background:
    Given a fleet with the following clusters:
      | name          | region     | healthy | kv_cache_size | kv_cache_hit_rate |
      | us-east-prod  | us-east-1  | true    | 8Gi           | 0.72              |
      | eu-west-prod  | eu-west-1  | true    | 6Gi           | 0.65              |
      | ap-south-prod | ap-south-1 | true    | 4Gi           | 0.41              |
    And a FleetInferencePool "llama-70b" deployed on all clusters
    And a KVCacheTransferPolicy "llama-kv-policy" with transport:
      | protocol | compression | encryption | maxConcurrentTransfers |
      | nixl     | lz4         | true       | 4                      |

  Scenario: Hot failover transfers KV cache when a cluster fails
    Given the transfer policy has a trigger:
      | type             | action      | minCacheSize | minHitRate |
      | clusterFailover  | transferHot | 1Gi          | 0.5        |
    And the retention policy is:
      | sourceRetentionAfterTransfer | maxRetentionSize |
      | 1h                           | 10Gi             |
    When cluster "us-east-prod" becomes unhealthy
    Then the KV cache transfer should be initiated from "us-east-prod" to failover targets
    And the transfer type should be "transferHot"
    And the transfer should include cache entries from "us-east-prod" with hit rate above 0.5
    And the transfer should include at least 1Gi of cache data
    And the target cluster should begin receiving traffic with pre-warmed KV cache
    And the source cluster "us-east-prod" should retain cache data for 1 hour
    And a fleet event "KVCacheTransferStarted" should be emitted with:
      | source       | target      | transfer_size | protocol |
      | us-east-prod | eu-west-prod | 8Gi          | nixl     |
    When the transfer completes
    Then a fleet event "KVCacheTransferCompleted" should be emitted
    And the target cluster KV cache hit rate should improve within 5 minutes

  Scenario: Warm migration pre-populates KV cache before a planned traffic shift
    Given the transfer policy has a trigger:
      | type           | action              | minCacheSize | maxStaleness |
      | loadMigration  | transferPrefixTree  | 500Mi        | 1h           |
    And a planned migration of 50% traffic from "eu-west-prod" to "ap-south-prod" is scheduled
    When the fleet controller initiates the load migration
    Then the KV cache prefix tree transfer should start before traffic is shifted
    And the transfer should include only cache entries newer than 1 hour
    And the transfer should include only entries where cache size exceeds 500Mi
    And the transfer should use protocol "nixl" with compression "lz4"
    And the transfer data should be encrypted in transit
    When the prefix tree transfer reaches 90% completion
    Then the fleet controller should begin shifting traffic to "ap-south-prod"
    And the "ap-south-prod" cluster should show improved KV cache hit rate
    And the first requests to "ap-south-prod" should benefit from pre-populated cache
    And the observed TTFT on "ap-south-prod" should be lower than cold-start baseline

  Scenario: Transfer bandwidth limiting respects maxBandwidthMbps constraint
    Given the transfer policy transport has maxBandwidthMbps set to 500
    And the transfer policy has a trigger:
      | type             | action      |
      | clusterFailover  | transferHot |
    And the network link between "us-east-prod" and "eu-west-prod" has capacity 10000 Mbps
    When a KV cache transfer of 8Gi is initiated from "us-east-prod" to "eu-west-prod"
    Then the transfer should not exceed 500 Mbps sustained throughput
    And the transfer should take approximately 131 seconds at 500 Mbps for 8Gi
    And during the transfer, inference traffic latency should not degrade by more than 10%
    And the metric "fleet_llmd_kv_transfer_bandwidth_mbps" should not exceed 500
    And the metric "fleet_llmd_kv_transfer_progress_ratio" should increase monotonically from 0.0 to 1.0
    When maxConcurrentTransfers is set to 4 and 4 transfers run simultaneously
    Then each transfer should receive approximately 125 Mbps (500 / 4)
    And the total bandwidth across all concurrent transfers should not exceed 500 Mbps

  Scenario: KV cache transfer includes proof receipt for verification
    Given a KV cache transfer from "us-east-1" to "us-west-2"
    When the transfer completes
    Then a proof receipt should be issued with the KV cache hash
    And the receiving cluster should verify the receipt
    And the receipt verification should confirm data integrity
