package intents

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var gclFixtureKey = []byte("a-secure-test-key-with-at-least-thirty-two-bytes")

const gclFixtureKeyID = "test-key-v1"

func TestGCLDecisionPackageDecoderMapsVerifiedProposal(t *testing.T) {
	payload, decision, digest := buildGCLDecisionEvent(t, nil)
	now := decision.CreatedAt.Add(time.Minute)
	decoder := NewGCLDecisionPackageDecoder(map[string][]byte{gclFixtureKeyID: gclFixtureKey})
	if digest != "sha256:fc9fb9540f206152bb4e399011fa95a1ba8727ae8ddb818b3d6a6f91117e9024" ||
		!bytes.Contains(payload, []byte(`"signature":"fdzA3rcwPTzathMVXw_udlyW3RN-zhLWt8UYpvtrhuw"`)) {
		t.Fatalf("fixture no longer matches Python canonical_json/HMAC output: digest=%s", digest)
	}

	intent, err := decoder.DecodeAt(GCLDecisionPackageCloudEventContentType, payload, now)
	if err != nil {
		t.Fatal(err)
	}
	if intent.ID != decision.PackageID || intent.Type != IntentScale {
		t.Fatalf("identity/type = %q/%q, want %q/%q", intent.ID, intent.Type, decision.PackageID, IntentScale)
	}
	if intent.CorrelationID != decision.CorrelationID || intent.IdempotencyKey != decision.IdempotencyID {
		t.Fatalf("correlation/idempotency not preserved: %+v", intent)
	}
	if intent.DecisionPackageRef != "gcl://decision-packages/"+decision.PackageID {
		t.Fatalf("decision reference = %q", intent.DecisionPackageRef)
	}
	if intent.DecisionPackageDigest != strings.TrimPrefix(digest, "sha256:") {
		t.Fatalf("decision digest = %q, want prefix-stripped %q", intent.DecisionPackageDigest, digest)
	}
	if intent.Pool != "primary" || intent.Model != "qwen" || intent.DesiredReplicas != 6 || intent.CurrentReplicas != 3 {
		t.Fatalf("selected parameters were not projected: %+v", intent)
	}
	if len(intent.TargetClusters) != 2 || intent.TargetClusters[0] != "spoke-a" || intent.TargetClusters[1] != "spoke-b" {
		t.Fatalf("target clusters = %#v", intent.TargetClusters)
	}
	if intent.HorizonSeconds != 300 || intent.CreatedAt != decision.CreatedAt || intent.ExpiresAt == nil || !intent.ExpiresAt.Equal(decision.ExpiresAt) {
		t.Fatalf("validity window was not projected: %+v", intent)
	}
	if intent.Proposer == nil || intent.Proposer.Subject != "spiffe://llm-d.ai/ns/gcl/sa/controller" ||
		intent.Proposer.AuthorityRef != "gcl://signing-keys/test-key-v1" || intent.Proposer.MaxAction != "fleet.scale" {
		t.Fatalf("proposer projection = %#v", intent.Proposer)
	}
	if len(intent.Evidence) != 2 {
		t.Fatalf("evidence = %#v", intent.Evidence)
	}
	for i, evidence := range intent.Evidence {
		if evidence.URI != "urn:sha256:"+evidence.SHA256 || len(evidence.SHA256) != 64 {
			t.Fatalf("evidence[%d] = %#v", i, evidence)
		}
	}
	parameters, ok := intent.StateSnapshot["parameters"].(map[string]any)
	if !ok || parameters["pool"] != "primary" {
		t.Fatalf("canonical parameters were not retained: %#v", intent.StateSnapshot)
	}
	if intent.StateSnapshot["tenant"] != "tenant-a" || intent.StateSnapshot["zone"] != "us-central" {
		t.Fatalf("scope was not retained: %#v", intent.StateSnapshot)
	}
	if err := ValidateGovernedIntent(intent, now); err != nil {
		t.Fatalf("projected intent does not satisfy Fleet v2 validation: %v", err)
	}
}

func TestGCLDecisionPackageDecoderRejectsIntegrityFailures(t *testing.T) {
	payload, decision, _ := buildGCLDecisionEvent(t, nil)
	now := decision.CreatedAt.Add(time.Minute)

	t.Run("tampered package", func(t *testing.T) {
		tampered := bytes.ReplaceAll(payload, []byte(`"tenant":"tenant-a"`), []byte(`"tenant":"tenant-b"`))
		_, err := DecodeGCLDecisionPackageCloudEvent(GCLDecisionPackageCloudEventContentType, tampered, map[string][]byte{gclFixtureKeyID: gclFixtureKey}, now)
		if err == nil || !strings.Contains(err.Error(), "digest does not match") {
			t.Fatalf("expected digest rejection, got %v", err)
		}
	})

	t.Run("unknown key", func(t *testing.T) {
		_, err := DecodeGCLDecisionPackageCloudEvent(GCLDecisionPackageCloudEventContentType, payload, nil, now)
		if err == nil || !strings.Contains(err.Error(), "unknown") {
			t.Fatalf("expected unknown-key rejection, got %v", err)
		}
	})

	t.Run("short key", func(t *testing.T) {
		_, err := DecodeGCLDecisionPackageCloudEvent(GCLDecisionPackageCloudEventContentType, payload, map[string][]byte{gclFixtureKeyID: []byte("short")}, now)
		if err == nil || !strings.Contains(err.Error(), "at least 32 bytes") {
			t.Fatalf("expected short-key rejection, got %v", err)
		}
	})

	t.Run("invalid signature", func(t *testing.T) {
		invalid := rewriteGCLCloudEvent(t, payload, func(event map[string]any) {
			event["data"].(map[string]any)["signature"] = strings.Repeat("A", 43)
		})
		_, err := DecodeGCLDecisionPackageCloudEvent(GCLDecisionPackageCloudEventContentType, invalid, map[string][]byte{gclFixtureKeyID: gclFixtureKey}, now)
		if err == nil || !strings.Contains(err.Error(), "signature verification failed") {
			t.Fatalf("expected signature rejection, got %v", err)
		}
	})

	t.Run("expired", func(t *testing.T) {
		_, err := DecodeGCLDecisionPackageCloudEvent(GCLDecisionPackageCloudEventContentType, payload, map[string][]byte{gclFixtureKeyID: gclFixtureKey}, decision.ExpiresAt)
		if err == nil || !strings.Contains(err.Error(), "expired") {
			t.Fatalf("expected expiry rejection, got %v", err)
		}
	})

	t.Run("not yet valid", func(t *testing.T) {
		_, err := DecodeGCLDecisionPackageCloudEvent(GCLDecisionPackageCloudEventContentType, payload, map[string][]byte{gclFixtureKeyID: gclFixtureKey}, decision.CreatedAt.Add(-time.Nanosecond))
		if err == nil || !strings.Contains(err.Error(), "not yet valid") {
			t.Fatalf("expected future-package rejection, got %v", err)
		}
	})
}

func TestGCLDecisionPackageDecoderEnforcesExactCloudEventContract(t *testing.T) {
	payload, decision, _ := buildGCLDecisionEvent(t, nil)
	now := decision.CreatedAt.Add(time.Minute)
	keys := map[string][]byte{gclFixtureKeyID: gclFixtureKey}

	for name, contentType := range map[string]string{
		"plain json":   "application/json",
		"binary event": "application/cloudevents-batch+json",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeGCLDecisionPackageCloudEvent(contentType, payload, keys, now); err == nil {
				t.Fatal("expected content-type rejection")
			}
		})
	}
	if _, err := DecodeGCLDecisionPackageCloudEvent(GCLDecisionPackageCloudEventContentType+"; charset=utf-8", payload, keys, now); err != nil {
		t.Fatalf("UTF-8 structured CloudEvent parameter should be accepted: %v", err)
	}

	tests := []struct {
		name  string
		field string
		value any
	}{
		{"specversion", "specversion", "0.3"},
		{"type", "type", "example.other"},
		{"dataschema", "dataschema", "https://example.invalid/schema.json"},
		{"datacontenttype", "datacontenttype", "application/octet-stream"},
		{"subject", "subject", "decision-package/different"},
		{"id", "id", "urn:sha256:" + strings.Repeat("0", 64)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			invalid := rewriteGCLCloudEvent(t, payload, func(event map[string]any) {
				event[test.field] = test.value
			})
			if _, err := DecodeGCLDecisionPackageCloudEvent(GCLDecisionPackageCloudEventContentType, invalid, keys, now); err == nil {
				t.Fatalf("expected %s rejection", test.field)
			}
		})
	}

	t.Run("unknown envelope field", func(t *testing.T) {
		invalid := rewriteGCLCloudEvent(t, payload, func(event map[string]any) {
			event["unexpected"] = true
		})
		_, err := DecodeGCLDecisionPackageCloudEvent(GCLDecisionPackageCloudEventContentType, invalid, keys, now)
		if err == nil || !strings.Contains(err.Error(), "unknown field") {
			t.Fatalf("expected strict field rejection, got %v", err)
		}
	})
}

func TestGCLDecisionPackageDecoderValidatesEvidenceConsistencyAndPool(t *testing.T) {
	keys := map[string][]byte{gclFixtureKeyID: gclFixtureKey}

	t.Run("package schema version is exact", func(t *testing.T) {
		payload, decision, _ := buildGCLDecisionEvent(t, func(decision *gclDecisionPackage) {
			decision.SchemaVersion = "gcl.llm-d.ai/decision-package/v2"
		})
		_, err := DecodeGCLDecisionPackageCloudEvent(GCLDecisionPackageCloudEventContentType, payload, keys, decision.CreatedAt.Add(time.Minute))
		if err == nil || !strings.Contains(err.Error(), "schema_version") {
			t.Fatalf("expected package-version rejection, got %v", err)
		}
	})

	t.Run("package id is canonical", func(t *testing.T) {
		payload, decision, _ := buildGCLDecisionEvent(t, func(decision *gclDecisionPackage) {
			decision.PackageID = "not-a-uuid"
		})
		_, err := DecodeGCLDecisionPackageCloudEvent(GCLDecisionPackageCloudEventContentType, payload, keys, decision.CreatedAt.Add(time.Minute))
		if err == nil || !strings.Contains(err.Error(), "canonical UUID") {
			t.Fatalf("expected package-id rejection, got %v", err)
		}
	})

	t.Run("envelope timestamp differs", func(t *testing.T) {
		payload, decision, _ := buildGCLDecisionEvent(t, nil)
		invalid := rewriteGCLCloudEvent(t, payload, func(event map[string]any) {
			event["time"] = decision.CreatedAt.Add(time.Second).Format(time.RFC3339)
		})
		_, err := DecodeGCLDecisionPackageCloudEvent(GCLDecisionPackageCloudEventContentType, invalid, keys, decision.CreatedAt.Add(time.Minute))
		if err == nil || !strings.Contains(err.Error(), "timestamps do not match") {
			t.Fatalf("expected timestamp-consistency rejection, got %v", err)
		}
	})

	t.Run("nested evidence absent from package", func(t *testing.T) {
		payload, decision, _ := buildGCLDecisionEvent(t, func(decision *gclDecisionPackage) {
			decision.Constraints[0].EvidenceRefs = []string{"sha256:" + strings.Repeat("c", 64)}
		})
		_, err := DecodeGCLDecisionPackageCloudEvent(GCLDecisionPackageCloudEventContentType, payload, keys, decision.CreatedAt.Add(time.Minute))
		if err == nil || !strings.Contains(err.Error(), "absent from evidence_refs") {
			t.Fatalf("expected nested evidence rejection, got %v", err)
		}
	})

	t.Run("envelope evidence differs", func(t *testing.T) {
		payload, decision, _ := buildGCLDecisionEvent(t, nil)
		invalid := rewriteGCLCloudEvent(t, payload, func(event map[string]any) {
			event["evidence"] = []any{"sha256:" + strings.Repeat("c", 64)}
		})
		_, err := DecodeGCLDecisionPackageCloudEvent(GCLDecisionPackageCloudEventContentType, invalid, keys, decision.CreatedAt.Add(time.Minute))
		if err == nil || !strings.Contains(err.Error(), "evidence does not match") {
			t.Fatalf("expected envelope evidence rejection, got %v", err)
		}
	})

	t.Run("package evidence is unique", func(t *testing.T) {
		payload, decision, _ := buildGCLDecisionEvent(t, func(decision *gclDecisionPackage) {
			decision.EvidenceRefs = append(decision.EvidenceRefs, decision.EvidenceRefs[0])
		})
		_, err := DecodeGCLDecisionPackageCloudEvent(GCLDecisionPackageCloudEventContentType, payload, keys, decision.CreatedAt.Add(time.Minute))
		if err == nil || !strings.Contains(err.Error(), "must be unique") {
			t.Fatalf("expected duplicate-evidence rejection, got %v", err)
		}
	})

	t.Run("pool is mandatory", func(t *testing.T) {
		payload, decision, _ := buildGCLDecisionEvent(t, func(decision *gclDecisionPackage) {
			delete(decision.Candidates[0].Parameters, "pool")
		})
		_, err := DecodeGCLDecisionPackageCloudEvent(GCLDecisionPackageCloudEventContentType, payload, keys, decision.CreatedAt.Add(time.Minute))
		if err == nil || !strings.Contains(err.Error(), `parameter "pool"`) {
			t.Fatalf("expected pool rejection, got %v", err)
		}
	})

	t.Run("selected candidate fails falsification", func(t *testing.T) {
		payload, decision, _ := buildGCLDecisionEvent(t, func(decision *gclDecisionPackage) {
			decision.FalsificationResults[0].Verdict = "fails"
		})
		_, err := DecodeGCLDecisionPackageCloudEvent(GCLDecisionPackageCloudEventContentType, payload, keys, decision.CreatedAt.Add(time.Minute))
		if err == nil || !strings.Contains(err.Error(), "must survive") {
			t.Fatalf("expected failed-falsification rejection, got %v", err)
		}
	})

	t.Run("selected candidate lacks falsification", func(t *testing.T) {
		payload, decision, _ := buildGCLDecisionEvent(t, func(decision *gclDecisionPackage) {
			decision.FalsificationResults[0].CandidateID = decision.Candidates[1].CandidateID
		})
		_, err := DecodeGCLDecisionPackageCloudEvent(GCLDecisionPackageCloudEventContentType, payload, keys, decision.CreatedAt.Add(time.Minute))
		if err == nil || !strings.Contains(err.Error(), "requires a falsification result") {
			t.Fatalf("expected missing-falsification rejection, got %v", err)
		}
	})
}

func TestGCLDecisionPackageDecoderMapsEveryCanonicalActionClass(t *testing.T) {
	expected := map[string]IntentType{
		"fleet.deploy":      IntentDeploy,
		"fleet.scale":       IntentScale,
		"fleet.route":       IntentRoute,
		"fleet.prewarm":     IntentPreWarm,
		"fleet.shed_load":   IntentShedLoad,
		"fleet.migrate":     IntentMigrate,
		"fleet.kv_transfer": IntentKVTransfer,
	}
	for actionClass, intentType := range expected {
		t.Run(actionClass, func(t *testing.T) {
			payload, decision, _ := buildGCLDecisionEvent(t, func(decision *gclDecisionPackage) {
				decision.Candidates[0].ActionClass = actionClass
				switch actionClass {
				case "fleet.prewarm":
					decision.Candidates[0].Parameters["target_replicas"] = 2
				case "fleet.shed_load":
					decision.Candidates[0].Parameters["max_inflight"] = 20
					decision.Candidates[0].Parameters["duration_seconds"] = 60
				case "fleet.migrate":
					delete(decision.Candidates[0].Parameters, "pool")
					decision.Candidates[0].Parameters["source_pool"] = "primary"
					decision.Candidates[0].Parameters["target_pool"] = "sovereign"
				case "fleet.kv_transfer":
					decision.Candidates[0].Parameters["source_cluster"] = "spoke-a"
					decision.Candidates[0].Parameters["target_cluster"] = "spoke-b"
				}
			})
			validationTime := decision.CreatedAt.Add(time.Minute)
			intent, err := DecodeGCLDecisionPackageCloudEvent(
				GCLDecisionPackageCloudEventContentType,
				payload,
				map[string][]byte{gclFixtureKeyID: gclFixtureKey},
				validationTime,
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateGovernedIntent(intent, validationTime); err != nil {
				t.Fatalf("projected canonical action was not admissible: %v", err)
			}
			if intent.Type != intentType || intent.Proposer.MaxAction != actionClass {
				t.Fatalf("projection = %q/%q, want %q/%q", intent.Type, intent.Proposer.MaxAction, intentType, actionClass)
			}
			if actionClass == "fleet.migrate" && intent.Pool != "primary" {
				t.Fatalf("migration source pool = %q, want primary", intent.Pool)
			}
		})
	}
}

func TestCanonicalGCLJSONMatchesPythonJSONDumps(t *testing.T) {
	raw := []byte(`{"z":"λ<&\u000b","a":{"small":1e-7,"fixed":1e15,"scientific":1e16,"whole":1.0,"integer":-0}}`)
	canonical, err := canonicalGCLJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":{"fixed":1000000000000000.0,"integer":0,"scientific":1e+16,"small":1e-07,"whole":1.0},"z":"λ<&\u000b"}`
	if string(canonical) != want {
		t.Fatalf("canonical JSON:\n got %s\nwant %s", canonical, want)
	}
}

func buildGCLDecisionEvent(t *testing.T, mutate func(*gclDecisionPackage)) ([]byte, gclDecisionPackage, string) {
	t.Helper()
	createdAt := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	expiresAt := createdAt.Add(5 * time.Minute)
	hard := true
	bound := 5000.0
	constraintConfidence := 0.91
	candidateConfidence := 0.87
	rejectedConfidence := 0.62
	packageConfidence := 0.87
	evidenceOne := "sha256:" + strings.Repeat("a", 64)
	evidenceTwo := "sha256:" + strings.Repeat("b", 64)
	decision := gclDecisionPackage{
		SchemaVersion: GCLDecisionPackageSchemaVersion,
		PackageID:     "018f6a4c-7b31-7d2a-9f28-4c6b5f9a1234",
		CreatedAt:     createdAt,
		ExpiresAt:     expiresAt,
		CorrelationID: "corr-123",
		CausationID:   "forecast-456",
		IdempotencyID: "decision:corr-123",
		Tenant:        "tenant-a",
		Zone:          "us-central",
		Proposer: gclProposerIdentity{
			AgentID:          "gcl",
			WorkloadIdentity: "spiffe://llm-d.ai/ns/gcl/sa/controller",
			TrustDomain:      "llm-d.ai",
		},
		Constraints: []gclDecisionConstraint{{
			ConstraintID:   "latency-slo",
			ConstraintType: "latency",
			Hard:           &hard,
			Bound:          &bound,
			Confidence:     &constraintConfidence,
			EvidenceRefs:   []string{evidenceOne},
		}},
		Candidates: []gclDecisionCandidate{
			{
				CandidateID: "candidate-scale",
				ActionClass: "fleet.scale",
				Parameters: map[string]any{
					"pool":             "primary",
					"model":            "qwen",
					"replicas":         6,
					"current_replicas": 3,
					"target_clusters":  []string{"spoke-a", "spoke-b"},
				},
				PredictedEffect: map[string]any{"latency_ms": 3900},
				Confidence:      &candidateConfidence,
			},
			{
				CandidateID:     "candidate-route",
				ActionClass:     "fleet.route",
				Parameters:      map[string]any{"pool": "primary"},
				PredictedEffect: map[string]any{},
				Confidence:      &rejectedConfidence,
			},
		},
		SelectedCandidateID: "candidate-scale",
		RejectedAlternatives: []gclRejectedAlternative{{
			Candidate: gclDecisionCandidate{
				CandidateID:     "candidate-route",
				ActionClass:     "fleet.route",
				Parameters:      map[string]any{"pool": "primary"},
				PredictedEffect: map[string]any{},
				Confidence:      &rejectedConfidence,
			},
			ReasonCode: "RECEDING_HORIZON_NOT_SELECTED",
			Reasoning:  "The route change was not selected.",
		}},
		FalsificationResults: []gclDecisionFalsification{{
			CandidateID:  "candidate-scale",
			CheckID:      "all-required-checks",
			Verdict:      "survives",
			Reasoning:    "All deterministic disconfirmation checks survived.",
			EvidenceRefs: []string{evidenceOne, evidenceTwo},
		}},
		Confidence:      &packageConfidence,
		EvidenceSources: []string{"deepfield-fleet"},
		EvidenceRefs:    []string{evidenceOne, evidenceTwo},
	}
	if mutate != nil {
		mutate(&decision)
	}
	packageJSON, err := json.Marshal(decision)
	if err != nil {
		t.Fatal(err)
	}
	// pydantic serializes the typed float bound as 5000.0. Go's JSON encoder
	// elides the decimal for an integral float, so retain the producer token in
	// this cross-language fixture.
	packageJSON = bytes.Replace(packageJSON, []byte(`"bound":5000`), []byte(`"bound":5000.0`), 1)
	canonical, err := canonicalGCLJSON(packageJSON)
	if err != nil {
		t.Fatal(err)
	}
	digestBytes := sha256.Sum256(canonical)
	digest := "sha256:" + hex.EncodeToString(digestBytes[:])
	mac := hmac.New(sha256.New, gclFixtureKey)
	_, _ = mac.Write(canonical)
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	signed := gclSignedDecisionPackage{
		Package: packageJSON, Digest: digest, Signature: signature,
		Algorithm: "HMAC-SHA256", KeyID: gclFixtureKeyID,
	}
	fingerprint := sha256.Sum256([]byte(decision.PackageID + ":" + digest + ":" + GCLDecisionPackageCloudEventType))
	event := gclDecisionPackageCloudEvent{
		SpecVersion:     "1.0",
		ID:              "urn:sha256:" + hex.EncodeToString(fingerprint[:]),
		Source:          "spiffe://llm-d.ai/ns/gcl/sa/controller",
		Type:            GCLDecisionPackageCloudEventType,
		Subject:         "decision-package/" + decision.PackageID,
		Time:            decision.CreatedAt,
		DataContentType: "application/json",
		DataSchema:      GCLDecisionPackageSchemaURI,
		CorrelationID:   decision.CorrelationID,
		CausationID:     decision.CausationID,
		IdempotencyID:   decision.IdempotencyID,
		Tenant:          decision.Tenant,
		Zone:            decision.Zone,
		Expiry:          decision.ExpiresAt,
		Evidence:        append([]string(nil), decision.EvidenceRefs...),
		Data:            signed,
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return payload, decision, digest
}

func rewriteGCLCloudEvent(t *testing.T, payload []byte, rewrite func(map[string]any)) []byte {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var event map[string]any
	if err := decoder.Decode(&event); err != nil {
		t.Fatal(err)
	}
	rewrite(event)
	rewritten, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return rewritten
}
