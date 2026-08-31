package intents

import (
	"strings"
	"testing"
	"time"
)

func governedTestIntent(now time.Time) FleetIntent {
	expires := now.Add(time.Hour)
	authorityExpires := now.Add(30 * time.Minute)
	return FleetIntent{
		Type: IntentScale, Confidence: 0.9, HorizonSeconds: 900, Justification: "forecast capacity shortfall",
		IdempotencyKey: "decision-1-scale", ExpiresAt: &expires, Pool: "granite-prod",
		DecisionPackageRef: "oci://decisions/decision-1", DecisionPackageDigest: strings.Repeat("a", 64),
		Proposer:        &ProposerAuthority{Subject: "spiffe://example/gcl", AuthorityRef: "authority/1", ExpiresAt: &authorityExpires},
		Evidence:        []EvidenceDigest{{URI: "oci://evidence/forecast-1", SHA256: strings.Repeat("b", 64)}},
		StateSnapshot:   map[string]interface{}{"replicas": float64(2)},
		DesiredReplicas: 4,
	}
}

func TestValidateGovernedIntent(t *testing.T) {
	now := time.Now().UTC()
	if err := ValidateGovernedIntent(governedTestIntent(now), now); err != nil {
		t.Fatal(err)
	}
}

func TestValidateGovernedIntentRejectsMissingTrustEnvelope(t *testing.T) {
	err := ValidateGovernedIntent(FleetIntent{Type: IntentScale, Confidence: 0.9, Justification: "scale", Pool: "p"}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "horizon_seconds") {
		t.Fatalf("expected trust-envelope validation error, got %v", err)
	}
}

func TestValidateGovernedIntentAcceptsExactlyCanonicalActionClasses(t *testing.T) {
	now := time.Now().UTC()
	valid := map[IntentType]func(*FleetIntent){
		IntentDeploy: func(intent *FleetIntent) {
			intent.Model = "qwen"
			intent.DesiredReplicas = 2
		},
		IntentScale: func(intent *FleetIntent) {
			intent.DesiredReplicas = 2
		},
		IntentRoute: func(intent *FleetIntent) {
			intent.TargetClusters = []string{"spoke-a"}
		},
		IntentPreWarm: func(intent *FleetIntent) {
			intent.TargetReplicas = 2
		},
		IntentShedLoad: func(intent *FleetIntent) {
			intent.MaxInflight = 20
			intent.DurationSeconds = 60
		},
		IntentMigrate: func(intent *FleetIntent) {
			intent.StateSnapshot["parameters"] = map[string]interface{}{"target_pool": "sovereign"}
		},
		IntentKVTransfer: func(intent *FleetIntent) {
			intent.StateSnapshot["parameters"] = map[string]interface{}{
				"source_cluster": "spoke-a",
				"target_cluster": "spoke-b",
			}
		},
	}
	for intentType, configure := range valid {
		t.Run(string(intentType), func(t *testing.T) {
			intent := governedTestIntent(now)
			intent.Type = intentType
			intent.DesiredReplicas = 0
			configure(&intent)
			if err := ValidateGovernedIntent(intent, now); err != nil {
				t.Fatalf("canonical %s intent was rejected: %v", intentType, err)
			}
		})
	}
	for _, intentType := range []IntentType{IntentAlert, IntentNoAction, "unknown"} {
		t.Run("reject_"+string(intentType), func(t *testing.T) {
			intent := governedTestIntent(now)
			intent.Type = intentType
			if err := ValidateGovernedIntent(intent, now); err == nil || !strings.Contains(err.Error(), "unsupported intent type") {
				t.Fatalf("non-canonical %s intent error = %v", intentType, err)
			}
		})
	}
}

func TestValidateGovernedIntentRejectsIncompleteCommonContract(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name       string
		mutate     func(*FleetIntent)
		wantSubstr string
	}{
		{"zero horizon", func(intent *FleetIntent) { intent.HorizonSeconds = 0 }, "horizon_seconds"},
		{"missing state", func(intent *FleetIntent) { intent.StateSnapshot = nil }, "state_snapshot"},
		{"missing evidence", func(intent *FleetIntent) { intent.Evidence = nil }, "evidence reference"},
		{"negative target replicas", func(intent *FleetIntent) { intent.TargetReplicas = -1 }, "target_replicas"},
		{"negative desired replicas", func(intent *FleetIntent) { intent.DesiredReplicas = -1 }, "desired_replicas"},
		{"negative current replicas", func(intent *FleetIntent) { intent.CurrentReplicas = -1 }, "current_replicas"},
		{"negative max inflight", func(intent *FleetIntent) { intent.MaxInflight = -1 }, "max_inflight"},
		{"negative duration", func(intent *FleetIntent) { intent.DurationSeconds = -1 }, "duration_seconds"},
		{"empty target", func(intent *FleetIntent) { intent.TargetClusters = []string{" "} }, "target_clusters[0]"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			intent := governedTestIntent(now)
			test.mutate(&intent)
			err := ValidateGovernedIntent(intent, now)
			if err == nil || !strings.Contains(err.Error(), test.wantSubstr) {
				t.Fatalf("error = %v, want %q", err, test.wantSubstr)
			}
		})
	}
}

func TestValidateGovernedIntentRejectsIncompleteActionParameters(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name       string
		configure  func(*FleetIntent)
		wantSubstr string
	}{
		{"deploy model", func(intent *FleetIntent) { intent.Type = IntentDeploy }, "requires model"},
		{"deploy replicas", func(intent *FleetIntent) {
			intent.Type = IntentDeploy
			intent.Model = "qwen"
			intent.DesiredReplicas = 0
		}, "desired_replicas"},
		{"scale replicas", func(intent *FleetIntent) { intent.Type = IntentScale; intent.DesiredReplicas = 0 }, "desired_replicas"},
		{"route targets", func(intent *FleetIntent) { intent.Type = IntentRoute; intent.DesiredReplicas = 0 }, "target_cluster"},
		{"prewarm replicas", func(intent *FleetIntent) { intent.Type = IntentPreWarm; intent.DesiredReplicas = 0 }, "target_replicas"},
		{"shed max inflight", func(intent *FleetIntent) { intent.Type = IntentShedLoad; intent.DesiredReplicas = 0 }, "max_inflight"},
		{"shed duration", func(intent *FleetIntent) {
			intent.Type = IntentShedLoad
			intent.DesiredReplicas = 0
			intent.MaxInflight = 10
		}, "duration_seconds"},
		{"migration target", func(intent *FleetIntent) { intent.Type = IntentMigrate; intent.DesiredReplicas = 0 }, "target_pool"},
		{"migration self target", func(intent *FleetIntent) {
			intent.Type = IntentMigrate
			intent.DesiredReplicas = 0
			intent.StateSnapshot["parameters"] = map[string]interface{}{"target_pool": intent.Pool}
		}, "must differ"},
		{"transfer source", func(intent *FleetIntent) { intent.Type = IntentKVTransfer; intent.DesiredReplicas = 0 }, "source_cluster"},
		{"transfer target", func(intent *FleetIntent) {
			intent.Type = IntentKVTransfer
			intent.DesiredReplicas = 0
			intent.StateSnapshot["parameters"] = map[string]interface{}{"source_cluster": "spoke-a"}
		}, "target_cluster"},
		{"transfer self target", func(intent *FleetIntent) {
			intent.Type = IntentKVTransfer
			intent.DesiredReplicas = 0
			intent.StateSnapshot["parameters"] = map[string]interface{}{"source_cluster": "spoke-a", "target_cluster": "spoke-a"}
		}, "must differ"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			intent := governedTestIntent(now)
			test.configure(&intent)
			err := ValidateGovernedIntent(intent, now)
			if err == nil || !strings.Contains(err.Error(), test.wantSubstr) {
				t.Fatalf("error = %v, want %q", err, test.wantSubstr)
			}
		})
	}
}
