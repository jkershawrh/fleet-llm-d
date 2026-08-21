package server

import (
	"net/http"
	"time"
)

// handleVerifyChains verifies all ledger decision chains.
func (fc *FleetController) handleVerifyChains(w http.ResponseWriter, r *http.Request) {
	requestsTotal.Inc()
	defer ObserveRequest(time.Now())
	results, err := fc.FleetRecorder.VerifyAllChains(r.Context())
	if err != nil {
		errorsTotal.Inc()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, results)
}
