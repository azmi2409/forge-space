// Package api implements the REST API per CONTRACT.md §3 (net/http stdlib mux).
// Errors are always {error:{code,message}} with a matching HTTP status.
package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/azmi2409/forge-space/internal/auth"
	"github.com/azmi2409/forge-space/internal/config"
	"github.com/azmi2409/forge-space/internal/livekitx"
	"github.com/azmi2409/forge-space/internal/realtime"
	"github.com/azmi2409/forge-space/internal/store"
)

// Server wires store, hub, LiveKit and config into HTTP handlers.
type Server struct {
	store *store.Store
	hub   *realtime.Hub
	lk    *livekitx.Client
	cfg   config.Config

	joinLimit *rateLimiter
}

// New creates the API server.
func New(st *store.Store, hub *realtime.Hub, lk *livekitx.Client, cfg config.Config) *Server {
	return &Server{
		store:     st,
		hub:       hub,
		lk:        lk,
		cfg:       cfg,
		joinLimit: newRateLimiter(10, time.Minute),
	}
}

// Router builds the mux with all routes.
func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()

	// auth
	mux.HandleFunc("POST /api/v1/auth/register", s.handleRegister)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/v1/agent-keys", s.handleCreateAgentKey)
	mux.HandleFunc("GET /api/v1/agent-keys", s.handleListAgentKeys)
	mux.HandleFunc("DELETE /api/v1/agent-keys/{id}", s.handleDeleteAgentKey)

	// spaces & maps
	mux.HandleFunc("GET /api/v1/spaces", s.handleListSpaces)
	mux.HandleFunc("POST /api/v1/spaces", s.handleCreateSpace)
	mux.HandleFunc("GET /api/v1/spaces/{id}", s.handleGetSpace)
	mux.HandleFunc("PATCH /api/v1/spaces/{id}", s.handlePatchSpace)
	mux.HandleFunc("DELETE /api/v1/spaces/{id}", s.handleDeleteSpace)
	mux.HandleFunc("POST /api/v1/spaces/{id}/maps", s.handleCreateMap)
	mux.HandleFunc("GET /api/v1/spaces/{id}/maps/{mapID}", s.handleGetMap)
	mux.HandleFunc("PUT /api/v1/spaces/{id}/maps/{mapID}", s.handlePutMap)

	// areas & objects
	mux.HandleFunc("GET /api/v1/spaces/{id}/areas", s.handleListAreas)
	mux.HandleFunc("POST /api/v1/spaces/{id}/areas", s.handleCreateArea)
	mux.HandleFunc("DELETE /api/v1/areas/{id}", s.handleDeleteArea)
	mux.HandleFunc("GET /api/v1/spaces/{id}/objects", s.handleListObjects)
	mux.HandleFunc("POST /api/v1/spaces/{id}/objects", s.handleCreateObject)
	mux.HandleFunc("PATCH /api/v1/objects/{id}", s.handlePatchObject)
	mux.HandleFunc("DELETE /api/v1/objects/{id}", s.handleDeleteObject)

	// whiteboard v1 (WHITEBOARD_V1.md)
	mux.HandleFunc("GET /api/v1/objects/{id}/board", s.handleGetBoard)
	mux.HandleFunc("POST /api/v1/objects/{id}/board", s.handleBoardDraw)
	mux.HandleFunc("DELETE /api/v1/objects/{id}/board", s.handleBoardClear)

	// play
	mux.HandleFunc("GET /api/v1/spaces/{id}/participants", s.handleListParticipants)
	mux.HandleFunc("POST /api/v1/spaces/{id}/join", s.handleJoin)
	mux.HandleFunc("POST /api/v1/spaces/{id}/leave", s.handleLeave)
	mux.HandleFunc("DELETE /api/v1/spaces/{id}/participants/{pid}", s.handleKick)
	mux.HandleFunc("POST /api/v1/spaces/{id}/chat", s.handleSendChat)
	mux.HandleFunc("GET /api/v1/spaces/{id}/chat", s.handleListChat)
	mux.HandleFunc("POST /api/v1/participants/me/move", s.handleMeMove)
	mux.HandleFunc("POST /api/v1/participants/me/portal", s.handleMePortal)
	mux.HandleFunc("POST /api/v1/participants/me/state", s.handleMeState)
	mux.HandleFunc("GET /api/v1/media/token", s.handleMediaToken)

	// realtime + health (served by the hub)
	mux.HandleFunc("GET /api/v1/stream", s.hub.ServeSSE)
	mux.HandleFunc("GET /api/v1/spaces/{id}/ws", func(w http.ResponseWriter, r *http.Request) {
		s.hub.ServeWS(w, r, r.PathValue("id"))
	})
	mux.HandleFunc("GET /api/v1/health", s.handleHealth)

	return s.cors(mux)
}

// ---------- middleware ----------

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && s.cfg.CORSAllowed(origin) {
			if s.cfg.CORSOrigins[0] == "*" {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
			}
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---------- errors & JSON ----------

type apiError struct {
	status  int
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *apiError) Error() string { return e.Code + ": " + e.Message }

func errBadRequest(msg string) *apiError { return &apiError{http.StatusBadRequest, "bad_request", msg} }
func errUnauthorized(msg string) *apiError {
	return &apiError{http.StatusUnauthorized, "unauthorized", msg}
}
func errForbidden(msg string) *apiError { return &apiError{http.StatusForbidden, "forbidden", msg} }
func errNotFound(msg string) *apiError  { return &apiError{http.StatusNotFound, "not_found", msg} }
func errRateLimited(msg string) *apiError {
	return &apiError{http.StatusTooManyRequests, "rate_limited", msg}
}
func errConflict(msg string) *apiError { return &apiError{http.StatusConflict, "conflict", msg} }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAPIError(w http.ResponseWriter, err *apiError) {
	writeJSON(w, err.status, map[string]any{"error": map[string]string{
		"code": err.Code, "message": err.Message,
	}})
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) *apiError {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errBadRequest("invalid JSON body: " + err.Error())
	}
	return nil
}

// ---------- auth helpers ----------

// caller resolves the Authorization bearer, or 401.
func (s *Server) caller(r *http.Request) (auth.Caller, *apiError) {
	c, err := auth.Resolve(s.store, auth.BearerToken(r.Header.Get("Authorization")))
	if err != nil {
		return auth.Caller{}, errUnauthorized("invalid or expired credential")
	}
	return c, nil
}

// requireHuman rejects agent keys (for user-only routes like agent-keys).
func requireHuman(c auth.Caller) *apiError {
	if c.IsAgent() {
		return errForbidden("agent keys cannot use this route")
	}
	return nil
}

// agentEffective reports whether an agent key holds at least the "agent"
// capability (admin implies agent).
func agentEffective(c auth.Caller) bool {
	return c.HasScope(auth.ScopeAgent) || c.HasScope(auth.ScopeAdmin)
}

// canAdmin: space owner (human) or admin-scoped agent key.
func canAdmin(space *store.Space, c auth.Caller) bool {
	if !c.IsAgent() && c.UserID == space.OwnerUserID {
		return true
	}
	return c.IsAgent() && c.HasScope(auth.ScopeAdmin)
}

// participantOf finds the caller's participant in a space, or 404.
func (s *Server) participantOf(spaceID string, c auth.Caller) (*store.Participant, *apiError) {
	p, err := s.store.GetParticipantByOwner(spaceID, c.UserID, c.AgentKeyID)
	if err != nil {
		return nil, errNotFound("join the space first")
	}
	// refresh from hub runtime if present (positions are authoritative there)
	if rt := s.hub.GetRuntime(p.ID); rt != nil {
		return rt, nil
	}
	return p, nil
}

// spaceOr404 loads a space or 404.
func (s *Server) spaceOr404(id string) (*store.Space, *apiError) {
	sp, err := s.store.GetSpace(id)
	if err != nil {
		return nil, errNotFound("space not found")
	}
	return sp, nil
}

// ---------- rate limiter ----------

type rateLimiter struct {
	mu      sync.Mutex
	windows map[string][]time.Time
	max     int
	window  time.Duration
}

func newRateLimiter(max int, window time.Duration) *rateLimiter {
	return &rateLimiter{windows: map[string][]time.Time{}, max: max, window: window}
}

func (r *rateLimiter) allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	cut := now.Add(-r.window)
	win := r.windows[key][:0]
	for _, t := range r.windows[key] {
		if t.After(cut) {
			win = append(win, t)
		}
	}
	if len(win) >= r.max {
		r.windows[key] = win
		return false
	}
	r.windows[key] = append(win, now)
	return true
}

// clientIP extracts the caller IP for rate limiting.
func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return strings.TrimSpace(strings.Split(fwd, ",")[0])
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	return host
}
