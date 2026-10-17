package httpapi

import (
	"net/http"
)

// startIntelligence creates the report immediately (status "running") and
// queues the actual web-research step, so the client can navigate straight to
// the report page and poll it — the same job + poll pattern used by discover
// and research elsewhere in this API.
func (a *API) startIntelligence(w http.ResponseWriter, r *http.Request) {
	var in struct {
		CompanyName string `json:"company_name"`
		Website     string `json:"website"`
	}
	if !decode(w, r, &in) {
		return
	}
	rep, err := a.intel.StartReport(r.Context(), orgID(r), in.CompanyName, in.Website)
	if err != nil {
		fail(w, err)
		return
	}
	job, err := a.jobs.Analyze(r.Context(), orgID(r), rep.ID)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"report": rep, "job": job})
}

func (a *API) listReports(w http.ResponseWriter, r *http.Request) {
	reps, err := a.store.ListReports(r.Context(), orgID(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reps)
}

func (a *API) getReport(w http.ResponseWriter, r *http.Request) {
	rep, err := a.store.GetReport(r.Context(), orgID(r), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (a *API) deleteReport(w http.ResponseWriter, r *http.Request) {
	if err := a.intel.DeleteReport(r.Context(), orgID(r), r.PathValue("id")); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
