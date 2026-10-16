package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"github.com/azmi2409/forge-space/internal/store"
)

type connKind int

const (
	connWS connKind = iota
	connSSE
)

// Conn is one realtime attachment (WS or SSE) for a participant.
// All outbound traffic goes through sendCh, serviced by exactly one writer
// goroutine, so nhooyr's single-writer rule holds. writeMu additionally
// serializes the writer against synchronous priority writes (kick).
type Conn struct {
	kind      connKind
	pid       string
	spaceID   string
	ws        *websocket.Conn // connWS only
	w         http.ResponseWriter
	flusher   http.Flusher // connSSE only
	sendCh    chan []byte
	writeMu   sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once
}

// newConn creates a Conn and starts its writer goroutine.
func newConn(kind connKind, pid, spaceID string, ws *websocket.Conn, w http.ResponseWriter) *Conn {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Conn{
		kind: kind, pid: pid, spaceID: spaceID,
		ws: ws, w: w,
		sendCh: make(chan []byte, 128),
		ctx:    ctx, cancel: cancel,
	}
	if w != nil {
		if f, ok := w.(http.Flusher); ok {
			c.flusher = f
		}
	}
	go c.writer()
	return c
}

func (c *Conn) writer() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case msg, ok := <-c.sendCh:
			if !ok {
				return
			}
			if c.kind == connWS {
				wctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
				c.writeMu.Lock()
				err := c.ws.Write(wctx, websocket.MessageText, msg)
				c.writeMu.Unlock()
				cancel()
				if err != nil {
					return
				}
			} else {
				if c.flusher == nil {
					return
				}
				if _, err := c.w.Write(append(msg, '\n')); err != nil {
					return
				}
				c.flusher.Flush()
			}
		}
	}
}

// shutdown closes the conn (idempotent).
func (c *Conn) shutdown() {
	c.closeOnce.Do(func() {
		c.cancel()
		if c.kind == connWS && c.ws != nil {
			_ = c.ws.Close(websocket.StatusNormalClosure, "bye")
		}
	})
}

// sendTo enqueues a message; drops + closes the conn on a full buffer
// (slow consumer protection).
func (h *Hub) sendTo(c *Conn, msg []byte) {
	if c == nil {
		return
	}
	select {
	case c.sendCh <- msg:
	default:
		go h.dropConn(c.pid)
	}
}

func (h *Hub) dropConn(pid string) {
	h.mu.Lock()
	c := h.conns[pid]
	delete(h.conns, pid)
	h.mu.Unlock()
	if c != nil {
		c.shutdown()
	}
	h.LeaveParticipant(pid)
}

// detachConn removes and closes a socket without removing the participant.
func (h *Hub) detachConn(pid string) {
	h.mu.Lock()
	c := h.conns[pid]
	delete(h.conns, pid)
	h.mu.Unlock()
	if c != nil {
		c.shutdown()
	}
}

func (h *Hub) connFor(pid string) *Conn {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.conns[pid]
}

func (h *Hub) connsForSpaceLocked(spaceID string) []*Conn {
	var out []*Conn
	for _, c := range h.conns {
		if c.spaceID == spaceID {
			out = append(out, c)
		}
	}
	return out
}

// ---------- tickets ----------

// RedeemTicket validates a presented ticket value, atomically marks it used,
// and returns the participant. Single-use, 60s TTL (CONTRACT.md §1).
func (h *Hub) RedeemTicket(ticket string) (*store.Participant, error) {
	t, err := h.store.GetTicketByHash(sha256hex(ticket))
	if err != nil {
		return nil, errors.New("invalid ticket")
	}
	exp, err := time.Parse(time.RFC3339, t.ExpiresAt)
	if err != nil || time.Now().UTC().After(exp) {
		return nil, errors.New("ticket expired")
	}
	ok, err := h.store.ClaimTicket(t.ID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errors.New("ticket already used")
	}
	p, err := h.store.GetParticipant(t.ParticipantID)
	if err != nil {
		return nil, errors.New("participant gone")
	}
	return p, nil
}

// ---------- hello ----------

// buildHello assembles the WS hello payload for a participant.
func (h *Hub) buildHello(pid string) ([]byte, error) {
	h.mu.Lock()
	prt, _ := h.findPartLocked(pid)
	if prt == nil {
		h.mu.Unlock()
		return nil, errors.New("unknown participant")
	}
	p := prt.p
	rt := h.spaceOfLocked(pid)
	mrt := rt.maps[p.MapID]
	parts := make([]store.Participant, 0, len(mrt.parts))
	for _, q := range mrt.parts {
		parts = append(parts, q.p)
	}
	areas := make([]store.Area, 0, len(mrt.areas))
	for _, a := range mrt.areas {
		if full, err := h.store.GetArea(a.ID); err == nil {
			areas = append(areas, *full)
		}
	}
	h.mu.Unlock()

	m, err := h.store.GetMap(p.MapID)
	if err != nil {
		return nil, err
	}
	return mustJSON(map[string]any{
		"t":            "hello",
		"you":          pid,
		"participants": parts,
		"areas":        areas,
		"objects":      mrt.objects,
		"map":          m,
		"now":          time.Now().UnixMilli(),
	}), nil
}

// ---------- WebSocket ----------

// insecureWS controls the WS origin check; set from config (dev: allow all).
var insecureWS = true

// ServeWS handles GET /api/v1/spaces/{id}/ws?ticket=.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request, spaceID string) {
	ticket := r.URL.Query().Get("ticket")
	if ticket == "" {
		http.Error(w, `{"error":{"code":"unauthorized","message":"ticket required"}}`, http.StatusUnauthorized)
		return
	}
	p, err := h.RedeemTicket(ticket)
	if err != nil {
		http.Error(w, `{"error":{"code":"unauthorized","message":"`+err.Error()+`"}}`, http.StatusUnauthorized)
		return
	}
	if p.SpaceID != spaceID {
		http.Error(w, `{"error":{"code":"forbidden","message":"ticket is for another space"}}`, http.StatusForbidden)
		return
	}

	opts := &websocket.AcceptOptions{}
	if insecureWS {
		opts.InsecureSkipVerify = true
	}
	ws, err := websocket.Accept(w, r, opts)
	if err != nil {
		return
	}

	h.mu.Lock()
	if _, exists := h.conns[p.ID]; exists {
		h.mu.Unlock()
		_ = ws.Close(websocket.StatusPolicyViolation, "already connected")
		return
	}
	c := newConn(connWS, p.ID, p.SpaceID, ws, nil)
	h.conns[p.ID] = c
	h.mu.Unlock()

	// The participant is already in the hub runtime (registered at join);
	// this only attaches the socket.
	hello, err := h.buildHello(p.ID)
	if err != nil {
		h.detachConn(p.ID) // close socket only; participant stays joined
		return
	}
	h.sendTo(c, hello)

	// Reader loop. WS close → leave (CONTRACT.md §3).
	defer h.LeaveParticipant(p.ID)
	for {
		typ, data, err := ws.Read(r.Context())
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		h.handleClientMessage(p.ID, data)
	}
}

// handleClientMessage processes one client→server JSON message.
// Malformed input → {"t":"error",...}, never a disconnect (CONTRACT.md §4).
func (h *Hub) handleClientMessage(pid string, data []byte) {
	var base struct {
		T string `json:"t"`
	}
	if err := json.Unmarshal(data, &base); err != nil {
		h.sendError(pid, "bad_request", "malformed JSON")
		return
	}
	c := h.connFor(pid)
	if c == nil {
		return
	}
	switch base.T {
	case "input":
		var m struct {
			Seq int     `json:"seq"`
			Dx  float64 `json:"dx"`
			Dy  float64 `json:"dy"`
		}
		if err := json.Unmarshal(data, &m); err != nil {
			h.sendError(pid, "bad_request", "bad input message")
			return
		}
		if m.Dx < -1 || m.Dx > 1 || m.Dy < -1 || m.Dy > 1 {
			h.sendError(pid, "bad_request", "dx/dy must be in [-1,1]")
			return
		}
		h.SetInput(pid, m.Dx, m.Dy, m.Seq)
	case "chat":
		var m struct {
			Scope string `json:"scope"`
			To    string `json:"to"`
			Text  string `json:"text"`
		}
		if err := json.Unmarshal(data, &m); err != nil {
			h.sendError(pid, "bad_request", "bad chat message")
			return
		}
		h.chatFromSocket(pid, m.Scope, m.To, m.Text, c)
	case "interact":
		var m struct {
			ObjectID string `json:"object_id"`
		}
		if err := json.Unmarshal(data, &m); err != nil || m.ObjectID == "" {
			h.sendError(pid, "bad_request", "bad interact message")
			return
		}
		obj, err := h.InteractObject(pid, m.ObjectID)
		if err != nil {
			h.sendError(pid, "forbidden", err.Error())
			return
		}
		h.sendTo(c, mustJSON(map[string]any{"t": "object", "object": obj}))
	case "state":
		// Speaking flag (and friends) relayed from client per CONTRACT.md §5.
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			h.sendError(pid, "bad_request", "bad state message")
			return
		}
		patch := map[string]any{}
		for _, k := range []string{"speaking", "muted", "camera_off", "ghost", "emote", "status"} {
			if v, ok := m[k]; ok {
				patch[k] = v
			}
		}
		if _, err := h.UpdateState(pid, patch); err != nil {
			h.sendError(pid, "bad_request", err.Error())
		}
	case "ping":
		var m struct {
			At int64 `json:"at"`
		}
		_ = json.Unmarshal(data, &m)
		h.sendTo(c, mustJSON(map[string]any{"t": "pong", "at": m.At}))
	default:
		h.sendError(pid, "bad_request", "unknown message type "+base.T)
	}
}

func (h *Hub) sendError(pid, code, message string) {
	if c := h.connFor(pid); c != nil {
		h.sendTo(c, mustJSON(map[string]any{"t": "error", "code": code, "message": message}))
	}
}

// chatFromSocket runs the shared chat pipeline for WS-sent messages.
func (h *Hub) chatFromSocket(pid, scope, to, text string, c *Conn) {
	p := h.GetRuntime(pid)
	if p == nil {
		h.sendError(pid, "bad_request", "no participant")
		return
	}
	if scope != "nearby" && scope != "space" && scope != "dm" {
		h.sendError(pid, "bad_request", "bad scope")
		return
	}
	if len(text) < 1 || len(text) > 500 {
		h.sendError(pid, "bad_request", "text must be 1..500 chars")
		return
	}
	if !h.CheckChatRate(pid) {
		h.sendError(pid, "rate_limited", "chat rate limit: 5 per 5s")
		return
	}
	if _, err := h.SendChat(p, scope, to, text, false); err != nil {
		h.sendError(pid, "bad_request", err.Error())
	}
}

// ---------- SSE ----------

// ServeSSE handles GET /api/v1/stream?ticket= (CONTRACT.md §7).
// Every line is a JSON object with a "t" discriminator; keepalive lines are
// {"t":"heartbeat"} (parseable, per integration ruling — not an SSE comment).
func (h *Hub) ServeSSE(w http.ResponseWriter, r *http.Request) {
	ticket := r.URL.Query().Get("ticket")
	if ticket == "" {
		http.Error(w, `{"error":{"code":"unauthorized","message":"ticket required"}}`, http.StatusUnauthorized)
		return
	}
	p, err := h.RedeemTicket(ticket)
	if err != nil {
		http.Error(w, `{"error":{"code":"unauthorized","message":"`+err.Error()+`"}}`, http.StatusUnauthorized)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	if _, ok := w.(http.Flusher); !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	h.mu.Lock()
	if _, exists := h.conns[p.ID]; exists {
		h.mu.Unlock()
		http.Error(w, `{"error":{"code":"conflict","message":"already connected"}}`, http.StatusConflict)
		return
	}
	c := newConn(connSSE, p.ID, p.SpaceID, nil, w)
	h.conns[p.ID] = c
	h.mu.Unlock()

	// Participant already in the hub runtime (registered at join).
	h.sendTo(c, mustJSON(map[string]any{
		"t": "hello", "you": p.ID, "space_id": p.SpaceID, "map_id": p.MapID,
		"now": time.Now().UnixMilli(),
	}))

	// Heartbeat every 15s (CONTRACT.md §7).
	hb := time.NewTicker(15 * time.Second)
	defer hb.Stop()
	defer h.LeaveParticipant(p.ID)
	for {
		select {
		case <-r.Context().Done():
			return
		case <-c.ctx.Done():
			return
		case <-hb.C:
			h.sendTo(c, mustJSON(map[string]any{"t": "heartbeat", "now": time.Now().UnixMilli()}))
		}
	}
}

// broadcastPresence fans a join/leave out as SSE presence events.
func (h *Hub) broadcastPresence(p *store.Participant, kind string) {
	h.mu.Lock()
	conns := h.connsForSpaceLocked(p.SpaceID)
	h.mu.Unlock()
	payload := mustJSON(map[string]any{"t": "presence", "kind": kind, "participant": p})
	for _, c := range conns {
		if c.kind == connSSE {
			h.sendTo(c, payload)
		}
	}
}

// broadcastEmoteSSE fans an emote change to SSE conns in the space.
func (h *Hub) broadcastEmoteSSE(p *store.Participant) {
	h.mu.Lock()
	conns := h.connsForSpaceLocked(p.SpaceID)
	h.mu.Unlock()
	payload := mustJSON(map[string]any{"t": "emote", "participant_id": p.ID, "emote": p.Emote})
	for _, c := range conns {
		if c.kind == connSSE {
			h.sendTo(c, payload)
		}
	}
}

// ObjectChanged announces an object create/update/delete on a map to SSE
// conns whose participant is on that map.
func (h *Hub) ObjectChanged(spaceID, mapID, objectID string) {
	h.mu.Lock()
	var targets []*Conn
	for _, c := range h.conns {
		if c.kind == connSSE && c.spaceID == spaceID {
			if prt, _ := h.findPartLocked(c.pid); prt != nil && prt.p.MapID == mapID {
				targets = append(targets, c)
			}
		}
	}
	h.mu.Unlock()
	payload := mustJSON(map[string]any{"t": "object_changed", "object_id": objectID})
	for _, c := range targets {
		h.sendTo(c, payload)
	}
}

// flushPositions persists dirty participant positions every 5s.
func (h *Hub) flushPositions() {
	h.mu.Lock()
	dirty := make([]store.Participant, 0, len(h.dirty))
	for pid := range h.dirty {
		if prt, _ := h.findPartLocked(pid); prt != nil {
			dirty = append(dirty, prt.p)
		}
	}
	h.mu.Unlock()
	now := store.Now()
	for _, p := range dirty {
		_ = h.store.UpdateParticipantPosition(p.ID, p.X, p.Y, p.Dir, p.AreaID, now)
	}
	h.mu.Lock()
	for _, p := range dirty {
		delete(h.dirty, p.ID)
	}
	h.mu.Unlock()
}

// SetInsecureWS configures the WS origin check (dev default: allow all).
func SetInsecureWS(v bool) { insecureWS = v }
