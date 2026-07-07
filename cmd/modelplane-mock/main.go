package main

// ModelPlane Mock API Server
// Serves fake InferenceCluster, ModelDeployment, ModelEndpoint, InferenceClass,
// and ModelService resources so fleet-llm-d's ModelPlane watcher can consume them.

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

// ---------------------------------------------------------------------------
// K8s-style list wrapper
// ---------------------------------------------------------------------------

type listResponse struct {
	APIVersion string      `json:"apiVersion"`
	Kind       string      `json:"kind"`
	Items      interface{} `json:"items"`
}

func writeList(w http.ResponseWriter, kind string, items interface{}) {
	w.Header().Set("Content-Type", "application/json")
	resp := listResponse{
		APIVersion: "modelplane.ai/v1alpha1",
		Kind:       kind,
		Items:      items,
	}
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("ERROR: encoding response: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Resource types — mirrors pkg/modelplane/types.go
// ---------------------------------------------------------------------------

type clusterStatus struct {
	Phase string `json:"phase"`
	Nodes int    `json:"nodes"`
}

type nodePool struct {
	Name      string `json:"name"`
	GPUType   string `json:"gpuType"`
	Count     int    `json:"count"`
	Available int    `json:"available"`
}

type inferenceCluster struct {
	Name     string            `json:"name"`
	Region   string            `json:"region"`
	Provider string            `json:"provider"`
	Labels   map[string]string `json:"labels"`
	Status   clusterStatus     `json:"status"`
	Pools    []nodePool        `json:"pools"`
}

type deploymentStatus struct {
	Phase         string   `json:"phase"`
	ReadyReplicas int      `json:"readyReplicas"`
	Clusters      []string `json:"clusters"`
}

type modelDeployment struct {
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Model       string            `json:"model"`
	Engine      string            `json:"engine"`
	Replicas    int               `json:"replicas"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	Status      deploymentStatus  `json:"status"`
}

type modelEndpoint struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	URL       string `json:"url"`
	Model     string `json:"model"`
	Cluster   string `json:"cluster"`
	Ready     bool   `json:"ready"`
}

type weightedEndpoint struct {
	EndpointName string `json:"endpointName"`
	Weight       int    `json:"weight"`
}

type modelService struct {
	Name      string             `json:"name"`
	Namespace string             `json:"namespace"`
	Endpoints []weightedEndpoint `json:"endpoints"`
}

type inferenceClass struct {
	Name    string `json:"name"`
	GPUType string `json:"gpuType"`
	Count   int    `json:"count"`
	Memory  int    `json:"memoryGB"`
}

// ---------------------------------------------------------------------------
// Seed data builders
// ---------------------------------------------------------------------------

func seedClusters() []inferenceCluster {
	return []inferenceCluster{
		{
			Name:     "edge-east",
			Region:   "us-east",
			Provider: "existing",
			Labels:   map[string]string{"region": "us-east"},
			Status:   clusterStatus{Phase: "Ready", Nodes: 12},
			Pools: []nodePool{
				{Name: "h200-pool", GPUType: "H200", Count: 4, Available: 4},
				{Name: "a100-pool", GPUType: "A100", Count: 8, Available: 8},
			},
		},
		{
			Name:     "edge-west",
			Region:   "us-west",
			Provider: "existing",
			Labels:   map[string]string{"region": "us-west"},
			Status:   clusterStatus{Phase: "Ready", Nodes: 4},
			Pools: []nodePool{
				{Name: "b200-pool", GPUType: "B200", Count: 4, Available: 4},
			},
		},
		{
			Name:     "sovereign-eu",
			Region:   "eu-west",
			Provider: "existing",
			Labels:   map[string]string{"region": "eu-west"},
			Status:   clusterStatus{Phase: "Ready", Nodes: 2},
			Pools: []nodePool{
				{Name: "h200-pool", GPUType: "H200", Count: 2, Available: 2},
			},
		},
	}
}

func seedDeployments(ns string) []modelDeployment {
	return []modelDeployment{
		{
			Name:        "granite-fleet",
			Namespace:   ns,
			Model:       "ibm-granite/granite-3.3-2b",
			Engine:      "vllm",
			Replicas:    4,
			Labels:      map[string]string{"model": "granite-3.3-2b"},
			Annotations: map[string]string{},
			Status: deploymentStatus{
				Phase:         "Running",
				ReadyReplicas: 4,
				Clusters:      []string{"edge-east", "edge-west"},
			},
		},
		{
			Name:        "llama-serve",
			Namespace:   ns,
			Model:       "meta-llama/llama-3.3-70b",
			Engine:      "vllm",
			Replicas:    2,
			Labels:      map[string]string{"model": "llama-3.3-70b"},
			Annotations: map[string]string{},
			Status: deploymentStatus{
				Phase:         "Running",
				ReadyReplicas: 2,
				Clusters:      []string{"edge-east"},
			},
		},
	}
}

func seedEndpoints(ns string) []modelEndpoint {
	return []modelEndpoint{
		{
			Name:      "granite-east",
			Namespace: ns,
			Model:     "granite-3.3-2b",
			Cluster:   "edge-east",
			URL:       "http://granite-east:8000",
			Ready:     true,
		},
		{
			Name:      "granite-west",
			Namespace: ns,
			Model:     "granite-3.3-2b",
			Cluster:   "edge-west",
			URL:       "http://granite-west:8000",
			Ready:     true,
		},
		{
			Name:      "llama-east",
			Namespace: ns,
			Model:     "llama-3.3-70b",
			Cluster:   "edge-east",
			URL:       "http://llama-east:8000",
			Ready:     true,
		},
	}
}

func seedServices(ns string) []modelService {
	return []modelService{
		{
			Name:      "granite-service",
			Namespace: ns,
			Endpoints: []weightedEndpoint{
				{EndpointName: "granite-east", Weight: 70},
				{EndpointName: "granite-west", Weight: 30},
			},
		},
	}
}

func seedInferenceClasses() []inferenceClass {
	return []inferenceClass{
		{Name: "gpu-h200", GPUType: "H200", Count: 1, Memory: 141},
		{Name: "gpu-b200", GPUType: "B200", Count: 1, Memory: 192},
		{Name: "gpu-a100", GPUType: "A100", Count: 1, Memory: 80},
	}
}

// ---------------------------------------------------------------------------
// HTTP handlers
// ---------------------------------------------------------------------------

// apiPrefix is the base path for all ModelPlane CRD endpoints.
const apiPrefix = "/apis/modelplane.ai/v1alpha1"

func main() {
	port := flag.Int("port", 8090, "listen port")
	ns := flag.String("namespace", "default", "default namespace for seed data")
	flag.Parse()

	mux := http.NewServeMux()

	// Healthz
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	// InferenceCluster — the watcher fetches this without a namespace prefix.
	mux.HandleFunc(apiPrefix+"/inferenceclusters", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			handlePatch(w, r, "InferenceCluster")
			return
		}
		writeList(w, "InferenceClusterList", seedClusters())
	})

	// Namespaced routes: /apis/modelplane.ai/v1alpha1/namespaces/{ns}/{resource}
	// We use a single handler on the /namespaces/ prefix and dispatch by resource
	// name extracted from the URL path.
	nsPrefix := apiPrefix + "/namespaces/"
	mux.HandleFunc(nsPrefix, namespacedDispatcher(ns))

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("modelplane-mock listening on %s (namespace=%s)", addr, *ns)
	log.Fatal(http.ListenAndServe(addr, logMiddleware(mux)))
}

// namespacedDispatcher returns a handler that routes requests under
// /apis/modelplane.ai/v1alpha1/namespaces/{ns}/{resource} to the
// appropriate resource handler based on the resource segment.
func namespacedDispatcher(ns *string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		resource := extractResource(r.URL.Path)
		switch resource {
		case "inferenceclusters":
			if r.Method == http.MethodPatch {
				handlePatch(w, r, "InferenceCluster")
				return
			}
			writeList(w, "InferenceClusterList", seedClusters())
		case "modeldeployments":
			if r.Method == http.MethodPatch {
				handlePatch(w, r, "ModelDeployment")
				return
			}
			writeList(w, "ModelDeploymentList", seedDeployments(*ns))
		case "modelendpoints":
			if r.Method == http.MethodPatch {
				handlePatch(w, r, "ModelEndpoint")
				return
			}
			writeList(w, "ModelEndpointList", seedEndpoints(*ns))
		case "modelservices":
			if r.Method == http.MethodPatch {
				handlePatch(w, r, "ModelService")
				return
			}
			writeList(w, "ModelServiceList", seedServices(*ns))
		case "inferenceclasses":
			if r.Method == http.MethodPatch {
				handlePatch(w, r, "InferenceClass")
				return
			}
			writeList(w, "InferenceClassList", seedInferenceClasses())
		default:
			http.NotFound(w, r)
		}
	}
}

// extractResource pulls the resource name from a path like
// /apis/modelplane.ai/v1alpha1/namespaces/{ns}/{resource}[/{name}].
func extractResource(path string) string {
	prefix := apiPrefix + "/namespaces/"
	rest := strings.TrimPrefix(path, prefix)
	// rest is "{ns}/{resource}" or "{ns}/{resource}/{name}"
	parts := strings.SplitN(rest, "/", 3)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}

// handlePatch accepts a PATCH request, logs the body, and returns 200.
func handlePatch(w http.ResponseWriter, r *http.Request, kind string) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()
	log.Printf("PATCH %s %s: %s", kind, r.URL.Path, string(body))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"status":"patched","kind":%q}`, kind)
}

// logMiddleware logs each request method and path.
func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
