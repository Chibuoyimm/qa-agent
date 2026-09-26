package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Chibuoyimm/qa-agent/internal/qa"
)

type API struct {
	store       *qa.Store
	apiToken    string
	workerToken string
	logger      *slog.Logger
}

func New(store *qa.Store, apiToken, workerToken string, logger *slog.Logger) *API {
	return &API{store: store, apiToken: apiToken, workerToken: workerToken, logger: logger}
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, struct {
			Status string `json:"status"`
		}{Status: "ok"})
	})
	mux.Handle("GET /api/projects", a.authorize(a.apiToken, a.listProjects))
	mux.Handle("POST /api/projects", a.authorize(a.apiToken, a.createProject))
	mux.Handle("GET /api/projects/{id}/scenarios", a.authorize(a.apiToken, a.listScenarios))
	mux.Handle("POST /api/projects/{id}/scenarios", a.authorize(a.apiToken, a.createScenario))
	mux.Handle("GET /api/projects/{id}/runs", a.authorize(a.apiToken, a.listRuns))
	mux.Handle("POST /api/projects/{id}/runs", a.authorize(a.apiToken, a.createRun))
	mux.Handle("GET /api/runs/{id}", a.authorize(a.apiToken, a.getRun))
	mux.Handle("POST /api/runs/{id}/cancel", a.authorize(a.apiToken, a.cancelRun))
	mux.Handle("POST /api/worker/claim", a.authorize(a.workerToken, a.claimRun))
	mux.Handle("POST /api/worker/runs/{id}/heartbeat", a.authorize(a.workerToken, a.heartbeat))
	mux.Handle("POST /api/worker/runs/{id}/complete", a.authorize(a.workerToken, a.completeRun))
	return mux
}

func (a *API) authorize(token string, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if bearer == r.Header.Get("Authorization") || subtle.ConstantTimeCompare([]byte(bearer), []byte(token)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	})
}

const maxBodyBytes = 1 << 20

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	var trailing struct{}
	if err := dec.Decode(&trailing); err != io.EOF {
		writeError(w, http.StatusBadRequest, "JSON body must contain one object")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, code int, message string) {
	writeJSON(w, code, struct {
		Error string `json:"error"`
	}{Error: message})
}

func (a *API) fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, qa.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, qa.ErrNotFound):
		writeError(w, http.StatusNotFound, "record not found")
	case errors.Is(err, qa.ErrConflict):
		writeError(w, http.StatusConflict, "invalid run state or lease")
	default:
		a.logger.Error("request failed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
}

func (a *API) listProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := a.store.ListProjects(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, projects)
}

func (a *API) createProject(w http.ResponseWriter, r *http.Request) {
	var in qa.ProjectInput
	if !decodeJSON(w, r, &in) {
		return
	}
	project, err := a.store.CreateProject(r.Context(), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, project)
}

func (a *API) listScenarios(w http.ResponseWriter, r *http.Request) {
	scenarios, err := a.store.ListScenarios(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, scenarios)
}

func (a *API) createScenario(w http.ResponseWriter, r *http.Request) {
	var in qa.ScenarioInput
	if !decodeJSON(w, r, &in) {
		return
	}
	scenario, err := a.store.CreateScenario(r.Context(), r.PathValue("id"), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, scenario)
}

func (a *API) listRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := a.store.ListRuns(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (a *API) createRun(w http.ResponseWriter, r *http.Request) {
	var in qa.RunInput
	if !decodeJSON(w, r, &in) {
		return
	}
	run, err := a.store.CreateRun(r.Context(), r.PathValue("id"), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, run)
}

func (a *API) getRun(w http.ResponseWriter, r *http.Request) {
	run, err := a.store.GetRun(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (a *API) cancelRun(w http.ResponseWriter, r *http.Request) {
	run, err := a.store.CancelRun(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (a *API) claimRun(w http.ResponseWriter, r *http.Request) {
	var in struct {
		WorkerID string `json:"worker_id"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	run, token, expiry, found, err := a.store.ClaimRun(r.Context(), in.WorkerID)
	if err != nil {
		a.fail(w, err)
		return
	}
	if !found {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Run            qa.Run    `json:"run"`
		LeaseToken     string    `json:"lease_token"`
		LeaseExpiresAt time.Time `json:"lease_expires_at"`
	}{Run: run, LeaseToken: token, LeaseExpiresAt: expiry})
}

func (a *API) heartbeat(w http.ResponseWriter, r *http.Request) {
	var in struct {
		LeaseToken string `json:"lease_token"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	expiry, err := a.store.Heartbeat(r.Context(), r.PathValue("id"), in.LeaseToken)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		LeaseExpiresAt time.Time `json:"lease_expires_at"`
	}{LeaseExpiresAt: expiry})
}

func (a *API) completeRun(w http.ResponseWriter, r *http.Request) {
	var in qa.CompleteInput
	if !decodeJSON(w, r, &in) {
		return
	}
	run, err := a.store.CompleteRun(r.Context(), r.PathValue("id"), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}
