package signals

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// PrometheusStore converts cluster-local llm-d EPP metrics into the small,
// pool-scoped Grid Signals contract. Source labels are deliberately discarded
// so pod, container, instance, and rank identities cannot cross the boundary.
type PrometheusStore struct {
	Endpoint         string
	HTTP             *http.Client
	MaxResponseBytes int64
	Now              func() time.Time
}

type signalAccumulator struct {
	priority int
	values   []float64
	agg      string
}

type signalMapping struct {
	output   string
	priority int
	agg      string
}

var gridSignalMappings = map[string]signalMapping{
	"llm_d_epp_average_queue_size":                {"llm_d_epp_average_queue_size", 2, "max"},
	"llm_d_epp_flow_control_queue_size":           {"llm_d_epp_average_queue_size", 2, "sum"},
	"llm_d_epp_per_endpoint_queue_size":           {"llm_d_epp_average_queue_size", 1, "average"},
	"llm_d_epp_average_kv_cache_utilization":      {"llm_d_epp_average_kv_cache_utilization", 2, "average"},
	"llm_d_epp_per_endpoint_kv_cache_utilization": {"llm_d_epp_average_kv_cache_utilization", 1, "average"},
	"llm_d_epp_ready_endpoints":                   {"llm_d_epp_ready_endpoints", 2, "max"},
	"llm_d_epp_flow_control_pool_saturation":      {"llm_d_epp_flow_control_pool_saturation", 2, "max"},
}

func (s *PrometheusStore) Samples(ctx context.Context) ([]Sample, error) {
	if strings.TrimSpace(s.Endpoint) == "" {
		return nil, fmt.Errorf("Prometheus endpoint is required")
	}
	client := s.HTTP
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.Endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("scrape local EPP metrics: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("local EPP metrics returned %d", resp.StatusCode)
	}
	limit := s.MaxResponseBytes
	if limit <= 0 {
		limit = DefaultMaxResponseBytes
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("local EPP metrics exceed %d bytes", limit)
	}

	accumulators := map[string]signalAccumulator{}
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		name := strings.SplitN(fields[0], "{", 2)[0]
		mapping, ok := gridSignalMappings[name]
		if !ok {
			continue
		}
		value, err := strconv.ParseFloat(fields[1], 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		current := accumulators[mapping.output]
		if mapping.priority < current.priority {
			continue
		}
		if mapping.priority > current.priority {
			current = signalAccumulator{priority: mapping.priority, agg: mapping.agg}
		}
		current.values = append(current.values, value)
		accumulators[mapping.output] = current
	}

	now := time.Now().UTC()
	if s.Now != nil {
		now = s.Now().UTC()
	}
	result := make([]Sample, 0, len(accumulators))
	for name, accumulator := range accumulators {
		result = append(result, Sample{Name: name, Value: aggregateValues(accumulator.values, accumulator.agg), CollectedAt: now})
	}
	return result, nil
}

func aggregateValues(values []float64, operation string) float64 {
	if len(values) == 0 {
		return 0
	}
	value := values[0]
	if operation == "max" {
		for _, candidate := range values[1:] {
			value = math.Max(value, candidate)
		}
		return value
	}
	for _, candidate := range values[1:] {
		value += candidate
	}
	if operation == "average" {
		value /= float64(len(values))
	}
	return value
}
