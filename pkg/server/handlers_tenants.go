package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/llm-d/fleet-llm-d/pkg/tenant/metering"
)

// handleListTenants returns all tenants.
func (fc *FleetController) handleListTenants(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	tenants, err := fc.TenantRepo.List(r.Context())
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tenants)
}

// handleTenantUsage returns usage for a specific tenant.
func (fc *FleetController) handleTenantUsage(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
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
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, usage)
}
