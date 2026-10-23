package api

import (
	"testing"
	"time"
)

// ---------- whiteboard v1 (WHITEBOARD_V1.md) ----------

func createBoardObject(t *testing.T, ts *testStack, token, spaceID, kind string) string {
	t.Helper()
	var maps map[string]any
	_ = doJSON(t, "GET", ts.ts.URL+"/api/v1/spaces/"+spaceID, token, nil, &maps)
	mapID := maps["maps"].([]any)[0].(map[string]any)["id"].(string)
	var out map[string]any
	code := doJSON(t, "POST", ts.ts.URL+"/api/v1/spaces/"+spaceID+"/objects", token, map[string]any{
		"map_id": mapID, "kind": kind, "x": 10, "y": 10, "w": 4, "h": 3, "title": "Board",
	}, &out)
	if code != 201 {
		t.Fatalf("create %s object → %d: %v", kind, code, out)
	}
	return out["object"].(map[string]any)["id"].(string)
}

func strokeBody(color string, width float64, pts [][2]float64) map[string]any {
	points := make([]map[string]any, 0, len(pts))
	for _, p := range pts {
		points = append(points, map[string]any{"x": p[0], "y": p[1]})
	}
	return map[string]any{"color": color, "width": width, "points": points}
}

func goodStroke() map[string]any {
	return strokeBody("#ff0000", 3, [][2]float64{{10, 10}, {500, 900}})
}

func TestBoardRESTCRUD(t *testing.T) {
	ts := newTestStack(t)
	tok := register(t, ts, "owner@x.com", "Owner")
	spaceID := createSpace(t, ts, tok, "Board Space")
	join(t, ts, tok, spaceID, "Owner")
	boardID := createBoardObject(t, ts, tok, spaceID, "whiteboard")

	// empty board
	var out map[string]any
	code := doJSON(t, "GET", ts.ts.URL+"/api/v1/objects/"+boardID+"/board", tok, nil, &out)
	if code != 200 {
		t.Fatalf("GET board → %d: %v", code, out)
	}
	if sl, ok := out["strokes"].([]any); !ok || len(sl) != 0 {
		t.Fatalf("expected empty strokes list, got %v", out)
	}

	// draw → 201
	code = doJSON(t, "POST", ts.ts.URL+"/api/v1/objects/"+boardID+"/board", tok, goodStroke(), &out)
	if code != 201 {
		t.Fatalf("POST stroke → %d: %v", code, out)
	}
	st := out["stroke"].(map[string]any)
	if st["color"] != "#ff0000" || st["width"].(float64) != 3 {
		t.Fatalf("stroke echo mismatch: %v", st)
	}
	if pts := st["points"].([]any); len(pts) != 2 {
		t.Fatalf("expected 2 points, got %d", len(pts))
	}
	if st["participant_id"] == "" {
		t.Fatal("stroke missing participant_id")
	}

	// list → 1 stroke, oldest first
	code = doJSON(t, "GET", ts.ts.URL+"/api/v1/objects/"+boardID+"/board", tok, nil, &out)
	if code != 200 {
		t.Fatalf("GET board → %d", code)
	}
	if len(out["strokes"].([]any)) != 1 {
		t.Fatalf("expected 1 stroke, got %v", out["strokes"])
	}

	// clear → 200, then empty
	code = doJSON(t, "DELETE", ts.ts.URL+"/api/v1/objects/"+boardID+"/board", tok, nil, &out)
	if code != 200 {
		t.Fatalf("DELETE board → %d: %v", code, out)
	}
	code = doJSON(t, "GET", ts.ts.URL+"/api/v1/objects/"+boardID+"/board", tok, nil, &out)
	if code != 200 || len(out["strokes"].([]any)) != 0 {
		t.Fatalf("expected empty board after clear, got %d: %v", code, out)
	}
}

func TestBoardRESTValidation(t *testing.T) {
	ts := newTestStack(t)
	tok := register(t, ts, "owner@x.com", "Owner")
	spaceID := createSpace(t, ts, tok, "Board Val")
	join(t, ts, tok, spaceID, "Owner")
	boardID := createBoardObject(t, ts, tok, spaceID, "whiteboard")

	bad := []struct {
		name string
		body map[string]any
	}{
		{"bad color", strokeBody("red", 3, [][2]float64{{1, 1}, {2, 2}})},
		{"short hex", strokeBody("#fff", 3, [][2]float64{{1, 1}, {2, 2}})},
		{"width 0", strokeBody("#ff0000", 0, [][2]float64{{1, 1}, {2, 2}})},
		{"width 21", strokeBody("#ff0000", 21, [][2]float64{{1, 1}, {2, 2}})},
		{"one point", strokeBody("#ff0000", 3, [][2]float64{{1, 1}})},
		{"no points", strokeBody("#ff0000", 3, nil)},
		{"point out of range", strokeBody("#ff0000", 3, [][2]float64{{1, 1}, {2000, 2}})},
		{"negative point", strokeBody("#ff0000", 3, [][2]float64{{-1, 1}, {2, 2}})},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			var out map[string]any
			code := doJSON(t, "POST", ts.ts.URL+"/api/v1/objects/"+boardID+"/board", tok, tc.body, &out)
			if code != 400 {
				t.Fatalf("→ %d, want 400: %v", code, out)
			}
			if out["error"].(map[string]any)["code"] != "bad_request" {
				t.Fatalf("wrong error shape: %v", out)
			}
		})
	}
}

func TestBoardRESTNotFound(t *testing.T) {
	ts := newTestStack(t)
	tok := register(t, ts, "owner@x.com", "Owner")
	spaceID := createSpace(t, ts, tok, "Board 404")
	join(t, ts, tok, spaceID, "Owner")
	noteID := createBoardObject(t, ts, tok, spaceID, "note")

	for _, tc := range []struct {
		name   string
		method string
		url    string
	}{
		{"get missing", "GET", ts.ts.URL + "/api/v1/objects/ob_nope/board"},
		{"post missing", "POST", ts.ts.URL + "/api/v1/objects/ob_nope/board"},
		{"delete missing", "DELETE", ts.ts.URL + "/api/v1/objects/ob_nope/board"},
		{"get note kind", "GET", ts.ts.URL + "/api/v1/objects/" + noteID + "/board"},
		{"post note kind", "POST", ts.ts.URL + "/api/v1/objects/" + noteID + "/board"},
		{"delete note kind", "DELETE", ts.ts.URL + "/api/v1/objects/" + noteID + "/board"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code := doJSON(t, tc.method, tc.url, tok, goodStroke(), nil)
			if code != 404 {
				t.Fatalf("→ %d, want 404", code)
			}
		})
	}
	// unauthenticated → 401
	code := doJSON(t, "GET", ts.ts.URL+"/api/v1/objects/"+noteID+"/board", "", nil, nil)
	if code != 401 {
		t.Fatalf("unauthenticated GET → %d, want 401", code)
	}
}

func TestBoardAuthz(t *testing.T) {
	ts := newTestStack(t)
	ownerTok := register(t, ts, "owner@x.com", "Owner")
	otherTok := register(t, ts, "other@x.com", "Other")
	spaceID := createSpace(t, ts, ownerTok, "Board Authz")
	join(t, ts, ownerTok, spaceID, "Owner")
	join(t, ts, otherTok, spaceID, "Other")
	boardID := createBoardObject(t, ts, ownerTok, spaceID, "whiteboard")

	// agent-scoped key: draws OK, clears forbidden
	var out map[string]any
	code := doJSON(t, "POST", ts.ts.URL+"/api/v1/agent-keys", ownerTok,
		map[string]any{"name": "bot", "scopes": []string{"agent"}}, &out)
	if code != 201 {
		t.Fatalf("create agent key → %d", code)
	}
	agentSecret := out["secret"].(string)
	join(t, ts, agentSecret, spaceID, "Bot")

	code = doJSON(t, "POST", ts.ts.URL+"/api/v1/objects/"+boardID+"/board", agentSecret, goodStroke(), &out)
	if code != 201 {
		t.Fatalf("agent draw → %d, want 201: %v", code, out)
	}
	if out["stroke"].(map[string]any)["participant_id"] == "" {
		t.Fatal("agent stroke missing participant_id")
	}
	code = doJSON(t, "DELETE", ts.ts.URL+"/api/v1/objects/"+boardID+"/board", agentSecret, nil, nil)
	if code != 403 {
		t.Fatalf("agent clear → %d, want 403", code)
	}

	// non-owner human: draws OK, clears forbidden
	code = doJSON(t, "POST", ts.ts.URL+"/api/v1/objects/"+boardID+"/board", otherTok, goodStroke(), nil)
	if code != 201 {
		t.Fatalf("non-owner draw → %d, want 201", code)
	}
	code = doJSON(t, "DELETE", ts.ts.URL+"/api/v1/objects/"+boardID+"/board", otherTok, nil, nil)
	if code != 403 {
		t.Fatalf("non-owner clear → %d, want 403", code)
	}

	// admin-scoped agent key: clears OK
	code = doJSON(t, "POST", ts.ts.URL+"/api/v1/agent-keys", ownerTok,
		map[string]any{"name": "admin-bot", "scopes": []string{"admin"}}, &out)
	if code != 201 {
		t.Fatalf("create admin key → %d", code)
	}
	adminSecret := out["secret"].(string)
	code = doJSON(t, "DELETE", ts.ts.URL+"/api/v1/objects/"+boardID+"/board", adminSecret, nil, nil)
	if code != 200 {
		t.Fatalf("admin clear → %d, want 200", code)
	}

	// owner clears OK (board has 0 strokes now; clear is idempotent)
	code = doJSON(t, "DELETE", ts.ts.URL+"/api/v1/objects/"+boardID+"/board", ownerTok, nil, nil)
	if code != 200 {
		t.Fatalf("owner clear → %d, want 200", code)
	}
}

func TestBoardWSFanout(t *testing.T) {
	ts := newTestStack(t)
	tokA := register(t, ts, "a@x.com", "Alice")
	tokB := register(t, ts, "b@x.com", "Bob")
	spaceID := createSpace(t, ts, tokA, "Board WS")
	ja := join(t, ts, tokA, spaceID, "Alice")
	jb := join(t, ts, tokB, spaceID, "Bob")
	boardID := createBoardObject(t, ts, tokA, spaceID, "whiteboard")

	ca := wsDial(t, ja.wsURL)
	defer wsClose(ca)
	cb := wsDial(t, jb.wsURL)
	defer wsClose(cb)
	if m := readMsg(t, ca, 5*time.Second); m["t"] != "hello" {
		t.Fatalf("A hello: %v", m["t"])
	}
	if m := readMsg(t, cb, 5*time.Second); m["t"] != "hello" {
		t.Fatalf("B hello: %v", m["t"])
	}

	// A draws via WS → B receives wb_stroke
	wsWrite(t, ca, map[string]any{
		"t": "wb_stroke", "object_id": boardID,
		"stroke": map[string]any{
			"color": "#00ff00", "width": 5,
			"points": []map[string]any{{"x": 100, "y": 100}, {"x": 200, "y": 200}},
		},
	})
	m := waitForMsg(t, cb, "wb_stroke", 5*time.Second)
	if m["object_id"] != boardID {
		t.Fatalf("wb_stroke object_id = %v, want %s", m["object_id"], boardID)
	}
	st := m["stroke"].(map[string]any)
	if st["color"] != "#00ff00" || st["width"].(float64) != 5 {
		t.Fatalf("wb_stroke payload mismatch: %v", st)
	}
	if st["id"] == "" || st["participant_id"] == "" {
		t.Fatalf("wb_stroke missing ids: %v", st)
	}
	if pts := st["points"].([]any); len(pts) != 2 {
		t.Fatalf("expected 2 points in fanout, got %d", len(pts))
	}

	// persisted: REST GET shows the stroke
	var out map[string]any
	code := doJSON(t, "GET", ts.ts.URL+"/api/v1/objects/"+boardID+"/board", tokA, nil, &out)
	if code != 200 || len(out["strokes"].([]any)) != 1 {
		t.Fatalf("GET after WS draw → %d: %v", code, out)
	}

	// malformed stroke (bad color) → error, connection stays alive
	wsWrite(t, ca, map[string]any{
		"t": "wb_stroke", "object_id": boardID,
		"stroke": map[string]any{"color": "nope", "width": 3,
			"points": []map[string]any{{"x": 1, "y": 1}, {"x": 2, "y": 2}}},
	})
	if m := waitForMsg(t, ca, "error", 3*time.Second); m["code"] != "bad_request" {
		t.Fatalf("expected bad_request, got %v", m)
	}
	wsWrite(t, ca, map[string]any{"t": "ping", "at": 7})
	if m := waitForMsg(t, ca, "pong", 3*time.Second); m["at"].(float64) != 7 {
		t.Fatalf("expected pong after error, got %v", m)
	}

	// non-owner B clears via WS → forbidden
	wsWrite(t, cb, map[string]any{"t": "wb_clear", "object_id": boardID})
	if m := waitForMsg(t, cb, "error", 3*time.Second); m["code"] != "forbidden" {
		t.Fatalf("expected forbidden clear for B, got %v", m)
	}

	// owner A clears → both receive wb_clear, board empty
	wsWrite(t, ca, map[string]any{"t": "wb_clear", "object_id": boardID})
	if m := waitForMsg(t, cb, "wb_clear", 5*time.Second); m["object_id"] != boardID {
		t.Fatalf("B wb_clear: %v", m)
	}
	if m := waitForMsg(t, ca, "wb_clear", 5*time.Second); m["object_id"] != boardID {
		t.Fatalf("A wb_clear: %v", m)
	}
	code = doJSON(t, "GET", ts.ts.URL+"/api/v1/objects/"+boardID+"/board", tokA, nil, &out)
	if code != 200 || len(out["strokes"].([]any)) != 0 {
		t.Fatalf("expected empty board after WS clear, got %d: %v", code, out)
	}
}
