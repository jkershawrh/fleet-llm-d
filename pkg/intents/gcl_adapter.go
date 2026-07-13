package intents

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"mime"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// GCLDecisionPackageCloudEventContentType is the structured CloudEvents
	// projection accepted from governed-cognitive-loop.
	GCLDecisionPackageCloudEventContentType = "application/cloudevents+json"
	GCLDecisionPackageCloudEventType        = "ai.llm-d.gcl.decision-package.v1"
	GCLDecisionPackageSchemaVersion         = "gcl.llm-d.ai/decision-package/v1"
	GCLDecisionPackageSchemaURI             = "https://schemas.llm-d.ai/gcl/decision-package/v1/schema.json"
)

var (
	gclDigestPattern      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	gclSignaturePattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	gclUUIDPattern        = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	gclSourcePattern      = regexp.MustCompile(`^[a-z][a-z0-9+.-]*:.+$`)
	gclSPIFFEPattern      = regexp.MustCompile(`^spiffe://[a-z0-9.-]+(?:/[A-Za-z0-9._~!$&'()*+,;=:@%-]+)*$`)
	gclTrustDomainPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?$`)
	gclTraceparentPattern = regexp.MustCompile(`^00-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$`)
)

var gclActionClasses = map[string]IntentType{
	"fleet.deploy":      IntentDeploy,
	"fleet.scale":       IntentScale,
	"fleet.route":       IntentRoute,
	"fleet.prewarm":     IntentPreWarm,
	"fleet.shed_load":   IntentShedLoad,
	"fleet.migrate":     IntentMigrate,
	"fleet.kv_transfer": IntentKVTransfer,
}

// GCLDecisionPackageDecoder verifies producer-owned DecisionPackage events and
// projects the selected proposal into Fleet's admission model. Its keyring is
// copied on construction so a decoder can be shared safely by HTTP handlers.
// The signature proves GCL authorship; Fleet remains the authorization owner.
type GCLDecisionPackageDecoder struct {
	keyring map[string][]byte
}

func NewGCLDecisionPackageDecoder(keyring map[string][]byte) *GCLDecisionPackageDecoder {
	copyOfKeyring := make(map[string][]byte, len(keyring))
	for keyID, key := range keyring {
		copyOfKeyring[keyID] = append([]byte(nil), key...)
	}
	return &GCLDecisionPackageDecoder{keyring: copyOfKeyring}
}

// Decode verifies a structured DecisionPackage CloudEvent at the current time.
func (d *GCLDecisionPackageDecoder) Decode(contentType string, payload []byte) (FleetIntent, error) {
	return d.DecodeAt(contentType, payload, time.Now().UTC())
}

// DecodeAt is the deterministic form used by replay processors and tests.
func (d *GCLDecisionPackageDecoder) DecodeAt(contentType string, payload []byte, now time.Time) (FleetIntent, error) {
	if d == nil {
		return FleetIntent{}, fmt.Errorf("GCL decision package decoder is nil")
	}
	return decodeGCLDecisionPackageCloudEvent(contentType, payload, d.keyring, now)
}

// DecodeGCLDecisionPackageCloudEvent is a convenience entry point for callers
// that manage key rotation outside the decoder. Long-lived handlers should use
// NewGCLDecisionPackageDecoder and swap the decoder atomically when keys rotate.
func DecodeGCLDecisionPackageCloudEvent(contentType string, payload []byte, keyring map[string][]byte, now time.Time) (FleetIntent, error) {
	return decodeGCLDecisionPackageCloudEvent(contentType, payload, keyring, now)
}

type gclDecisionPackageCloudEvent struct {
	SpecVersion     string                   `json:"specversion"`
	ID              string                   `json:"id"`
	Source          string                   `json:"source"`
	Type            string                   `json:"type"`
	Subject         string                   `json:"subject"`
	Time            time.Time                `json:"time"`
	DataContentType string                   `json:"datacontenttype"`
	DataSchema      string                   `json:"dataschema"`
	CorrelationID   string                   `json:"correlationid"`
	CausationID     string                   `json:"causationid"`
	IdempotencyID   string                   `json:"idempotencyid"`
	Tenant          string                   `json:"tenant"`
	Zone            string                   `json:"zone"`
	Traceparent     *string                  `json:"traceparent"`
	Expiry          time.Time                `json:"expiry"`
	Evidence        []string                 `json:"evidence"`
	Data            gclSignedDecisionPackage `json:"data"`
}

type gclSignedDecisionPackage struct {
	Package   json.RawMessage `json:"package"`
	Digest    string          `json:"digest"`
	Signature string          `json:"signature"`
	Algorithm string          `json:"algorithm"`
	KeyID     string          `json:"key_id"`
}

type gclDecisionPackage struct {
	SchemaVersion        string                        `json:"schema_version"`
	PackageID            string                        `json:"package_id"`
	CreatedAt            time.Time                     `json:"created_at"`
	ExpiresAt            time.Time                     `json:"expires_at"`
	CorrelationID        string                        `json:"correlation_id"`
	CausationID          string                        `json:"causation_id"`
	IdempotencyID        string                        `json:"idempotency_id"`
	Tenant               string                        `json:"tenant"`
	Zone                 string                        `json:"zone"`
	Proposer             gclProposerIdentity           `json:"proposer"`
	AgentPromotion       *gclAgentPromotionAttestation `json:"agent_promotion"`
	Constraints          []gclDecisionConstraint       `json:"constraints"`
	Candidates           []gclDecisionCandidate        `json:"candidates"`
	SelectedCandidateID  string                        `json:"selected_candidate_id"`
	RejectedAlternatives []gclRejectedAlternative      `json:"rejected_alternatives"`
	FalsificationResults []gclDecisionFalsification    `json:"falsification_results"`
	Confidence           *float64                      `json:"confidence"`
	EvidenceSources      []string                      `json:"evidence_sources"`
	EvidenceRefs         []string                      `json:"evidence_refs"`
}

type gclDecisionConstraint struct {
	ConstraintID   string   `json:"constraint_id"`
	ConstraintType string   `json:"constraint_type"`
	Hard           *bool    `json:"hard"`
	Bound          *float64 `json:"bound"`
	Confidence     *float64 `json:"confidence"`
	EvidenceRefs   []string `json:"evidence_refs"`
}

type gclDecisionCandidate struct {
	CandidateID     string         `json:"candidate_id"`
	ActionClass     string         `json:"action_class"`
	Parameters      map[string]any `json:"parameters"`
	PredictedEffect map[string]any `json:"predicted_effect"`
	Confidence      *float64       `json:"confidence"`
}

type gclRejectedAlternative struct {
	Candidate  gclDecisionCandidate `json:"candidate"`
	ReasonCode string               `json:"reason_code"`
	Reasoning  string               `json:"reasoning"`
}

type gclDecisionFalsification struct {
	CandidateID  string   `json:"candidate_id"`
	CheckID      string   `json:"check_id"`
	Verdict      string   `json:"verdict"`
	Reasoning    string   `json:"reasoning"`
	EvidenceRefs []string `json:"evidence_refs"`
}

type gclProposerIdentity struct {
	AgentID          string `json:"agent_id"`
	WorkloadIdentity string `json:"workload_identity"`
	TrustDomain      string `json:"trust_domain"`
}

type gclAgentPromotionAttestation struct {
	Provider         string    `json:"provider"`
	NonAuthoritative *bool     `json:"non_authoritative"`
	Subject          string    `json:"subject"`
	ActionClass      string    `json:"action_class"`
	Decision         string    `json:"decision"`
	ConsequenceScore *float64  `json:"consequence_score"`
	AutonomyCeiling  *float64  `json:"autonomy_ceiling"`
	AttestationRef   string    `json:"attestation_ref"`
	IssuedAt         time.Time `json:"issued_at"`
	ExpiresAt        time.Time `json:"expires_at"`
}

func decodeGCLDecisionPackageCloudEvent(contentType string, payload []byte, keyring map[string][]byte, now time.Time) (FleetIntent, error) {
	if err := validateGCLContentType(contentType); err != nil {
		return FleetIntent{}, err
	}
	var event gclDecisionPackageCloudEvent
	if err := strictGCLJSONDecode(payload, &event); err != nil {
		return FleetIntent{}, fmt.Errorf("decode GCL DecisionPackage CloudEvent: %w", err)
	}
	if len(event.Data.Package) == 0 || bytes.Equal(bytes.TrimSpace(event.Data.Package), []byte("null")) {
		return FleetIntent{}, fmt.Errorf("GCL DecisionPackage data.package is required")
	}
	var decision gclDecisionPackage
	if err := strictGCLJSONDecode(event.Data.Package, &decision); err != nil {
		return FleetIntent{}, fmt.Errorf("decode GCL DecisionPackage data.package: %w", err)
	}
	if err := validateGCLDecisionPackage(decision, now); err != nil {
		return FleetIntent{}, err
	}
	canonical, err := canonicalGCLJSON(event.Data.Package)
	if err != nil {
		return FleetIntent{}, fmt.Errorf("canonicalize GCL DecisionPackage: %w", err)
	}
	if err := verifyGCLSignedPackage(event.Data, canonical, keyring); err != nil {
		return FleetIntent{}, err
	}
	if err := validateGCLEnvelope(event, decision); err != nil {
		return FleetIntent{}, err
	}
	return projectGCLDecision(event, decision)
}

func validateGCLContentType(contentType string) error {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return fmt.Errorf("invalid Content-Type for GCL DecisionPackage: %w", err)
	}
	if mediaType != GCLDecisionPackageCloudEventContentType {
		return fmt.Errorf("GCL DecisionPackage requires Content-Type %q", GCLDecisionPackageCloudEventContentType)
	}
	for name, value := range params {
		if !strings.EqualFold(name, "charset") || !strings.EqualFold(value, "utf-8") {
			return fmt.Errorf("unsupported GCL DecisionPackage Content-Type parameter %q", name)
		}
	}
	return nil
}

func strictGCLJSONDecode(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values are not allowed")
		}
		return fmt.Errorf("invalid trailing JSON: %w", err)
	}
	return nil
}

func validateGCLDecisionPackage(decision gclDecisionPackage, now time.Time) error {
	if decision.SchemaVersion != GCLDecisionPackageSchemaVersion {
		return fmt.Errorf("unsupported GCL DecisionPackage schema_version %q", decision.SchemaVersion)
	}
	if !gclUUIDPattern.MatchString(decision.PackageID) {
		return fmt.Errorf("GCL DecisionPackage package_id must be a canonical UUID")
	}
	if decision.CreatedAt.IsZero() || decision.ExpiresAt.IsZero() {
		return fmt.Errorf("GCL DecisionPackage created_at and expires_at are required")
	}
	if !decision.ExpiresAt.After(decision.CreatedAt) {
		return fmt.Errorf("GCL DecisionPackage must expire after it is created")
	}
	if now.Before(decision.CreatedAt) {
		return fmt.Errorf("GCL DecisionPackage is not yet valid")
	}
	if !now.Before(decision.ExpiresAt) {
		return fmt.Errorf("GCL DecisionPackage is expired")
	}
	for name, value := range map[string]string{
		"correlation_id": decision.CorrelationID,
		"causation_id":   decision.CausationID,
		"idempotency_id": decision.IdempotencyID,
		"tenant":         decision.Tenant,
		"zone":           decision.Zone,
	} {
		if err := validateGCLString(name, value, 256); err != nil {
			return err
		}
	}
	if err := validateGCLProposer(decision.Proposer); err != nil {
		return err
	}
	if decision.AgentPromotion != nil {
		if err := validateGCLAgentPromotion(*decision.AgentPromotion, decision.CreatedAt); err != nil {
			return err
		}
	}
	if decision.Confidence == nil || !gclUnitInterval(*decision.Confidence) {
		return fmt.Errorf("GCL DecisionPackage confidence must be present and between 0 and 1")
	}
	if len(decision.EvidenceSources) == 0 {
		return fmt.Errorf("GCL DecisionPackage requires at least one evidence source")
	}
	for i, source := range decision.EvidenceSources {
		if strings.TrimSpace(source) == "" {
			return fmt.Errorf("GCL DecisionPackage evidence_sources[%d] is empty", i)
		}
	}
	if len(decision.EvidenceRefs) == 0 {
		return fmt.Errorf("GCL DecisionPackage requires at least one evidence reference")
	}
	knownEvidence := make(map[string]struct{}, len(decision.EvidenceRefs))
	for i, ref := range decision.EvidenceRefs {
		if !gclDigestPattern.MatchString(ref) {
			return fmt.Errorf("GCL DecisionPackage evidence_refs[%d] is not sha256:<64 lowercase hex>", i)
		}
		if _, duplicate := knownEvidence[ref]; duplicate {
			return fmt.Errorf("GCL DecisionPackage evidence references must be unique")
		}
		knownEvidence[ref] = struct{}{}
	}
	if len(decision.Constraints) == 0 {
		return fmt.Errorf("GCL DecisionPackage requires at least one constraint")
	}
	for i, constraint := range decision.Constraints {
		if err := validateGCLConstraint(i, constraint, knownEvidence); err != nil {
			return err
		}
	}
	if len(decision.Candidates) == 0 {
		return fmt.Errorf("GCL DecisionPackage requires at least one candidate")
	}
	candidateIDs := make(map[string]struct{}, len(decision.Candidates))
	for i, candidate := range decision.Candidates {
		if err := validateGCLCandidate(fmt.Sprintf("candidates[%d]", i), candidate); err != nil {
			return err
		}
		if _, duplicate := candidateIDs[candidate.CandidateID]; duplicate {
			return fmt.Errorf("GCL DecisionPackage candidate ids must be unique")
		}
		candidateIDs[candidate.CandidateID] = struct{}{}
	}
	if _, ok := candidateIDs[decision.SelectedCandidateID]; !ok {
		return fmt.Errorf("GCL DecisionPackage selected_candidate_id references an unknown candidate")
	}
	if decision.RejectedAlternatives == nil {
		return fmt.Errorf("GCL DecisionPackage rejected_alternatives is required")
	}
	for i, rejected := range decision.RejectedAlternatives {
		if err := validateGCLCandidate(fmt.Sprintf("rejected_alternatives[%d].candidate", i), rejected.Candidate); err != nil {
			return err
		}
		if rejected.Candidate.CandidateID == decision.SelectedCandidateID {
			return fmt.Errorf("GCL DecisionPackage selected candidate cannot also be rejected")
		}
		if err := validateGCLString(fmt.Sprintf("rejected_alternatives[%d].reason_code", i), rejected.ReasonCode, 128); err != nil {
			return err
		}
		if err := validateGCLString(fmt.Sprintf("rejected_alternatives[%d].reasoning", i), rejected.Reasoning, 4096); err != nil {
			return err
		}
	}
	if len(decision.FalsificationResults) == 0 {
		return fmt.Errorf("GCL DecisionPackage requires at least one falsification result")
	}
	selectedFalsification := false
	for i, result := range decision.FalsificationResults {
		if _, ok := candidateIDs[result.CandidateID]; !ok {
			return fmt.Errorf("GCL DecisionPackage falsification_results[%d] references an unknown candidate", i)
		}
		if err := validateGCLString(fmt.Sprintf("falsification_results[%d].check_id", i), result.CheckID, 256); err != nil {
			return err
		}
		if result.Verdict != "survives" && result.Verdict != "fails" {
			return fmt.Errorf("GCL DecisionPackage falsification_results[%d] has invalid verdict %q", i, result.Verdict)
		}
		if err := validateGCLString(fmt.Sprintf("falsification_results[%d].reasoning", i), result.Reasoning, 4096); err != nil {
			return err
		}
		if len(result.EvidenceRefs) == 0 {
			return fmt.Errorf("GCL DecisionPackage falsification_results[%d] requires evidence", i)
		}
		if err := validateGCLNestedEvidence(result.EvidenceRefs, knownEvidence); err != nil {
			return fmt.Errorf("GCL DecisionPackage falsification_results[%d]: %w", i, err)
		}
		if result.CandidateID == decision.SelectedCandidateID {
			selectedFalsification = true
			if result.Verdict != "survives" {
				return fmt.Errorf("GCL DecisionPackage selected candidate must survive every falsification check")
			}
		}
	}
	if !selectedFalsification {
		return fmt.Errorf("GCL DecisionPackage selected candidate requires a falsification result")
	}
	return nil
}

func validateGCLProposer(proposer gclProposerIdentity) error {
	if err := validateGCLString("proposer.agent_id", proposer.AgentID, 256); err != nil {
		return err
	}
	if !gclSPIFFEPattern.MatchString(proposer.WorkloadIdentity) {
		return fmt.Errorf("GCL DecisionPackage proposer.workload_identity is not a valid SPIFFE URI")
	}
	if !gclTrustDomainPattern.MatchString(proposer.TrustDomain) {
		return fmt.Errorf("GCL DecisionPackage proposer.trust_domain is invalid")
	}
	return nil
}

func validateGCLAgentPromotion(attestation gclAgentPromotionAttestation, packageCreatedAt time.Time) error {
	if attestation.Provider != "agent-promotion" {
		return fmt.Errorf("GCL DecisionPackage agent_promotion.provider must be agent-promotion")
	}
	if attestation.NonAuthoritative == nil || !*attestation.NonAuthoritative {
		return fmt.Errorf("GCL DecisionPackage agent_promotion must be non-authoritative")
	}
	if err := validateGCLString("agent_promotion.subject", attestation.Subject, 256); err != nil {
		return err
	}
	if _, ok := gclActionClasses[attestation.ActionClass]; !ok {
		return fmt.Errorf("GCL DecisionPackage agent_promotion.action_class %q is not canonical", attestation.ActionClass)
	}
	switch attestation.Decision {
	case "allow", "refuse", "route_human", "unavailable":
	default:
		return fmt.Errorf("GCL DecisionPackage agent_promotion.decision %q is invalid", attestation.Decision)
	}
	if attestation.ConsequenceScore == nil || !gclUnitInterval(*attestation.ConsequenceScore) ||
		attestation.AutonomyCeiling == nil || !gclUnitInterval(*attestation.AutonomyCeiling) {
		return fmt.Errorf("GCL DecisionPackage agent_promotion scores must be present and between 0 and 1")
	}
	if !gclDigestPattern.MatchString(attestation.AttestationRef) {
		return fmt.Errorf("GCL DecisionPackage agent_promotion.attestation_ref is invalid")
	}
	if attestation.IssuedAt.IsZero() || attestation.ExpiresAt.IsZero() || !attestation.ExpiresAt.After(attestation.IssuedAt) {
		return fmt.Errorf("GCL DecisionPackage agent_promotion must expire after it is issued")
	}
	if !attestation.ExpiresAt.After(packageCreatedAt) {
		return fmt.Errorf("GCL DecisionPackage agent_promotion is already expired at package creation")
	}
	return nil
}

func validateGCLConstraint(index int, constraint gclDecisionConstraint, knownEvidence map[string]struct{}) error {
	if err := validateGCLString(fmt.Sprintf("constraints[%d].constraint_id", index), constraint.ConstraintID, 256); err != nil {
		return err
	}
	if err := validateGCLString(fmt.Sprintf("constraints[%d].constraint_type", index), constraint.ConstraintType, 128); err != nil {
		return err
	}
	if constraint.Hard == nil || constraint.Bound == nil {
		return fmt.Errorf("GCL DecisionPackage constraints[%d] requires hard and bound", index)
	}
	if constraint.Confidence == nil || !gclUnitInterval(*constraint.Confidence) {
		return fmt.Errorf("GCL DecisionPackage constraints[%d].confidence must be present and between 0 and 1", index)
	}
	if len(constraint.EvidenceRefs) == 0 {
		return fmt.Errorf("GCL DecisionPackage constraints[%d] requires evidence", index)
	}
	if err := validateGCLNestedEvidence(constraint.EvidenceRefs, knownEvidence); err != nil {
		return fmt.Errorf("GCL DecisionPackage constraints[%d]: %w", index, err)
	}
	return nil
}

func validateGCLCandidate(path string, candidate gclDecisionCandidate) error {
	if err := validateGCLString(path+".candidate_id", candidate.CandidateID, 256); err != nil {
		return err
	}
	if _, ok := gclActionClasses[candidate.ActionClass]; !ok {
		return fmt.Errorf("GCL DecisionPackage %s.action_class %q is not canonical", path, candidate.ActionClass)
	}
	if candidate.Parameters == nil {
		return fmt.Errorf("GCL DecisionPackage %s.parameters is required", path)
	}
	if candidate.Confidence == nil || !gclUnitInterval(*candidate.Confidence) {
		return fmt.Errorf("GCL DecisionPackage %s.confidence must be present and between 0 and 1", path)
	}
	return nil
}

func validateGCLNestedEvidence(refs []string, knownEvidence map[string]struct{}) error {
	for _, ref := range refs {
		if !gclDigestPattern.MatchString(ref) {
			return fmt.Errorf("nested evidence reference %q is invalid", ref)
		}
		if _, ok := knownEvidence[ref]; !ok {
			return fmt.Errorf("nested evidence reference %q is absent from evidence_refs", ref)
		}
	}
	return nil
}

func verifyGCLSignedPackage(signed gclSignedDecisionPackage, canonical []byte, keyring map[string][]byte) error {
	if signed.Algorithm != "HMAC-SHA256" {
		return fmt.Errorf("GCL DecisionPackage algorithm must be HMAC-SHA256")
	}
	if !gclDigestPattern.MatchString(signed.Digest) {
		return fmt.Errorf("GCL DecisionPackage digest must be sha256:<64 lowercase hex>")
	}
	if err := validateGCLString("data.key_id", signed.KeyID, 256); err != nil {
		return err
	}
	if !gclSignaturePattern.MatchString(signed.Signature) {
		return fmt.Errorf("GCL DecisionPackage signature must be unpadded base64url HMAC-SHA256")
	}
	digestBytes := sha256.Sum256(canonical)
	expectedDigest := "sha256:" + hex.EncodeToString(digestBytes[:])
	if !hmac.Equal([]byte(signed.Digest), []byte(expectedDigest)) {
		return fmt.Errorf("GCL DecisionPackage digest does not match canonical package")
	}
	key, ok := keyring[signed.KeyID]
	if !ok {
		return fmt.Errorf("GCL DecisionPackage signing key %q is unknown", signed.KeyID)
	}
	if len(key) < 32 {
		return fmt.Errorf("GCL DecisionPackage signing key %q must contain at least 32 bytes", signed.KeyID)
	}
	providedSignature, err := base64.RawURLEncoding.DecodeString(signed.Signature)
	if err != nil || len(providedSignature) != sha256.Size {
		return fmt.Errorf("GCL DecisionPackage signature is invalid")
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(canonical)
	if !hmac.Equal(providedSignature, mac.Sum(nil)) {
		return fmt.Errorf("GCL DecisionPackage signature verification failed")
	}
	return nil
}

func validateGCLEnvelope(event gclDecisionPackageCloudEvent, decision gclDecisionPackage) error {
	if event.SpecVersion != "1.0" {
		return fmt.Errorf("GCL DecisionPackage CloudEvent specversion must be 1.0")
	}
	if event.Type != GCLDecisionPackageCloudEventType {
		return fmt.Errorf("GCL DecisionPackage CloudEvent type must be %q", GCLDecisionPackageCloudEventType)
	}
	if event.DataContentType != "application/json" {
		return fmt.Errorf("GCL DecisionPackage CloudEvent datacontenttype must be application/json")
	}
	if event.DataSchema != GCLDecisionPackageSchemaURI {
		return fmt.Errorf("GCL DecisionPackage CloudEvent dataschema must be %q", GCLDecisionPackageSchemaURI)
	}
	if !gclSourcePattern.MatchString(event.Source) {
		return fmt.Errorf("GCL DecisionPackage CloudEvent source is invalid")
	}
	if event.Traceparent != nil && !gclTraceparentPattern.MatchString(*event.Traceparent) {
		return fmt.Errorf("GCL DecisionPackage CloudEvent traceparent is invalid")
	}
	expectedFingerprint := sha256.Sum256([]byte(decision.PackageID + ":" + event.Data.Digest + ":" + GCLDecisionPackageCloudEventType))
	expectedID := "urn:sha256:" + hex.EncodeToString(expectedFingerprint[:])
	if event.ID != expectedID {
		return fmt.Errorf("GCL DecisionPackage CloudEvent id does not match package identity")
	}
	if event.Subject != "decision-package/"+decision.PackageID {
		return fmt.Errorf("GCL DecisionPackage CloudEvent subject does not match package_id")
	}
	if !event.Time.Equal(decision.CreatedAt) || !event.Expiry.Equal(decision.ExpiresAt) {
		return fmt.Errorf("GCL DecisionPackage CloudEvent timestamps do not match package timestamps")
	}
	if event.CorrelationID != decision.CorrelationID || event.CausationID != decision.CausationID ||
		event.IdempotencyID != decision.IdempotencyID || event.Tenant != decision.Tenant || event.Zone != decision.Zone {
		return fmt.Errorf("GCL DecisionPackage CloudEvent extensions do not match package scope and identities")
	}
	if len(event.Evidence) != len(decision.EvidenceRefs) {
		return fmt.Errorf("GCL DecisionPackage CloudEvent evidence does not match package evidence_refs")
	}
	for i := range event.Evidence {
		if event.Evidence[i] != decision.EvidenceRefs[i] {
			return fmt.Errorf("GCL DecisionPackage CloudEvent evidence does not match package evidence_refs")
		}
	}
	return nil
}

func projectGCLDecision(event gclDecisionPackageCloudEvent, decision gclDecisionPackage) (FleetIntent, error) {
	var selected *gclDecisionCandidate
	for i := range decision.Candidates {
		if decision.Candidates[i].CandidateID == decision.SelectedCandidateID {
			selected = &decision.Candidates[i]
			break
		}
	}
	if selected == nil {
		return FleetIntent{}, fmt.Errorf("GCL DecisionPackage selected candidate is unavailable")
	}
	intentType, ok := gclActionClasses[selected.ActionClass]
	if !ok {
		return FleetIntent{}, fmt.Errorf("GCL DecisionPackage selected action class %q is unsupported", selected.ActionClass)
	}
	poolParameter := "pool"
	if selected.ActionClass == "fleet.migrate" {
		poolParameter = "source_pool"
		if _, err := requiredGCLStringParameter(selected.Parameters, "target_pool"); err != nil {
			return FleetIntent{}, err
		}
	}
	pool, err := requiredGCLStringParameter(selected.Parameters, poolParameter)
	if err != nil {
		return FleetIntent{}, err
	}
	expiresAt := decision.ExpiresAt.UTC()
	horizon := int64(decision.ExpiresAt.Sub(decision.CreatedAt) / time.Second)
	maxInt := int64(^uint(0) >> 1)
	if horizon < 0 || horizon > maxInt {
		return FleetIntent{}, fmt.Errorf("GCL DecisionPackage validity horizon cannot be represented by Fleet")
	}
	justification := fmt.Sprintf("GCL decision %s selected candidate %s", decision.PackageID, selected.CandidateID)
	for _, result := range decision.FalsificationResults {
		if result.CandidateID == selected.CandidateID {
			justification = result.Reasoning
			break
		}
	}
	evidence := make([]EvidenceDigest, 0, len(decision.EvidenceRefs))
	for _, ref := range decision.EvidenceRefs {
		digest := strings.TrimPrefix(ref, "sha256:")
		evidence = append(evidence, EvidenceDigest{
			URI:    "urn:sha256:" + digest,
			SHA256: digest,
		})
	}
	intent := FleetIntent{
		ID:             decision.PackageID,
		CorrelationID:  decision.CorrelationID,
		IdempotencyKey: decision.IdempotencyID,
		Type:           intentType,
		Confidence:     *decision.Confidence,
		HorizonSeconds: int(horizon),
		Justification:  justification,
		StateSnapshot: map[string]any{
			"parameters":            selected.Parameters,
			"predicted_effect":      selected.PredictedEffect,
			"tenant":                decision.Tenant,
			"zone":                  decision.Zone,
			"causation_id":          decision.CausationID,
			"event_id":              event.ID,
			"event_source":          event.Source,
			"proposer_agent_id":     decision.Proposer.AgentID,
			"proposer_trust_domain": decision.Proposer.TrustDomain,
		},
		CreatedAt:             decision.CreatedAt.UTC(),
		ExpiresAt:             &expiresAt,
		DecisionPackageRef:    "gcl://decision-packages/" + decision.PackageID,
		DecisionPackageDigest: strings.TrimPrefix(event.Data.Digest, "sha256:"),
		Evidence:              evidence,
		Proposer: &ProposerAuthority{
			Subject:      decision.Proposer.WorkloadIdentity,
			AuthorityRef: "gcl://signing-keys/" + url.PathEscape(event.Data.KeyID),
			MaxAction:    selected.ActionClass,
			ExpiresAt:    &expiresAt,
		},
		Pool: pool,
	}
	if err := applyGCLParameters(&intent, selected.Parameters); err != nil {
		return FleetIntent{}, err
	}
	return intent, nil
}

func applyGCLParameters(intent *FleetIntent, parameters map[string]any) error {
	stringFields := []struct {
		name   string
		target *string
	}{
		{"model", &intent.Model},
		{"metric", &intent.Metric},
		{"severity", &intent.Severity},
		{"message", &intent.Message},
		{"reason", &intent.Reason},
		{"recommended_action", &intent.RecommendedAction},
	}
	for _, field := range stringFields {
		value, present, err := optionalGCLStringParameter(parameters, field.name)
		if err != nil {
			return err
		}
		if present {
			*field.target = value
		}
	}
	intFields := []struct {
		name   string
		target *int
	}{
		{"target_replicas", &intent.TargetReplicas},
		{"replicas", &intent.DesiredReplicas},
		{"current_replicas", &intent.CurrentReplicas},
		{"max_inflight", &intent.MaxInflight},
		{"duration_seconds", &intent.DurationSeconds},
	}
	for _, field := range intFields {
		value, present, err := optionalGCLIntParameter(parameters, field.name)
		if err != nil {
			return err
		}
		if present {
			*field.target = value
		}
	}
	targetClusters, present, err := optionalGCLStringSliceParameter(parameters, "target_clusters")
	if err != nil {
		return err
	}
	if present {
		intent.TargetClusters = targetClusters
	}
	return nil
}

func requiredGCLStringParameter(parameters map[string]any, name string) (string, error) {
	value, present, err := optionalGCLStringParameter(parameters, name)
	if err != nil {
		return "", err
	}
	if !present || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("GCL DecisionPackage selected candidate requires non-empty string parameter %q", name)
	}
	return value, nil
}

func optionalGCLStringParameter(parameters map[string]any, name string) (string, bool, error) {
	raw, present := parameters[name]
	if !present {
		return "", false, nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", false, fmt.Errorf("GCL DecisionPackage selected candidate parameter %q must be a string", name)
	}
	return value, true, nil
}

func optionalGCLIntParameter(parameters map[string]any, name string) (int, bool, error) {
	raw, present := parameters[name]
	if !present {
		return 0, false, nil
	}
	switch value := raw.(type) {
	case json.Number:
		integer, err := fleetRangeGCLInteger(value.String())
		if err != nil {
			return 0, false, fmt.Errorf("GCL DecisionPackage selected candidate parameter %q %w", name, err)
		}
		return integer, true, nil
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value {
			return 0, false, fmt.Errorf("GCL DecisionPackage selected candidate parameter %q must be an integer", name)
		}
		integer, err := fleetRangeGCLInteger(strconv.FormatFloat(value, 'f', -1, 64))
		if err != nil {
			return 0, false, fmt.Errorf("GCL DecisionPackage selected candidate parameter %q %w", name, err)
		}
		return integer, true, nil
	case float32:
		return optionalGCLIntParameter(map[string]any{name: float64(value)}, name)
	case int:
		return value, true, nil
	case int64:
		integer, err := fleetRangeGCLInteger(strconv.FormatInt(value, 10))
		if err != nil {
			return 0, false, fmt.Errorf("GCL DecisionPackage selected candidate parameter %q %w", name, err)
		}
		return integer, true, nil
	default:
		return 0, false, fmt.Errorf("GCL DecisionPackage selected candidate parameter %q must be an integer", name)
	}
}

func fleetRangeGCLInteger(raw string) (int, error) {
	rational, ok := new(big.Rat).SetString(raw)
	if !ok || !rational.IsInt() {
		return 0, fmt.Errorf("must be an integer")
	}
	integer := rational.Num()
	if !integer.IsInt64() {
		return 0, fmt.Errorf("is outside Fleet integer range")
	}
	value := integer.Int64()
	if strconv.IntSize == 32 && (value > math.MaxInt32 || value < math.MinInt32) {
		return 0, fmt.Errorf("is outside Fleet integer range")
	}
	return int(value), nil
}

func optionalGCLStringSliceParameter(parameters map[string]any, name string) ([]string, bool, error) {
	raw, present := parameters[name]
	if !present {
		return nil, false, nil
	}
	items, ok := raw.([]any)
	if !ok {
		if stringsValue, ok := raw.([]string); ok {
			return append([]string(nil), stringsValue...), true, nil
		}
		return nil, false, fmt.Errorf("GCL DecisionPackage selected candidate parameter %q must be an array of strings", name)
	}
	values := make([]string, len(items))
	for i, item := range items {
		value, ok := item.(string)
		if !ok {
			return nil, false, fmt.Errorf("GCL DecisionPackage selected candidate parameter %q[%d] must be a string", name, i)
		}
		values[i] = value
	}
	return values, true, nil
}

func validateGCLString(name, value string, maxLength int) error {
	length := utf8.RuneCountInString(value)
	if length == 0 || length > maxLength {
		return fmt.Errorf("GCL DecisionPackage %s must contain 1 to %d characters", name, maxLength)
	}
	return nil
}

func gclUnitInterval(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 1
}

// canonicalGCLJSON matches GCL's canonical_json contract: object keys are
// sorted, separators are compact, arrays retain order, and UTF-8 is not
// rewritten as ASCII escapes. Numbers are normalized with Python json.dumps
// integer/float notation rather than pydantic-core's transport notation.
func canonicalGCLJSON(payload []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values are not allowed")
		}
		return nil, err
	}
	var result bytes.Buffer
	if err := writeCanonicalGCLJSON(&result, value); err != nil {
		return nil, err
	}
	return result.Bytes(), nil
}

func writeCanonicalGCLJSON(destination *bytes.Buffer, value any) error {
	switch typed := value.(type) {
	case nil:
		destination.WriteString("null")
	case bool:
		if typed {
			destination.WriteString("true")
		} else {
			destination.WriteString("false")
		}
	case json.Number:
		canonicalNumber, err := canonicalGCLNumber(typed)
		if err != nil {
			return err
		}
		destination.WriteString(canonicalNumber)
	case string:
		writeCanonicalGCLString(destination, typed)
	case []any:
		destination.WriteByte('[')
		for i, item := range typed {
			if i > 0 {
				destination.WriteByte(',')
			}
			if err := writeCanonicalGCLJSON(destination, item); err != nil {
				return err
			}
		}
		destination.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		destination.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				destination.WriteByte(',')
			}
			writeCanonicalGCLString(destination, key)
			destination.WriteByte(':')
			if err := writeCanonicalGCLJSON(destination, typed[key]); err != nil {
				return err
			}
		}
		destination.WriteByte('}')
	default:
		return fmt.Errorf("unsupported canonical JSON value %T", value)
	}
	return nil
}

func canonicalGCLNumber(number json.Number) (string, error) {
	raw := number.String()
	if !strings.ContainsAny(raw, ".eE") {
		integer, ok := new(big.Int).SetString(raw, 10)
		if !ok {
			return "", fmt.Errorf("invalid canonical JSON integer %q", raw)
		}
		return integer.String(), nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return "", fmt.Errorf("invalid canonical JSON float %q", raw)
	}
	abs := math.Abs(value)
	format := byte('e')
	if abs == 0 {
		format = 'f'
	} else {
		exponent := int(math.Floor(math.Log10(abs)))
		// CPython's float repr (and therefore json.dumps) uses fixed notation
		// for decimal exponents -4 through 15, inclusive.
		if exponent >= -4 && exponent < 16 {
			format = 'f'
		}
	}
	result := strconv.FormatFloat(value, format, -1, 64)
	if !strings.ContainsAny(result, ".e") {
		result += ".0"
	}
	return result, nil
}

func writeCanonicalGCLString(destination *bytes.Buffer, value string) {
	const hexDigits = "0123456789abcdef"
	destination.WriteByte('"')
	for _, character := range value {
		switch character {
		case '"':
			destination.WriteString(`\"`)
		case '\\':
			destination.WriteString(`\\`)
		case '\b':
			destination.WriteString(`\b`)
		case '\f':
			destination.WriteString(`\f`)
		case '\n':
			destination.WriteString(`\n`)
		case '\r':
			destination.WriteString(`\r`)
		case '\t':
			destination.WriteString(`\t`)
		default:
			if character < 0x20 {
				destination.WriteString(`\u00`)
				destination.WriteByte(hexDigits[byte(character)>>4])
				destination.WriteByte(hexDigits[byte(character)&0x0f])
			} else {
				destination.WriteRune(character)
			}
		}
	}
	destination.WriteByte('"')
}
