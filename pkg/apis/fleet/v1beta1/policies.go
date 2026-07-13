package v1beta1

// FleetInferencePool is the authoritative desired and observed state for a
// model deployed across a fleet.
type FleetInferencePool struct {
	TypeMeta `json:",inline"`
	Metadata ObjectMeta               `json:"metadata,omitempty"`
	Spec     FleetInferencePoolSpec   `json:"spec"`
	Status   FleetInferencePoolStatus `json:"status,omitempty"`
}

type FleetInferencePoolSpec struct {
	Model                  ModelSpec               `json:"model"`
	Placement              PlacementRef            `json:"placement"`
	Routing                RoutingRef              `json:"routing,omitempty"`
	Scaling                ScalingRef              `json:"scaling,omitempty"`
	Serving                ServingSpec             `json:"serving"`
	Lifecycle              LifecycleRef            `json:"lifecycle,omitempty"`
	InfrastructureProvider InfrastructureProvider  `json:"infrastructureProvider,omitempty"`
	HomeFleet              string                  `json:"homeFleet"`
	AuthorizationRef       *AuthorizationReference `json:"authorizationRef,omitempty"`
}

type ModelSpec struct {
	Name    string `json:"name"`
	Source  string `json:"source,omitempty"`
	Version string `json:"version,omitempty"`
	OCIRef  string `json:"ociRef,omitempty"`
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
	TargetPorts       []int             `json:"targetPorts"`
	EndpointPickerRef EndpointPickerRef `json:"endpointPickerRef,omitempty"`
}
type EndpointPickerRef struct {
	Name string `json:"name,omitempty"`
}
type LifecycleRef struct {
	RolloutStrategy string      `json:"rolloutStrategy,omitempty"`
	Canary          *CanarySpec `json:"canary,omitempty"`
}
type CanarySpec struct {
	Weight        int    `json:"weight"`
	PauseDuration string `json:"pauseDuration,omitempty"`
}

type FleetInferencePoolStatus struct {
	CommonStatus
	Phase             FleetPhase           `json:"phase,omitempty"`
	Clusters          []ClusterAssignment  `json:"clusters,omitempty"`
	AggregatedMetrics *AggregatedMetrics   `json:"aggregatedMetrics,omitempty"`
	ResolvedConfig    *ResolvedModelConfig `json:"resolvedConfig,omitempty"`
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
type ResolvedModelConfig struct {
	ParamSize       string  `json:"paramSize,omitempty"`
	Precision       string  `json:"precision,omitempty"`
	Format          string  `json:"format,omitempty"`
	GPUMemoryGB     float64 `json:"gpuMemoryGB,omitempty"`
	RecommendedGPUs int     `json:"recommendedGPUs,omitempty"`
}

type PlacementPolicy struct {
	TypeMeta `json:",inline"`
	Metadata ObjectMeta            `json:"metadata,omitempty"`
	Spec     PlacementPolicySpec   `json:"spec"`
	Status   PlacementPolicyStatus `json:"status,omitempty"`
}
type PlacementPolicySpec struct {
	Constraints      []PlacementConstraint   `json:"constraints,omitempty"`
	Affinity         []AffinityRule          `json:"affinity,omitempty"`
	Spreading        *SpreadingRule          `json:"spreading,omitempty"`
	AuthorizationRef *AuthorizationReference `json:"authorizationRef,omitempty"`
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
type PlacementDecision struct {
	Cluster  string   `json:"cluster"`
	Selected bool     `json:"selected"`
	Score    float64  `json:"score,omitempty"`
	Reasons  []string `json:"reasons,omitempty"`
}
type PlacementPolicyStatus struct {
	CommonStatus
	Decisions []PlacementDecision `json:"decisions,omitempty"`
}

type FleetRoutingPolicy struct {
	TypeMeta `json:",inline"`
	Metadata ObjectMeta               `json:"metadata,omitempty"`
	Spec     FleetRoutingPolicySpec   `json:"spec"`
	Status   FleetRoutingPolicyStatus `json:"status,omitempty"`
}
type FleetRoutingPolicySpec struct {
	Provider         RoutingProvider         `json:"provider,omitempty"`
	Strategy         string                  `json:"strategy"`
	Rules            []RoutingRule           `json:"rules,omitempty"`
	HealthCheck      *HealthCheckSpec        `json:"healthCheck,omitempty"`
	AuthorizationRef *AuthorizationReference `json:"authorizationRef,omitempty"`
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
	PreferLocal     bool      `json:"preferLocal,omitempty"`
	PreferCheapest  bool      `json:"preferCheapest,omitempty"`
	KVCacheAffinity bool      `json:"kvCacheAffinity,omitempty"`
	MaxLatencyMs    int       `json:"maxLatencyMs,omitempty"`
	Failover        *Failover `json:"failover,omitempty"`
}
type Failover struct {
	Clusters []string `json:"clusters"`
}
type HealthCheckSpec struct {
	Interval           string `json:"interval"`
	UnhealthyThreshold int    `json:"unhealthyThreshold"`
}
type FleetRoutingPolicyStatus struct {
	CommonStatus
	SnapshotDigest  string   `json:"snapshotDigest,omitempty"`
	AcceptedTargets []string `json:"acceptedTargets,omitempty"`
}

type TenantProfile struct {
	TypeMeta `json:",inline"`
	Metadata ObjectMeta          `json:"metadata,omitempty"`
	Spec     TenantProfileSpec   `json:"spec"`
	Status   TenantProfileStatus `json:"status,omitempty"`
}
type TenantProfileSpec struct {
	Quotas           TenantQuota             `json:"quotas"`
	RateLimit        RateLimitSpec           `json:"rateLimit,omitempty"`
	Priority         int                     `json:"priority"`
	CostControl      *CostControlSpec        `json:"costControl,omitempty"`
	Clusters         *ClusterScope           `json:"clusters,omitempty"`
	AuthorizationRef *AuthorizationReference `json:"authorizationRef,omitempty"`
}
type TenantQuota struct {
	MaxTokensPerMinute    int64     `json:"maxTokensPerMinute"`
	MaxConcurrentRequests int       `json:"maxConcurrentRequests"`
	MaxModels             int       `json:"maxModels"`
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
	CommonStatus
	Usage TenantUsage `json:"usage,omitempty"`
}
type TenantUsage struct {
	CurrentMonthCost string `json:"currentMonthCost,omitempty"`
	TokensConsumed   int64  `json:"tokensConsumed"`
	AvgLatency       string `json:"avgLatency,omitempty"`
}

type FleetScalingPolicy struct {
	TypeMeta `json:",inline"`
	Metadata ObjectMeta               `json:"metadata,omitempty"`
	Spec     FleetScalingPolicySpec   `json:"spec"`
	Status   FleetScalingPolicyStatus `json:"status,omitempty"`
}
type FleetScalingPolicySpec struct {
	Objectives       []ScalingObjective      `json:"objectives"`
	Constraints      ScalingConstraints      `json:"constraints"`
	Strategy         string                  `json:"strategy"`
	CrossCluster     *CrossClusterScaling    `json:"crossCluster,omitempty"`
	ScaleToZero      *ScaleToZeroSpec        `json:"scaleToZero,omitempty"`
	AuthorizationRef *AuthorizationReference `json:"authorizationRef,omitempty"`
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
type FleetScalingPolicyStatus struct {
	CommonStatus
	LastRecommendation string `json:"lastRecommendation,omitempty"`
	CooldownUntil      string `json:"cooldownUntil,omitempty"`
}

type ModelLifecycle struct {
	TypeMeta `json:",inline"`
	Metadata ObjectMeta           `json:"metadata,omitempty"`
	Spec     ModelLifecycleSpec   `json:"spec"`
	Status   ModelLifecycleStatus `json:"status,omitempty"`
}
type ModelLifecycleSpec struct {
	Model            ModelRef                `json:"model"`
	FleetPoolRef     string                  `json:"fleetPoolRef"`
	Strategy         RolloutStrategy         `json:"strategy"`
	Clusters         *ClusterOrder           `json:"clusters,omitempty"`
	AuthorizationRef *AuthorizationReference `json:"authorizationRef,omitempty"`
}
type ModelRef struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}
type RolloutStrategy struct {
	Type   string        `json:"type"`
	Canary *CanaryConfig `json:"canary,omitempty"`
}
type CanaryConfig struct {
	InitialWeight     int      `json:"initialWeight"`
	WeightIncrement   int      `json:"weightIncrement"`
	Interval          string   `json:"interval"`
	SLOGate           *SLOGate `json:"sloGate,omitempty"`
	RollbackOnFailure bool     `json:"rollbackOnFailure"`
}
type SLOGate struct {
	MaxTTFTRegression    string `json:"maxTTFTRegression"`
	MaxErrorRateIncrease string `json:"maxErrorRateIncrease"`
}
type ClusterOrder struct {
	Order []string `json:"order"`
}
type ModelLifecycleStatus struct {
	CommonStatus
	Phase         string                 `json:"phase,omitempty"`
	CurrentWeight int                    `json:"currentWeight,omitempty"`
	ClusterStatus []ClusterRolloutStatus `json:"clusterStatus,omitempty"`
}
type ClusterRolloutStatus struct {
	Cluster       string `json:"cluster"`
	Phase         string `json:"phase"`
	CurrentWeight int    `json:"currentWeight,omitempty"`
	SLOMet        bool   `json:"sloMet"`
}

type KVCacheTransferPolicy struct {
	TypeMeta `json:",inline"`
	Metadata ObjectMeta                  `json:"metadata,omitempty"`
	Spec     KVCacheTransferPolicySpec   `json:"spec"`
	Status   KVCacheTransferPolicyStatus `json:"status,omitempty"`
}
type KVCacheTransferPolicySpec struct {
	Provider         KVCacheProvider         `json:"provider,omitempty"`
	Triggers         []TransferTrigger       `json:"triggers"`
	Transport        TransportSpec           `json:"transport"`
	Retention        RetentionSpec           `json:"retention,omitempty"`
	AuthorizationRef *AuthorizationReference `json:"authorizationRef,omitempty"`
}
type TransferTrigger struct {
	Type   string `json:"type"`
	Action string `json:"action"`
}
type TransportSpec struct {
	Protocol         TransferProtocol       `json:"protocol,omitempty"`
	FallbackPolicy   TransferFallbackPolicy `json:"fallbackPolicy,omitempty"`
	MaxBandwidthMbps int                    `json:"maxBandwidthMbps,omitempty"`
}
type RetentionSpec struct {
	SourceRetentionAfterTransfer string `json:"sourceRetentionAfterTransfer,omitempty"`
}
type KVCacheTransferPolicyStatus struct {
	CommonStatus
	ActiveTransfers int    `json:"activeTransfers,omitempty"`
	LastCheckpoint  string `json:"lastCheckpoint,omitempty"`
}
