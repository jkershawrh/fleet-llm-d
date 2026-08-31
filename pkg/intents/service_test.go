package intents

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/ledger"
)

type fakeProofVerifier struct {
	verifications map[string]*ledger.ProofVerification
	err           error
}

func newFakeProofVerifier() *fakeProofVerifier {
	return &fakeProofVerifier{verifications: make(map[string]*ledger.ProofVerification)}
}

func (f *fakeProofVerifier) VerifyProof(_ context.Context, entryHash, entryType string) (*ledger.ProofVerification, error) {
	if f.err != nil {
		return nil, f.err
	}
	verification, ok := f.verifications[entryHash+"\x00"+entryType]
	if !ok {
		return &ledger.ProofVerification{Valid: false, EntryType: entryType}, nil
	}
	result := *verification
	result.Content = append([]byte(nil), verification.Content...)
	return &result, nil
}

func (f *fakeProofVerifier) trustOutcome(t *testing.T, receipt OperationLedgerReceipt, operation FleetOperation) {
	t.Helper()
	content, err := NewOutcomeEvidence(operation)
	if err != nil {
		t.Fatalf("NewOutcomeEvidence() unexpected error: %v", err)
	}
	f.verifications[receipt.EntryHash+"\x00"+receipt.EntryType] = &ledger.ProofVerification{
		Valid: true, EntryID: receipt.EntryID, EntryType: receipt.EntryType,
		CorrelationID: operation.CorrelationID, InputHash: receipt.InputHash, Content: content,
		ChainPosition: receipt.ChainPosition, WrittenAt: time.UnixMilli(receipt.WrittenTS).UTC(),
	}
}

func TestServiceSubmitCreatesAcceptedOperationWithoutExecuting(t *testing.T) {
	repo := NewMemoryRepository()
	service := NewService(repo, DefaultPolicyConfig())

	response, err := service.Submit(context.Background(), FleetIntent{
		ID:             "intent-1",
		IdempotencyKey: "dedupe-1",
		Type:           IntentScale,
		Confidence:     0.9,
		TargetReplicas: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.State != StateAccepted {
		t.Fatalf("state = %s, want %s", response.State, StateAccepted)
	}
	operation, err := service.GetOperation(context.Background(), response.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if operation.State == StateSucceeded || operation.State == StateActuating {
		t.Fatalf("admission must not report actuation, got %s", operation.State)
	}
	if len(operation.Transitions) != 2 || operation.Transitions[0].State != StateReceived {
		t.Fatalf("unexpected transition journal: %#v", operation.Transitions)
	}
}

func TestMemoryRepositoryPersistsProviderReferenceByValue(t *testing.T) {
	repository := NewMemoryRepository()
	provider := &ProviderReference{Type: "ModelPlane", Name: "modelplane-primary", Namespace: "fleet-system"}
	operation := FleetOperation{ID: "operation-1", IntentID: "intent-1", Provider: provider}
	if err := repository.Create(context.Background(), FleetIntent{ID: "intent-1"}, operation); err != nil {
		t.Fatal(err)
	}
	provider.Name = "caller-mutation"
	stored, err := repository.GetOperation(context.Background(), operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Provider == nil || stored.Provider.Name != "modelplane-primary" {
		t.Fatalf("provider was not persisted by value: %#v", stored.Provider)
	}
	stored.Provider.Generation = 7
	if err := repository.UpdateOperation(context.Background(), stored); err != nil {
		t.Fatal(err)
	}
	stored.Provider.Generation = 8
	again, err := repository.GetOperation(context.Background(), operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Provider == nil || again.Provider.Generation != 7 {
		t.Fatalf("provider update was not cloned: %#v", again.Provider)
	}
}

func TestServiceSubmitIsIdempotent(t *testing.T) {
	service := NewService(NewMemoryRepository(), DefaultPolicyConfig())
	intent := FleetIntent{IdempotencyKey: "same", Type: IntentPreWarm, Confidence: 0.9}
	first, err := service.Submit(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Submit(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationID != second.OperationID || first.IntentID != second.IntentID {
		t.Fatalf("idempotent replay created new resources: first=%+v second=%+v", first, second)
	}
}

func TestServiceConcurrentIdempotencyUsesOneOperationIdentity(t *testing.T) {
	service := NewService(NewMemoryRepository(), DefaultPolicyConfig())
	intent := FleetIntent{IdempotencyKey: "concurrent-key", Type: IntentPreWarm, Confidence: 0.9}
	responses := make(chan SubmissionResponse, 2)
	errors := make(chan error, 2)
	var workers sync.WaitGroup
	for i := 0; i < 2; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			response, err := service.Submit(context.Background(), intent)
			responses <- response
			errors <- err
		}()
	}
	workers.Wait()
	close(responses)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var operationID string
	for response := range responses {
		if operationID == "" {
			operationID = response.OperationID
		}
		if response.OperationID != operationID {
			t.Fatalf("concurrent replay produced operations %q and %q", operationID, response.OperationID)
		}
	}
	if operationID != operationResourceID("operation", intent.IdempotencyKey) {
		t.Fatalf("operation ID = %q, want deterministic identity", operationID)
	}
}

func TestServiceRejectsExpiredIntent(t *testing.T) {
	service := NewService(NewMemoryRepository(), DefaultPolicyConfig())
	expired := time.Now().Add(-time.Minute)
	response, err := service.Submit(context.Background(), FleetIntent{
		Type: IntentScale, Confidence: 0.9, ExpiresAt: &expired,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.State != StateExpired {
		t.Fatalf("state = %s, want %s", response.State, StateExpired)
	}
}

func TestServiceEnforcesLifecycle(t *testing.T) {
	repository := NewMemoryRepository()
	proofs := newFakeProofVerifier()
	service := NewService(repository, DefaultPolicyConfig(), proofs)
	response, err := service.Submit(context.Background(), FleetIntent{IdempotencyKey: "deploy-1", Type: IntentDeploy, Confidence: 0.9})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Transition(context.Background(), response.OperationID, StateActuating, "skip", "test"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("expected invalid transition, got %v", err)
	}
	if _, err := service.Transition(context.Background(), response.OperationID, StatePlanned, "test", "test"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("planned transition without digest must fail, got %v", err)
	}
	operation, err := service.GetOperation(context.Background(), response.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	operation.PlanDigest = strings.Repeat("a", 64)
	if err := repository.UpdateOperation(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Transition(context.Background(), response.OperationID, StatePlanned, "test", "test"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("planned transition without provider must fail, got %v", err)
	}
	operation.Provider = &ProviderReference{Type: "ModelPlane", Name: "modelplane-primary"}
	if err := repository.UpdateOperation(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	for _, state := range []OperationState{StatePlanned, StateGovernancePending} {
		if _, err := service.Transition(context.Background(), response.OperationID, state, "test", "test"); err != nil {
			t.Fatalf("transition to %s: %v", state, err)
		}
	}
	if _, err := service.Transition(context.Background(), response.OperationID, StateAuthorized, "test", "test"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("authorized transition without grant must fail, got %v", err)
	}
	operation, err = service.GetOperation(context.Background(), response.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	operation.AuthorizationRef = &AuthorizationReference{
		GrantID: "fleet-approval-1", Subject: "operator:test", ActionClass: operation.ActionClass,
		ObjectUID: operation.ObjectUID, SpecDigest: operation.PlanDigest,
		Audience: AuthorizationAudienceFleetController, ExpiresAt: time.Now().Add(time.Hour),
		IdempotencyKey: operation.IdempotencyKey,
	}
	if err := repository.UpdateOperation(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Transition(context.Background(), response.OperationID, StateAuthorized, "test", "test"); err != nil {
		t.Fatalf("transition to authorized: %v", err)
	}
	for _, state := range []OperationState{StateActuating, StateObserving, StateVerified, StateEvidencePending} {
		if _, err := service.Transition(context.Background(), response.OperationID, state, "test", "test"); err != nil {
			t.Fatalf("transition to %s: %v", state, err)
		}
	}
	if _, err := service.Transition(context.Background(), response.OperationID, StateSucceeded, "test", "test"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("succeeded transition without outcome receipt must fail, got %v", err)
	}
	outcome := OperationLedgerReceipt{
		EntryID: "outcome-entry", EntryHash: strings.Repeat("c", 64), EntryType: LedgerEntryTypeOutcome,
		ChainPosition: 1, WrittenTS: time.Now().UnixMilli(), InputHash: strings.Repeat("d", 64), Purpose: ReceiptPurposeOutcome,
	}
	operation, err = service.GetOperation(context.Background(), response.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	operation.ObservedDigest = outcome.InputHash
	if err := repository.UpdateOperation(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	proofs.trustOutcome(t, outcome, operation)
	if _, err := service.AttachLedgerReceipt(context.Background(), response.OperationID, outcome); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Transition(context.Background(), response.OperationID, StateSucceeded, "test", "test"); err != nil {
		t.Fatalf("transition to succeeded: %v", err)
	}
	operation, err = service.GetOperation(context.Background(), response.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	if operation.State != StateSucceeded {
		t.Fatalf("state = %s, want succeeded", operation.State)
	}
}

func TestAttachLedgerReceiptVerifiesEveryProofBinding(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	receipt := OperationLedgerReceipt{
		EntryID: "entry-1", EntryHash: strings.Repeat("a", 64), EntryType: LedgerEntryTypeOutcome,
		ChainPosition: 9, WrittenTS: now.UnixMilli(), InputHash: strings.Repeat("b", 64), Purpose: ReceiptPurposeOutcome,
	}
	baseVerification := func(t *testing.T, operation FleetOperation) *ledger.ProofVerification {
		t.Helper()
		content, err := NewOutcomeEvidence(operation)
		if err != nil {
			t.Fatal(err)
		}
		return &ledger.ProofVerification{
			Valid: true, EntryID: receipt.EntryID, EntryType: receipt.EntryType,
			CorrelationID: "correlation-1", InputHash: receipt.InputHash, Content: content,
			ChainPosition: receipt.ChainPosition, WrittenAt: now,
		}
	}
	tests := map[string]func(*ledger.ProofVerification){
		"invalid proof":     func(v *ledger.ProofVerification) { v.Valid = false },
		"entry ID":          func(v *ledger.ProofVerification) { v.EntryID = "other-entry" },
		"entry type":        func(v *ledger.ProofVerification) { v.EntryType = "fleet.operation.other" },
		"correlation ID":    func(v *ledger.ProofVerification) { v.CorrelationID = "other-correlation" },
		"input hash":        func(v *ledger.ProofVerification) { v.InputHash = strings.Repeat("c", 64) },
		"chain position":    func(v *ledger.ProofVerification) { v.ChainPosition++ },
		"written timestamp": func(v *ledger.ProofVerification) { v.WrittenAt = v.WrittenAt.Add(time.Millisecond) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			repository := NewMemoryRepository()
			operation := FleetOperation{
				ID: "operation-1", ObjectUID: "operation-1", IntentID: "intent-1", CorrelationID: "correlation-1",
				IdempotencyKey: "dedupe-1", ActionClass: "fleet.deploy", State: StateEvidencePending,
				PlanDigest: strings.Repeat("d", 64), ObservedDigest: receipt.InputHash, CreatedAt: now, UpdatedAt: now,
				Transitions: []OperationTransition{{Sequence: 1, State: StateEvidencePending, At: now}},
			}
			if err := repository.Create(context.Background(), FleetIntent{ID: "intent-1"}, operation); err != nil {
				t.Fatal(err)
			}
			verification := baseVerification(t, operation)
			mutate(verification)
			proofs := newFakeProofVerifier()
			proofs.verifications[receipt.EntryHash+"\x00"+receipt.EntryType] = verification
			service := NewService(repository, DefaultPolicyConfig(), proofs)
			if _, err := service.AttachLedgerReceipt(context.Background(), operation.ID, receipt); err == nil {
				t.Fatal("mismatched proof was accepted")
			}
			stored, err := repository.GetOperation(context.Background(), operation.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(stored.LedgerReceipts) != 0 {
				t.Fatal("unverified receipt was stored")
			}
		})
	}

	t.Run("valid proof", func(t *testing.T) {
		repository := NewMemoryRepository()
		operation := FleetOperation{
			ID: "operation-1", ObjectUID: "operation-1", IntentID: "intent-1", CorrelationID: "correlation-1",
			IdempotencyKey: "dedupe-1", ActionClass: "fleet.deploy", State: StateEvidencePending,
			PlanDigest: strings.Repeat("d", 64), ObservedDigest: receipt.InputHash, CreatedAt: now, UpdatedAt: now,
			Transitions: []OperationTransition{{Sequence: 1, State: StateEvidencePending, At: now}},
		}
		if err := repository.Create(context.Background(), FleetIntent{ID: "intent-1"}, operation); err != nil {
			t.Fatal(err)
		}
		proofs := newFakeProofVerifier()
		proofs.trustOutcome(t, receipt, operation)
		service := NewService(repository, DefaultPolicyConfig(), proofs)
		stored, err := service.AttachLedgerReceipt(context.Background(), operation.ID, receipt)
		if err != nil {
			t.Fatal(err)
		}
		if len(stored.LedgerReceipts) != 1 || stored.LedgerReceipts[0] != receipt {
			t.Fatalf("verified receipt was not stored exactly: %#v", stored.LedgerReceipts)
		}
	})

	boundOperation := FleetOperation{
		ID: "operation-1", ObjectUID: "uid-1", IntentID: "intent-1", CorrelationID: "correlation-1",
		IdempotencyKey: "dedupe-1", ActionClass: "fleet.deploy", State: StateEvidencePending,
		PlanDigest: strings.Repeat("d", 64), ObservedDigest: receipt.InputHash, CreatedAt: now, UpdatedAt: now,
		Transitions: []OperationTransition{{Sequence: 1, State: StateEvidencePending, At: now}},
	}
	contentMutations := map[string]func(*FleetOperation){
		"operation ID":    func(operation *FleetOperation) { operation.ID = "operation-other" },
		"operation UID":   func(operation *FleetOperation) { operation.ObjectUID = "uid-other" },
		"plan digest":     func(operation *FleetOperation) { operation.PlanDigest = strings.Repeat("e", 64) },
		"idempotency key": func(operation *FleetOperation) { operation.IdempotencyKey = "dedupe-other" },
		"observed digest": func(operation *FleetOperation) { operation.ObservedDigest = strings.Repeat("f", 64) },
	}
	for name, mutate := range contentMutations {
		t.Run("committed content "+name, func(t *testing.T) {
			repository := NewMemoryRepository()
			if err := repository.Create(context.Background(), FleetIntent{ID: boundOperation.IntentID}, boundOperation); err != nil {
				t.Fatal(err)
			}
			other := boundOperation
			mutate(&other)
			content, err := NewOutcomeEvidence(other)
			if err != nil {
				t.Fatal(err)
			}
			verification := baseVerification(t, boundOperation)
			verification.Content = content
			proofs := newFakeProofVerifier()
			proofs.verifications[receipt.EntryHash+"\x00"+receipt.EntryType] = verification
			service := NewService(repository, DefaultPolicyConfig(), proofs)
			if _, err := service.AttachLedgerReceipt(context.Background(), boundOperation.ID, receipt); err == nil {
				t.Fatalf("outcome proof with mismatched %s was accepted", name)
			}
		})
	}

	t.Run("outcome purpose is bound to verified event type", func(t *testing.T) {
		repository := NewMemoryRepository()
		operation := FleetOperation{
			ID: "operation-1", ObjectUID: "operation-1", IntentID: "intent-1", CorrelationID: "correlation-1",
			IdempotencyKey: "dedupe-1", ActionClass: "fleet.deploy", State: StateEvidencePending,
			PlanDigest: strings.Repeat("d", 64), ObservedDigest: receipt.InputHash, CreatedAt: now, UpdatedAt: now,
			Transitions: []OperationTransition{{Sequence: 1, State: StateEvidencePending, At: now}},
		}
		if err := repository.Create(context.Background(), FleetIntent{ID: "intent-1"}, operation); err != nil {
			t.Fatal(err)
		}
		mislabeled := receipt
		mislabeled.EntryType = "fleet.operation.authorization_decision"
		proofs := newFakeProofVerifier()
		proofs.trustOutcome(t, mislabeled, operation)
		service := NewService(repository, DefaultPolicyConfig(), proofs)
		if _, err := service.AttachLedgerReceipt(context.Background(), operation.ID, mislabeled); err == nil {
			t.Fatal("authorization-decision entry was accepted as outcome evidence")
		}
	})

	t.Run("outcome input is bound to observed state", func(t *testing.T) {
		repository := NewMemoryRepository()
		operation := FleetOperation{
			ID: "operation-1", ObjectUID: "operation-1", IntentID: "intent-1", CorrelationID: "correlation-1",
			IdempotencyKey: "dedupe-1", ActionClass: "fleet.deploy", State: StateEvidencePending,
			PlanDigest: strings.Repeat("d", 64), ObservedDigest: strings.Repeat("c", 64), CreatedAt: now, UpdatedAt: now,
			Transitions: []OperationTransition{{Sequence: 1, State: StateEvidencePending, At: now}},
		}
		if err := repository.Create(context.Background(), FleetIntent{ID: "intent-1"}, operation); err != nil {
			t.Fatal(err)
		}
		proofs := newFakeProofVerifier()
		proofs.trustOutcome(t, receipt, operation)
		service := NewService(repository, DefaultPolicyConfig(), proofs)
		if _, err := service.AttachLedgerReceipt(context.Background(), operation.ID, receipt); err == nil {
			t.Fatal("outcome proof for a different observed digest was accepted")
		}
	})

	t.Run("outcome proof cannot replay across operations", func(t *testing.T) {
		repository := NewMemoryRepository()
		first := FleetOperation{
			ID: "operation-1", ObjectUID: "uid-1", IntentID: "intent-1", CorrelationID: "correlation-1",
			IdempotencyKey: "dedupe-1", ActionClass: "fleet.deploy", State: StateEvidencePending,
			PlanDigest: strings.Repeat("d", 64), ObservedDigest: receipt.InputHash, CreatedAt: now, UpdatedAt: now,
			Transitions: []OperationTransition{{Sequence: 1, State: StateEvidencePending, At: now}},
		}
		second := first
		second.ID = "operation-2"
		second.ObjectUID = "uid-2"
		second.IntentID = "intent-2"
		second.IdempotencyKey = "dedupe-2"
		if err := repository.Create(context.Background(), FleetIntent{ID: first.IntentID}, first); err != nil {
			t.Fatal(err)
		}
		if err := repository.Create(context.Background(), FleetIntent{ID: second.IntentID}, second); err != nil {
			t.Fatal(err)
		}
		proofs := newFakeProofVerifier()
		proofs.trustOutcome(t, receipt, first)
		service := NewService(repository, DefaultPolicyConfig(), proofs)
		if _, err := service.AttachLedgerReceipt(context.Background(), second.ID, receipt); err == nil {
			t.Fatal("outcome proof committed for another operation was accepted")
		}
	})
}

func TestFakeOutcomeReceiptCannotUnlockSucceeded(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	repository := NewMemoryRepository()
	fakeReceipt := OperationLedgerReceipt{
		EntryID: "fake-entry", EntryHash: strings.Repeat("a", 64), EntryType: LedgerEntryTypeOutcome,
		ChainPosition: 1, WrittenTS: now.UnixMilli(), InputHash: strings.Repeat("b", 64), Purpose: ReceiptPurposeOutcome,
	}
	operation := FleetOperation{
		ID: "operation-1", ObjectUID: "operation-1", IntentID: "intent-1", CorrelationID: "correlation-1",
		IdempotencyKey: "dedupe-1", ActionClass: "fleet.deploy", State: StateEvidencePending,
		PlanDigest: strings.Repeat("d", 64), ObservedDigest: fakeReceipt.InputHash, CreatedAt: now, UpdatedAt: now,
		Transitions:    []OperationTransition{{Sequence: 1, State: StateEvidencePending, At: now}},
		LedgerReceipts: []OperationLedgerReceipt{fakeReceipt},
	}
	if err := repository.Create(context.Background(), FleetIntent{ID: "intent-1"}, operation); err != nil {
		t.Fatal(err)
	}
	service := NewService(repository, DefaultPolicyConfig(), newFakeProofVerifier())
	if _, err := service.Transition(context.Background(), operation.ID, StateSucceeded, "fake evidence", "test"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("fake outcome receipt unlocked SUCCEEDED: %v", err)
	}
}

func TestServiceRequiresAuthorizationBoundToOperation(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	baseOperation := FleetOperation{
		ID: "operation-1", ObjectUID: "uid-1", IntentID: "intent-1", CorrelationID: "correlation-1",
		IdempotencyKey: "dedupe-1", ActionClass: "fleet.scale", State: StateGovernancePending,
		PlanDigest: strings.Repeat("a", 64), CreatedAt: now, UpdatedAt: now,
		Transitions: []OperationTransition{{Sequence: 1, State: StateGovernancePending, At: now}},
	}
	valid := AuthorizationReference{
		GrantID: "grant-1", Subject: "operator:test", ActionClass: baseOperation.ActionClass,
		ObjectUID: baseOperation.ObjectUID, SpecDigest: baseOperation.PlanDigest,
		Audience: AuthorizationAudienceFleetController, ExpiresAt: now.Add(time.Minute),
		IdempotencyKey: baseOperation.IdempotencyKey,
	}
	tests := map[string]func(*AuthorizationReference){
		"action class":    func(ref *AuthorizationReference) { ref.ActionClass = "fleet.route" },
		"object UID":      func(ref *AuthorizationReference) { ref.ObjectUID = "uid-other" },
		"plan digest":     func(ref *AuthorizationReference) { ref.SpecDigest = strings.Repeat("b", 64) },
		"audience":        func(ref *AuthorizationReference) { ref.Audience = "another-service" },
		"idempotency key": func(ref *AuthorizationReference) { ref.IdempotencyKey = "other-key" },
		"expiry":          func(ref *AuthorizationReference) { ref.ExpiresAt = now },
		"subject":         func(ref *AuthorizationReference) { ref.Subject = "" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			repository := NewMemoryRepository()
			operation := cloneOperation(baseOperation)
			authorization := valid
			mutate(&authorization)
			operation.AuthorizationRef = &authorization
			if err := repository.Create(context.Background(), FleetIntent{ID: "intent-1"}, operation); err != nil {
				t.Fatal(err)
			}
			service := NewService(repository, DefaultPolicyConfig())
			service.now = func() time.Time { return now }
			if _, err := service.Transition(context.Background(), operation.ID, StateAuthorized, "authorized", "test"); !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("unbound authorization was accepted: %v", err)
			}
		})
	}

	t.Run("valid authorization and actuating recheck", func(t *testing.T) {
		repository := NewMemoryRepository()
		operation := cloneOperation(baseOperation)
		authorization := valid
		operation.AuthorizationRef = &authorization
		if err := repository.Create(context.Background(), FleetIntent{ID: "intent-1"}, operation); err != nil {
			t.Fatal(err)
		}
		service := NewService(repository, DefaultPolicyConfig())
		service.now = func() time.Time { return now }
		if _, err := service.Transition(context.Background(), operation.ID, StateAuthorized, "authorized", "test"); err != nil {
			t.Fatal(err)
		}
		service.now = func() time.Time { return valid.ExpiresAt }
		if _, err := service.Transition(context.Background(), operation.ID, StateActuating, "actuate", "test"); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("expired authorization was not rechecked at ACTUATING: %v", err)
		}
	})
}

func TestServiceCancelAcceptsDeferredGovernanceAndAuthorizedStates(t *testing.T) {
	for _, start := range []OperationState{StateDeferred, StateGovernancePending, StateAuthorized} {
		t.Run(string(start), func(t *testing.T) {
			repository := NewMemoryRepository()
			service := NewService(repository, DefaultPolicyConfig())
			now := time.Now().UTC()
			operation := FleetOperation{
				ID: "operation", IntentID: "intent", State: start, CreatedAt: now, UpdatedAt: now,
				Transitions: []OperationTransition{{Sequence: 1, State: start, At: now}},
			}
			if err := repository.Create(context.Background(), FleetIntent{ID: "intent"}, operation); err != nil {
				t.Fatal(err)
			}
			cancelled, err := service.Cancel(context.Background(), operation.ID, "operator")
			if err != nil {
				t.Fatal(err)
			}
			if cancelled.State != StateFailed {
				t.Fatalf("cancel state = %s, want FAILED", cancelled.State)
			}
		})
	}
}

func TestServiceDefersCriticalIntentForApproval(t *testing.T) {
	service := NewService(NewMemoryRepository(), DefaultPolicyConfig())
	response, err := service.Submit(context.Background(), FleetIntent{
		Type: IntentAlert, Confidence: 0.9, Severity: "critical",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.State != StateDeferred {
		t.Fatalf("state = %s, want deferred", response.State)
	}
	operation, err := service.Approve(context.Background(), response.OperationID, "human:test")
	if err != nil {
		t.Fatal(err)
	}
	if operation.State != StateAccepted {
		t.Fatalf("state = %s, want accepted", operation.State)
	}
}
