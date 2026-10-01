package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/Chibuoyimm/qa-agent/internal/opencode"
)

func (a *API) openCodeStatus(w http.ResponseWriter, r *http.Request) {
	if a.planner.OpenCode == nil {
		a.fail(w, opencode.ErrUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	status, err := a.planner.OpenCode.Status(ctx)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (a *API) openCodeConnect(w http.ResponseWriter, r *http.Request) {
	if a.planner.OpenCode == nil {
		a.fail(w, opencode.ErrUnavailable)
		return
	}
	var input struct {
		Key string `json:"key"`
	}
	if !decodeJSONLimit(w, r, &input, 8192) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	status, err := a.planner.OpenCode.Connect(ctx, input.Key)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (a *API) openCodeDisconnect(w http.ResponseWriter, r *http.Request) {
	if a.planner.OpenCode == nil {
		a.fail(w, opencode.ErrUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	status, err := a.planner.OpenCode.Disconnect(ctx)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}
