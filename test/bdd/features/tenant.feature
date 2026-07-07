Feature: Tenant Profile Governance
  As a platform administrator
  I want to enforce per-tenant quotas, rate limits, and cost controls
  So that multi-tenant inference workloads are isolated, metered, and budgeted.

  Background:
    Given a fleet with the following clusters:
      | name          | region     | healthy |
      | us-east-prod  | us-east-1  | true    |
      | eu-west-prod  | eu-west-1  | true    |
      | ap-south-prod | ap-south-1 | true    |
    And a FleetInferencePool "llama-70b" deployed on all clusters

  Scenario: Quota enforcement rejects requests when token limit is exceeded
    Given a TenantProfile "startup-tier" with quotas:
      | maxTokensPerMinute | maxConcurrentRequests |
      | 50000              | 10                    |
    And tenant "startup-tier" has consumed 49500 tokens in the current minute
    When tenant "startup-tier" sends a request expecting 1000 output tokens
    Then the request should be rejected with status 429
    And the response should include header "X-Quota-Remaining-Tokens: 500"
    And the response body should contain error code "QUOTA_EXCEEDED"
    And the response body should contain message "token quota exceeded: 50500 requested exceeds limit of 50000 tokens per minute"
    And the TenantProfile status should have condition "QuotaExceeded" with status "True"

  Scenario: Rate limiting rejects requests when requests per second is exceeded
    Given a TenantProfile "basic-tier" with rate limits:
      | requestsPerSecond | burstSize |
      | 10                | 15        |
    When tenant "basic-tier" sends 16 requests within 1 second
    Then the first 15 requests should be accepted
    And the 16th request should be rejected with status 429
    And the response should include header "Retry-After"
    And the response body should contain error code "RATE_LIMITED"
    And the TenantProfile status should have condition "RateLimited" with status "True"
    When 1 second elapses with no new requests
    Then the next request from tenant "basic-tier" should be accepted
    And the TenantProfile status should have condition "RateLimited" with status "False"

  Scenario: Cost cap enforcement alerts and optionally rejects at budget threshold
    Given a TenantProfile "enterprise-tier" with cost controls:
      | monthlyBudget | alertThreshold |
      | 5000.00       | 0.8            |
    And tenant "enterprise-tier" has accumulated cost of "3900.00" this month
    When tenant "enterprise-tier" sends a request that incurs cost "200.00"
    Then the request should be accepted
    And the accumulated cost should be "4100.00"
    And a "BudgetAlert" condition should be set with status "True"
    And the condition message should contain "cost $4100.00 exceeds 80% alert threshold of $5000.00 budget"
    And a fleet event "TenantBudgetAlert" should be emitted for tenant "enterprise-tier"
    When tenant "enterprise-tier" continues to send requests until cost reaches "5000.00"
    Then subsequent requests should be rejected with status 402
    And the response body should contain error code "BUDGET_EXHAUSTED"
    And the condition "QuotaExceeded" should be set with reason "MonthlyBudgetExceeded"

  Scenario: Multi-tenant isolation prevents noisy neighbor from affecting priority tenant SLO
    Given a TenantProfile "gold-tier" with:
      | priority | maxConcurrentRequests | maxTokensPerMinute |
      | 900      | 100                   | 500000             |
    And a TenantProfile "free-tier" with:
      | priority | maxConcurrentRequests | maxTokensPerMinute |
      | 100      | 5                     | 10000              |
    When tenant "free-tier" saturates its concurrent request limit with 5 long-running requests
    And tenant "gold-tier" sends a request simultaneously
    Then tenant "gold-tier" request should be scheduled with higher priority
    And tenant "gold-tier" request TTFT should remain below the SLO target of 200ms
    And tenant "free-tier" requests should not consume GPU resources allocated to "gold-tier"
    And the fleet gateway should report separate latency metrics per tenant

  Scenario: Usage metering accurately counts tokens across multiple clusters
    Given a TenantProfile "metered-tier" with quotas:
      | maxTokensPerMinute |
      | 100000             |
    When tenant "metered-tier" sends the following requests across clusters:
      | cluster       | input_tokens | output_tokens |
      | us-east-prod  | 512          | 1024          |
      | eu-west-prod  | 256          | 2048          |
      | us-east-prod  | 1024         | 512           |
      | ap-south-prod | 128          | 4096          |
    Then the TenantProfile status should show tokensConsumed as 9600
    And the per-cluster token breakdown should be:
      | cluster       | tokens |
      | us-east-prod  | 3072   |
      | eu-west-prod  | 2304   |
      | ap-south-prod | 4224   |
    And the usage metering event stream should contain 4 token-count records
    And the total across all cluster records should equal the aggregate tokensConsumed

  Scenario: Tenant usage metering is tamper-proof via ledger
    Given tenant "telco-voice-ai" consuming inference on cluster "us-east-1"
    When 1000000 tokens are consumed
    Then a ledger entry of type "fleet.tenant.usage" should be recorded
    And the entry should be hash-chained to the previous usage entry
    And the chain should verify successfully
