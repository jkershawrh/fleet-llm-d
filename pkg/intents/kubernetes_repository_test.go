package intents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	v1beta1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1beta1"
)

func TestCanonicalActionClassMapping(t *testing.T) {
	tests := []struct {
		intent IntentType
		action string
	}{
		{IntentDeploy, "fleet.deploy"},
		{IntentScale, "fleet.scale"},
		{IntentRoute, "fleet.route"},
		{IntentPreWarm, "fleet.prewarm"},
		{IntentShedLoad, "fleet.shed_load"},
		{IntentMigrate, "fleet.migrate"},
		{IntentKVTransfer, "fleet.kv_transfer"},
	}
	for _, test := range tests {
		t.Run(test.action, func(t *testing.T) {
			if got := actionClass(test.intent); got != test.action {
				t.Fatalf("actionClass(%q) = %q, want %q", test.intent, got, test.action)
			}
			if got := intentType(test.action); got != test.intent {
				t.Fatalf("intentType(%q) = %q, want %q", test.action, got, test.intent)
			}
		})
	}
}

func TestKubernetesRepositoryCreateGetAndIdempotency(t *testing.T) {
	fake := newFakeKubernetesAPI()
	server := httptest.NewServer(fake)
	defer server.Close()
	repository := NewKubernetesRepository(server.URL, "fleet-system", "test-token", server.Client())

	intent, operation := repositoryFixtures(StateAccepted)
	intent.ID = "Forecast/Intent_ABC"
	operation.ID = "Operation/ABC_123"
	operation.IntentID = intent.ID
	intent.IdempotencyKey = ""
	operation.IdempotencyKey = ""

	if err := repository.Create(context.Background(), intent, operation); err != nil {
		t.Fatal(err)
	}

	intentName := resourceName(intent.ID)
	operationName := resourceName(operation.ID)
	storedIntent := fake.intent(t, intentName)
	storedOperation := fake.operation(t, operationName)
	wantKey := "legacy-intent:" + intent.ID
	if storedIntent.Spec.IdempotencyKey != wantKey || storedOperation.Spec.IdempotencyKey != wantKey {
		t.Fatalf("derived idempotency key not persisted: intent=%q operation=%q", storedIntent.Spec.IdempotencyKey, storedOperation.Spec.IdempotencyKey)
	}
	if storedOperation.Spec.ActionClass != "fleet.scale" {
		t.Fatalf("operation action class = %q, want fleet.scale", storedOperation.Spec.ActionClass)
	}
	if storedOperation.Spec.TargetRef.Name != intent.Pool {
		t.Fatalf("operation target = %q, want %q", storedOperation.Spec.TargetRef.Name, intent.Pool)
	}
	if storedOperation.Spec.IntentRef.Name != intentName {
		t.Fatalf("operation intent ref = %q, want %q", storedOperation.Spec.IntentRef.Name, intentName)
	}
	if storedIntent.Status.Phase != v1beta1.OperationAccepted || storedIntent.Status.AcceptedAt == nil {
		t.Fatalf("accepted intent status not initialized honestly: %#v", storedIntent.Status)
	}
	if storedOperation.Status.Phase != v1beta1.OperationAccepted {
		t.Fatalf("operation status = %q, want ACCEPTED", storedOperation.Status.Phase)
	}
	if storedIntent.Metadata.Annotations[originalIDAnnotation] != intent.ID || storedOperation.Metadata.Annotations[originalIDAnnotation] != operation.ID {
		t.Fatal("original public IDs were not preserved in CRD annotations")
	}

	gotIntent, err := repository.GetIntent(context.Background(), intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotIntent.ID != intent.ID || gotIntent.IdempotencyKey != wantKey {
		t.Fatalf("intent round trip changed authority fields: %#v", gotIntent)
	}
	gotOperation, err := repository.GetOperation(context.Background(), operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotOperation.ID != operation.ID || gotOperation.IntentID != intent.ID || gotOperation.IdempotencyKey != wantKey {
		t.Fatalf("operation round trip changed public IDs: %#v", gotOperation)
	}
	replayed, err := repository.FindByIdempotencyKey(context.Background(), wantKey)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.ID != operation.ID {
		t.Fatalf("idempotency lookup returned %q, want %q", replayed.ID, operation.ID)
	}
	if _, err := repository.FindByIdempotencyKey(context.Background(), ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("empty idempotency lookup = %v, want ErrNotFound", err)
	}
	if fake.unauthenticatedRequests() != 0 {
		t.Fatal("repository omitted the Kubernetes bearer token")
	}
}

func TestKubernetesRepositoryStatusUpdateSynchronizesIntent(t *testing.T) {
	fake := newFakeKubernetesAPI()
	server := httptest.NewServer(fake)
	defer server.Close()
	repository := NewKubernetesRepository(server.URL, "fleet-system", "test-token", server.Client())

	intent, operation := repositoryFixtures(StateDeferred)
	if err := repository.Create(context.Background(), intent, operation); err != nil {
		t.Fatal(err)
	}
	fake.decorateOperationStatus(resourceName(operation.ID))
	beforeIntent := fake.intent(t, resourceName(intent.ID))
	beforeOperation := fake.operation(t, resourceName(operation.ID))
	if beforeIntent.Status.AcceptedAt != nil {
		t.Fatal("deferred intent must not be stamped accepted")
	}

	acceptedAt := operation.UpdatedAt.Add(time.Second)
	operation.State = StateAccepted
	operation.UpdatedAt = acceptedAt
	operation.Transitions = append(operation.Transitions, OperationTransition{
		Sequence: 3, State: StateAccepted, At: acceptedAt, Reason: "human approval recorded", Actor: "human:test",
	})
	operation.LedgerReceipts = append(operation.LedgerReceipts, OperationLedgerReceipt{
		EntryID: "ledger-entry-1", ChainHash: strings.Repeat("a", 64), Sequence: 1, RecordedAt: acceptedAt, Purpose: ReceiptPurposeAdmission,
	})
	operation.LedgerEntryID = "ledger-entry-1"
	if err := repository.UpdateOperation(context.Background(), operation); err != nil {
		t.Fatal(err)
	}

	afterIntent := fake.intent(t, resourceName(intent.ID))
	afterOperation := fake.operation(t, resourceName(operation.ID))
	if afterOperation.Status.Phase != v1beta1.OperationAccepted || len(afterOperation.Status.Transitions) != 3 || len(afterOperation.Status.LedgerReceipts) != 1 {
		t.Fatalf("operation status was not authoritative after update: %#v", afterOperation.Status)
	}
	if afterIntent.Status.Phase != v1beta1.OperationAccepted || afterIntent.Status.AcceptedAt == nil || !afterIntent.Status.AcceptedAt.Equal(acceptedAt) {
		t.Fatalf("intent status did not follow operation acceptance: %#v", afterIntent.Status)
	}
	if afterOperation.Spec.ActionClass != beforeOperation.Spec.ActionClass || afterOperation.Spec.TargetRef.Name != beforeOperation.Spec.TargetRef.Name {
		t.Fatal("status update mutated FleetOperation spec")
	}
	if afterOperation.Status.SpecDigest != strings.Repeat("e", 64) || afterOperation.Status.Result == nil || afterOperation.Status.Result.ProviderOperationID != "provider-operation-1" {
		t.Fatal("REST status update erased controller-owned operation status")
	}
	if afterIntent.Spec.IdempotencyKey != beforeIntent.Spec.IdempotencyKey {
		t.Fatal("status update mutated FleetIntent spec")
	}

	got, err := repository.GetOperation(context.Background(), operation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateAccepted || got.LedgerEntryID != "ledger-entry-1" || len(got.LedgerReceipts) != 1 {
		t.Fatalf("updated operation projection is incomplete: %#v", got)
	}
}

func TestKubernetesRepositoryRejectsMismatchedIdempotencyKeys(t *testing.T) {
	repository := NewKubernetesRepository("http://unused.invalid", "default", "", http.DefaultClient)
	intent, operation := repositoryFixtures(StateAccepted)
	intent.IdempotencyKey = "intent-key"
	operation.IdempotencyKey = "operation-key"
	if err := repository.Create(context.Background(), intent, operation); err == nil || !strings.Contains(err.Error(), "must match") {
		t.Fatalf("mismatched keys error = %v", err)
	}
}

func repositoryFixtures(state OperationState) (FleetIntent, FleetOperation) {
	createdAt := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	intent := FleetIntent{
		ID:                    "intent-original",
		CorrelationID:         "correlation-1",
		Type:                  IntentScale,
		Confidence:            0.91,
		HorizonSeconds:        900,
		Justification:         "forecast demand exceeds capacity",
		StateSnapshot:         map[string]interface{}{"replicas": float64(2)},
		CreatedAt:             createdAt,
		DecisionPackageRef:    "are://decision-package/1",
		DecisionPackageDigest: strings.Repeat("b", 64),
		Pool:                  "qwen-prod",
	}
	operation := FleetOperation{
		ID:            "operation-original",
		IntentID:      intent.ID,
		CorrelationID: intent.CorrelationID,
		State:         state,
		CreatedAt:     createdAt,
		UpdatedAt:     createdAt.Add(time.Second),
		Transitions: []OperationTransition{
			{Sequence: 1, State: StateReceived, At: createdAt, Reason: "intent received", Actor: "fleet-api"},
			{Sequence: 2, State: state, At: createdAt.Add(time.Second), Reason: "admission result", Actor: "fleet-policy"},
		},
	}
	return intent, operation
}

type fakeKubernetesAPI struct {
	mu              sync.Mutex
	intents         map[string]v1beta1.FleetIntent
	operations      map[string]v1beta1.FleetOperation
	resourceVersion int
	unauthenticated int
}

func newFakeKubernetesAPI() *fakeKubernetesAPI {
	return &fakeKubernetesAPI{
		intents:    make(map[string]v1beta1.FleetIntent),
		operations: make(map[string]v1beta1.FleetOperation),
	}
}

func (f *fakeKubernetesAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer test-token" {
		f.unauthenticated++
	}
	prefix := "/apis/fleet.llm-d.ai/v1beta1/namespaces/fleet-system/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, prefix), "/")
	if len(parts) == 0 {
		http.NotFound(w, r)
		return
	}
	switch parts[0] {
	case "fleetintents":
		f.serveIntents(w, r, parts)
	case "fleetoperations":
		f.serveOperations(w, r, parts)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeKubernetesAPI) serveIntents(w http.ResponseWriter, r *http.Request, parts []string) {
	switch {
	case r.Method == http.MethodPost && len(parts) == 1:
		var resource v1beta1.FleetIntent
		decodeRequest(w, r, &resource)
		if _, exists := f.intents[resource.Metadata.Name]; exists {
			http.Error(w, "already exists", http.StatusConflict)
			return
		}
		resource.Status = v1beta1.FleetIntentStatus{}
		f.initializeMetadata(&resource.Metadata)
		f.intents[resource.Metadata.Name] = resource
		writeFakeJSON(w, http.StatusCreated, resource)
	case r.Method == http.MethodGet && len(parts) == 2:
		resource, ok := f.intents[parts[1]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeFakeJSON(w, http.StatusOK, resource)
	case r.Method == http.MethodPut && len(parts) == 3 && parts[2] == "status":
		var requested v1beta1.FleetIntent
		decodeRequest(w, r, &requested)
		stored, ok := f.intents[parts[1]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		stored.Status = requested.Status
		f.bumpResourceVersion(&stored.Metadata)
		f.intents[parts[1]] = stored
		writeFakeJSON(w, http.StatusOK, stored)
	case r.Method == http.MethodDelete && len(parts) == 2:
		delete(f.intents, parts[1])
		w.WriteHeader(http.StatusOK)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeKubernetesAPI) serveOperations(w http.ResponseWriter, r *http.Request, parts []string) {
	switch {
	case r.Method == http.MethodPost && len(parts) == 1:
		var resource v1beta1.FleetOperation
		decodeRequest(w, r, &resource)
		if _, exists := f.operations[resource.Metadata.Name]; exists {
			http.Error(w, "already exists", http.StatusConflict)
			return
		}
		resource.Status = v1beta1.FleetOperationStatus{}
		f.initializeMetadata(&resource.Metadata)
		f.operations[resource.Metadata.Name] = resource
		writeFakeJSON(w, http.StatusCreated, resource)
	case r.Method == http.MethodGet && len(parts) == 1:
		selector := r.URL.Query().Get("labelSelector")
		items := make([]v1beta1.FleetOperation, 0)
		for _, item := range f.operations {
			if selector == "" || selector == "fleet.llm-d.ai/idempotency-hash="+item.Metadata.Labels["fleet.llm-d.ai/idempotency-hash"] {
				items = append(items, item)
			}
		}
		writeFakeJSON(w, http.StatusOK, map[string]interface{}{"items": items})
	case r.Method == http.MethodGet && len(parts) == 2:
		resource, ok := f.operations[parts[1]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeFakeJSON(w, http.StatusOK, resource)
	case r.Method == http.MethodPut && len(parts) == 3 && parts[2] == "status":
		var requested v1beta1.FleetOperation
		decodeRequest(w, r, &requested)
		stored, ok := f.operations[parts[1]]
		if !ok {
			http.NotFound(w, r)
			return
		}
		stored.Status = requested.Status
		f.bumpResourceVersion(&stored.Metadata)
		f.operations[parts[1]] = stored
		writeFakeJSON(w, http.StatusOK, stored)
	case r.Method == http.MethodDelete && len(parts) == 2:
		delete(f.operations, parts[1])
		w.WriteHeader(http.StatusOK)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeKubernetesAPI) initializeMetadata(metadata *v1beta1.ObjectMeta) {
	created := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	metadata.Namespace = "fleet-system"
	metadata.CreationTimestamp = &created
	f.bumpResourceVersion(metadata)
}

func (f *fakeKubernetesAPI) bumpResourceVersion(metadata *v1beta1.ObjectMeta) {
	f.resourceVersion++
	metadata.ResourceVersion = strconv.Itoa(f.resourceVersion)
}

func (f *fakeKubernetesAPI) intent(t *testing.T, name string) v1beta1.FleetIntent {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	resource, ok := f.intents[name]
	if !ok {
		t.Fatalf("FleetIntent %q not found", name)
	}
	return resource
}

func (f *fakeKubernetesAPI) operation(t *testing.T, name string) v1beta1.FleetOperation {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	resource, ok := f.operations[name]
	if !ok {
		t.Fatalf("FleetOperation %q not found", name)
	}
	return resource
}

func (f *fakeKubernetesAPI) unauthenticatedRequests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.unauthenticated
}

func (f *fakeKubernetesAPI) decorateOperationStatus(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	resource := f.operations[name]
	resource.Status.SpecDigest = strings.Repeat("e", 64)
	resource.Status.ProviderRefs = []v1beta1.ProviderReference{{Type: "ModelPlane", Name: "primary"}}
	resource.Status.Result = &v1beta1.OperationResult{ProviderOperationID: "provider-operation-1"}
	f.operations[name] = resource
}

func decodeRequest(w http.ResponseWriter, r *http.Request, target interface{}) {
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		http.Error(w, fmt.Sprintf("invalid JSON: %v", err), http.StatusBadRequest)
	}
}

func writeFakeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
