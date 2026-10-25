package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"

	"github.com/Chibuoyimm/qa-agent/internal/qa"
)

func (a *API) listTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := a.store.ListTasks(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, tasks)
}
func (a *API) createTask(w http.ResponseWriter, r *http.Request) {
	var in qa.TaskInput
	if !decodeJSON(w, r, &in) {
		return
	}
	task, err := a.store.CreateTask(r.Context(), r.PathValue("id"), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 201, task)
}
func (a *API) getTask(w http.ResponseWriter, r *http.Request) {
	task, err := a.store.GetTask(r.Context(), r.PathValue("id"), r.PathValue("task_id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, task)
}
func (a *API) discoverTask(w http.ResponseWriter, r *http.Request) {
	var in qa.TaskDiscoveryInput
	if !decodeJSON(w, r, &in) {
		return
	}
	discovery, err := a.store.DiscoverTask(r.Context(), r.PathValue("id"), r.PathValue("task_id"), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 202, discovery)
}
func (a *API) approveTask(w http.ResponseWriter, r *http.Request) {
	var in qa.TaskApprovalInput
	if !decodeJSON(w, r, &in) {
		return
	}
	approval, err := a.store.ApproveTask(r.Context(), r.PathValue("id"), r.PathValue("task_id"), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, approval)
}
func (a *API) listJourneys(w http.ResponseWriter, r *http.Request) {
	list, err := a.store.ListJourneys(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, list)
}
func (a *API) createJourney(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	journey, err := a.store.CreateJourney(r.Context(), r.PathValue("id"), in.Name)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 201, journey)
}
func (a *API) assignJourney(w http.ResponseWriter, r *http.Request) {
	var in struct {
		JourneyID string `json:"journey_id"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := a.store.AssignJourney(r.Context(), r.PathValue("id"), r.PathValue("scenario_id"), in.JourneyID); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(204)
}
func (a *API) coverage(w http.ResponseWriter, r *http.Request) {
	coverage, err := a.store.Coverage(r.Context(), r.PathValue("id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, coverage)
}
func (a *API) recheckRun(w http.ResponseWriter, r *http.Request) {
	var in qa.RecheckInput
	if !decodeJSON(w, r, &in) {
		return
	}
	run, err := a.store.RecheckRun(r.Context(), r.PathValue("run_id"), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 202, run)
}
func (a *API) findingReviews(w http.ResponseWriter, r *http.Request) {
	reviews, err := a.store.FindingReviews(r.Context(), r.PathValue("run_id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, reviews)
}
func (a *API) reviewFinding(w http.ResponseWriter, r *http.Request) {
	var in qa.FindingReview
	if !decodeJSON(w, r, &in) {
		return
	}
	review, err := a.store.ReviewFinding(r.Context(), r.PathValue("run_id"), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, review)
}
func (a *API) taskRun(w http.ResponseWriter, r *http.Request) {
	var in qa.ChatRunInput
	if !decodeJSON(w, r, &in) {
		return
	}
	in.TaskID = r.PathValue("task_id")
	raw, err := json.Marshal(in)
	if err != nil {
		a.fail(w, err)
		return
	}
	sum := sha256.Sum256(raw)
	turn, err := a.store.CreateChatRun(r.Context(), r.PathValue("id"), in, hex.EncodeToString(sum[:]))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 202, turn)
}

// Only evidence explicitly recorded on this run can be opened. Tokens never enter media URLs.
func (a *API) artifact(w http.ResponseWriter, r *http.Request) {
	run, err := a.store.GetRun(r.Context(), r.PathValue("run_id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || index < 0 {
		writeError(w, 404, "Evidence not found")
		return
	}
	var artifact *qa.Artifact
	for _, result := range run.Results {
		if result.ScenarioID == r.PathValue("scenario_id") && index < len(result.Artifacts) {
			artifact = &result.Artifacts[index]
			break
		}
	}
	if artifact == nil {
		writeError(w, 404, "Evidence not found")
		return
	}
	if a.ArtifactDirectory == "" {
		writeError(w, 503, "Recording access is not configured. Set the API artifact directory to the worker’s shared evidence directory.")
		return
	}
	root, err := os.OpenRoot(a.ArtifactDirectory)
	if err != nil {
		writeError(w, 404, "Evidence is unavailable on this server")
		return
	}
	defer root.Close()
	file, err := root.Open(artifact.Path)
	if err != nil {
		writeError(w, 404, "Evidence is missing or has expired")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		writeError(w, 404, "Evidence is unavailable")
		return
	}
	contentType := "image/png"
	if artifact.Kind == "video" {
		contentType = "video/webm"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	http.ServeContent(w, r, fmt.Sprintf("evidence-%d", index), info.ModTime(), file)
}
