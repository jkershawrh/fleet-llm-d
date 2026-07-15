package server

import (
	"net/http"
)

// handleListPools returns all fleet inference pools. It merges data from
// the reconciler (which tracks live CRD state) with the repository.
func (fc *FleetController) handleListPools(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)

	// Prefer reconciler state when available -- it reflects live CRD watches.
	if fc.Reconciler != nil {
		reconciled := fc.Reconciler.ListPools()
		if len(reconciled) > 0 {
			writeJSON(w, http.StatusOK, reconciled)
			return
		}
	}

	// Fall back to the store-backed repository.
	pools, err := fc.PoolRepo.List(r.Context())
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pools)
}

// handleGetPoolState returns the reconciled state for a single pool by name.
func (fc *FleetController) handleGetPoolState(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Add(1)
	name := r.PathValue("name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "pool name is required")
		return
	}
	state, err := fc.Reconciler.GetPoolState(name)
	if err != nil {
		errorsTotal.Add(1)
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, state)
}
