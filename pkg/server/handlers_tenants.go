package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/store/postgres"
	"github.com/llm-d/fleet-llm-d/pkg/tenant/metering"
)

func decodeJSON(w http.ResponseWriter, r *http.Request, target interface{}) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	return decoder.Decode(target)
}

// handleListTenants returns all tenants.
func (fc *FleetController) handleListTenants(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Inc()
	tenants, err := fc.TenantRepo.List(r.Context())
	if err != nil {
		errorsTotal.Inc()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tenants)
}

// handleCreateTenant creates a new tenant with quotas.
func (fc *FleetController) handleCreateTenant(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Inc()
	defer ObserveRequest(time.Now())

	var req struct {
		ID          string                 `json:"id"`
		Name        string                 `json:"name"`
		Priority    int                    `json:"priority"`
		Quotas      map[string]interface{} `json:"quotas"`
		RateLimit   map[string]interface{} `json:"rate_limit"`
		CostControl map[string]interface{} `json:"cost_control"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		errorsTotal.Inc()
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.ID) == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	if req.Name == "" {
		req.Name = req.ID
	}

	record := postgres.TenantRecord{
		ID:          req.ID,
		Name:        req.Name,
		Priority:    req.Priority,
		Quotas:      req.Quotas,
		RateLimit:   req.RateLimit,
		CostControl: req.CostControl,
		CreatedAt:   time.Now().UTC(),
	}
	if err := fc.TenantRepo.Create(r.Context(), record); err != nil {
		errorsTotal.Inc()
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	tenantsGauge.Inc()
	writeJSON(w, http.StatusCreated, map[string]string{"id": req.ID, "status": "created"})
}

// handleTenantUsage returns usage for a specific tenant.
func (fc *FleetController) handleTenantUsage(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Inc()
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "tenant id is required")
		return
	}
	if len(id) > 256 || strings.ContainsAny(id, "'\";\n\r") {
		writeError(w, http.StatusBadRequest, "invalid tenant id")
		return
	}
	// Default to current month.
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := now

	period := metering.TimePeriod{Start: start, End: end}
	usage, err := fc.UsageTracker.GetUsage(r.Context(), id, period)
	if err != nil {
		errorsTotal.Inc()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, usage)
}
