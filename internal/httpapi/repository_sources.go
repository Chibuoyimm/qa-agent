package httpapi

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Chibuoyimm/qa-agent/internal/qa"
	"github.com/Chibuoyimm/qa-agent/internal/repository"
)

func (a *API) projectSources(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := a.store.GetProject(r.Context(), id); err != nil {
		a.fail(w, err)
		return
	}
	sources, err := a.store.ProjectSources(r.Context(), id)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, sources)
}

// Bind ciphertext to one project and repository even when the same token has wider access.
func (a *API) sourceCipher(project string, source qa.ProjectSource) (cipher.AEAD, []byte, error) {
	if a.RepositoryKey == nil {
		return nil, nil, errors.New("saved repository access is unavailable")
	}
	key, err := a.RepositoryKey()
	if err != nil {
		return nil, nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCMWithRandomNonce(block)
	return gcm, []byte(project + "\x00" + source.Provider + "\x00" + strings.ToLower(source.Repository)), err
}

// Only the repository client receives the decrypted token.
func (a *API) sourceToken(project string, source qa.ProjectSource) (string, error) {
	if len(source.Credential) == 0 {
		return "", nil
	}
	gcm, aad, err := a.sourceCipher(project, source)
	if err != nil {
		return "", err
	}
	raw, err := gcm.Open(nil, nil, source.Credential, aad)
	return string(raw), err
}

func (a *API) connectSource(w http.ResponseWriter, r *http.Request) {
	var in struct {
		repository.Input
		Remember bool `json:"remember_credentials"`
	}
	if !decodeJSONLimit(w, r, &in, 16<<10) {
		return
	}
	id := r.PathValue("id")
	ctx := r.Context()
	if _, err := a.store.GetProject(ctx, id); err != nil {
		a.fail(w, err)
		return
	}
	token, err := repositoryCredential(r, in.Provider)
	if err != nil {
		a.fail(w, err)
		return
	}
	if (token != "" && !in.Remember) || (in.Remember && token == "") {
		a.fail(w, fmt.Errorf("%w: recurring private access requires an explicit choice to save a read token", qa.ErrInvalid))
		return
	}
	if err := a.repositories.Resolve(ctx, in.Input, token); err != nil {
		a.fail(w, err)
		return
	}
	source := qa.ProjectSource{Ref: in.Ref, Provider: repository.EffectiveProvider(in.Provider), Repository: in.Repository, Role: in.Role}
	if in.Remember {
		gcm, aad, err := a.sourceCipher(id, source)
		if err != nil {
			a.fail(w, err)
			return
		}
		source.Credential = gcm.Seal(nil, nil, []byte(token), aad)
	}
	source, err = a.store.ConnectSource(ctx, id, source)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, source)
}
func (a *API) disconnectSource(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DisconnectSource(r.Context(), r.PathValue("id"), r.PathValue("source")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (a *API) forgetSourceCredential(w http.ResponseWriter, r *http.Request) {
	if err := a.store.ForgetRepositoryCredential(r.Context(), r.PathValue("id"), r.PathValue("source")); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
