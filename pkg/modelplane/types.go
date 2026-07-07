package modelplane

type InferenceCluster struct {
	Name     string            `json:"name"`
	Region   string            `json:"region"`
	Provider string            `json:"provider"` // gke, eks, existing
	Labels   map[string]string `json:"labels"`
	Status   ClusterStatus     `json:"status"`
	Pools    []NodePool        `json:"pools"`
}

type ClusterStatus struct {
	Phase string `json:"phase"` // Ready, Provisioning, Error
	Nodes int    `json:"nodes"`
}

type NodePool struct {
	Name      string `json:"name"`
	GPUType   string `json:"gpuType"`
	Count     int    `json:"count"`
	Available int    `json:"available"`
}

type ModelDeployment struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Model       string            `json:"model"`
	Engine      string            `json:"engine"` // vllm, sglang, etc
	Replicas    int               `json:"replicas"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	Status      DeploymentStatus  `json:"status"`
}

type DeploymentStatus struct {
	Phase         string   `json:"phase"` // Running, Scaling, Error
	ReadyReplicas int      `json:"readyReplicas"`
	Clusters      []string `json:"clusters"` // clusters where replicas are placed
}

type ModelEndpoint struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	URL       string `json:"url"`
	Model     string `json:"model"`
	Cluster   string `json:"cluster"`
	Ready     bool   `json:"ready"`
}

type ModelService struct {
	Name      string             `json:"name"`
	Namespace string             `json:"namespace"`
	Endpoints []WeightedEndpoint `json:"endpoints"`
}

type WeightedEndpoint struct {
	EndpointName string `json:"endpointName"`
	Weight       int    `json:"weight"`
}

type InferenceClass struct {
	Name    string `json:"name"`
	GPUType string `json:"gpuType"`
	Count   int    `json:"count"`
	Memory  int    `json:"memoryGB"`
}

type ModelCache struct {
	Name    string `json:"name"`
	Model   string `json:"model"`
	Cluster string `json:"cluster"`
	Ready   bool   `json:"ready"`
}
