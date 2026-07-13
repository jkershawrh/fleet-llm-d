package intents

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/ledger"
)

var (
	ErrNotFound          = errors.New("intent or operation not found")
	ErrInvalidTransition = errors.New("invalid operation transition")
)

// Repository is the persistence boundary for FleetIntent and FleetOperation.
// Production implementations persist these as v1beta1 CRDs; MemoryRepository
// is intentionally limited to tests and standalone development.
type Repository interface {
	Create(context.Context, FleetIntent, FleetOperation) error
	GetIntent(context.Context, string) (FleetIntent, error)
	GetOperation(context.Context, string) (FleetOperation, error)
	FindByIdempotencyKey(context.Context, string) (FleetOperation, error)
	UpdateOperation(context.Context, FleetOperation) error
}

// MemoryRepository is a concurrency-safe development repository.
type MemoryRepository struct {
	mu          sync.RWMutex
	intents     map[string]FleetIntent
	operations  map[string]FleetOperation
	idempotency map[string]string
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		intents:     make(map[string]FleetIntent),
		operations:  make(map[string]FleetOperation),
		idempotency: make(map[string]string),
	}
}

func (r *MemoryRepository) Create(_ context.Context, intent FleetIntent, operation FleetOperation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.intents[intent.ID]; exists {
		return fmt.Errorf("intent %q already exists", intent.ID)
	}
	if intent.IdempotencyKey != "" {
		if existing, exists := r.idempotency[intent.IdempotencyKey]; exists {
			return fmt.Errorf("idempotency key already belongs to operation %q", existing)
		}
		r.idempotency[intent.IdempotencyKey] = operation.ID
	}
	r.intents[intent.ID] = cloneIntent(intent)
	r.operations[operation.ID] = cloneOperation(operation)
	return nil
}

func (r *MemoryRepository) GetIntent(_ context.Context, id string) (FleetIntent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	intent, ok := r.intents[id]
	if !ok {
		return FleetIntent{}, ErrNotFound
	}
	return cloneIntent(intent), nil
}

func (r *MemoryRepository) GetOperation(_ context.Context, id string) (FleetOperation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	operation, ok := r.operations[id]
	if !ok {
		return FleetOperation{}, ErrNotFound
	}
	return cloneOperation(operation), nil
}

func (r *MemoryRepository) FindByIdempotencyKey(_ context.Context, key string) (FleetOperation, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.idempotency[key]
	if !ok {
		return FleetOperation{}, ErrNotFound
	}
	return cloneOperation(r.operations[id]), nil
}

func (r *MemoryRepository) UpdateOperation(_ context.Context, operation FleetOperation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.operations[operation.ID]; !ok {
		return ErrNotFound
	}
	r.operations[operation.ID] = cloneOperation(operation)
	return nil
}

func cloneIntent(in FleetIntent) FleetIntent {
	out := in
	out.TargetClusters = append([]string(nil), in.TargetClusters...)
	out.Evidence = append([]EvidenceDigest(nil), in.Evidence...)
	if in.StateSnapshot != nil {
		out.StateSnapshot = make(map[string]interface{}, len(in.StateSnapshot))
		for key, value := range in.StateSnapshot {
			out.StateSnapshot[key] = value
		}
	}
	return out
}

func cloneOperation(in FleetOperation) FleetOperation {
	out := in
	out.Transitions = append([]OperationTransition(nil), in.Transitions...)
	out.LedgerReceipts = append([]OperationLedgerReceipt(nil), in.LedgerReceipts...)
	if in.AuthorizationRef != nil {
		authorization := *in.AuthorizationRef
		out.AuthorizationRef = &authorization
	}
	if in.Provider != nil {
		provider := *in.Provider
		out.Provider = &provider
	}
	return out
}

// ProofVerifier validates a portable receipt against the standalone immutable
// ledger. ledger.LedgerClient implements this interface directly.
type ProofVerifier interface {
	VerifyProof(context.Context, string, string) (*ledger.ProofVerification, error)
}

// Service owns asynchronous intent admission and operation lifecycle rules.
type Service struct {
	repository Repository
	policy     PolicyConfig
	proofs     ProofVerifier
	now        func() time.Time
}

func NewService(repository Repository, policy PolicyConfig, proofVerifier ...ProofVerifier) *Service {
	service := &Service{repository: repository, policy: policy, now: time.Now}
	if len(proofVerifier) > 0 {
		service.proofs = proofVerifier[0]
	}
	return service
}

func (s *Service) Submit(ctx context.Context, intent FleetIntent) (SubmissionResponse, error) {
	if intent.IdempotencyKey != "" {
		if existing, err := s.repository.FindByIdempotencyKey(ctx, intent.IdempotencyKey); err == nil {
			return submissionFromOperation(existing, "idempotent replay"), nil
		} else if !errors.Is(err, ErrNotFound) {
			return SubmissionResponse{}, err
		}
	}

	now := s.now().UTC()
	if intent.ID == "" {
		intent.ID = operationResourceID("intent", intent.IdempotencyKey)
	}
	if intent.CorrelationID == "" {
		intent.CorrelationID = intent.ID
	}
	if intent.CreatedAt.IsZero() {
		intent.CreatedAt = now
	}

	operation := FleetOperation{
		ID:             operationResourceID("operation", intent.IdempotencyKey),
		IntentID:       intent.ID,
		CorrelationID:  intent.CorrelationID,
		IdempotencyKey: intent.IdempotencyKey,
		ActionClass:    actionClass(intent.Type),
		State:          StateReceived,
		CreatedAt:      now,
		UpdatedAt:      now,
		Transitions: []OperationTransition{{
			Sequence: 1,
			State:    StateReceived,
			At:       now,
			Reason:   "intent received",
			Actor:    "fleet-api",
		}},
	}
	operation.ObjectUID = operation.ID

	reason := ""
	next := StateAccepted
	if intent.ExpiresAt != nil && !intent.ExpiresAt.After(now) {
		next = StateExpired
		reason = "intent expired before admission"
	} else {
		decision := Evaluate(ctx, intent, s.policy)
		reason = decision.Reason
		switch decision.Status {
		case StatusAccepted:
			next = StateAccepted
		case StatusDeferred:
			next = StateDeferred
		case StatusRefused:
			next = StateRejected
		default:
			return SubmissionResponse{}, fmt.Errorf("unsupported admission status %q", decision.Status)
		}
	}
	operation = appendTransition(operation, next, reason, "fleet-policy")
	if err := s.repository.Create(ctx, intent, operation); err != nil {
		if intent.IdempotencyKey != "" {
			if existing, replayErr := s.repository.FindByIdempotencyKey(ctx, intent.IdempotencyKey); replayErr == nil {
				return submissionFromOperation(existing, "idempotent concurrent replay"), nil
			}
		}
		return SubmissionResponse{}, err
	}
	return submissionFromOperation(operation, reason), nil
}

func (s *Service) GetIntent(ctx context.Context, id string) (FleetIntent, error) {
	return s.repository.GetIntent(ctx, id)
}

func (s *Service) GetOperation(ctx context.Context, id string) (FleetOperation, error) {
	return s.repository.GetOperation(ctx, id)
}

// AttachLedgerReceipt records durable evidence without advancing the
// lifecycle. Advancing to AUTHORIZED or SUCCEEDED remains an explicit step.
func (s *Service) AttachLedgerReceipt(ctx context.Context, id string, receipt OperationLedgerReceipt) (FleetOperation, error) {
	operation, err := s.repository.GetOperation(ctx, id)
	if err != nil {
		return FleetOperation{}, err
	}
	if err := s.verifyLedgerReceipt(ctx, operation, receipt); err != nil {
		return FleetOperation{}, fmt.Errorf("verify immutable-ledger receipt: %w", err)
	}
	for _, existing := range operation.LedgerReceipts {
		if existing.EntryID == receipt.EntryID || existing.EntryHash == receipt.EntryHash {
			if existing == receipt {
				return operation, nil
			}
			return FleetOperation{}, fmt.Errorf("immutable-ledger receipt conflicts with existing entry %q", existing.EntryID)
		}
	}
	operation.LedgerEntryID = receipt.EntryID
	operation.LedgerReceipts = append(operation.LedgerReceipts, receipt)
	if len(operation.Transitions) > 0 {
		operation.Transitions[len(operation.Transitions)-1].LedgerEntryID = receipt.EntryID
	}
	operation.UpdatedAt = s.now().UTC()
	if err := s.repository.UpdateOperation(ctx, operation); err != nil {
		return FleetOperation{}, err
	}
	return operation, nil
}

func (s *Service) Transition(ctx context.Context, id string, next OperationState, reason, actor string) (FleetOperation, error) {
	operation, err := s.repository.GetOperation(ctx, id)
	if err != nil {
		return FleetOperation{}, err
	}
	if !allowedTransition(operation.State, next) {
		return FleetOperation{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, operation.State, next)
	}
	if err := s.validateTransitionEvidence(ctx, operation, next); err != nil {
		return FleetOperation{}, fmt.Errorf("%w: %s -> %s: %v", ErrInvalidTransition, operation.State, next, err)
	}
	operation = appendTransition(operation, next, reason, actor)
	if err := s.repository.UpdateOperation(ctx, operation); err != nil {
		return FleetOperation{}, err
	}
	return operation, nil
}

func (s *Service) Approve(ctx context.Context, id, actor string) (FleetOperation, error) {
	return s.Transition(ctx, id, StateAccepted, "human approval recorded", actor)
}

func (s *Service) Cancel(ctx context.Context, id, actor string) (FleetOperation, error) {
	operation, err := s.repository.GetOperation(ctx, id)
	if err != nil {
		return FleetOperation{}, err
	}
	next := StateFailed
	if operation.State == StateActuating || operation.State == StateObserving || operation.State == StateVerified || operation.State == StateEvidencePending {
		next = StateRollingBack
	}
	return s.Transition(ctx, id, next, "operation cancelled", actor)
}

func appendTransition(operation FleetOperation, state OperationState, reason, actor string) FleetOperation {
	now := time.Now().UTC()
	operation.State = state
	operation.UpdatedAt = now
	operation.Transitions = append(operation.Transitions, OperationTransition{
		Sequence: int64(len(operation.Transitions) + 1),
		State:    state,
		At:       now,
		Reason:   reason,
		Actor:    actor,
	})
	return operation
}

func submissionFromOperation(operation FleetOperation, reason string) SubmissionResponse {
	return SubmissionResponse{
		IntentID:      operation.IntentID,
		OperationID:   operation.ID,
		CorrelationID: operation.CorrelationID,
		State:         operation.State,
		Reason:        reason,
		StatusURL:     "/api/v2/operations/" + operation.ID,
	}
}

func allowedTransition(from, to OperationState) bool {
	allowed := map[OperationState]map[OperationState]bool{
		StateReceived:          {StateAccepted: true, StateDeferred: true, StateRejected: true, StateExpired: true},
		StateDeferred:          {StateAccepted: true, StateRejected: true, StateExpired: true, StateFailed: true},
		StateAccepted:          {StatePlanned: true, StateFailed: true, StateExpired: true},
		StatePlanned:           {StateGovernancePending: true, StateFailed: true},
		StateGovernancePending: {StateAuthorized: true, StateRejected: true, StateDeferred: true, StateFailed: true, StateQuarantined: true},
		StateAuthorized:        {StateActuating: true, StateFailed: true, StateExpired: true, StateQuarantined: true},
		StateActuating:         {StateObserving: true, StateFailed: true, StateRollingBack: true, StateQuarantined: true},
		StateObserving:         {StateVerified: true, StateFailed: true, StateRollingBack: true, StateQuarantined: true},
		StateVerified:          {StateEvidencePending: true, StateRollingBack: true, StateQuarantined: true},
		StateEvidencePending:   {StateSucceeded: true, StateRollingBack: true, StateQuarantined: true},
		StateRollingBack:       {StateRolledBack: true, StateQuarantined: true},
	}
	return allowed[from][to]
}

func (s *Service) validateTransitionEvidence(ctx context.Context, operation FleetOperation, next OperationState) error {
	switch next {
	case StatePlanned:
		if !validSHA256(operation.PlanDigest) {
			return fmt.Errorf("a durable SHA-256 plan digest is required")
		}
		if operation.Provider == nil || strings.TrimSpace(operation.Provider.Type) == "" || strings.TrimSpace(operation.Provider.Name) == "" {
			return fmt.Errorf("a provider reference with type and name is required")
		}
	case StateAuthorized:
		if err := validateAuthorizationReference(operation, s.now().UTC()); err != nil {
			return err
		}
	case StateActuating:
		if err := validateAuthorizationReference(operation, s.now().UTC()); err != nil {
			return err
		}
	case StateSucceeded:
		if err := s.verifyReceiptPurpose(ctx, operation, ReceiptPurposeOutcome); err != nil {
			return fmt.Errorf("a durable verified immutable-ledger outcome receipt is required: %w", err)
		}
	}
	return nil
}

func (s *Service) verifyReceiptPurpose(ctx context.Context, operation FleetOperation, purpose LedgerReceiptPurpose) error {
	found := false
	var lastErr error
	for _, receipt := range operation.LedgerReceipts {
		if receipt.Purpose != purpose {
			continue
		}
		found = true
		if err := s.verifyLedgerReceipt(ctx, operation, receipt); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	if !found {
		return fmt.Errorf("receipt purpose %q is absent", purpose)
	}
	return lastErr
}

func (s *Service) verifyLedgerReceipt(ctx context.Context, operation FleetOperation, receipt OperationLedgerReceipt) error {
	if strings.TrimSpace(receipt.EntryID) == "" || strings.TrimSpace(receipt.EntryHash) == "" ||
		strings.TrimSpace(receipt.EntryType) == "" || receipt.ChainPosition < 1 || receipt.WrittenTS < 1 ||
		!validSHA256(receipt.InputHash) || !validReceiptPurpose(receipt.Purpose) {
		return fmt.Errorf("receipt requires entry ID/hash/type, a SHA-256 input hash, positive chain position/written timestamp, and a recognized purpose")
	}
	if expected := expectedLedgerEntryType(operation.ActionClass, receipt.Purpose); receipt.EntryType != expected {
		return fmt.Errorf("entry type %q cannot satisfy purpose %q; expected %q", receipt.EntryType, receipt.Purpose, expected)
	}
	if s.proofs == nil {
		return fmt.Errorf("proof verifier is not configured")
	}
	verification, err := s.proofs.VerifyProof(ctx, receipt.EntryHash, receipt.EntryType)
	if err != nil {
		return err
	}
	if verification == nil || !verification.Valid {
		if verification != nil && strings.TrimSpace(verification.FailureReason) != "" {
			return fmt.Errorf("ledger rejected proof: %s", verification.FailureReason)
		}
		return fmt.Errorf("ledger rejected proof")
	}
	if verification.EntryType != receipt.EntryType {
		return fmt.Errorf("proof entry type does not match receipt")
	}
	if verification.EntryID != "" && verification.EntryID != receipt.EntryID {
		return fmt.Errorf("proof entry ID does not match receipt")
	}
	if verification.CorrelationID != operation.CorrelationID {
		return fmt.Errorf("proof correlation ID %q does not match operation %q", verification.CorrelationID, operation.CorrelationID)
	}
	if verification.InputHash != receipt.InputHash {
		return fmt.Errorf("proof input hash does not match receipt")
	}
	if receipt.Purpose == ReceiptPurposeOutcome {
		if !validSHA256(operation.ObservedDigest) {
			return fmt.Errorf("operation requires a SHA-256 observed digest before outcome evidence can be attached")
		}
		if !strings.EqualFold(receipt.InputHash, operation.ObservedDigest) {
			return fmt.Errorf("outcome input hash is not bound to the operation observed digest")
		}
		if err := verifyOutcomeEvidence(operation, verification.Content); err != nil {
			return err
		}
	}
	if verification.ChainPosition != receipt.ChainPosition {
		return fmt.Errorf("proof chain position does not match receipt")
	}
	if verification.WrittenAt.IsZero() || verification.WrittenAt.UnixMilli() != receipt.WrittenTS {
		return fmt.Errorf("proof written timestamp does not match receipt")
	}
	return nil
}

const outcomeEvidenceSchemaV1 = "fleet.llm-d.ai/operation-outcome/v1"

type outcomeEvidenceV1 struct {
	SchemaVersion  string `json:"schema_version"`
	OperationID    string `json:"operation_id"`
	OperationUID   string `json:"operation_uid"`
	PlanDigest     string `json:"plan_digest"`
	IdempotencyKey string `json:"idempotency_key"`
	ObservedDigest string `json:"observed_digest"`
}

// NewOutcomeEvidence returns the canonical JSON payload that must be written
// to the immutable ledger for an operation outcome. The proof entry hash
// commits this envelope, binding otherwise reusable correlation and observed
// digests to one operation identity and plan.
func NewOutcomeEvidence(operation FleetOperation) ([]byte, error) {
	operationID := strings.TrimSpace(operation.ID)
	if operationID == "" {
		return nil, fmt.Errorf("operation ID is required for outcome evidence")
	}
	operationUID := strings.TrimSpace(operation.ObjectUID)
	if operationUID == "" {
		operationUID = operationID
	}
	if !validSHA256(operation.PlanDigest) {
		return nil, fmt.Errorf("a SHA-256 plan digest is required for outcome evidence")
	}
	if !validSHA256(operation.ObservedDigest) {
		return nil, fmt.Errorf("a SHA-256 observed digest is required for outcome evidence")
	}
	return json.Marshal(outcomeEvidenceV1{
		SchemaVersion:  outcomeEvidenceSchemaV1,
		OperationID:    operationID,
		OperationUID:   operationUID,
		PlanDigest:     strings.ToLower(operation.PlanDigest),
		IdempotencyKey: operation.IdempotencyKey,
		ObservedDigest: strings.ToLower(operation.ObservedDigest),
	})
}

func verifyOutcomeEvidence(operation FleetOperation, content []byte) error {
	if len(content) == 0 {
		return fmt.Errorf("outcome proof is missing its committed operation evidence")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var evidence outcomeEvidenceV1
	if err := decoder.Decode(&evidence); err != nil {
		return fmt.Errorf("decode committed outcome evidence: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return fmt.Errorf("decode committed outcome evidence: %w", err)
	}
	expectedContent, err := NewOutcomeEvidence(operation)
	if err != nil {
		return err
	}
	var expected outcomeEvidenceV1
	if err := json.Unmarshal(expectedContent, &expected); err != nil {
		return fmt.Errorf("construct expected outcome evidence: %w", err)
	}
	if evidence != expected || !bytes.Equal(content, expectedContent) {
		return fmt.Errorf("outcome proof content is not bound to this operation identity, plan, idempotency key, and observed digest")
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing interface{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func expectedLedgerEntryType(actionClass string, purpose LedgerReceiptPurpose) string {
	switch purpose {
	case ReceiptPurposeAdmission:
		action := strings.TrimPrefix(actionClass, "fleet.")
		if action == "prewarm" {
			action = string(IntentPreWarm)
		}
		return "fleet.intent." + action
	case ReceiptPurposeAuthorizationDecision:
		return LedgerEntryTypeAuthorizationDecision
	case ReceiptPurposeOutcome:
		return LedgerEntryTypeOutcome
	default:
		return ""
	}
}

func validateAuthorizationReference(operation FleetOperation, now time.Time) error {
	ref := operation.AuthorizationRef
	if ref == nil {
		return fmt.Errorf("a fleet authorization reference is required")
	}
	required := []struct {
		name  string
		value string
	}{
		{name: "grant ID", value: ref.GrantID},
		{name: "subject", value: ref.Subject},
		{name: "action class", value: ref.ActionClass},
		{name: "object UID", value: ref.ObjectUID},
		{name: "spec digest", value: ref.SpecDigest},
		{name: "audience", value: ref.Audience},
		{name: "idempotency key", value: ref.IdempotencyKey},
	}
	for _, field := range required {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("fleet authorization %s is required", field.name)
		}
	}
	if ref.ActionClass != operation.ActionClass {
		return fmt.Errorf("fleet authorization action class %q does not match operation %q", ref.ActionClass, operation.ActionClass)
	}
	objectUID := operation.ObjectUID
	if strings.TrimSpace(objectUID) == "" {
		objectUID = operation.ID
	}
	if ref.ObjectUID != objectUID {
		return fmt.Errorf("fleet authorization object UID %q does not match operation %q", ref.ObjectUID, objectUID)
	}
	if !validSHA256(operation.PlanDigest) || !strings.EqualFold(ref.SpecDigest, operation.PlanDigest) {
		return fmt.Errorf("fleet authorization spec digest is not bound to the operation plan")
	}
	if ref.Audience != AuthorizationAudienceFleetController {
		return fmt.Errorf("fleet authorization audience %q does not match %q", ref.Audience, AuthorizationAudienceFleetController)
	}
	if ref.IdempotencyKey != operation.IdempotencyKey {
		return fmt.Errorf("fleet authorization idempotency key does not match the operation")
	}
	if ref.ExpiresAt.IsZero() || !ref.ExpiresAt.After(now) {
		return fmt.Errorf("fleet authorization is expired")
	}
	if ref.BreakGlass && strings.TrimSpace(ref.IncidentRef) == "" {
		return fmt.Errorf("fleet break-glass authorization requires an incident reference")
	}
	return nil
}

func validReceiptPurpose(purpose LedgerReceiptPurpose) bool {
	switch purpose {
	case ReceiptPurposeAdmission, ReceiptPurposeAuthorizationDecision, ReceiptPurposeOutcome:
		return true
	default:
		return false
	}
}

func newID(prefix string) string {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(raw[:])
}

func operationResourceID(prefix, idempotencyKey string) string {
	if strings.TrimSpace(idempotencyKey) == "" {
		return newID(prefix)
	}
	digest := sha256.Sum256([]byte(idempotencyKey))
	return prefix + "-" + hex.EncodeToString(digest[:12])
}
