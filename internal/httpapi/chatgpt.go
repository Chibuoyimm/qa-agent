package httpapi

import (
	"net/http"

	"github.com/Chibuoyimm/qa-agent/internal/chatgpt"
)

type chatGPTProfileInput struct {
	ProfileID string `json:"profile_id"`
}

func (a *API) chatGPTStatus(w http.ResponseWriter, r *http.Request) {
	if a.planner.ChatGPT == nil {
		a.fail(w, chatgpt.ErrUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, a.planner.ChatGPT.Status())
}

func (a *API) chatGPTLogin(w http.ResponseWriter, r *http.Request) {
	if a.planner.ChatGPT == nil {
		a.fail(w, chatgpt.ErrUnavailable)
		return
	}
	var input chatGPTProfileInput
	if !decodeJSONLimit(w, r, &input, 4096) {
		return
	}
	login, err := a.planner.ChatGPT.StartLogin(r.Context(), input.ProfileID)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, login)
}

func (a *API) chatGPTCancelLogin(w http.ResponseWriter, r *http.Request) {
	if a.planner.ChatGPT == nil {
		a.fail(w, chatgpt.ErrUnavailable)
		return
	}
	var input struct {
		LoginID string `json:"login_id"`
	}
	if !decodeJSONLimit(w, r, &input, 4096) {
		return
	}
	if err := a.planner.ChatGPT.CancelLogin(input.LoginID); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a.planner.ChatGPT.Status())
}

func (a *API) chatGPTSelect(w http.ResponseWriter, r *http.Request) {
	if a.planner.ChatGPT == nil {
		a.fail(w, chatgpt.ErrUnavailable)
		return
	}
	var input chatGPTProfileInput
	if !decodeJSONLimit(w, r, &input, 4096) {
		return
	}
	if err := a.planner.ChatGPT.Select(input.ProfileID); err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a.planner.ChatGPT.Status())
}

func (a *API) chatGPTDisconnect(w http.ResponseWriter, r *http.Request) {
	if a.planner.ChatGPT == nil {
		a.fail(w, chatgpt.ErrUnavailable)
		return
	}
	var input chatGPTProfileInput
	if !decodeJSONLimit(w, r, &input, 4096) {
		return
	}
	status, err := a.planner.ChatGPT.Disconnect(r.Context(), input.ProfileID)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (a *API) chatGPTModels(w http.ResponseWriter, r *http.Request) {
	if a.planner.ChatGPT == nil {
		a.fail(w, chatgpt.ErrUnavailable)
		return
	}
	models, err := a.planner.ChatGPT.Models(r.Context(), r.URL.Query().Get("profile_id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, models)
}
