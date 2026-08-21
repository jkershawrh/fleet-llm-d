package intents

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Ed25519 is asymmetric: GCL holds the private key, Fleet holds only the public
// half, so a valid signature is evidence GCL authored the package. HMAC-SHA256
// is symmetric, so Fleet holds the same key GCL signs with and can mint
// packages indistinguishable from real ones. Accepting HMAC therefore forfeits
// the authorship property the signed DecisionPackage exists to provide, which
// is why it is refused unless an operator opts in for a migration window.

// resignFixtureWithHMAC rewrites a fixture event to carry an HMAC-SHA256
// signature over the same canonical package bytes.
func resignFixtureWithHMAC(t *testing.T, payload []byte) []byte {
	t.Helper()

	// Take the package bytes as they appear on the wire. Round-tripping them
	// through a map would re-encode the producer's JSON and sign something
	// other than what the decoder canonicalizes.
	var envelope struct {
		Data struct {
			Package json.RawMessage `json:"package"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalGCLJSON(envelope.Data.Package)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, gclFixtureKey)
	_, _ = mac.Write(canonical)
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return rewriteGCLCloudEvent(t, payload, func(event map[string]any) {
		data := event["data"].(map[string]any)
		data["signature"] = signature
		data["algorithm"] = "HMAC-SHA256"
	})
}

func TestHMACDecisionPackagesAreRefusedByDefault(t *testing.T) {
	payload, decision, _ := buildGCLDecisionEvent(t, nil)
	now := decision.CreatedAt.Add(time.Minute)
	hmacPayload := resignFixtureWithHMAC(t, payload)

	decoder := NewGCLDecisionPackageDecoder(map[string][]byte{gclFixtureKeyID: gclFixtureKey})
	_, err := decoder.DecodeAt(GCLDecisionPackageCloudEventContentType, hmacPayload, now)
	if err == nil {
		t.Fatal("a shared-secret HMAC package was accepted; it cannot prove GCL authorship")
	}
	if !strings.Contains(err.Error(), "HMAC-SHA256 is refused") {
		t.Fatalf("error should explain why the algorithm is refused, got: %v", err)
	}
}

func TestHMACDecisionPackagesAcceptedOnlyWithExplicitOptIn(t *testing.T) {
	payload, decision, _ := buildGCLDecisionEvent(t, nil)
	now := decision.CreatedAt.Add(time.Minute)
	hmacPayload := resignFixtureWithHMAC(t, payload)

	decoder := NewGCLDecisionPackageDecoderWithPolicy(
		map[string][]byte{gclFixtureKeyID: gclFixtureKey}, true)
	if _, err := decoder.DecodeAt(GCLDecisionPackageCloudEventContentType, hmacPayload, now); err != nil {
		t.Fatalf("migration window should still accept HMAC: %v", err)
	}
}

func TestEd25519DecisionPackagesAcceptedUnderTheDefaultPolicy(t *testing.T) {
	payload, decision, _ := buildGCLDecisionEvent(t, nil)
	now := decision.CreatedAt.Add(time.Minute)

	decoder := NewGCLDecisionPackageDecoder(
		map[string][]byte{gclFixtureKeyID: gclFixtureEd25519Public})
	if _, err := decoder.DecodeAt(GCLDecisionPackageCloudEventContentType, payload, now); err != nil {
		t.Fatalf("Ed25519 package rejected under the default policy: %v", err)
	}
}

// TestEd25519PrivateKeyIsNotAValidVerificationKey guards the deployment
// mistake this migration invites: pasting GCL's signing seed into Fleet's
// keyring instead of the derived public key.
func TestEd25519PrivateKeyIsNotAValidVerificationKey(t *testing.T) {
	payload, decision, _ := buildGCLDecisionEvent(t, nil)
	now := decision.CreatedAt.Add(time.Minute)

	decoder := NewGCLDecisionPackageDecoder(
		map[string][]byte{gclFixtureKeyID: gclFixtureSeed})
	if _, err := decoder.DecodeAt(GCLDecisionPackageCloudEventContentType, payload, now); err == nil {
		t.Fatal("the signing seed was accepted as a verification key")
	}
}

func TestUnknownAlgorithmIsRefused(t *testing.T) {
	payload, decision, _ := buildGCLDecisionEvent(t, nil)
	now := decision.CreatedAt.Add(time.Minute)
	tampered := rewriteGCLCloudEvent(t, payload, func(event map[string]any) {
		event["data"].(map[string]any)["algorithm"] = "RS256"
	})

	decoder := NewGCLDecisionPackageDecoder(
		map[string][]byte{gclFixtureKeyID: gclFixtureEd25519Public})
	_, err := decoder.DecodeAt(GCLDecisionPackageCloudEventContentType, tampered, now)
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("expected unsupported-algorithm rejection, got %v", err)
	}
}
