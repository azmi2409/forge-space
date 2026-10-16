package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/azmi2409/forge-space/internal/auth"
	"github.com/azmi2409/forge-space/internal/livekitx"
	"github.com/azmi2409/forge-space/internal/spatial"
	"github.com/azmi2409/forge-space/internal/store"
)

// ---------- participants ----------

// GET /api/v1/spaces/{id}/participants → {participants:[...]}
func (s *Server) handleListParticipants(w http.ResponseWriter, r *http.Request) {
	if _, apiErr := s.caller(r); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	sp, apiErr := s.spaceOr404(r.PathValue("id"))
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	parts, err := s.store.ListParticipantsBySpace(sp.ID)
	if err != nil {
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	if parts == nil {
		parts = []*store.Participant{}
	}
	// refresh live positions from the hub runtime
	for i, p := range parts {
		if rt := s.hub.GetRuntime(p.ID); rt != nil {
			parts[i] = rt
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"participants": parts})
}

// POST /api/v1/spaces/{id}/join {display_name?, avatar?} →
// 200 {participant, ticket, ws_url, livekit:{url,token}|null}
func (s *Server) handleJoin(w http.ResponseWriter, r *http.Request) {
	c, apiErr := s.caller(r)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if !s.joinLimit.allow(clientIP(r)) {
		writeAPIError(w, errRateLimited("join rate limit: 10 per minute per IP"))
		return
	}
	sp, apiErr := s.spaceOr404(r.PathValue("id"))
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	// one login → one participant per space
	if _, err := s.store.GetParticipantByOwner(sp.ID, c.UserID, c.AgentKeyID); err == nil {
		writeAPIError(w, errConflict("already joined this space (leave first)"))
		return
	}

	var in struct {
		DisplayName string        `json:"display_name"`
		Avatar      *store.Avatar `json:"avatar"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		writeAPIError(w, err)
		return
	}
	displayName := strings.TrimSpace(in.DisplayName)
	if displayName == "" {
		if c.IsAgent() {
			displayName = c.KeyName
		} else if u, err := s.store.GetUserByID(c.UserID); err == nil {
			displayName = u.DisplayName
		}
	}
	if apiErr := validateDisplayName(displayName); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	avatar := store.Avatar{}
	if in.Avatar != nil {
		avatar = *in.Avatar
	}
	if apiErr := validateAvatar(avatar); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}

	maps, err := s.store.ListMapsBySpace(sp.ID)
	if err != nil || len(maps) == 0 {
		writeAPIError(w, errBadRequest("space has no maps"))
		return
	}
	m, err := s.store.GetMap(maps[0].ID)
	if err != nil {
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	sx, sy := spawnPoint(m)

	kind := "human"
	if c.IsAgent() {
		kind = "agent"
	}
	p := &store.Participant{
		ID: store.NewID(store.PrefixParticipant), SpaceID: sp.ID, MapID: m.ID,
		Kind: kind, DisplayName: displayName, Avatar: avatar,
		X: sx, Y: sy, Dir: "down",
		OwnerUserID: c.UserID, OwnerAgentKeyID: c.AgentKeyID,
		CreatedAt: store.Now(), UpdatedAt: store.Now(),
	}
	if err := s.store.CreateParticipant(p); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeAPIError(w, errConflict("already joined this space (leave first)"))
			return
		}
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}

	// single-use 60s ticket: value = ticketID + "." + secret
	ticketID := store.NewID(store.PrefixTicket)
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		_ = s.store.DeleteParticipant(p.ID)
		writeAPIError(w, errBadRequest("could not create ticket"))
		return
	}
	ticket := ticketID + "." + hex.EncodeToString(secret)
	exp := time.Now().UTC().Add(60 * time.Second).Format(time.RFC3339)
	if err := s.store.CreateTicket(ticketID, p.ID, auth.SHA256Hex(ticket), exp, store.Now()); err != nil {
		_ = s.store.DeleteParticipant(p.ID)
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}

	// The participant exists in the world immediately (REST-only agents can
	// act without ever opening a socket); WS/SSE connect only attaches.
	if err := s.hub.JoinParticipant(p); err != nil {
		_ = s.store.DeleteParticipant(p.ID)
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}

	resp := map[string]any{
		"participant": p,
		"ticket":      ticket,
		"ws_url":      s.wsURL(r, sp.ID, ticket),
		"livekit":     nil,
	}
	if s.lk.Enabled() {
		token, err := s.lk.MintToken(p.ID, p.DisplayName, livekitx.RoomName(sp.ID, m.ID))
		if err != nil {
			writeAPIError(w, errBadRequest("livekit token failed: "+err.Error()))
			return
		}
		resp["livekit"] = map[string]string{"url": s.lk.URL, "token": token}
	}
	writeJSON(w, http.StatusOK, resp)
}

func validateAvatar(a store.Avatar) *apiError {
	if a.Skin < 0 || a.Skin > 4 || a.Hair < 0 || a.Hair > 5 ||
		a.HairColor < 0 || a.HairColor > 5 || a.Top < 0 || a.Top > 7 {
		return errBadRequest("avatar fields out of range")
	}
	return nil
}

// spawnPoint returns the map spawn, or a random nearby floor tile if blocked.
func spawnPoint(m *store.Map) (float64, float64) {
	sm := &spatial.Map{Width: m.Width, Height: m.Height, Tiles: m.Tiles}
	if !spatial.Collides(sm, nil, m.Spawn.X, m.Spawn.Y) {
		return m.Spawn.X, m.Spawn.Y
	}
	for r := 1; r <= 5; r++ {
		for dy := -r; dy <= r; dy++ {
			for dx := -r; dx <= r; dx++ {
				x, y := m.Spawn.X+float64(dx), m.Spawn.Y+float64(dy)
				if !spatial.Collides(sm, nil, x, y) {
					return x, y
				}
			}
		}
	}
	return m.Spawn.X, m.Spawn.Y
}

func (s *Server) wsURL(r *http.Request, spaceID, ticket string) string {
	scheme := "ws"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "wss"
	}
	return scheme + "://" + r.Host + "/api/v1/spaces/" + spaceID + "/ws?ticket=" + ticket
}

// POST /api/v1/spaces/{id}/leave (auth) → 200 {}
func (s *Server) handleLeave(w http.ResponseWriter, r *http.Request) {
	c, apiErr := s.caller(r)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	sp, apiErr := s.spaceOr404(r.PathValue("id"))
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	p, apiErr := s.participantOf(sp.ID, c)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	s.hub.LeaveParticipant(p.ID)
	writeJSON(w, http.StatusOK, map[string]any{})
}

// DELETE /api/v1/spaces/{id}/participants/{pid} (owner or admin agent key)
// → 200 {}. The kicked socket receives {"t":"event","kind":"kick"} then closes.
func (s *Server) handleKick(w http.ResponseWriter, r *http.Request) {
	c, apiErr := s.caller(r)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	sp, apiErr := s.spaceOr404(r.PathValue("id"))
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if !canAdmin(sp, c) {
		writeAPIError(w, errForbidden("only the space owner (or admin agent key) can kick"))
		return
	}
	pid := r.PathValue("pid")
	p, err := s.store.GetParticipant(pid)
	if err != nil || p.SpaceID != sp.ID {
		writeAPIError(w, errNotFound("participant not found"))
		return
	}
	s.hub.KickParticipant(pid)
	writeJSON(w, http.StatusOK, map[string]any{})
}

// ---------- chat ----------

// POST /api/v1/spaces/{id}/chat {scope, to?, text, announce?} → 201 {message}
func (s *Server) handleSendChat(w http.ResponseWriter, r *http.Request) {
	c, apiErr := s.caller(r)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	sp, apiErr := s.spaceOr404(r.PathValue("id"))
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	p, apiErr := s.participantOf(sp.ID, c)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	var in struct {
		Scope    string `json:"scope"`
		To       string `json:"to"`
		Text     string `json:"text"`
		Announce bool   `json:"announce"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		writeAPIError(w, err)
		return
	}
	if in.Scope != "nearby" && in.Scope != "space" && in.Scope != "dm" {
		writeAPIError(w, errBadRequest(`scope must be "nearby", "space" or "dm"`))
		return
	}
	// agent scope may only send nearby/dm (admin scope adds the rest)
	if c.IsAgent() && !c.HasScope(auth.ScopeAdmin) && in.Scope == "space" {
		writeAPIError(w, errForbidden("agent scope may only send nearby/dm chat"))
		return
	}
	if in.Announce && !canAdmin(sp, c) {
		writeAPIError(w, errForbidden("announcements are owner/admin only"))
		return
	}
	if in.Scope == "dm" && in.To == "" {
		writeAPIError(w, errBadRequest(`dm requires "to"`))
		return
	}
	if len(in.Text) < 1 || len(in.Text) > 500 {
		writeAPIError(w, errBadRequest("text must be 1..500 chars"))
		return
	}
	if !s.hub.CheckChatRate(p.ID) {
		writeAPIError(w, errRateLimited("chat rate limit: 5 per 5s"))
		return
	}
	msg, err := s.hub.SendChat(p, in.Scope, in.To, in.Text, in.Announce)
	if err != nil {
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"message": msg})
}

// GET /api/v1/spaces/{id}/chat?scope=&limit= → {messages:[...]} visible to caller
func (s *Server) handleListChat(w http.ResponseWriter, r *http.Request) {
	c, apiErr := s.caller(r)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	sp, apiErr := s.spaceOr404(r.PathValue("id"))
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	p, apiErr := s.participantOf(sp.ID, c)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	limit := 50
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := parsePositiveInt(q); err == nil {
			limit = n
		}
	}
	if limit > 200 {
		limit = 200
	}
	scopeFilter := r.URL.Query().Get("scope")
	msgs, err := s.store.ListChatMessages(sp.ID, limit)
	if err != nil {
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	visible := []*store.ChatMessage{}
	for _, m := range msgs {
		if scopeFilter != "" && m.Scope != scopeFilter {
			continue
		}
		if !chatVisibleTo(m, p.ID) {
			continue
		}
		visible = append(visible, m)
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": visible})
}

func chatVisibleTo(m *store.ChatMessage, pid string) bool {
	switch m.Scope {
	case "space":
		return true
	case "dm":
		return m.FromParticipantID == pid || m.ToParticipantID == pid
	case "nearby":
		if m.FromParticipantID == pid {
			return true
		}
		for _, id := range m.Recipients {
			if id == pid {
				return true
			}
		}
		return false
	}
	return false
}

func parsePositiveInt(q string) (int, error) {
	n := 0
	for _, r := range q {
		if r < '0' || r > '9' {
			return 0, errBadRequest("bad int")
		}
		n = n*10 + int(r-'0')
	}
	if n <= 0 {
		return 0, errBadRequest("bad int")
	}
	return n, nil
}

// ---------- me/* ----------

// meParticipant resolves the caller's most recently active participant.
func (s *Server) meParticipant(c auth.Caller) (*store.Participant, *apiError) {
	parts, err := s.store.ListParticipantsByOwner(c.UserID, c.AgentKeyID)
	if err != nil || len(parts) == 0 {
		return nil, errNotFound("join a space first")
	}
	p := parts[0]
	if rt := s.hub.GetRuntime(p.ID); rt != nil {
		return rt, nil
	}
	return p, nil
}

// POST /api/v1/participants/me/move {x?,y?,dx?,dy?} → 200 {participant}
func (s *Server) handleMeMove(w http.ResponseWriter, r *http.Request) {
	c, apiErr := s.caller(r)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	p, apiErr := s.meParticipant(c)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	var in struct {
		X  *float64 `json:"x"`
		Y  *float64 `json:"y"`
		Dx *float64 `json:"dx"`
		Dy *float64 `json:"dy"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		writeAPIError(w, err)
		return
	}
	hasTarget := in.X != nil || in.Y != nil
	hasDelta := in.Dx != nil || in.Dy != nil
	if hasTarget == hasDelta {
		writeAPIError(w, errBadRequest("provide either x/y or dx/dy"))
		return
	}
	if hasDelta {
		dx, dy := 0.0, 0.0
		if in.Dx != nil {
			dx = *in.Dx
		}
		if in.Dy != nil {
			dy = *in.Dy
		}
		if math.Abs(dx) > 40 || math.Abs(dy) > 40 {
			writeAPIError(w, errBadRequest("dx/dy too large"))
			return
		}
		np, err := s.hub.NudgeMove(p.ID, dx, dy)
		if err != nil {
			writeAPIError(w, errBadRequest(err.Error()))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"participant": np})
		return
	}
	// absolute target: BFS reachability check (≤40 tiles), then path-follow
	tx, ty := p.X, p.Y
	if in.X != nil {
		tx = *in.X
	}
	if in.Y != nil {
		ty = *in.Y
	}
	if err := s.hub.PlanMove(p.ID, tx, ty); err != nil {
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	np := s.hub.GetRuntime(p.ID)
	if np == nil {
		np = p
	}
	writeJSON(w, http.StatusOK, map[string]any{"participant": np})
}

// POST /api/v1/participants/me/portal {object_id} → 200 {participant}
func (s *Server) handleMePortal(w http.ResponseWriter, r *http.Request) {
	c, apiErr := s.caller(r)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	p, apiErr := s.meParticipant(c)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	var in struct {
		ObjectID string `json:"object_id"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		writeAPIError(w, err)
		return
	}
	obj, err := s.hub.InteractObject(p.ID, in.ObjectID)
	if err != nil {
		writeAPIError(w, errForbidden(err.Error()))
		return
	}
	if obj.Kind != "portal" {
		writeAPIError(w, errBadRequest("object is not a portal"))
		return
	}
	if obj.TargetMap == "" {
		writeAPIError(w, errBadRequest("portal has no target"))
		return
	}
	tm, err := s.store.GetMap(obj.TargetMap)
	if err != nil || tm.SpaceID != p.SpaceID {
		writeAPIError(w, errBadRequest("portal target map not found"))
		return
	}
	np, err := s.hub.Teleport(p.ID, tm.ID, obj.TargetX, obj.TargetY)
	if err != nil {
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	// keep the DB row in sync (hub flushes positions periodically, but map
	// changes should persist immediately)
	np.UpdatedAt = store.Now()
	_ = s.store.UpdateParticipant(np)
	writeJSON(w, http.StatusOK, map[string]any{"participant": np})
}

// POST /api/v1/participants/me/state {emote?,ghost?,muted?,camera_off?,status?,speaking?}
func (s *Server) handleMeState(w http.ResponseWriter, r *http.Request) {
	c, apiErr := s.caller(r)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	p, apiErr := s.meParticipant(c)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	var raw map[string]any
	dec := json.NewDecoder(r.Body)
	defer r.Body.Close()
	if err := dec.Decode(&raw); err != nil {
		writeAPIError(w, errBadRequest("invalid JSON body"))
		return
	}
	patch := map[string]any{}
	for _, k := range []string{"emote", "ghost", "muted", "camera_off", "status", "speaking", "dir"} {
		if v, ok := raw[k]; ok {
			patch[k] = v
		}
	}
	if em, ok := patch["emote"].(string); ok && len(em) > 20 {
		writeAPIError(w, errBadRequest("emote too long"))
		return
	}
	if st, ok := patch["status"].(string); ok && len(st) > 140 {
		writeAPIError(w, errBadRequest("status ≤140 chars"))
		return
	}
	np, err := s.hub.UpdateState(p.ID, patch)
	if err != nil {
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	np.UpdatedAt = store.Now()
	_ = s.store.UpdateParticipant(np)
	writeJSON(w, http.StatusOK, map[string]any{"participant": np})
}

// GET /api/v1/media/token → {url, token} (401 if media disabled)
func (s *Server) handleMediaToken(w http.ResponseWriter, r *http.Request) {
	c, apiErr := s.caller(r)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if !s.lk.Enabled() {
		writeAPIError(w, errUnauthorized("voice/video not configured on this server"))
		return
	}
	p, apiErr := s.meParticipant(c)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	token, err := s.lk.MintToken(p.ID, p.DisplayName, livekitx.RoomName(p.SpaceID, p.MapID))
	if err != nil {
		writeAPIError(w, errBadRequest("livekit token failed: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": s.lk.URL, "token": token})
}

// GET /api/v1/health → {ok:true, media:bool}
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "media": s.lk.Enabled()})
}
