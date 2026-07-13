package intents

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

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
	service := NewService(repository, DefaultPolicyConfig())
	response, err := service.Submit(context.Background(), FleetIntent{Type: IntentDeploy, Confidence: 0.9})
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
	operation.AuthorizationRef = "are-grant-1"
	if err := repository.UpdateOperation(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Transition(context.Background(), response.OperationID, StateAuthorized, "test", "test"); err != nil {
		t.Fatalf("transition to authorized: %v", err)
	}
	if _, err := service.Transition(context.Background(), response.OperationID, StateActuating, "test", "test"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("actuating transition without authorization receipt must fail, got %v", err)
	}
	if _, err := service.AttachLedgerReceipt(context.Background(), response.OperationID, OperationLedgerReceipt{
		EntryID: "authorization-entry", ChainHash: "authorization-hash", Purpose: ReceiptPurposeAuthorization, RecordedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []OperationState{StateActuating, StateObserving, StateVerified, StateEvidencePending} {
		if _, err := service.Transition(context.Background(), response.OperationID, state, "test", "test"); err != nil {
			t.Fatalf("transition to %s: %v", state, err)
		}
	}
	if _, err := service.Transition(context.Background(), response.OperationID, StateSucceeded, "test", "test"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("succeeded transition without outcome receipt must fail, got %v", err)
	}
	if _, err := service.AttachLedgerReceipt(context.Background(), response.OperationID, OperationLedgerReceipt{
		EntryID: "outcome-entry", ChainHash: "outcome-hash", Purpose: ReceiptPurposeOutcome, RecordedAt: time.Now().UTC(),
	}); err != nil {
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
