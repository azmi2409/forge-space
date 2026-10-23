package api

import (
	"net/http"

	"github.com/azmi2409/forge-space/internal/store"
)

// ---------- whiteboard v1 (WHITEBOARD_V1.md) ----------

// whiteboardOr404 loads an object and enforces kind=whiteboard.
func (s *Server) whiteboardOr404(id string) (*store.SpaceObject, *apiError) {
	o, err := s.store.GetObject(id)
	if err != nil || o.Kind != "whiteboard" {
		return nil, errNotFound("whiteboard not found")
	}
	return o, nil
}

// GET /api/v1/objects/{id}/board → 200 {strokes:[...]} ordered by created_at.
// 404 if the object is missing or not a whiteboard.
func (s *Server) handleGetBoard(w http.ResponseWriter, r *http.Request) {
	if _, apiErr := s.caller(r); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	o, apiErr := s.whiteboardOr404(r.PathValue("id"))
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	strokes, err := s.store.ListStrokes(o.ID)
	if err != nil {
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"strokes": strokes})
}

// POST /api/v1/objects/{id}/board {color, width, points} → 201 {stroke}.
// Caller must have joined the object's space. Agent keys may draw (agent
// scope effective); admin scope also draws (it implies agent).
func (s *Server) handleBoardDraw(w http.ResponseWriter, r *http.Request) {
	c, apiErr := s.caller(r)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if c.IsAgent() && !agentEffective(c) {
		writeAPIError(w, errForbidden("drawing requires agent scope"))
		return
	}
	o, apiErr := s.whiteboardOr404(r.PathValue("id"))
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	m, err := s.store.GetMap(o.MapID)
	if err != nil {
		writeAPIError(w, errNotFound("map not found"))
		return
	}
	sp, apiErr := s.spaceOr404(m.SpaceID)
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
		Color  string                  `json:"color"`
		Width  float64                 `json:"width"`
		Points []store.WhiteboardPoint `json:"points"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		writeAPIError(w, err)
		return
	}
	if verr := store.ValidateStroke(in.Color, in.Width, in.Points); verr != nil {
		writeAPIError(w, errBadRequest(verr.Error()))
		return
	}
	st := &store.WhiteboardStroke{
		ID:            store.NewID(store.PrefixWhiteboardStroke),
		ObjectID:      o.ID,
		ParticipantID: p.ID,
		Color:         in.Color,
		Width:         in.Width,
		Points:        in.Points,
		CreatedAt:     store.Now(),
	}
	if err := s.store.CreateStroke(st); err != nil {
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	// Keep WS clients in sync: strokes drawn via REST fan out the same way.
	s.hub.FanoutBoardStroke(st, o.MapID)
	writeJSON(w, http.StatusCreated, map[string]any{"stroke": st})
}

// DELETE /api/v1/objects/{id}/board → 200 {} clears all strokes.
// Space owner or admin-scope agent key only.
func (s *Server) handleBoardClear(w http.ResponseWriter, r *http.Request) {
	c, apiErr := s.caller(r)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	o, apiErr := s.whiteboardOr404(r.PathValue("id"))
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	m, err := s.store.GetMap(o.MapID)
	if err != nil {
		writeAPIError(w, errNotFound("map not found"))
		return
	}
	sp, apiErr := s.spaceOr404(m.SpaceID)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if !canAdmin(sp, c) {
		writeAPIError(w, errForbidden("only the space owner (or admin agent key) can clear the board"))
		return
	}
	if err := s.store.ClearStrokes(o.ID); err != nil {
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	s.hub.FanoutBoardClear(o.ID, o.MapID)
	writeJSON(w, http.StatusOK, map[string]any{})
}
