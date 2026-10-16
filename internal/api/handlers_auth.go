package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/azmi2409/forge-space/internal/auth"
	"github.com/azmi2409/forge-space/internal/store"
)

// POST /api/v1/auth/register {email, password>=8, display_name} → 201 {user}
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email       string `json:"email"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		writeAPIError(w, err)
		return
	}
	in.Email = strings.TrimSpace(strings.ToLower(in.Email))
	if !strings.Contains(in.Email, "@") || len(in.Email) > 254 {
		writeAPIError(w, errBadRequest("invalid email"))
		return
	}
	if err := validateDisplayName(in.DisplayName); err != nil {
		writeAPIError(w, err)
		return
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	u, err := s.store.CreateUser(store.NewID(store.PrefixUser), in.Email, hash, in.DisplayName, store.Now())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeAPIError(w, errConflict("email already registered"))
			return
		}
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"user": u})
}

// POST /api/v1/auth/login {email,password} → 200 {token, user}
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		writeAPIError(w, err)
		return
	}
	u, hash, err := s.store.GetUserByEmail(strings.TrimSpace(strings.ToLower(in.Email)))
	if err != nil {
		writeAPIError(w, errUnauthorized("invalid email or password"))
		return
	}
	ok, err := auth.VerifyPassword(in.Password, hash)
	if err != nil || !ok {
		writeAPIError(w, errUnauthorized("invalid email or password"))
		return
	}
	token, tokenHash, err := auth.NewSessionToken()
	if err != nil {
		writeAPIError(w, errBadRequest("could not create session"))
		return
	}
	now := time.Now().UTC()
	exp := now.Add(auth.SessionTTL).Format("2006-01-02T15:04:05Z07:00")
	if err := s.store.CreateSession(tokenHash, u.ID, exp, now.Format("2006-01-02T15:04:05Z07:00")); err != nil {
		writeAPIError(w, errBadRequest("could not create session"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "user": u})
}

// POST /api/v1/agent-keys (user only) {name, scopes:["agent"|"admin"]} → 201
func (s *Server) handleCreateAgentKey(w http.ResponseWriter, r *http.Request) {
	c, apiErr := s.caller(r)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if apiErr := requireHuman(c); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	var in struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		writeAPIError(w, err)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 60 {
		writeAPIError(w, errBadRequest("name must be 1..60 chars"))
		return
	}
	if len(in.Scopes) == 0 {
		in.Scopes = []string{auth.ScopeAgent}
	}
	for _, sc := range in.Scopes {
		if sc != auth.ScopeAgent && sc != auth.ScopeAdmin {
			writeAPIError(w, errBadRequest(`scope must be "agent" or "admin"`))
			return
		}
	}
	secret, secretHash, err := auth.NewAgentKey()
	if err != nil {
		writeAPIError(w, errBadRequest("could not create key"))
		return
	}
	k, err := s.store.CreateAgentKey(store.NewID(store.PrefixAgentKey), c.UserID, in.Name, secretHash, in.Scopes, store.Now())
	if err != nil {
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	// secret is shown ONCE — never stored raw, never returned again.
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": k.ID, "name": k.Name, "prefix": auth.AgentKeyPrefix, "secret": secret,
	})
}

// GET /api/v1/agent-keys → {keys:[...]} (no secrets)
func (s *Server) handleListAgentKeys(w http.ResponseWriter, r *http.Request) {
	c, apiErr := s.caller(r)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if apiErr := requireHuman(c); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	keys, err := s.store.ListAgentKeys(c.UserID)
	if err != nil {
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	if keys == nil {
		keys = []*store.AgentKey{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

// DELETE /api/v1/agent-keys/{id} → 200 {}
func (s *Server) handleDeleteAgentKey(w http.ResponseWriter, r *http.Request) {
	c, apiErr := s.caller(r)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if apiErr := requireHuman(c); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if err := s.store.DeleteAgentKey(r.PathValue("id"), c.UserID); err != nil {
		if err == store.ErrNotFound {
			writeAPIError(w, errNotFound("agent key not found"))
			return
		}
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{})
}

func validateDisplayName(name string) *apiError {
	if len(name) < 1 || len(name) > 30 {
		return errBadRequest("display_name must be 1..30 chars")
	}
	return nil
}
