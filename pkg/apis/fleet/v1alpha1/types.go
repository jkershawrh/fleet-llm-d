package v1alpha1

import (
	"time"
)

// FleetInferencePoolSpec declares a model's fleet-wide deployment intent.
type FleetInferencePoolSpec struct {
	Model     ModelSpec     `json:"model"`
	Placement PlacementRef `json:"placement"`
	Routing   RoutingRef   `json:"routing,omitempty"`
	Scaling   ScalingRef   `json:"scaling,omitempty"`
	Serving   ServingSpec  `json:"serving"`
	Lifecycle LifecycleRef `json:"lifecycle,omitempty"`
}

type ModelSpec struct {
	Name    string `json:"name"`
	Source  string `json:"source"`
	Version string `json:"version,omitempty"`
	OciRef  string `json:"ociRef,omitempty"` // ModelPack OCI reference for model metadata resolution
}

type PlacementRef struct {
	PolicyRef   string `json:"policyRef"`
	MinClusters int    `json:"minClusters,omitempty"`
	MaxClusters int    `json:"maxClusters,omitempty"`
}

type RoutingRef struct {
	PolicyRef string `json:"policyRef,omitempty"`
}

type ScalingRef struct {
	PolicyRef string `json:"policyRef,omitempty"`
}

type ServingSpec struct {
	InferencePoolTemplate InferencePoolTemplate `json:"inferencePoolTemplate"`
}

type InferencePoolTemplate struct {
	Spec InferencePoolTemplateSpec `json:"spec"`
}

type InferencePoolTemplateSpec struct {
	TargetPorts        []int               `json:"targetPorts"`
	EndpointPickerRef  EndpointPickerRef   `json:"endpointPickerRef,omitempty"`
}

type EndpointPickerRef struct {
	Name string `json:"name"`
}

type LifecycleRef struct {
	RolloutStrategy string      `json:"rolloutStrategy,omitempty"`
	Canary          *CanarySpec `json:"canary,omitempty"`
}

type CanarySpec struct {
	Weight        int    `json:"weight"`
	PauseDuration string `json:"pauseDuration,omitempty"`
}

// ResolvedModelConfig captures the key fields from a resolved ModelPack
// manifest, surfaced in the pool status for observability.
type ResolvedModelConfig struct {
	ParamSize      string  `json:"paramSize,omitempty"`
	Precision      string  `json:"precision,omitempty"`
	Format         string  `json:"format,omitempty"`
	GPUMemoryGB    float64 `json:"gpuMemoryGB,omitempty"`
	RecommendedGPUs int    `json:"recommendedGPUs,omitempty"`
}

// FleetInferencePoolStatus is the observed state.
type FleetInferencePoolStatus struct {
	Phase             FleetPhase           `json:"phase"`
	Clusters          []ClusterAssignment  `json:"clusters,omitempty"`
	AggregatedMetrics *AggregatedMetrics   `json:"aggregatedMetrics,omitempty"`
	Conditions        []Condition          `json:"conditions,omitempty"`
	ResolvedConfig    *ResolvedModelConfig `json:"resolvedConfig,omitempty"` // populated when OciRef is resolved via ModelPack
}

type FleetPhase string

const (
	FleetPhasePending  FleetPhase = "Pending"
	FleetPhasePlacing  FleetPhase = "Placing"
	FleetPhaseRunning  FleetPhase = "Running"
	FleetPhaseDegraded FleetPhase = "Degraded"
	FleetPhaseFailed   FleetPhase = "Failed"
)

type ClusterAssignment struct {
	Name     string `json:"name"`
	Replicas int    `json:"replicas"`
	GPUType  string `json:"gpuType,omitempty"`
	Status   string `json:"status"`
}

type AggregatedMetrics struct {
	TotalThroughput string `json:"totalThroughput,omitempty"`
	AvgTTFT         string `json:"avgTTFT,omitempty"`
	KVCacheHitRate  string `json:"kvCacheHitRate,omitempty"`
}

type Condition struct {
	Type               string    `json:"type"`
	Status             string    `json:"status"`
	LastTransitionTime time.Time `json:"lastTransitionTime"`
	Reason             string    `json:"reason,omitempty"`
	Message            string    `json:"message,omitempty"`
}

// PlacementPolicySpec defines where models can and should run.
type PlacementPolicySpec struct {
	Constraints []PlacementConstraint `json:"constraints,omitempty"`
	Affinity    []AffinityRule        `json:"affinity,omitempty"`
	Spreading   *SpreadingRule        `json:"spreading,omitempty"`
}

type PlacementConstraint struct {
	Type string `json:"type"`
	Rule string `json:"rule"`
}

type AffinityRule struct {
	Type   string  `json:"type"`
	Weight float64 `json:"weight"`
}

type SpreadingRule struct {
	MaxSkew     int    `json:"maxSkew"`
	TopologyKey string `json:"topologyKey"`
}

// TenantProfileSpec defines per-tenant governance.
type TenantProfileSpec struct {
	Quotas      TenantQuota      `json:"quotas"`
	RateLimit   RateLimitSpec    `json:"rateLimit,omitempty"`
	Priority    int              `json:"priority"`
	CostControl *CostControlSpec `json:"costControl,omitempty"`
	Clusters    *ClusterScope    `json:"clusters,omitempty"`
}

type TenantQuota struct {
	MaxTokensPerMinute    int64    `json:"maxTokensPerMinute"`
	MaxConcurrentRequests int      `json:"maxConcurrentRequests"`
	MaxModels             int      `json:"maxModels"`
	GPUBudget             GPUBudget `json:"gpuBudget,omitempty"`
}

type GPUBudget struct {
	MaxGPUs  int      `json:"maxGPUs"`
	GPUTypes []string `json:"gpuTypes,omitempty"`
}

type RateLimitSpec struct {
	RequestsPerSecond int `json:"requestsPerSecond"`
	BurstSize         int `json:"burstSize"`
}

type CostControlSpec struct {
	MonthlyBudget  string  `json:"monthlyBudget"`
	AlertThreshold float64 `json:"alertThreshold"`
}

type ClusterScope struct {
	Allowed []string `json:"allowed,omitempty"`
	Denied  []string `json:"denied,omitempty"`
}

type TenantProfileStatus struct {
	Usage TenantUsage `json:"usage,omitempty"`
}

type TenantUsage struct {
	CurrentMonthCost string `json:"currentMonthCost,omitempty"`
	TokensConsumed   int64  `json:"tokensConsumed"`
	AvgLatency       string `json:"avgLatency,omitempty"`
}

// FleetRoutingPolicySpec defines cross-cluster traffic routing.
type FleetRoutingPolicySpec struct {
	Strategy    string            `json:"strategy"`
	Rules       []RoutingRule     `json:"rules,omitempty"`
	HealthCheck *HealthCheckSpec  `json:"healthCheck,omitempty"`
}

type RoutingRule struct {
	Match  RoutingMatch  `json:"match"`
	Action RoutingAction `json:"action"`
}

type RoutingMatch struct {
	Headers map[string]string `json:"headers,omitempty"`
	Source  string            `json:"source,omitempty"`
}

type RoutingAction struct {
	PreferLocal     bool     `json:"preferLocal,omitempty"`
	PreferCheapest  bool     `json:"preferCheapest,omitempty"`
	KVCacheAffinity bool     `json:"kvCacheAffinity,omitempty"`
	MaxLatencyMs    int      `json:"maxLatencyMs,omitempty"`
	Failover        *Failover `json:"failover,omitempty"`
}

type Failover struct {
	Clusters []string `json:"clusters"`
}

type HealthCheckSpec struct {
	Interval           string `json:"interval"`
	UnhealthyThreshold int    `json:"unhealthyThreshold"`
}

// FleetScalingPolicySpec defines fleet-wide autoscaling.
type FleetScalingPolicySpec struct {
	Objectives   []ScalingObjective   `json:"objectives"`
	Constraints  ScalingConstraints   `json:"constraints"`
	Strategy     string               `json:"strategy"`
	CrossCluster *CrossClusterScaling `json:"crossCluster,omitempty"`
	ScaleToZero  *ScaleToZeroSpec     `json:"scaleToZero,omitempty"`
}

type ScalingObjective struct {
	Metric string `json:"metric"`
	Target string `json:"target"`
}

type ScalingConstraints struct {
	GlobalMaxGPUs  int `json:"globalMaxGPUs"`
	MaxScaleUpRate int `json:"maxScaleUpRate"`
}

type CrossClusterScaling struct {
	EnableMigration    bool    `json:"enableMigration"`
	MigrationThreshold float64 `json:"migrationThreshold"`
}

type ScaleToZeroSpec struct {
	Enabled        bool   `json:"enabled"`
	CooldownPeriod string `json:"cooldownPeriod"`
}

// ModelLifecycleSpec defines fleet-wide model deployment lifecycle.
type ModelLifecycleSpec struct {
	Model        ModelRef         `json:"model"`
	FleetPoolRef string           `json:"fleetPoolRef"`
	Strategy     RolloutStrategy  `json:"strategy"`
	Clusters     *ClusterOrder    `json:"clusters,omitempty"`
}

type ModelRef struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type RolloutStrategy struct {
	Type    string         `json:"type"`
	Canary  *CanaryConfig  `json:"canary,omitempty"`
}

type CanaryConfig struct {
	InitialWeight   int     `json:"initialWeight"`
	WeightIncrement int     `json:"weightIncrement"`
	Interval        string  `json:"interval"`
	SLOGate         *SLOGate `json:"sloGate,omitempty"`
	RollbackOnFailure bool  `json:"rollbackOnFailure"`
}

type SLOGate struct {
	MaxTTFTRegression      string `json:"maxTTFTRegression"`
	MaxErrorRateIncrease   string `json:"maxErrorRateIncrease"`
}

type ClusterOrder struct {
	Order []string `json:"order"`
}

type ModelLifecycleStatus struct {
	Phase          string                `json:"phase"`
	CurrentWeight  int                   `json:"currentWeight"`
	ClusterStatus  []ClusterRolloutStatus `json:"clusterStatus,omitempty"`
}

type ClusterRolloutStatus struct {
	Cluster       string `json:"cluster"`
	Phase         string `json:"phase"`
	CurrentWeight int    `json:"currentWeight,omitempty"`
	SLOMet        bool   `json:"sloMet"`
}

// KVCacheTransferPolicySpec defines cross-cluster KV cache migration.
type KVCacheTransferPolicySpec struct {
	Triggers  []TransferTrigger  `json:"triggers"`
	Transport TransportSpec      `json:"transport"`
	Retention RetentionSpec      `json:"retention,omitempty"`
}

type TransferTrigger struct {
	Type   string `json:"type"`
	Action string `json:"action"`
}

type TransportSpec struct {
	Protocol        string `json:"protocol"`
	MaxBandwidthMbps int   `json:"maxBandwidthMbps,omitempty"`
}

type RetentionSpec struct {
	SourceRetentionAfterTransfer string `json:"sourceRetentionAfterTransfer,omitempty"`
}
