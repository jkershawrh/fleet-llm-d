package intents

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
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
	return out
}

// Service owns asynchronous intent admission and operation lifecycle rules.
type Service struct {
	repository Repository
	policy     PolicyConfig
	now        func() time.Time
}

func NewService(repository Repository, policy PolicyConfig) *Service {
	return &Service{repository: repository, policy: policy, now: time.Now}
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
	if strings.TrimSpace(receipt.EntryID) == "" || strings.TrimSpace(receipt.ChainHash) == "" || !validReceiptPurpose(receipt.Purpose) {
		return FleetOperation{}, fmt.Errorf("ledger receipt requires entry ID, chain hash, and a recognized purpose")
	}
	for _, existing := range operation.LedgerReceipts {
		if existing.EntryID == receipt.EntryID {
			return operation, nil
		}
	}
	operation.LedgerEntryID = receipt.EntryID
	operation.LedgerReceipts = append(operation.LedgerReceipts, receipt)
	if len(operation.Transitions) > 0 {
		operation.Transitions[len(operation.Transitions)-1].LedgerEntryID = receipt.EntryID
	}
	operation.UpdatedAt = time.Now().UTC()
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
	if err := validateTransitionEvidence(operation, next); err != nil {
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

func validateTransitionEvidence(operation FleetOperation, next OperationState) error {
	switch next {
	case StatePlanned:
		if !validSHA256(operation.PlanDigest) {
			return fmt.Errorf("a durable SHA-256 plan digest is required")
		}
	case StateAuthorized:
		if strings.TrimSpace(operation.AuthorizationRef) == "" {
			return fmt.Errorf("an ARE authorization reference is required")
		}
	case StateActuating:
		if !hasReceiptPurpose(operation, ReceiptPurposeAuthorization) {
			return fmt.Errorf("a durable ARE authorization receipt is required")
		}
	case StateSucceeded:
		if !hasReceiptPurpose(operation, ReceiptPurposeOutcome) {
			return fmt.Errorf("a durable ARE outcome acknowledgement is required")
		}
	}
	return nil
}

func hasReceiptPurpose(operation FleetOperation, purpose LedgerReceiptPurpose) bool {
	for _, receipt := range operation.LedgerReceipts {
		if receipt.Purpose == purpose {
			return true
		}
	}
	return false
}

func validReceiptPurpose(purpose LedgerReceiptPurpose) bool {
	switch purpose {
	case ReceiptPurposeAdmission, ReceiptPurposeAuthorization, ReceiptPurposeOutcome:
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
