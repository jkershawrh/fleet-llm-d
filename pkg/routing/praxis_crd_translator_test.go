package routing

import (
	"testing"
)

func TestTranslateClusterToGridSite(t *testing.T) {
	translator := NewGridCRDTranslator("https://k8s.local", "fleet-llm-d", "token", "test-network")

	cluster := FleetClusterInfo{
		ID:     "oberon-sno",
		Name:   "Oberon",
		Region: "us-east-1",
		Labels: map[string]string{
			"topology.kubernetes.io/zone":     "us-east-1a",
			"fleet.llm-d.ai/sovereignty-zone": "eu-gdpr",
		},
		EgressAddress: "praxis-ai.fleet-llm-d.svc:8080",
	}

	spec := translator.TranslateClusterToGridSite(cluster)

	if spec.GridNetworkRef != "test-network" {
		t.Errorf("GridNetworkRef = %q, want test-network", spec.GridNetworkRef)
	}
	if spec.Region != "us-east-1" {
		t.Errorf("Region = %q, want us-east-1", spec.Region)
	}
	if spec.Zone != "us-east-1a" {
		t.Errorf("Zone = %q, want us-east-1a", spec.Zone)
	}
	if spec.SovereigntyZone != "eu-gdpr" {
		t.Errorf("SovereigntyZone = %q, want eu-gdpr", spec.SovereigntyZone)
	}
	if spec.Egress == nil || spec.Egress.Address != "praxis-ai.fleet-llm-d.svc:8080" {
		t.Errorf("Egress address = %v, want praxis-ai.fleet-llm-d.svc:8080", spec.Egress)
	}
	if spec.Egress.TLS == nil || spec.Egress.TLS.Mode != "Mutual" {
		t.Error("expected mTLS mode")
	}
}

func TestTranslateClusterNoEgress(t *testing.T) {
	translator := NewGridCRDTranslator("https://k8s.local", "fleet-llm-d", "", "net")

	spec := translator.TranslateClusterToGridSite(FleetClusterInfo{
		ID:     "test",
		Region: "eu-west-1",
	})

	if spec.Egress != nil {
		t.Error("expected nil egress when no address provided")
	}
}

func TestTranslateClusterUsesPortableTransportContract(t *testing.T) {
	translator := NewGridCRDTranslator("https://k8s.local", "fleet-llm-d", "", "net")
	spec := translator.TranslateClusterToGridSite(FleetClusterInfo{
		ID:        "portable",
		Transport: EndpointTransport{RoutingURL: "https://gateway.example", TLSServerName: "backend.internal"},
	})
	if spec.Egress == nil || spec.Egress.Address != "https://gateway.example" {
		t.Fatalf("egress = %#v", spec.Egress)
	}
	if spec.Egress.TLS == nil || spec.Egress.TLS.ServerName != "backend.internal" {
		t.Fatalf("tls = %#v", spec.Egress.TLS)
	}
}

func TestTranslatePoolToInferenceProvider(t *testing.T) {
	translator := NewGridCRDTranslator("https://k8s.local", "fleet-llm-d", "", "test-network")

	pool := FleetPoolInfo{
		Name:            "granite-2b-pool",
		ModelName:       "granite-2b-cpu",
		Clusters:        []string{"oberon-sno"},
		TargetPorts:     []int{8080},
		MetricsEndpoint: "http://fleet-agent.fleet-llm-d.svc:8090",
	}

	spec := translator.TranslatePoolToInferenceProvider(pool)

	if spec.GridNetworkRef != "test-network" {
		t.Errorf("GridNetworkRef = %q", spec.GridNetworkRef)
	}
	if spec.ProviderKind != "InCluster" {
		t.Errorf("ProviderKind = %q", spec.ProviderKind)
	}
	if spec.Endpoint != "http://granite-2b-pool.fleet-llm-d.svc:8080" {
		t.Errorf("Endpoint = %q", spec.Endpoint)
	}
	if len(spec.Models) != 1 || spec.Models[0].Name != "granite-2b-cpu" {
		t.Errorf("Models = %v", spec.Models)
	}
	if spec.MetricsConfig == nil {
		t.Fatal("expected MetricsConfig")
	}
	if spec.MetricsConfig.PoolName != "granite-2b-cpu" {
		t.Errorf("PoolName = %q", spec.MetricsConfig.PoolName)
	}
	if spec.MetricsConfig.SignalNames["queueDepth"] != "llm_d_router_epp_average_queue_size" {
		t.Errorf("SignalNames = %v", spec.MetricsConfig.SignalNames)
	}
	if spec.SiteSelector == nil || spec.SiteSelector.MatchLabels["fleet.llm-d.ai/cluster-id"] != "oberon-sno" {
		t.Errorf("SiteSelector = %v", spec.SiteSelector)
	}
}

func TestTranslatePoolNoMetrics(t *testing.T) {
	translator := NewGridCRDTranslator("https://k8s.local", "ns", "", "net")

	spec := translator.TranslatePoolToInferenceProvider(FleetPoolInfo{
		Name:      "test",
		ModelName: "model",
	})

	if spec.MetricsConfig != nil {
		t.Error("expected nil MetricsConfig when no endpoint")
	}
	if spec.Endpoint != "" {
		t.Errorf("expected empty endpoint, got %q", spec.Endpoint)
	}
}

func TestTranslatePoolPreservesAllTargetClusters(t *testing.T) {
	translator := NewGridCRDTranslator("https://k8s.local", "ns", "", "net")
	spec := translator.TranslatePoolToInferenceProvider(FleetPoolInfo{
		Name: "test", ModelName: "model", Clusters: []string{"east", "west"},
	})
	if spec.SiteSelector == nil || len(spec.SiteSelector.MatchExpressions) != 1 {
		t.Fatalf("site selector = %#v, want one set-based expression", spec.SiteSelector)
	}
	requirement := spec.SiteSelector.MatchExpressions[0]
	if requirement.Operator != "In" || len(requirement.Values) != 2 || requirement.Values[0] != "east" || requirement.Values[1] != "west" {
		t.Fatalf("target clusters were truncated: %#v", requirement)
	}
}
