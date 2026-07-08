package intents

import (
	"time"
)

type IntentType string

const (
	IntentPreWarm  IntentType = "pre_warm"
	IntentScale    IntentType = "scale"
	IntentShedLoad IntentType = "shed_load"
	IntentAlert    IntentType = "alert"
	IntentMigrate  IntentType = "migrate"
	IntentNoAction IntentType = "no_action"
)

type IntentStatus string

const (
	StatusExecuted IntentStatus = "executed"
	StatusRefused  IntentStatus = "refused"
	StatusDeferred IntentStatus = "deferred"
)

type FleetIntent struct {
	ID              string                 `json:"id"`
	Type            IntentType             `json:"type"`
	Confidence      float64                `json:"confidence"`
	HorizonSeconds  int                    `json:"horizon_seconds"`
	Justification   string                 `json:"justification"`
	StateSnapshot   map[string]interface{} `json:"state_snapshot"`
	CreatedAt       time.Time              `json:"created_at"`

	// Type-specific fields (flattened for simplicity)
	Model             string   `json:"model,omitempty"`
	Pool              string   `json:"pool,omitempty"`
	TargetReplicas    int      `json:"target_replicas,omitempty"`
	DesiredReplicas   int      `json:"desired_replicas,omitempty"`
	CurrentReplicas   int      `json:"current_replicas,omitempty"`
	TargetClusters    []string `json:"target_clusters,omitempty"`
	MaxInflight       int      `json:"max_inflight,omitempty"`
	DurationSeconds   int      `json:"duration_seconds,omitempty"`
	Metric            string   `json:"metric,omitempty"`
	Severity          string   `json:"severity,omitempty"`
	Message           string   `json:"message,omitempty"`
	Reason            string   `json:"reason,omitempty"`
	RecommendedAction string   `json:"recommended_action,omitempty"`
}

type IntentResponse struct {
	IntentID      string       `json:"intent_id"`
	Status        IntentStatus `json:"status"`
	Reason        string       `json:"reason"`
	LedgerEntryID string       `json:"ledger_entry_id,omitempty"`
}
