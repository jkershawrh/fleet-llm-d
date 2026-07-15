package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)


type AgentSim struct {
	ClusterID   string
	Region      string
	GPUType     string
	GPUCount    int
	ControlURL  string
	AuthSecret  string
	Port        int
	mu          sync.RWMutex
	metrics     SimMetrics
	registered  bool
	startTime   time.Time
	loadPattern string
}

type SimMetrics struct {
	LatencyMs      float64 `json:"latency_ms"`
	ThroughputTPS  float64 `json:"throughput_tps"`
	GPUUtilization float64 `json:"gpu_utilization"`
	QueueDepth     int     `json:"queue_depth"`
	Replicas       int     `json:"replicas"`
	Healthy        bool    `json:"healthy"`
}

func main() {
	clusterID := flag.String("cluster-id", "", "Unique cluster identifier (auto-derived from hostname if empty)")
	region := flag.String("region", "", "Cluster region (auto-derived from ordinal if empty)")
	gpuType := flag.String("gpu-type", "", "GPU type (auto-derived from ordinal if empty)")
	gpuCount := flag.Int("gpu-count", 0, "GPU count (auto-derived from ordinal if 0)")
	controlURL := flag.String("control-url", "http://fleet-controller.fleet-llm-d.svc:8080", "Fleet controller URL")
	authSecret := flag.String("auth-secret", "", "HMAC auth secret for fleet controller (env FLEET_AUTH_SECRET)")
	port := flag.Int("port", 8090, "Agent HTTP port")
	loadPattern := flag.String("load-pattern", "", "Load pattern: steady, spike, degradation, recovery, random (auto if empty)")
	flag.Parse()

	if *authSecret == "" {
		*authSecret = os.Getenv("FLEET_AUTH_SECRET")
	}

	hostname, _ := os.Hostname()
	ordinal := 0
	if hostname != "" {
		parts := strings.Split(hostname, "-")
		if len(parts) > 0 {
			if n, err := fmt.Sscanf(parts[len(parts)-1], "%d", &ordinal); n == 0 || err != nil {
				ordinal = 0
			}
		}
	}

	regions := []string{"us-east", "us-west", "eu-west", "ap-south", "us-central", "eu-central", "ap-north"}
	gpuTypes := []string{"A100", "H100", "A10G", "H200", "B200"}
	patterns := []string{"steady", "spike", "degradation", "recovery", "random"}

	if *clusterID == "" {
		*clusterID = fmt.Sprintf("sim-cluster-%d", ordinal)
	}
	if *region == "" {
		*region = regions[ordinal%len(regions)]
	}
	if *gpuType == "" {
		*gpuType = gpuTypes[ordinal%len(gpuTypes)]
	}
	if *gpuCount == 0 {
		*gpuCount = 4 + ordinal%12
	}
	if *loadPattern == "" {
		*loadPattern = patterns[ordinal%len(patterns)]
	}

	sim := &AgentSim{
		ClusterID:   *clusterID,
		Region:      *region,
		GPUType:     *gpuType,
		GPUCount:    *gpuCount,
		ControlURL:  strings.TrimRight(*controlURL, "/"),
		AuthSecret:  *authSecret,
		Port:        *port,
		startTime:   time.Now(),
		loadPattern: *loadPattern,
		metrics: SimMetrics{
			LatencyMs:      50,
			ThroughputTPS:  100,
			GPUUtilization: 0.45,
			QueueDepth:     2,
			Replicas:       2,
			Healthy:        true,
		},
	}

	sim.register()

	go sim.updateMetricsLoop()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", sim.handleHealthz)
	mux.HandleFunc("/metrics", sim.handleMetrics)
	mux.HandleFunc("/readyz", sim.handleReadyz)

	addr := fmt.Sprintf(":%d", sim.Port)
	log.Printf("fleet-agent-sim %s (%s, %s x%d) listening on %s, pattern=%s",
		sim.ClusterID, sim.Region, sim.GPUType, sim.GPUCount, addr, sim.loadPattern)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func (s *AgentSim) register() {
	payload := fmt.Sprintf(`{"name":%q,"provider":"simulated","region":%q}`,
		s.ClusterID, s.Region)

	req, err := http.NewRequest("POST", s.ControlURL+"/api/v1/clusters",
		strings.NewReader(payload))
	if err != nil {
		log.Printf("WARNING: failed to create registration request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	if s.AuthSecret != "" {
		token, err := generateSimToken(s.AuthSecret, s.ClusterID)
		if err == nil {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Printf("WARNING: failed to register with controller: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode < 300 || resp.StatusCode == 409 {
		s.registered = true
		log.Printf("Registered cluster %s (status %d)", s.ClusterID, resp.StatusCode)
	} else {
		log.Printf("WARNING: registration returned %d", resp.StatusCode)
	}
}

func generateSimToken(secret, subject string) (string, error) {
	type claims struct {
		Sub  string    `json:"sub"`
		Role string    `json:"role"`
		Iat  time.Time `json:"iat"`
		Exp  time.Time `json:"exp"`
	}
	c := claims{
		Sub:  subject,
		Role: "operator",
		Iat:  time.Now(),
		Exp:  time.Now().Add(24 * time.Hour),
	}
	claimsJSON, _ := json.Marshal(c)
	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsJSON)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(claimsJSON)
	sig := mac.Sum(nil)
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)

	return claimsB64 + "." + sigB64, nil
}

func (s *AgentSim) updateMetricsLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		elapsed := time.Since(s.startTime).Seconds()
		s.mu.Lock()
		switch s.loadPattern {
		case "steady":
			s.metrics.LatencyMs = 50 + 10*math.Sin(elapsed/30)
			s.metrics.GPUUtilization = 0.45 + 0.05*math.Sin(elapsed/60)
			s.metrics.ThroughputTPS = 100 + 10*math.Sin(elapsed/45)
			s.metrics.QueueDepth = 2 + int(math.Abs(math.Sin(elapsed/20)))
		case "spike":
			cycle := math.Mod(elapsed, 300)
			if cycle > 200 && cycle < 260 {
				s.metrics.LatencyMs = 500 + 200*math.Sin(elapsed/5)
				s.metrics.GPUUtilization = 0.95
				s.metrics.QueueDepth = 50 + int(30*math.Sin(elapsed/3))
				s.metrics.ThroughputTPS = 30
			} else {
				s.metrics.LatencyMs = 50 + 10*math.Sin(elapsed/30)
				s.metrics.GPUUtilization = 0.45
				s.metrics.QueueDepth = 2
				s.metrics.ThroughputTPS = 100
			}
		case "degradation":
			factor := math.Min(elapsed/600, 1.0)
			s.metrics.LatencyMs = 50 + 450*factor
			s.metrics.GPUUtilization = 0.45 + 0.50*factor
			s.metrics.ThroughputTPS = 100 - 70*factor
			s.metrics.QueueDepth = 2 + int(48*factor)
		case "recovery":
			factor := math.Max(1.0-elapsed/600, 0.0)
			s.metrics.LatencyMs = 50 + 450*factor
			s.metrics.GPUUtilization = 0.45 + 0.50*factor
			s.metrics.ThroughputTPS = 100 - 70*factor
			s.metrics.QueueDepth = 2 + int(48*factor)
		case "random":
			s.metrics.LatencyMs = 30 + rand.Float64()*200
			s.metrics.GPUUtilization = 0.2 + rand.Float64()*0.7
			s.metrics.ThroughputTPS = 20 + rand.Float64()*150
			s.metrics.QueueDepth = rand.Intn(30)
		}
		s.mu.Unlock()
	}
}

func (s *AgentSim) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "cluster": s.ClusterID})
}

func (s *AgentSim) handleReadyz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if s.registered {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ready"})
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"status": "not registered"})
	}
}

func (s *AgentSim) handleMetrics(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	m := s.metrics
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintf(w, "# HELP fleet_agent_sim_latency_ms Simulated inference latency\n")
	fmt.Fprintf(w, "fleet_agent_sim_latency_ms{cluster=%q} %.1f\n", s.ClusterID, m.LatencyMs)
	fmt.Fprintf(w, "# HELP fleet_agent_sim_throughput_tps Simulated throughput\n")
	fmt.Fprintf(w, "fleet_agent_sim_throughput_tps{cluster=%q} %.1f\n", s.ClusterID, m.ThroughputTPS)
	fmt.Fprintf(w, "# HELP fleet_agent_sim_gpu_utilization Simulated GPU utilization\n")
	fmt.Fprintf(w, "fleet_agent_sim_gpu_utilization{cluster=%q} %.3f\n", s.ClusterID, m.GPUUtilization)
	fmt.Fprintf(w, "# HELP fleet_agent_sim_queue_depth Simulated queue depth\n")
	fmt.Fprintf(w, "fleet_agent_sim_queue_depth{cluster=%q} %d\n", s.ClusterID, m.QueueDepth)
	fmt.Fprintf(w, "# HELP fleet_agent_sim_replicas Simulated replica count\n")
	fmt.Fprintf(w, "fleet_agent_sim_replicas{cluster=%q} %d\n", s.ClusterID, m.Replicas)
	fmt.Fprintf(w, "# HELP fleet_agent_sim_healthy Simulated health status\n")
	fmt.Fprintf(w, "fleet_agent_sim_healthy{cluster=%q} 1\n", s.ClusterID)
}
