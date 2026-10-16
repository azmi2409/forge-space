package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"unicode"

	"github.com/azmi2409/forge-space/internal/auth"
	"github.com/azmi2409/forge-space/internal/spatial"
	"github.com/azmi2409/forge-space/internal/store"
)

// ---------- spaces ----------

// GET /api/v1/spaces → {spaces:[...]} (public list for MVP)
func (s *Server) handleListSpaces(w http.ResponseWriter, r *http.Request) {
	spaces, err := s.store.ListSpaces()
	if err != nil {
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	if spaces == nil {
		spaces = []*store.Space{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"spaces": spaces})
}

// POST /api/v1/spaces (user, or admin agent key) {name} → 201 {space}
// Also creates the default map "Lobby" 40x30 (floor + border walls, spawn center).
func (s *Server) handleCreateSpace(w http.ResponseWriter, r *http.Request) {
	c, apiErr := s.caller(r)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if c.IsAgent() && !c.HasScope(auth.ScopeAdmin) {
		writeAPIError(w, errForbidden("agent scope cannot create spaces"))
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		writeAPIError(w, err)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 80 {
		writeAPIError(w, errBadRequest("name must be 1..80 chars"))
		return
	}
	sp := &store.Space{
		ID:          store.NewID(store.PrefixSpace),
		Name:        in.Name,
		Slug:        s.uniqueSlug(in.Name),
		OwnerUserID: c.UserID, // for admin agent keys: the key owner's user id
		Settings:    store.SpaceSettings{ProximityRadius: 5},
		CreatedAt:   store.Now(),
	}
	if err := s.store.CreateSpace(sp); err != nil {
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	if err := s.createDefaultMap(sp.ID); err != nil {
		writeAPIError(w, errBadRequest("space created but default map failed: "+err.Error()))
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"space": sp})
}

func (s *Server) uniqueSlug(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		case r == ' ' || r == '-' || r == '_':
			b.WriteRune('-')
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "space"
	}
	if len(slug) > 50 {
		slug = slug[:50]
	}
	candidate := slug
	for i := 0; ; i++ {
		if _, err := s.store.GetSpaceBySlug(candidate); err == store.ErrNotFound {
			return candidate
		} else if err != nil {
			return candidate // DB trouble; let the insert surface it
		}
		candidate = slug + "-" + store.NewID("")[3:7]
		if i > 10 {
			return candidate
		}
	}
}

// createDefaultMap builds the 40x30 Lobby: floor + border walls, spawn center.
func (s *Server) createDefaultMap(spaceID string) error {
	const w, h = 40, 30
	tiles := make([]int, w*h)
	for i := range tiles {
		tiles[i] = spatial.TileFloor
	}
	for x := 0; x < w; x++ {
		tiles[x] = spatial.TileWall
		tiles[(h-1)*w+x] = spatial.TileWall
	}
	for y := 0; y < h; y++ {
		tiles[y*w] = spatial.TileWall
		tiles[y*w+w-1] = spatial.TileWall
	}
	sx, sy := w/2, h/2
	tiles[sy*w+sx] = spatial.TileSpawn
	m := &store.Map{
		ID: store.NewID(store.PrefixMap), SpaceID: spaceID, Name: "Lobby",
		Width: w, Height: h, Tiles: tiles,
		Spawn:     store.Spawn{X: float64(sx), Y: float64(sy)},
		CreatedAt: store.Now(),
	}
	return s.store.CreateMap(m)
}

// GET /api/v1/spaces/{id} → {space, maps:[summaries]}
func (s *Server) handleGetSpace(w http.ResponseWriter, r *http.Request) {
	c, apiErr := s.caller(r)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	_ = c
	sp, apiErr := s.spaceOr404(r.PathValue("id"))
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	maps, err := s.store.ListMapsBySpace(sp.ID)
	if err != nil {
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	summaries := make([]store.MapSummary, 0, len(maps))
	for _, m := range maps {
		summaries = append(summaries, m.Summary())
	}
	writeJSON(w, http.StatusOK, map[string]any{"space": sp, "maps": summaries})
}

// PATCH /api/v1/spaces/{id} (owner) {name?, settings?}
func (s *Server) handlePatchSpace(w http.ResponseWriter, r *http.Request) {
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
		writeAPIError(w, errForbidden("only the space owner can edit it"))
		return
	}
	var in struct {
		Name     *string              `json:"name"`
		Settings *store.SpaceSettings `json:"settings"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		writeAPIError(w, err)
		return
	}
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" || len(n) > 80 {
			writeAPIError(w, errBadRequest("name must be 1..80 chars"))
			return
		}
		sp.Name = n
	}
	if in.Settings != nil {
		if in.Settings.ProximityRadius < 1 || in.Settings.ProximityRadius > 50 {
			writeAPIError(w, errBadRequest("proximity_radius must be 1..50"))
			return
		}
		sp.Settings = *in.Settings
	}
	if err := s.store.UpdateSpace(sp); err != nil {
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	s.hub.RefreshSpace(sp.ID)
	writeJSON(w, http.StatusOK, map[string]any{"space": sp})
}

// DELETE /api/v1/spaces/{id} (owner)
func (s *Server) handleDeleteSpace(w http.ResponseWriter, r *http.Request) {
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
		writeAPIError(w, errForbidden("only the space owner can delete it"))
		return
	}
	// drop runtime participants first so sockets close cleanly
	if parts, err := s.store.ListParticipantsBySpace(sp.ID); err == nil {
		for _, p := range parts {
			s.hub.LeaveParticipant(p.ID)
		}
	}
	if err := s.store.DeleteSpace(sp.ID); err != nil {
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	s.hub.RefreshSpace(sp.ID)
	writeJSON(w, http.StatusOK, map[string]any{})
}

// ---------- maps ----------

// POST /api/v1/spaces/{id}/maps (owner) {name,width,height} → 201 {map}
func (s *Server) handleCreateMap(w http.ResponseWriter, r *http.Request) {
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
		writeAPIError(w, errForbidden("only the space owner can create maps"))
		return
	}
	var in struct {
		Name   string `json:"name"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		writeAPIError(w, err)
		return
	}
	if apiErr := validateMapDims(in.Width, in.Height); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 80 {
		writeAPIError(w, errBadRequest("name must be 1..80 chars"))
		return
	}
	m := &store.Map{
		ID: store.NewID(store.PrefixMap), SpaceID: sp.ID, Name: in.Name,
		Width: in.Width, Height: in.Height,
		Tiles:     blankMap(in.Width, in.Height),
		Spawn:     store.Spawn{X: float64(in.Width) / 2, Y: float64(in.Height) / 2},
		CreatedAt: store.Now(),
	}
	if err := s.store.CreateMap(m); err != nil {
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	s.hub.RefreshSpace(sp.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"map": m})
}

func blankMap(w, h int) []int {
	tiles := make([]int, w*h)
	for i := range tiles {
		tiles[i] = spatial.TileFloor
	}
	for x := 0; x < w; x++ {
		tiles[x] = spatial.TileWall
		tiles[(h-1)*w+x] = spatial.TileWall
	}
	for y := 0; y < h; y++ {
		tiles[y*w] = spatial.TileWall
		tiles[y*w+w-1] = spatial.TileWall
	}
	return tiles
}

func validateMapDims(w, h int) *apiError {
	if w < 5 || h < 5 || w > 200 || h > 200 {
		return errBadRequest("map dimensions must be 5..200 x 5..200")
	}
	return nil
}

// GET /api/v1/spaces/{id}/maps/{mapID} → {map} (full tiles)
func (s *Server) handleGetMap(w http.ResponseWriter, r *http.Request) {
	if _, apiErr := s.caller(r); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	sp, apiErr := s.spaceOr404(r.PathValue("id"))
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	m, err := s.store.GetMap(r.PathValue("mapID"))
	if err != nil || m.SpaceID != sp.ID {
		writeAPIError(w, errNotFound("map not found"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"map": m})
}

// PUT /api/v1/spaces/{id}/maps/{mapID} (owner) {name?, tiles?, width?, height?, spawn?}
func (s *Server) handlePutMap(w http.ResponseWriter, r *http.Request) {
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
		writeAPIError(w, errForbidden("only the space owner can edit maps"))
		return
	}
	m, err := s.store.GetMap(r.PathValue("mapID"))
	if err != nil || m.SpaceID != sp.ID {
		writeAPIError(w, errNotFound("map not found"))
		return
	}
	var in struct {
		Name   *string      `json:"name"`
		Tiles  []int        `json:"tiles"`
		Width  *int         `json:"width"`
		Height *int         `json:"height"`
		Spawn  *store.Spawn `json:"spawn"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		writeAPIError(w, err)
		return
	}
	width, height := m.Width, m.Height
	if in.Width != nil {
		width = *in.Width
	}
	if in.Height != nil {
		height = *in.Height
	}
	if apiErr := validateMapDims(width, height); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" || len(n) > 80 {
			writeAPIError(w, errBadRequest("name must be 1..80 chars"))
			return
		}
		m.Name = n
	}
	if in.Tiles != nil {
		if len(in.Tiles) != width*height {
			writeAPIError(w, errBadRequest("tiles length must equal width*height"))
			return
		}
		for _, t := range in.Tiles {
			if t < 0 || t > 5 {
				writeAPIError(w, errBadRequest("tile values must be 0..5"))
				return
			}
		}
		m.Tiles = in.Tiles
		m.Width, m.Height = width, height
	} else if width != m.Width || height != m.Height {
		writeAPIError(w, errBadRequest("resizing requires new tiles"))
		return
	}
	if in.Spawn != nil {
		if in.Spawn.X < 0 || in.Spawn.Y < 0 || in.Spawn.X >= float64(m.Width) || in.Spawn.Y >= float64(m.Height) {
			writeAPIError(w, errBadRequest("spawn out of map bounds"))
			return
		}
		m.Spawn = *in.Spawn
	}
	if err := s.store.UpdateMap(m); err != nil {
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	s.hub.RefreshSpace(sp.ID)
	writeJSON(w, http.StatusOK, map[string]any{"map": m})
}

// ---------- areas ----------

// GET /api/v1/spaces/{id}/areas → {areas:[...]} (all maps in the space)
func (s *Server) handleListAreas(w http.ResponseWriter, r *http.Request) {
	if _, apiErr := s.caller(r); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	sp, apiErr := s.spaceOr404(r.PathValue("id"))
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	maps, err := s.store.ListMapsBySpace(sp.ID)
	if err != nil {
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	areas := []*store.Area{}
	for _, m := range maps {
		as, err := s.store.ListAreasByMap(m.ID)
		if err != nil {
			writeAPIError(w, errBadRequest(err.Error()))
			return
		}
		areas = append(areas, as...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"areas": areas})
}

// POST /api/v1/spaces/{id}/areas (owner) {map_id, kind, name, x,y,w,h}
func (s *Server) handleCreateArea(w http.ResponseWriter, r *http.Request) {
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
		writeAPIError(w, errForbidden("only the space owner can create areas"))
		return
	}
	var in struct {
		MapID string `json:"map_id"`
		Kind  string `json:"kind"`
		Name  string `json:"name"`
		X     int    `json:"x"`
		Y     int    `json:"y"`
		W     int    `json:"w"`
		H     int    `json:"h"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		writeAPIError(w, err)
		return
	}
	m, err := s.store.GetMap(in.MapID)
	if err != nil || m.SpaceID != sp.ID {
		writeAPIError(w, errBadRequest("map not found in space"))
		return
	}
	if in.Kind != "private" && in.Kind != "spotlight" {
		writeAPIError(w, errBadRequest(`kind must be "private" or "spotlight"`))
		return
	}
	if apiErr := validateAreaRect(in.X, in.Y, in.W, in.H, m.Width, m.Height); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 60 {
		writeAPIError(w, errBadRequest("name must be 1..60 chars"))
		return
	}
	a := &store.Area{
		ID: store.NewID(store.PrefixArea), MapID: m.ID, Kind: in.Kind, Name: in.Name,
		X: in.X, Y: in.Y, W: in.W, H: in.H, CreatedAt: store.Now(),
	}
	if err := s.store.CreateArea(a); err != nil {
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	s.hub.RefreshSpace(sp.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"area": a})
}

func validateAreaRect(x, y, w, h, mw, mh int) *apiError {
	if w < 1 || h < 1 || x < 0 || y < 0 || x+w > mw || y+h > mh {
		return errBadRequest("area rect must lie inside the map")
	}
	return nil
}

// DELETE /api/v1/areas/{id} (owner)
func (s *Server) handleDeleteArea(w http.ResponseWriter, r *http.Request) {
	c, apiErr := s.caller(r)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	a, err := s.store.GetArea(r.PathValue("id"))
	if err != nil {
		writeAPIError(w, errNotFound("area not found"))
		return
	}
	m, err := s.store.GetMap(a.MapID)
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
		writeAPIError(w, errForbidden("only the space owner can delete areas"))
		return
	}
	if err := s.store.DeleteArea(a.ID); err != nil {
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	s.hub.RefreshSpace(sp.ID)
	writeJSON(w, http.StatusOK, map[string]any{})
}

// ---------- objects ----------

var objectKinds = map[string]bool{
	"note": true, "whiteboard": true, "url": true,
	"video": true, "poster": true, "portal": true,
}

// GET /api/v1/spaces/{id}/objects → {objects:[...]}
func (s *Server) handleListObjects(w http.ResponseWriter, r *http.Request) {
	if _, apiErr := s.caller(r); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	sp, apiErr := s.spaceOr404(r.PathValue("id"))
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	maps, err := s.store.ListMapsBySpace(sp.ID)
	if err != nil {
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	objects := []*store.SpaceObject{}
	for _, m := range maps {
		os, err := s.store.ListObjectsByMap(m.ID)
		if err != nil {
			writeAPIError(w, errBadRequest(err.Error()))
			return
		}
		objects = append(objects, os...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"objects": objects})
}

// POST /api/v1/spaces/{id}/objects (owner)
func (s *Server) handleCreateObject(w http.ResponseWriter, r *http.Request) {
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
		writeAPIError(w, errForbidden("only the space owner can create objects"))
		return
	}
	var in struct {
		MapID     string  `json:"map_id"`
		Kind      string  `json:"kind"`
		X         float64 `json:"x"`
		Y         float64 `json:"y"`
		W         float64 `json:"w"`
		H         float64 `json:"h"`
		Title     string  `json:"title"`
		Body      string  `json:"body"`
		URL       string  `json:"url"`
		TargetMap string  `json:"target_map_id"`
		TargetX   float64 `json:"target_x"`
		TargetY   float64 `json:"target_y"`
		Solid     bool    `json:"solid"`
	}
	if err := decodeBody(w, r, &in); err != nil {
		writeAPIError(w, err)
		return
	}
	m, err := s.store.GetMap(in.MapID)
	if err != nil || m.SpaceID != sp.ID {
		writeAPIError(w, errBadRequest("map not found in space"))
		return
	}
	if !objectKinds[in.Kind] {
		writeAPIError(w, errBadRequest("invalid object kind"))
		return
	}
	if in.W <= 0 || in.H <= 0 || in.W > 50 || in.H > 50 {
		writeAPIError(w, errBadRequest("object w/h must be 0..50"))
		return
	}
	if len(in.Title) > 120 || len(in.Body) > 20000 {
		writeAPIError(w, errBadRequest("title ≤120 chars, body ≤20000 chars"))
		return
	}
	o := &store.SpaceObject{
		ID: store.NewID(store.PrefixObject), MapID: m.ID, Kind: in.Kind,
		X: in.X, Y: in.Y, W: in.W, H: in.H,
		Title: in.Title, Body: in.Body, URL: in.URL,
		TargetMap: in.TargetMap, TargetX: in.TargetX, TargetY: in.TargetY,
		Solid: in.Solid, CreatedAt: store.Now(), UpdatedAt: store.Now(),
	}
	if err := s.store.CreateObject(o); err != nil {
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	s.hub.RefreshSpace(sp.ID)
	s.hub.ObjectChanged(sp.ID, m.ID, o.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"object": o})
}

// PATCH /api/v1/objects/{id}
// Owner (or admin agent key): full patch. Agent scope: body field ONLY,
// any other field → 403 (integration ruling).
func (s *Server) handlePatchObject(w http.ResponseWriter, r *http.Request) {
	c, apiErr := s.caller(r)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	o, err := s.store.GetObject(r.PathValue("id"))
	if err != nil {
		writeAPIError(w, errNotFound("object not found"))
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

	// Decode as a raw map so we can enforce the body-only rule for agents.
	var raw map[string]json.RawMessage
	if err := decodeBody(w, r, &raw); err != nil {
		writeAPIError(w, err)
		return
	}
	full := canAdmin(sp, c)
	if !full {
		if !c.IsAgent() || !agentEffective(c) {
			writeAPIError(w, errForbidden("only the space owner can edit objects"))
			return
		}
		for k := range raw {
			if k != "body" {
				writeAPIError(w, errForbidden("agent keys may only edit the body field"))
				return
			}
		}
	}
	var in struct {
		Kind      *string  `json:"kind"`
		X         *float64 `json:"x"`
		Y         *float64 `json:"y"`
		W         *float64 `json:"w"`
		H         *float64 `json:"h"`
		Title     *string  `json:"title"`
		Body      *string  `json:"body"`
		URL       *string  `json:"url"`
		TargetMap *string  `json:"target_map_id"`
		TargetX   *float64 `json:"target_x"`
		TargetY   *float64 `json:"target_y"`
		Solid     *bool    `json:"solid"`
	}
	// Re-decode strictly into the struct (unknown fields already rejected).
	rb, _ := json.Marshal(raw)
	if err := json.Unmarshal(rb, &in); err != nil {
		writeAPIError(w, errBadRequest("invalid JSON body"))
		return
	}
	if in.Kind != nil {
		if !objectKinds[*in.Kind] {
			writeAPIError(w, errBadRequest("invalid object kind"))
			return
		}
		o.Kind = *in.Kind
	}
	if in.X != nil {
		o.X = *in.X
	}
	if in.Y != nil {
		o.Y = *in.Y
	}
	if in.W != nil {
		if *in.W <= 0 || *in.W > 50 {
			writeAPIError(w, errBadRequest("object w must be 0..50"))
			return
		}
		o.W = *in.W
	}
	if in.H != nil {
		if *in.H <= 0 || *in.H > 50 {
			writeAPIError(w, errBadRequest("object h must be 0..50"))
			return
		}
		o.H = *in.H
	}
	if in.Title != nil {
		if len(*in.Title) > 120 {
			writeAPIError(w, errBadRequest("title ≤120 chars"))
			return
		}
		o.Title = *in.Title
	}
	if in.Body != nil {
		if len(*in.Body) > 20000 {
			writeAPIError(w, errBadRequest("body ≤20000 chars"))
			return
		}
		o.Body = *in.Body
	}
	if in.URL != nil {
		o.URL = *in.URL
	}
	if in.TargetMap != nil {
		o.TargetMap = *in.TargetMap
	}
	if in.TargetX != nil {
		o.TargetX = *in.TargetX
	}
	if in.TargetY != nil {
		o.TargetY = *in.TargetY
	}
	if in.Solid != nil {
		o.Solid = *in.Solid
	}
	o.UpdatedAt = store.Now()
	if err := s.store.UpdateObject(o); err != nil {
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	s.hub.RefreshSpace(sp.ID)
	s.hub.ObjectChanged(sp.ID, m.ID, o.ID)
	writeJSON(w, http.StatusOK, map[string]any{"object": o})
}

// DELETE /api/v1/objects/{id} (owner)
func (s *Server) handleDeleteObject(w http.ResponseWriter, r *http.Request) {
	c, apiErr := s.caller(r)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	o, err := s.store.GetObject(r.PathValue("id"))
	if err != nil {
		writeAPIError(w, errNotFound("object not found"))
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
		writeAPIError(w, errForbidden("only the space owner can delete objects"))
		return
	}
	if err := s.store.DeleteObject(o.ID); err != nil {
		writeAPIError(w, errBadRequest(err.Error()))
		return
	}
	s.hub.RefreshSpace(sp.ID)
	s.hub.ObjectChanged(sp.ID, m.ID, o.ID)
	writeJSON(w, http.StatusOK, map[string]any{})
}
