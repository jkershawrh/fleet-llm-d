package intents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	v1beta1 "github.com/llm-d/fleet-llm-d/pkg/apis/fleet/v1beta1"
	"github.com/llm-d/fleet-llm-d/pkg/tlsutil"
)

const (
	restPayloadParameter       = "restPayload"
	originalIDAnnotation       = "fleet.llm-d.ai/original-id"
	originalIntentIDAnnotation = "fleet.llm-d.ai/original-intent-id"
	serviceAccountCAPath       = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
)

var invalidDNSLabel = regexp.MustCompile(`[^a-z0-9-]+`)

// KubernetesRepository persists intent and operation authority in v1beta1
// CRDs. PostgreSQL may project these resources, but is never consulted here.
type KubernetesRepository struct {
	apiServer string
	namespace string
	token     string
	http      *http.Client
}

func NewKubernetesRepository(apiServer, namespace, token string, client *http.Client) *KubernetesRepository {
	if namespace == "" {
		namespace = "default"
	}
	if client == nil {
		tlsOptions := tlsutil.TLSOptions{}
		if _, err := os.Stat(serviceAccountCAPath); err == nil {
			tlsOptions.CAPath = serviceAccountCAPath
		}
		tlsConfig, err := tlsutil.NewTLSConfig(tlsOptions)
		if err != nil {
			tlsConfig = &tls.Config{MinVersion: tls.VersionTLS13}
		}
		tlsConfig.MinVersion = tls.VersionTLS13
		client = &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: tlsConfig}}
	}
	return &KubernetesRepository{
		apiServer: strings.TrimRight(apiServer, "/"), namespace: namespace, token: token, http: client,
	}
}

func (r *KubernetesRepository) Create(ctx context.Context, intent FleetIntent, operation FleetOperation) error {
	if strings.TrimSpace(intent.ID) == "" || strings.TrimSpace(operation.ID) == "" {
		return fmt.Errorf("intent and operation IDs must not be empty")
	}
	if operation.IntentID == "" {
		operation.IntentID = intent.ID
	} else if operation.IntentID != intent.ID {
		return fmt.Errorf("operation intent ID %q does not match intent ID %q", operation.IntentID, intent.ID)
	}
	if intent.CorrelationID == "" {
		intent.CorrelationID = intent.ID
	}
	if operation.CorrelationID == "" {
		operation.CorrelationID = intent.CorrelationID
	} else if operation.CorrelationID != intent.CorrelationID {
		return fmt.Errorf("operation correlation ID %q does not match intent correlation ID %q", operation.CorrelationID, intent.CorrelationID)
	}
	idempotencyKey, err := normalizedIdempotencyKey(intent, operation)
	if err != nil {
		return err
	}
	intent.IdempotencyKey = idempotencyKey
	operation.IdempotencyKey = idempotencyKey

	intentResource, err := toIntentResource(intent, operation)
	if err != nil {
		return err
	}
	operationResource, err := toOperationResource(operation, intentResource)
	if err != nil {
		return err
	}

	intentPath := r.collectionPath("fleetintents")
	var createdIntent v1beta1.FleetIntent
	if err := r.request(ctx, http.MethodPost, intentPath, intentResource, &createdIntent); err != nil {
		return fmt.Errorf("create FleetIntent: %w", err)
	}
	operationPath := r.collectionPath("fleetoperations")
	var createdOperation v1beta1.FleetOperation
	if err := r.request(ctx, http.MethodPost, operationPath, operationResource, &createdOperation); err != nil {
		_ = r.request(ctx, http.MethodDelete, intentPath+"/"+url.PathEscape(createdIntent.Metadata.Name), nil, nil)
		return fmt.Errorf("create FleetOperation: %w", err)
	}

	createdOperation.Status = operationResource.Status
	if err := r.request(ctx, http.MethodPut, operationPath+"/"+url.PathEscape(createdOperation.Metadata.Name)+"/status", createdOperation, &createdOperation); err != nil {
		r.cleanupCreate(ctx, createdIntent.Metadata.Name, createdOperation.Metadata.Name)
		return fmt.Errorf("initialize FleetOperation status: %w", err)
	}
	createdIntent.Status = intentResource.Status
	if err := r.request(ctx, http.MethodPut, intentPath+"/"+url.PathEscape(createdIntent.Metadata.Name)+"/status", createdIntent, &createdIntent); err != nil {
		r.cleanupCreate(ctx, createdIntent.Metadata.Name, createdOperation.Metadata.Name)
		return fmt.Errorf("initialize FleetIntent status: %w", err)
	}
	return nil
}

func (r *KubernetesRepository) cleanupCreate(ctx context.Context, intentName, operationName string) {
	_ = r.request(ctx, http.MethodDelete, r.collectionPath("fleetoperations")+"/"+url.PathEscape(operationName), nil, nil)
	_ = r.request(ctx, http.MethodDelete, r.collectionPath("fleetintents")+"/"+url.PathEscape(intentName), nil, nil)
}

func (r *KubernetesRepository) GetIntent(ctx context.Context, id string) (FleetIntent, error) {
	var resource v1beta1.FleetIntent
	if err := r.request(ctx, http.MethodGet, r.collectionPath("fleetintents")+"/"+url.PathEscape(resourceName(id)), nil, &resource); err != nil {
		return FleetIntent{}, err
	}
	if raw, ok := resource.Spec.Parameters[restPayloadParameter]; ok {
		var intent FleetIntent
		if err := json.Unmarshal(raw, &intent); err != nil {
			return FleetIntent{}, fmt.Errorf("decode FleetIntent REST projection: %w", err)
		}
		return intent, nil
	}
	return intentFromResource(resource), nil
}

func (r *KubernetesRepository) GetOperation(ctx context.Context, id string) (FleetOperation, error) {
	var resource v1beta1.FleetOperation
	if err := r.request(ctx, http.MethodGet, r.collectionPath("fleetoperations")+"/"+url.PathEscape(resourceName(id)), nil, &resource); err != nil {
		return FleetOperation{}, err
	}
	return operationFromResource(resource), nil
}

func (r *KubernetesRepository) FindByIdempotencyKey(ctx context.Context, key string) (FleetOperation, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return FleetOperation{}, ErrNotFound
	}
	selector := url.QueryEscape("fleet.llm-d.ai/idempotency-hash=" + idempotencyHash(key))
	path := r.collectionPath("fleetoperations") + "?labelSelector=" + selector
	var list struct {
		Items []v1beta1.FleetOperation `json:"items"`
	}
	if err := r.request(ctx, http.MethodGet, path, nil, &list); err != nil {
		return FleetOperation{}, err
	}
	for _, item := range list.Items {
		// The label is a bounded index. Confirm the complete key from spec so a
		// theoretical truncated-hash collision cannot replay another operation.
		if item.Spec.IdempotencyKey == key {
			return operationFromResource(item), nil
		}
	}
	return FleetOperation{}, ErrNotFound
}

func (r *KubernetesRepository) UpdateOperation(ctx context.Context, operation FleetOperation) error {
	path := r.collectionPath("fleetoperations") + "/" + url.PathEscape(resourceName(operation.ID))
	var resource v1beta1.FleetOperation
	if err := r.request(ctx, http.MethodGet, path, nil, &resource); err != nil {
		return err
	}
	// Planning and authorization are controller-owned desired state. They are
	// populated after the operation is first created, so persist them through
	// the main resource endpoint before advancing observed status.
	if strings.TrimSpace(operation.ActionClass) != "" {
		resource.Spec.ActionClass = operation.ActionClass
	}
	if strings.TrimSpace(operation.PlanDigest) != "" {
		planDigest := operation.PlanDigest
		resource.Spec.PlanDigest = &planDigest
	}
	if operation.Provider != nil {
		resource.Spec.Provider = providerToResource(operation.Provider)
	}
	if operation.AuthorizationRef != nil {
		resource.Spec.AuthorizationRef = authorizationToResource(operation.AuthorizationRef)
	}
	if err := r.request(ctx, http.MethodPut, path, resource, &resource); err != nil {
		return fmt.Errorf("update FleetOperation desired state: %w", err)
	}
	resource.Status = mergedOperationStatus(resource.Status, operation)
	if err := r.request(ctx, http.MethodPut, path+"/status", resource, &resource); err != nil {
		return fmt.Errorf("update FleetOperation status: %w", err)
	}
	if err := r.updateIntentStatus(ctx, resource.Spec.IntentRef.Name, operation); err != nil {
		return fmt.Errorf("update FleetIntent status: %w", err)
	}
	return nil
}

func mergedOperationStatus(existing v1beta1.FleetOperationStatus, operation FleetOperation) v1beta1.FleetOperationStatus {
	next := operationStatus(operation)
	next.CommonStatus = existing.CommonStatus
	next.CorrelationID = operation.CorrelationID
	if existing.Result != nil {
		result := *existing.Result
		next.Result = &result
	}
	if strings.TrimSpace(operation.ObservedDigest) != "" {
		if next.Result == nil {
			next.Result = &v1beta1.OperationResult{}
		}
		next.Result.ObservedDigest = operation.ObservedDigest
	}
	next.CompensationRef = existing.CompensationRef
	return next
}

func (r *KubernetesRepository) updateIntentStatus(ctx context.Context, intentName string, operation FleetOperation) error {
	path := r.collectionPath("fleetintents") + "/" + url.PathEscape(intentName)
	var resource v1beta1.FleetIntent
	if err := r.request(ctx, http.MethodGet, path, nil, &resource); err != nil {
		return err
	}
	resource.Status.Phase = v1beta1.OperationPhase(operation.State)
	resource.Status.CorrelationID = operation.CorrelationID
	if len(resource.Status.OperationRefs) == 0 {
		resource.Status.OperationRefs = []v1beta1.LocalObjectReference{{Name: resourceName(operation.ID)}}
	}
	if resource.Status.AcceptedAt == nil {
		resource.Status.AcceptedAt = operationAcceptedAt(operation)
	}
	if err := r.request(ctx, http.MethodPut, path+"/status", resource, &resource); err != nil {
		return err
	}
	return nil
}

func (r *KubernetesRepository) collectionPath(resource string) string {
	return fmt.Sprintf("/apis/%s/%s/namespaces/%s/%s", v1beta1.GroupName, v1beta1.Version, url.PathEscape(r.namespace), resource)
}

func (r *KubernetesRepository) request(ctx context.Context, method, path string, input, output interface{}) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("marshal Kubernetes request: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, r.apiServer+path, body)
	if err != nil {
		return err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Kubernetes API returned %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if output != nil && len(responseBody) > 0 {
		if err := json.Unmarshal(responseBody, output); err != nil {
			return fmt.Errorf("decode Kubernetes response: %w", err)
		}
	}
	return nil
}

func toIntentResource(intent FleetIntent, operation FleetOperation) (v1beta1.FleetIntent, error) {
	payload, err := json.Marshal(intent)
	if err != nil {
		return v1beta1.FleetIntent{}, err
	}
	now := intent.CreatedAt.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	expiresAt := now.Add(time.Duration(max(intent.HorizonSeconds, 300)) * time.Second)
	if intent.ExpiresAt != nil {
		expiresAt = intent.ExpiresAt.UTC()
	}
	digest := intent.DecisionPackageDigest
	if !validSHA256(digest) {
		hash := sha256.Sum256(payload)
		digest = fmt.Sprintf("%x", hash[:])
	}
	decisionRef := intent.DecisionPackageRef
	if decisionRef == "" {
		decisionRef = "compat://v1/intents/" + intent.ID
	}
	proposer := v1beta1.ProposerAuthority{Subject: "legacy-v1-client", AttestationRef: "compat://v1", Ceiling: actionClass(intent.Type)}
	if intent.Proposer != nil {
		proposer = v1beta1.ProposerAuthority{Subject: intent.Proposer.Subject, AttestationRef: intent.Proposer.AuthorityRef, Ceiling: intent.Proposer.MaxAction}
	}
	evidence := make([]v1beta1.EvidenceReference, 0, len(intent.Evidence))
	for _, item := range intent.Evidence {
		evidence = append(evidence, v1beta1.EvidenceReference{URI: item.URI, SHA256: item.SHA256, MediaType: item.MediaType})
	}
	targetName := firstNonEmpty(intent.Pool, intent.Model, "unspecified")
	acceptedAt := operationAcceptedAt(operation)
	return v1beta1.FleetIntent{
		TypeMeta: v1beta1.TypeMeta{APIVersion: v1beta1.APIVersion, Kind: "FleetIntent"},
		Metadata: v1beta1.ObjectMeta{
			Name: resourceName(intent.ID),
			Labels: map[string]string{
				"fleet.llm-d.ai/idempotency-hash": idempotencyHash(intent.IdempotencyKey),
			},
			Annotations: map[string]string{originalIDAnnotation: intent.ID},
		},
		Spec: v1beta1.FleetIntentSpec{
			ActionClass:        actionClass(intent.Type),
			TargetRef:          v1beta1.NamespacedObjectReference{APIVersion: v1beta1.APIVersion, Kind: "FleetInferencePool", Name: targetName},
			DecisionPackageRef: v1beta1.DecisionPackageReference{URI: decisionRef, SHA256: digest},
			Proposer:           proposer, Evidence: evidence, ExpiresAt: expiresAt,
			CorrelationID: intent.CorrelationID, IdempotencyKey: intent.IdempotencyKey,
			Parameters: v1beta1.IntentParameters{restPayloadParameter: json.RawMessage(payload)},
		},
		Status: v1beta1.FleetIntentStatus{
			CommonStatus:  v1beta1.CommonStatus{CorrelationID: intent.CorrelationID},
			Phase:         v1beta1.OperationPhase(operation.State),
			OperationRefs: []v1beta1.LocalObjectReference{{Name: resourceName(operation.ID)}},
			AcceptedAt:    acceptedAt,
		},
	}, nil
}

func toOperationResource(operation FleetOperation, intentResource v1beta1.FleetIntent) (v1beta1.FleetOperation, error) {
	action := firstNonEmpty(operation.ActionClass, intentResource.Spec.ActionClass)
	var planDigest *string
	if strings.TrimSpace(operation.PlanDigest) != "" {
		digest := operation.PlanDigest
		planDigest = &digest
	}
	return v1beta1.FleetOperation{
		TypeMeta: v1beta1.TypeMeta{APIVersion: v1beta1.APIVersion, Kind: "FleetOperation"},
		Metadata: v1beta1.ObjectMeta{
			Name: resourceName(operation.ID),
			Labels: map[string]string{
				"fleet.llm-d.ai/idempotency-hash": idempotencyHash(operation.IdempotencyKey),
			},
			Annotations: map[string]string{
				originalIDAnnotation:       operation.ID,
				originalIntentIDAnnotation: operation.IntentID,
			},
		},
		Spec: v1beta1.FleetOperationSpec{
			IntentRef:        v1beta1.LocalObjectReference{Name: intentResource.Metadata.Name},
			ActionClass:      action,
			TargetRef:        intentResource.Spec.TargetRef,
			PlanDigest:       planDigest,
			Provider:         providerToResource(operation.Provider),
			AuthorizationRef: authorizationToResource(operation.AuthorizationRef),
			IdempotencyKey:   operation.IdempotencyKey,
		},
		Status: operationStatus(operation),
	}, nil
}

func operationStatus(operation FleetOperation) v1beta1.FleetOperationStatus {
	transitions := make([]v1beta1.OperationTransition, 0, len(operation.Transitions))
	for _, transition := range operation.Transitions {
		transitions = append(transitions, v1beta1.OperationTransition{
			Sequence: transition.Sequence, Phase: v1beta1.OperationPhase(transition.State), At: transition.At,
			Reason: firstNonEmpty(transition.Reason, "state transition"), Message: transition.Actor,
			LedgerEntryID: transition.LedgerEntryID,
		})
	}
	receipts := make([]v1beta1.LedgerReceipt, 0, len(operation.LedgerReceipts))
	for _, receipt := range operation.LedgerReceipts {
		receipts = append(receipts, v1beta1.LedgerReceipt{
			EntryID: receipt.EntryID, EntryHash: receipt.EntryHash, EntryType: receipt.EntryType,
			ChainPosition: receipt.ChainPosition, WrittenTS: receipt.WrittenTS, InputHash: receipt.InputHash,
			Purpose: string(receipt.Purpose),
		})
	}
	status := v1beta1.FleetOperationStatus{
		CommonStatus: v1beta1.CommonStatus{CorrelationID: operation.CorrelationID},
		Phase:        v1beta1.OperationPhase(operation.State), Transitions: transitions, LedgerReceipts: receipts,
	}
	if strings.TrimSpace(operation.ObservedDigest) != "" {
		status.Result = &v1beta1.OperationResult{ObservedDigest: operation.ObservedDigest}
	}
	return status
}

func operationFromResource(resource v1beta1.FleetOperation) FleetOperation {
	operation := FleetOperation{
		ID:             originalResourceID(resource.Metadata, originalIDAnnotation, resource.Metadata.Name),
		ObjectUID:      resource.Metadata.UID,
		IntentID:       originalResourceID(resource.Metadata, originalIntentIDAnnotation, resource.Spec.IntentRef.Name),
		CorrelationID:  resource.Status.CorrelationID,
		IdempotencyKey: resource.Spec.IdempotencyKey, ActionClass: resource.Spec.ActionClass, State: OperationState(resource.Status.Phase),
		CreatedAt: timeOrZero(resource.Metadata.CreationTimestamp), UpdatedAt: timeOrZero(resource.Metadata.CreationTimestamp),
	}
	if strings.TrimSpace(operation.ObjectUID) == "" {
		operation.ObjectUID = operation.ID
	}
	if resource.Spec.PlanDigest != nil {
		operation.PlanDigest = *resource.Spec.PlanDigest
	}
	if resource.Spec.Provider != nil {
		operation.Provider = providerFromResource(resource.Spec.Provider)
	}
	if resource.Spec.AuthorizationRef != nil {
		operation.AuthorizationRef = authorizationFromResource(resource.Spec.AuthorizationRef)
	}
	if resource.Status.Result != nil {
		operation.ObservedDigest = resource.Status.Result.ObservedDigest
	}
	for _, transition := range resource.Status.Transitions {
		operation.Transitions = append(operation.Transitions, OperationTransition{
			Sequence: transition.Sequence, State: OperationState(transition.Phase), At: transition.At,
			Reason: transition.Reason, Actor: transition.Message, LedgerEntryID: transition.LedgerEntryID,
		})
		if transition.At.After(operation.UpdatedAt) {
			operation.UpdatedAt = transition.At
		}
	}
	for _, receipt := range resource.Status.LedgerReceipts {
		operation.LedgerReceipts = append(operation.LedgerReceipts, OperationLedgerReceipt{
			EntryID: receipt.EntryID, EntryHash: receipt.EntryHash, EntryType: receipt.EntryType,
			ChainPosition: receipt.ChainPosition, WrittenTS: receipt.WrittenTS, InputHash: receipt.InputHash,
			Purpose: LedgerReceiptPurpose(receipt.Purpose),
		})
		operation.LedgerEntryID = receipt.EntryID
	}
	return operation
}

func authorizationToResource(ref *AuthorizationReference) *v1beta1.AuthorizationReference {
	if ref == nil {
		return nil
	}
	return &v1beta1.AuthorizationReference{
		GrantID: ref.GrantID, Subject: ref.Subject, ActionClass: ref.ActionClass, ObjectUID: ref.ObjectUID,
		SpecDigest: ref.SpecDigest, Audience: ref.Audience, ExpiresAt: ref.ExpiresAt,
		IdempotencyKey: ref.IdempotencyKey, BreakGlass: ref.BreakGlass, IncidentRef: ref.IncidentRef,
	}
}

func providerToResource(ref *ProviderReference) *v1beta1.ProviderReference {
	if ref == nil {
		return nil
	}
	return &v1beta1.ProviderReference{
		Type: ref.Type, Name: ref.Name, Namespace: ref.Namespace,
		ExternalID: ref.ExternalID, Generation: ref.Generation,
	}
}

func providerFromResource(ref *v1beta1.ProviderReference) *ProviderReference {
	if ref == nil {
		return nil
	}
	return &ProviderReference{
		Type: ref.Type, Name: ref.Name, Namespace: ref.Namespace,
		ExternalID: ref.ExternalID, Generation: ref.Generation,
	}
}

func authorizationFromResource(ref *v1beta1.AuthorizationReference) *AuthorizationReference {
	if ref == nil {
		return nil
	}
	return &AuthorizationReference{
		GrantID: ref.GrantID, Subject: ref.Subject, ActionClass: ref.ActionClass, ObjectUID: ref.ObjectUID,
		SpecDigest: ref.SpecDigest, Audience: ref.Audience, ExpiresAt: ref.ExpiresAt,
		IdempotencyKey: ref.IdempotencyKey, BreakGlass: ref.BreakGlass, IncidentRef: ref.IncidentRef,
	}
}

func intentFromResource(resource v1beta1.FleetIntent) FleetIntent {
	intent := FleetIntent{
		ID:            originalResourceID(resource.Metadata, originalIDAnnotation, resource.Metadata.Name),
		CorrelationID: resource.Spec.CorrelationID, IdempotencyKey: resource.Spec.IdempotencyKey,
		Type: intentType(resource.Spec.ActionClass), Pool: resource.Spec.TargetRef.Name,
		DecisionPackageRef: resource.Spec.DecisionPackageRef.URI, DecisionPackageDigest: resource.Spec.DecisionPackageRef.SHA256,
		ExpiresAt: &resource.Spec.ExpiresAt,
		Proposer:  &ProposerAuthority{Subject: resource.Spec.Proposer.Subject, AuthorityRef: resource.Spec.Proposer.AttestationRef, MaxAction: resource.Spec.Proposer.Ceiling},
		CreatedAt: timeOrZero(resource.Metadata.CreationTimestamp),
	}
	for _, item := range resource.Spec.Evidence {
		intent.Evidence = append(intent.Evidence, EvidenceDigest{URI: item.URI, SHA256: item.SHA256, MediaType: item.MediaType})
	}
	return intent
}

func actionClass(intentType IntentType) string {
	switch intentType {
	case IntentPreWarm:
		return "fleet.prewarm"
	case IntentShedLoad:
		return "fleet.shed_load"
	case IntentKVTransfer:
		return "fleet.kv_transfer"
	default:
		return "fleet." + string(intentType)
	}
}

func intentType(action string) IntentType {
	switch action {
	case "fleet.prewarm", "fleet.pre-warm":
		return IntentPreWarm
	case "fleet.shed_load", "fleet.shed-load":
		return IntentShedLoad
	case "fleet.kv_transfer", "fleet.kv-transfer":
		return IntentKVTransfer
	default:
		return IntentType(strings.TrimPrefix(action, "fleet."))
	}
}

func resourceName(id string) string {
	original := strings.TrimSpace(id)
	name := strings.ToLower(original)
	name = invalidDNSLabel.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	if name == "" {
		name = "resource"
	}
	if name != original || len(name) > 63 {
		hash := sha256.Sum256([]byte(original))
		if len(name) > 46 {
			name = strings.Trim(name[:46], "-")
		}
		if name == "" {
			name = "resource"
		}
		name = name + "-" + fmt.Sprintf("%x", hash[:8])
	}
	return name
}

func idempotencyHash(key string) string {
	hash := sha256.Sum256([]byte(key))
	return fmt.Sprintf("%x", hash[:16])
}

func normalizedIdempotencyKey(intent FleetIntent, operation FleetOperation) (string, error) {
	intentKey := strings.TrimSpace(intent.IdempotencyKey)
	operationKey := strings.TrimSpace(operation.IdempotencyKey)
	if intentKey != "" && operationKey != "" && intentKey != operationKey {
		return "", fmt.Errorf("intent and operation idempotency keys must match")
	}
	if intentKey != "" {
		return intentKey, nil
	}
	if operationKey != "" {
		return operationKey, nil
	}
	// The compatibility API predates mandatory idempotency keys. Its stable
	// intent ID is the only safe deterministic fallback; never hash an empty
	// string because that aliases every legacy request.
	if strings.TrimSpace(intent.ID) == "" {
		return "", fmt.Errorf("idempotency key or stable intent ID is required")
	}
	return "legacy-intent:" + intent.ID, nil
}

func operationAcceptedAt(operation FleetOperation) *time.Time {
	for _, transition := range operation.Transitions {
		if transition.State == StateAccepted {
			acceptedAt := transition.At
			return &acceptedAt
		}
	}
	if operation.State == StateAccepted {
		acceptedAt := operation.UpdatedAt
		return &acceptedAt
	}
	return nil
}

func originalResourceID(metadata v1beta1.ObjectMeta, annotation, fallback string) string {
	if metadata.Annotations != nil {
		if value := strings.TrimSpace(metadata.Annotations[annotation]); value != "" {
			return value
		}
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func timeOrZero(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}
