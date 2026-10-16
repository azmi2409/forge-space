package main

// All 19 MCP tools (CONTRACT §8), implemented against the REST+SSE shapes of
// CONTRACT §3 and §7.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// ---- schema helpers ----

func objSchema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{
		"type":                 "object",
		"properties":           props,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func numProp(desc string) map[string]any {
	return map[string]any{"type": "number", "description": desc}
}

func boolProp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

func intProp(desc string, def int) map[string]any {
	return map[string]any{"type": "integer", "description": desc, "default": def, "minimum": 1}
}

func enumProp(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": values}
}

// ---- shared helpers ----

func esc(s string) string { return url.PathEscape(s) }

func decodeArgs(args json.RawMessage, v any) error {
	if len(args) == 0 {
		args = []byte("{}")
	}
	if err := json.Unmarshal(args, v); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

// self returns my participant record by looking me up in the space's
// participant list (CONTRACT §3 has no GET /participants/me; the join response
// gives us the id to match on).
func (s *MCPServer) self(ctx context.Context, sess *session) (*Participant, error) {
	var out struct {
		Participants []Participant `json:"participants"`
	}
	if err := s.client.do(ctx, "GET", "/api/v1/spaces/"+esc(sess.spaceID)+"/participants", nil, nil, &out); err != nil {
		return nil, err
	}
	for i := range out.Participants {
		if out.Participants[i].ID == sess.meID {
			return &out.Participants[i], nil
		}
	}
	return nil, fmt.Errorf("my participant %s is no longer in the space (was I kicked?)", sess.meID)
}

func (s *MCPServer) listObjects(ctx context.Context, sess *session) ([]SpaceObject, error) {
	var out struct {
		Objects []SpaceObject `json:"objects"`
	}
	if err := s.client.do(ctx, "GET", "/api/v1/spaces/"+esc(sess.spaceID)+"/objects", nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Objects, nil
}

func participantSummary(p Participant) map[string]any {
	return map[string]any{
		"id":           p.ID,
		"display_name": p.DisplayName,
		"kind":         p.Kind,
		"map_id":       p.MapID,
		"x":            p.X,
		"y":            p.Y,
		"dir":          p.Dir,
		"emote":        p.Emote,
		"ghost":        p.Ghost,
		"speaking":     p.Speaking,
		"area_id":      p.AreaID,
		"status":       p.Status,
	}
}

func chatSummary(m ChatMessage) map[string]any {
	return map[string]any{
		"id":         m.ID,
		"from_name":  m.FromName,
		"from_kind":  m.FromKind,
		"scope":      m.Scope,
		"text":       m.Text,
		"created_at": m.CreatedAt,
	}
}

// ---- tool registration (CONTRACT §8 order) ----

func allTools(s *MCPServer) []*Tool {
	return []*Tool{
		{
			Name:        "space_list",
			Description: "List all public spaces on this forge-space server. No session needed; call this first to discover spaces.",
			InputSchema: objSchema(map[string]any{}),
			Handler:     s.toolSpaceList,
		},
		{
			Name:        "space_join",
			Description: "Join a space as an agent participant. Stores the session (participant + ticket) in memory for all later tools and starts the background SSE event stream. If already in a space, leaves it first.",
			InputSchema: objSchema(map[string]any{
				"space_id":     strProp("Space id, e.g. sp_xxxxxxxxxxxxxxxx (from space_list)."),
				"display_name": strProp("Your visible name in the space, 1-30 characters."),
			}, "space_id", "display_name"),
			Handler: s.toolSpaceJoin,
		},
		{
			Name:        "space_leave",
			Description: "Leave the current space. Stops the SSE event stream and clears the session.",
			InputSchema: objSchema(map[string]any{}),
			Handler:     s.toolSpaceLeave,
		},
		{
			Name:        "me_where",
			Description: "Return my current position and state: map, x/y tile coordinates, facing direction, emote, ghost mode, speaking flag, area.",
			InputSchema: objSchema(map[string]any{}),
			Handler:     s.toolMeWhere,
		},
		{
			Name:        "me_move_to",
			Description: "Move toward an absolute tile position (x, y floats). The server path-moves at 4.5 tiles/s with collision sliding; unreachable targets (>40 tiles away or no path) are rejected.",
			InputSchema: objSchema(map[string]any{
				"x": numProp("Target x tile coordinate (float)."),
				"y": numProp("Target y tile coordinate (float)."),
			}, "x", "y"),
			Handler: s.toolMeMoveTo,
		},
		{
			Name:        "me_step",
			Description: "Step a number of tiles in a cardinal direction from my current position. up decreases y, down increases y, left decreases x, right increases x (tile row-major coordinates).",
			InputSchema: objSchema(map[string]any{
				"direction": enumProp("Cardinal direction to step.", "up", "down", "left", "right"),
				"tiles":     intProp("How many tiles to step (default 1).", 1),
			}, "direction"),
			Handler: s.toolMeStep,
		},
		{
			Name:        "me_say",
			Description: "Send a chat message (1-500 chars). nearby reaches participants within proximity, dm is a direct message (requires to), space reaches everyone (may require admin scope). Rate limit: 5 messages per 5 seconds.",
			InputSchema: objSchema(map[string]any{
				"text":  strProp("Message text, 1-500 characters."),
				"scope": enumProp("Who receives the message (default nearby).", "nearby", "space", "dm"),
				"to":    strProp("Recipient participant id (pt_...) — required for dm scope, ignored otherwise."),
			}, "text"),
			Handler: s.toolMeSay,
		},
		{
			Name:        "me_emote",
			Description: "Play an emote on my avatar, visible to everyone who can see me. Known emotes: dance, wave. Empty string clears the emote.",
			InputSchema: objSchema(map[string]any{
				"name": strProp("Emote name: dance, wave, or empty string to clear."),
			}, "name"),
			Handler: s.toolMeEmote,
		},
		{
			Name:        "me_ghost",
			Description: "Toggle ghost mode. Ghosts walk through walls and are invisible and inaudible to everyone (and hear no one).",
			InputSchema: objSchema(map[string]any{
				"on": boolProp("true to become a ghost, false to become solid/visible again."),
			}, "on"),
			Handler: s.toolMeGhost,
		},
		{
			Name:        "me_speaking",
			Description: "Set my speaking indicator (the green ring others see). Voice agents should set this true while publishing audio and false when done.",
			InputSchema: objSchema(map[string]any{
				"on": boolProp("true while speaking, false when silent."),
			}, "on"),
			Handler: s.toolMeSpeaking,
		},
		{
			Name:        "world_look",
			Description: "Look around: the set of participants I can currently hear/see (audible set per proximity, private areas, spotlights, ghosts), objects within the 2.5-tile interact radius, and the area I am standing in.",
			InputSchema: objSchema(map[string]any{}),
			Handler:     s.toolWorldLook,
		},
		{
			Name:        "world_listen",
			Description: "Listen: recent chat messages audible to me plus recent SSE events (joins, leaves, emotes, area enter/exit, object changes). Poll this to perceive the world — positions are NOT streamed, use world_look for those.",
			InputSchema: objSchema(map[string]any{
				"limit": intProp("Max items per section (default 20, max 100).", 20),
			}),
			Handler: s.toolWorldListen,
		},
		{
			Name:        "object_read",
			Description: "Read an interactive object (note, whiteboard, poster, url, video, portal) by id. Returns its full content: title, body text, url, portal target.",
			InputSchema: objSchema(map[string]any{
				"id": strProp("Object id, e.g. ob_xxxxxxxxxxxxxxxx (from world_look)."),
			}, "id"),
			Handler: s.toolObjectRead,
		},
		{
			Name:        "object_write",
			Description: "Write text to a note/whiteboard object. append concatenates text onto the existing body; replace overwrites it.",
			InputSchema: objSchema(map[string]any{
				"id":   strProp("Object id, e.g. ob_xxxxxxxxxxxxxxxx."),
				"text": strProp("Text to write."),
				"mode": enumProp("append adds to the existing body, replace overwrites it (default append).", "append", "replace"),
			}, "id", "text"),
			Handler: s.toolObjectWrite,
		},
		{
			Name:        "portal_use",
			Description: "Teleport through a portal object to its target map and coordinates.",
			InputSchema: objSchema(map[string]any{
				"id": strProp("Portal object id, e.g. ob_xxxxxxxxxxxxxxxx."),
			}, "id"),
			Handler: s.toolPortalUse,
		},
		{
			Name:        "participants_list",
			Description: "List all participants currently in the space (humans and agents), with positions and state. Tickets are never exposed.",
			InputSchema: objSchema(map[string]any{}),
			Handler:     s.toolParticipantsList,
		},
		{
			Name:        "map_get",
			Description: "Get the full tile map of the map I am currently on, including tiles (row-major ints: 0=void 1=floor 2=wall 3=private 4=spotlight 5=spawn), dimensions, and spawn point.",
			InputSchema: objSchema(map[string]any{}),
			Handler:     s.toolMapGet,
		},
		{
			Name:        "media_token",
			Description: "Mint a LiveKit token so I can join voice/video with a LiveKit SDK of my choice and publish audio (e.g. TTS). Returns {url, token}; fails if media is disabled on the server.",
			InputSchema: objSchema(map[string]any{}),
			Handler:     s.toolMediaToken,
		},
		{
			Name:        "admin_broadcast",
			Description: "ADMIN SCOPE ONLY. Send an announcement to everyone in the space (space scope + announce flag). Returns a clean error if the key lacks admin scope — do not retry.",
			InputSchema: objSchema(map[string]any{
				"text": strProp("Announcement text, 1-500 characters."),
			}, "text"),
			Handler: s.toolAdminBroadcast,
		},
		{
			Name:        "admin_kick",
			Description: "ADMIN SCOPE ONLY. Remove a participant from the space by id. Returns a clean error if the key lacks admin scope — do not retry.",
			InputSchema: objSchema(map[string]any{
				"participant_id": strProp("Participant id to kick, e.g. pt_xxxxxxxxxxxxxxxx."),
			}, "participant_id"),
			Handler: s.toolAdminKick,
		},
	}
}

// ---- handlers ----

func (s *MCPServer) toolSpaceList(ctx context.Context, args json.RawMessage) (any, error) {
	var out struct {
		Spaces []Space `json:"spaces"`
	}
	if err := s.client.do(ctx, "GET", "/api/v1/spaces", nil, nil, &out); err != nil {
		return nil, err
	}
	spaces := make([]any, 0, len(out.Spaces))
	for _, sp := range out.Spaces {
		spaces = append(spaces, map[string]any{
			"id":   sp.ID,
			"name": sp.Name,
			"slug": sp.Slug,
		})
	}
	return map[string]any{"spaces": spaces}, nil
}

func (s *MCPServer) toolSpaceJoin(ctx context.Context, args json.RawMessage) (any, error) {
	var a struct {
		SpaceID     string `json:"space_id"`
		DisplayName string `json:"display_name"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if a.SpaceID == "" {
		return nil, fmt.Errorf("space_id is required")
	}
	if n := len([]rune(a.DisplayName)); n < 1 || n > 30 {
		return nil, fmt.Errorf("display_name must be 1-30 characters")
	}
	// Leave any existing session first (best effort).
	s.mu.Lock()
	old := s.sess
	s.sess = nil
	s.mu.Unlock()
	if old != nil {
		stopSSE(old)
		_ = s.client.do(ctx, "POST", "/api/v1/spaces/"+esc(old.spaceID)+"/leave", nil, nil, nil)
	}
	var jr JoinResponse
	if err := s.client.do(ctx, "POST", "/api/v1/spaces/"+esc(a.SpaceID)+"/join", nil,
		map[string]any{"display_name": a.DisplayName}, &jr); err != nil {
		return nil, err
	}
	if jr.Ticket == "" {
		return nil, fmt.Errorf("join succeeded but server returned no ticket")
	}
	sess := &session{spaceID: a.SpaceID, meID: jr.Participant.ID, displayName: jr.Participant.DisplayName}
	s.mu.Lock()
	s.sess = sess
	s.mu.Unlock()
	s.startSSE(sess, jr.Ticket) // consumes the single-use ticket
	return map[string]any{
		"space_id":    a.SpaceID,
		"participant": participantSummary(jr.Participant),
		"stream":      "connecting",
	}, nil
}

func (s *MCPServer) toolSpaceLeave(ctx context.Context, args json.RawMessage) (any, error) {
	sess, err := s.requireSession()
	if err != nil {
		return nil, err
	}
	// Cancel the SSE reader first: the server also treats stream close as leave
	// (CONTRACT §3), so the participant is cleaned up even if the POST fails.
	s.clearSession()
	_ = s.client.do(ctx, "POST", "/api/v1/spaces/"+esc(sess.spaceID)+"/leave", nil, nil, nil)
	return map[string]any{"ok": true}, nil
}

func (s *MCPServer) toolMeWhere(ctx context.Context, args json.RawMessage) (any, error) {
	sess, err := s.requireSession()
	if err != nil {
		return nil, err
	}
	me, err := s.self(ctx, sess)
	if err != nil {
		return nil, err
	}
	return participantSummary(*me), nil
}

func (s *MCPServer) toolMeMoveTo(ctx context.Context, args json.RawMessage) (any, error) {
	if _, err := s.requireSession(); err != nil {
		return nil, err
	}
	var a struct {
		X *float64 `json:"x"`
		Y *float64 `json:"y"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if a.X == nil || a.Y == nil {
		return nil, fmt.Errorf("x and y are required")
	}
	return s.postMove(ctx, *a.X, *a.Y)
}

func (s *MCPServer) toolMeStep(ctx context.Context, args json.RawMessage) (any, error) {
	sess, err := s.requireSession()
	if err != nil {
		return nil, err
	}
	var a struct {
		Direction string `json:"direction"`
		Tiles     *int   `json:"tiles"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	tiles := 1
	if a.Tiles != nil {
		tiles = *a.Tiles
	}
	if tiles < 1 {
		return nil, fmt.Errorf("tiles must be >= 1")
	}
	me, err := s.self(ctx, sess)
	if err != nil {
		return nil, err
	}
	tx, ty := me.X, me.Y
	switch a.Direction {
	case "up":
		ty -= float64(tiles)
	case "down":
		ty += float64(tiles)
	case "left":
		tx -= float64(tiles)
	case "right":
		tx += float64(tiles)
	default:
		return nil, fmt.Errorf("direction must be one of up|down|left|right")
	}
	return s.postMove(ctx, tx, ty)
}

// postMove calls POST /api/v1/participants/me/move (CONTRACT §3).
func (s *MCPServer) postMove(ctx context.Context, x, y float64) (any, error) {
	var out struct {
		Participant Participant `json:"participant"`
	}
	if err := s.client.do(ctx, "POST", "/api/v1/participants/me/move", nil,
		map[string]any{"x": x, "y": y}, &out); err != nil {
		return nil, err
	}
	return map[string]any{"x": out.Participant.X, "y": out.Participant.Y, "map_id": out.Participant.MapID}, nil
}

func (s *MCPServer) toolMeSay(ctx context.Context, args json.RawMessage) (any, error) {
	sess, err := s.requireSession()
	if err != nil {
		return nil, err
	}
	var a struct {
		Text  string `json:"text"`
		Scope string `json:"scope"`
		To    string `json:"to"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if n := len([]rune(a.Text)); n < 1 || n > 500 {
		return nil, fmt.Errorf("text must be 1-500 characters")
	}
	scope := a.Scope
	if scope == "" {
		scope = "nearby"
	}
	switch scope {
	case "nearby", "space", "dm":
	default:
		return nil, fmt.Errorf("scope must be one of nearby|space|dm")
	}
	if scope == "dm" && a.To == "" {
		return nil, fmt.Errorf("to (recipient participant id) is required for dm scope")
	}
	body := map[string]any{"scope": scope, "text": a.Text}
	if a.To != "" {
		body["to"] = a.To
	}
	var out struct {
		Message ChatMessage `json:"message"`
	}
	if err := s.client.do(ctx, "POST", "/api/v1/spaces/"+esc(sess.spaceID)+"/chat", nil, body, &out); err != nil {
		return nil, err
	}
	return map[string]any{"message": chatSummary(out.Message)}, nil
}

func (s *MCPServer) toolMeEmote(ctx context.Context, args json.RawMessage) (any, error) {
	if _, err := s.requireSession(); err != nil {
		return nil, err
	}
	var a struct {
		Name string `json:"name"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if err := s.client.do(ctx, "POST", "/api/v1/participants/me/state", nil,
		map[string]any{"emote": a.Name}, nil); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "emote": a.Name}, nil
}

func (s *MCPServer) toolMeGhost(ctx context.Context, args json.RawMessage) (any, error) {
	if _, err := s.requireSession(); err != nil {
		return nil, err
	}
	var a struct {
		On *bool `json:"on"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if a.On == nil {
		return nil, fmt.Errorf("on is required")
	}
	if err := s.client.do(ctx, "POST", "/api/v1/participants/me/state", nil,
		map[string]any{"ghost": *a.On}, nil); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "ghost": *a.On}, nil
}

func (s *MCPServer) toolMeSpeaking(ctx context.Context, args json.RawMessage) (any, error) {
	if _, err := s.requireSession(); err != nil {
		return nil, err
	}
	var a struct {
		On *bool `json:"on"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if a.On == nil {
		return nil, fmt.Errorf("on is required")
	}
	if err := s.client.do(ctx, "POST", "/api/v1/participants/me/state", nil,
		map[string]any{"speaking": *a.On}, nil); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "speaking": *a.On}, nil
}

func (s *MCPServer) toolWorldLook(ctx context.Context, args json.RawMessage) (any, error) {
	sess, err := s.requireSession()
	if err != nil {
		return nil, err
	}
	spacePath := "/api/v1/spaces/" + esc(sess.spaceID)
	var (
		parts struct {
			Participants []Participant `json:"participants"`
		}
		spaceResp struct {
			Space Space `json:"space"`
		}
		areasResp struct {
			Areas []Area `json:"areas"`
		}
		objectsResp struct {
			Objects []SpaceObject `json:"objects"`
		}
	)
	if err := s.client.do(ctx, "GET", spacePath+"/participants", nil, nil, &parts); err != nil {
		return nil, err
	}
	if err := s.client.do(ctx, "GET", spacePath, nil, nil, &spaceResp); err != nil {
		return nil, err
	}
	if err := s.client.do(ctx, "GET", spacePath+"/areas", nil, nil, &areasResp); err != nil {
		return nil, err
	}
	if err := s.client.do(ctx, "GET", spacePath+"/objects", nil, nil, &objectsResp); err != nil {
		return nil, err
	}
	var me *Participant
	for i := range parts.Participants {
		if parts.Participants[i].ID == sess.meID {
			me = &parts.Participants[i]
			break
		}
	}
	if me == nil {
		return nil, fmt.Errorf("my participant %s is no longer in the space (was I kicked?)", sess.meID)
	}
	radius := spaceResp.Space.Settings.ProximityRadius
	if radius <= 0 {
		radius = 5
	}
	audible := audibleSet(*me, parts.Participants, areasResp.Areas, radius)
	participants := make([]any, 0, len(audible))
	for _, q := range audible {
		var areaName any
		if a := areaContaining(areasResp.Areas, q.MapID, q.X, q.Y); a != nil {
			areaName = a.Name
		}
		participants = append(participants, map[string]any{
			"id":       q.ID,
			"name":     q.DisplayName,
			"kind":     q.Kind,
			"x":        q.X,
			"y":        q.Y,
			"speaking": q.Speaking,
			"area":     areaName,
		})
	}
	objects := make([]any, 0)
	for _, o := range objectsResp.Objects {
		if o.MapID != me.MapID {
			continue
		}
		if rectDist(me.X, me.Y, o) <= 2.5 { // interact radius (CONTRACT §8)
			objects = append(objects, map[string]any{
				"id":    o.ID,
				"kind":  o.Kind,
				"title": o.Title,
			})
		}
	}
	var myArea any
	if a := areaContaining(areasResp.Areas, me.MapID, me.X, me.Y); a != nil {
		myArea = map[string]any{"id": a.ID, "name": a.Name, "kind": a.Kind}
	}
	return map[string]any{
		"participants":     participants,
		"objects_in_range": objects,
		"my_area":          myArea,
	}, nil
}

func (s *MCPServer) toolWorldListen(ctx context.Context, args json.RawMessage) (any, error) {
	sess, err := s.requireSession()
	if err != nil {
		return nil, err
	}
	var a struct {
		Limit *int `json:"limit"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	limit := 20
	if a.Limit != nil {
		limit = *a.Limit
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	var chatResp struct {
		Messages []ChatMessage `json:"messages"`
	}
	if err := s.client.do(ctx, "GET", "/api/v1/spaces/"+esc(sess.spaceID)+"/chat",
		map[string]string{"limit": strconv.Itoa(limit)}, nil, &chatResp); err != nil {
		return nil, err
	}
	chat := make([]any, 0, len(chatResp.Messages))
	for _, m := range chatResp.Messages {
		chat = append(chat, chatSummary(m))
	}
	events := make([]any, 0)
	for _, e := range sess.events.last(limit) {
		events = append(events, e.Raw)
	}
	return map[string]any{
		"chat":   chat,
		"events": events,
		"stream": sess.state(),
	}, nil
}

func (s *MCPServer) toolObjectRead(ctx context.Context, args json.RawMessage) (any, error) {
	sess, err := s.requireSession()
	if err != nil {
		return nil, err
	}
	var a struct {
		ID string `json:"id"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if a.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	objects, err := s.listObjects(ctx, sess)
	if err != nil {
		return nil, err
	}
	for _, o := range objects {
		if o.ID == a.ID {
			return map[string]any{"object": o}, nil
		}
	}
	return nil, fmt.Errorf("object %s not found", a.ID)
}

func (s *MCPServer) toolObjectWrite(ctx context.Context, args json.RawMessage) (any, error) {
	sess, err := s.requireSession()
	if err != nil {
		return nil, err
	}
	var a struct {
		ID   string `json:"id"`
		Text string `json:"text"`
		Mode string `json:"mode"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if a.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	mode := a.Mode
	if mode == "" {
		mode = "append"
	}
	if mode != "append" && mode != "replace" {
		return nil, fmt.Errorf("mode must be append or replace")
	}
	objects, err := s.listObjects(ctx, sess)
	if err != nil {
		return nil, err
	}
	var target *SpaceObject
	for i := range objects {
		if objects[i].ID == a.ID {
			target = &objects[i]
			break
		}
	}
	if target == nil {
		return nil, fmt.Errorf("object %s not found", a.ID)
	}
	newBody := a.Text
	if mode == "append" {
		newBody = target.Body + a.Text
	}
	// NOTE: CONTRACT §3 lists PATCH /api/v1/objects/:id as owner-only, but also
	// lists "objects read/write" among agent-key-allowed operations. This maps
	// object_write to that endpoint with a body-only update; the server is
	// authoritative on scope enforcement.
	if err := s.client.do(ctx, "PATCH", "/api/v1/objects/"+esc(a.ID), nil,
		map[string]any{"body": newBody}, nil); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": a.ID, "mode": mode}, nil
}

func (s *MCPServer) toolPortalUse(ctx context.Context, args json.RawMessage) (any, error) {
	if _, err := s.requireSession(); err != nil {
		return nil, err
	}
	var a struct {
		ID string `json:"id"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if a.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	var out struct {
		Participant Participant `json:"participant"`
	}
	if err := s.client.do(ctx, "POST", "/api/v1/participants/me/portal", nil,
		map[string]any{"object_id": a.ID}, &out); err != nil {
		return nil, err
	}
	res := map[string]any{"ok": true}
	if out.Participant.ID != "" {
		res["map_id"] = out.Participant.MapID
		res["x"] = out.Participant.X
		res["y"] = out.Participant.Y
	}
	return res, nil
}

func (s *MCPServer) toolParticipantsList(ctx context.Context, args json.RawMessage) (any, error) {
	sess, err := s.requireSession()
	if err != nil {
		return nil, err
	}
	var out struct {
		Participants []Participant `json:"participants"`
	}
	if err := s.client.do(ctx, "GET", "/api/v1/spaces/"+esc(sess.spaceID)+"/participants", nil, nil, &out); err != nil {
		return nil, err
	}
	participants := make([]any, 0, len(out.Participants))
	for _, p := range out.Participants {
		participants = append(participants, participantSummary(p))
	}
	return map[string]any{"participants": participants}, nil
}

func (s *MCPServer) toolMapGet(ctx context.Context, args json.RawMessage) (any, error) {
	sess, err := s.requireSession()
	if err != nil {
		return nil, err
	}
	me, err := s.self(ctx, sess)
	if err != nil {
		return nil, err
	}
	var out struct {
		Map GameMap `json:"map"`
	}
	if err := s.client.do(ctx, "GET",
		"/api/v1/spaces/"+esc(sess.spaceID)+"/maps/"+esc(me.MapID), nil, nil, &out); err != nil {
		return nil, err
	}
	return map[string]any{"map": out.Map}, nil
}

func (s *MCPServer) toolMediaToken(ctx context.Context, args json.RawMessage) (any, error) {
	if _, err := s.requireSession(); err != nil {
		return nil, err
	}
	var out struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	if err := s.client.do(ctx, "GET", "/api/v1/media/token", nil, nil, &out); err != nil {
		return nil, err
	}
	return map[string]any{"url": out.URL, "token": out.Token}, nil
}

func (s *MCPServer) toolAdminBroadcast(ctx context.Context, args json.RawMessage) (any, error) {
	sess, err := s.requireSession()
	if err != nil {
		return nil, err
	}
	var a struct {
		Text string `json:"text"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if n := len([]rune(a.Text)); n < 1 || n > 500 {
		return nil, fmt.Errorf("text must be 1-500 characters")
	}
	var out struct {
		Message ChatMessage `json:"message"`
	}
	// Announcement = scope "space" + announce:true (CONTRACT §3). A 403 here
	// means the key lacks admin scope; it is surfaced as-is, never retried.
	if err := s.client.do(ctx, "POST", "/api/v1/spaces/"+esc(sess.spaceID)+"/chat", nil,
		map[string]any{"scope": "space", "announce": true, "text": a.Text}, &out); err != nil {
		return nil, err
	}
	return map[string]any{"message": chatSummary(out.Message)}, nil
}

func (s *MCPServer) toolAdminKick(ctx context.Context, args json.RawMessage) (any, error) {
	sess, err := s.requireSession()
	if err != nil {
		return nil, err
	}
	var a struct {
		ParticipantID string `json:"participant_id"`
	}
	if err := decodeArgs(args, &a); err != nil {
		return nil, err
	}
	if a.ParticipantID == "" {
		return nil, fmt.Errorf("participant_id is required")
	}
	// NOTE: CONTRACT §3/§8 define the admin_kick tool but no REST kick shape.
	// This uses the natural REST mapping; a 403 (no admin scope) is surfaced
	// as-is, never retried. Confirm with the server builder before relying on it.
	if err := s.client.do(ctx, "DELETE",
		"/api/v1/spaces/"+esc(sess.spaceID)+"/participants/"+esc(a.ParticipantID),
		nil, nil, nil); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "participant_id": a.ParticipantID}, nil
}
