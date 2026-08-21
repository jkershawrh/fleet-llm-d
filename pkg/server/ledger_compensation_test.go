package server

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/llm-d/fleet-llm-d/pkg/intents"
	"github.com/llm-d/fleet-llm-d/pkg/ledger"
)

var errLedgerUnavailable = errors.New("ledger unavailable")

type failingLedgerClient struct{}

func (failingLedgerClient) RecordDecision(context.Context, ledger.FleetDecision) (*ledger.LedgerReceipt, error) {
	return nil, errLedgerUnavailable
}
func (failingLedgerClient) VerifyDecisionChain(context.Context, string) (*ledger.ChainVerification, error) {
	return nil, errLedgerUnavailable
}
func (failingLedgerClient) QueryDecisions(context.Context, ledger.DecisionQuery) ([]ledger.FleetDecision, error) {
	return nil, errLedgerUnavailable
}
func (failingLedgerClient) IssueProofReceipt(context.Context, ledger.FleetDecision) (*ledger.ProofReceipt, error) {
	return nil, errLedgerUnavailable
}
func (failingLedgerClient) VerifyProof(context.Context, string, string) (*ledger.ProofVerification, error) {
	return nil, errLedgerUnavailable
}

func controllerWithFailingConfiguredLedger(t *testing.T) *FleetController {
	t.Helper()
	fc := newTestFleetController(t)
	fc.FleetRecorder = ledger.NewFleetRecorder(failingLedgerClient{}, "test", "test")
	fc.LedgerGatewayURL = "https://ledger.example.test"
	return fc
}

func TestClusterRegistrationCompensatesConfiguredLedgerFailure(t *testing.T) {
	fc := controllerWithFailingConfiguredLedger(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/clusters", bytes.NewBufferString(`{"id":"cluster-1","name":"cluster-1","region":"us-east"}`))
	response := httptest.NewRecorder()

	fc.handleRegisterCluster(response, req)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", response.Code, response.Body.String())
	}
	if _, err := fc.ClusterRepo.Get(context.Background(), "cluster-1"); err == nil {
		t.Fatal("cluster remained registered after configured ledger failure")
	}
}

func TestTenantCreationCompensatesConfiguredLedgerFailure(t *testing.T) {
	fc := controllerWithFailingConfiguredLedger(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tenants", bytes.NewBufferString(`{"id":"tenant-1","name":"tenant-1"}`))
	response := httptest.NewRecorder()

	fc.handleCreateTenant(response, req)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", response.Code, response.Body.String())
	}
	if _, err := fc.TenantRepo.Get(context.Background(), "tenant-1"); err == nil {
		t.Fatal("tenant remained durable after configured ledger failure")
	}
}

func TestIntentAdmissionCompensatesConfiguredLedgerFailure(t *testing.T) {
	fc := controllerWithFailingConfiguredLedger(t)
	repo := intents.NewMemoryRepository()
	fc.IntentService = intents.NewService(repo, intents.DefaultPolicyConfig(), failingLedgerClient{})
	intent := intents.FleetIntent{
		ID: "intent-1", IdempotencyKey: "request-1", Type: intents.IntentNoAction,
		Confidence: 1, Justification: "test",
	}

	if _, err := fc.submitIntent(context.Background(), intent); !errors.Is(err, errLedgerUnavailable) {
		t.Fatalf("submit error = %v, want ledger failure", err)
	}
	if _, err := repo.GetIntent(context.Background(), intent.ID); !errors.Is(err, intents.ErrNotFound) {
		t.Fatalf("intent lookup error = %v, want ErrNotFound", err)
	}
	if _, err := repo.FindByIdempotencyKey(context.Background(), intent.IdempotencyKey); !errors.Is(err, intents.ErrNotFound) {
		t.Fatalf("idempotency lookup error = %v, want ErrNotFound", err)
	}
}
