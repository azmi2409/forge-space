package realtime

import (
	"errors"

	"github.com/azmi2409/forge-space/internal/auth"
	"github.com/azmi2409/forge-space/internal/store"
)

// Whiteboard v1 (WHITEBOARD_V1.md): server-side stroke persistence and fanout.
// Called from both the WS handlers (serve.go) and the REST handlers (api).

// boardError carries a WS/REST error code alongside the message.
type boardError struct {
	code string
	msg  string
}

func (e *boardError) Error() string { return e.msg }

func boardBadRequest(msg string) *boardError { return &boardError{"bad_request", msg} }
func boardNotFound(msg string) *boardError   { return &boardError{"not_found", msg} }
func boardForbidden(msg string) *boardError  { return &boardError{"forbidden", msg} }

// BoardErrCode maps a whiteboard error to its wire error code.
func BoardErrCode(err error) string {
	var be *boardError
	if errors.As(err, &be) {
		return be.code
	}
	return "bad_request"
}

// whiteboardObject resolves objectID to a whiteboard object in the
// participant's space. Returns the object.
func (h *Hub) whiteboardObject(pid, objectID string) (*store.SpaceObject, error) {
	o, err := h.store.GetObject(objectID)
	if err != nil || o.Kind != "whiteboard" {
		return nil, boardNotFound("whiteboard not found")
	}
	h.mu.Lock()
	prt, _ := h.findPartLocked(pid)
	h.mu.Unlock()
	if prt == nil {
		return nil, boardBadRequest("unknown participant")
	}
	m, err := h.store.GetMap(o.MapID)
	if err != nil || m.SpaceID != prt.p.SpaceID {
		return nil, boardForbidden("object is not in your space")
	}
	return o, nil
}

// BoardStroke validates, persists and returns a stroke drawn by pid.
// Returns the stroke and the object's map ID (for fanout).
func (h *Hub) BoardStroke(pid, objectID, color string, width float64, points []store.WhiteboardPoint) (*store.WhiteboardStroke, string, error) {
	if err := store.ValidateStroke(color, width, points); err != nil {
		return nil, "", boardBadRequest(err.Error())
	}
	o, err := h.whiteboardObject(pid, objectID)
	if err != nil {
		return nil, "", err
	}
	st := &store.WhiteboardStroke{
		ID:            store.NewID(store.PrefixWhiteboardStroke),
		ObjectID:      o.ID,
		ParticipantID: pid,
		Color:         color,
		Width:         width,
		Points:        points,
		CreatedAt:     store.Now(),
	}
	if err := h.store.CreateStroke(st); err != nil {
		return nil, "", boardBadRequest(err.Error())
	}
	return st, o.MapID, nil
}

// BoardClear clears all strokes on a whiteboard. Space owner or admin-scope
// agent key only. Returns the object's map ID (for fanout).
func (h *Hub) BoardClear(pid, objectID string) (string, error) {
	o, err := h.whiteboardObject(pid, objectID)
	if err != nil {
		return "", err
	}
	h.mu.Lock()
	prt, _ := h.findPartLocked(pid)
	h.mu.Unlock()
	if prt == nil {
		return "", boardBadRequest("unknown participant")
	}
	m, err := h.store.GetMap(o.MapID)
	if err != nil {
		return "", boardNotFound("map not found")
	}
	sp, err := h.store.GetSpace(m.SpaceID)
	if err != nil {
		return "", boardNotFound("space not found")
	}
	admin := prt.p.OwnerUserID != "" && prt.p.OwnerUserID == sp.OwnerUserID
	if !admin && prt.p.OwnerAgentKeyID != "" {
		if k, err := h.store.GetAgentKey(prt.p.OwnerAgentKeyID, prt.p.OwnerUserID); err == nil {
			for _, sc := range k.Scopes {
				if sc == auth.ScopeAdmin {
					admin = true
					break
				}
			}
		}
	}
	if !admin {
		return "", boardForbidden("only the space owner (or admin agent key) can clear the board")
	}
	if err := h.store.ClearStrokes(o.ID); err != nil {
		return "", boardBadRequest(err.Error())
	}
	return o.MapID, nil
}

// boardTargets collects WS conns whose participant is on mapID.
func (h *Hub) boardTargets(mapID string) []*Conn {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []*Conn
	for _, c := range h.conns {
		if c.kind != connWS {
			continue
		}
		if prt, _ := h.findPartLocked(c.pid); prt != nil && prt.p.MapID == mapID {
			out = append(out, c)
		}
	}
	return out
}

// FanoutBoardStroke broadcasts a stroke to all WS participants on mapID.
func (h *Hub) FanoutBoardStroke(st *store.WhiteboardStroke, mapID string) {
	payload := mustJSON(map[string]any{"t": "wb_stroke", "object_id": st.ObjectID, "stroke": st})
	for _, c := range h.boardTargets(mapID) {
		h.sendTo(c, payload)
	}
}

// FanoutBoardClear broadcasts a clear to all WS participants on mapID.
func (h *Hub) FanoutBoardClear(objectID, mapID string) {
	payload := mustJSON(map[string]any{"t": "wb_clear", "object_id": objectID})
	for _, c := range h.boardTargets(mapID) {
		h.sendTo(c, payload)
	}
}
