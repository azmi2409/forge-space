package main

// Tool tests against a stub forge-space server. Every test asserts the exact
// REST path, method, and body per CONTRACT §3. No network leaves the process.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordedCall struct {
	method string
	path   string
	query  url.Values
	body   []byte
	auth   string
}

type fakeForge struct {
	t            *testing.T
	mu           sync.Mutex
	calls        []recordedCall
	streamStatus int // HTTP status for GET /api/v1/stream (default 403: ticket rejected)
	chatStatus   int // 0 = 201 success for POST chat; else error status
}

func pt(id, kind, name string, x, y float64) map[string]any {
	return map[string]any{
		"id": id, "space_id": "sp_abc", "map_id": "mp_1", "kind": kind,
		"display_name": name, "x": x, "y": y, "dir": "down", "emote": "",
		"ghost": false, "muted": false, "camera_off": false, "speaking": false,
		"status": "", "area_id": "", "updated_at": "2026-10-16T10:00:00Z",
	}
}

func (f *fakeForge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.calls = append(f.calls, recordedCall{r.Method, r.URL.Path, r.URL.Query(), body, r.Header.Get("Authorization")})
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	write := func(status int, v any) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(v)
	}
	errResp := func(status int, code, msg string) {
		write(status, map[string]any{"error": map[string]any{"code": code, "message": msg}})
	}

	participants := []any{
		pt("pt_me", "agent", "AgentBot", 10, 10),
		withSpeaking(pt("pt_near", "human", "Near Human", 12, 10)),
		pt("pt_far", "human", "Far Human", 30, 33),
		pt("pt_spot", "human", "Star", 30, 30),
		pt("pt_priv", "human", "Priv", 20, 20),
		withGhost(pt("pt_ghost", "agent", "Ghost", 10.5, 10)),
	}
	areas := []any{
		map[string]any{"id": "ar_priv", "map_id": "mp_1", "kind": "private", "name": "Quiet Room", "x": 18.0, "y": 18.0, "w": 4.0, "h": 4.0},
		map[string]any{"id": "ar_spot", "map_id": "mp_1", "kind": "spotlight", "name": "Stage", "x": 28.0, "y": 28.0, "w": 4.0, "h": 4.0},
	}
	objects := []any{
		map[string]any{"id": "ob_note", "map_id": "mp_1", "kind": "note", "x": 11.0, "y": 11.0, "w": 1.0, "h": 1.0, "title": "Welcome", "body": "Hello ", "solid": false},
		map[string]any{"id": "ob_far", "map_id": "mp_1", "kind": "note", "x": 40.0, "y": 40.0, "w": 1.0, "h": 1.0, "title": "Far", "body": "far", "solid": false},
		map[string]any{"id": "ob_portal", "map_id": "mp_1", "kind": "portal", "x": 20.0, "y": 9.0, "w": 1.0, "h": 1.0, "title": "To Garden", "body": "", "target_map_id": "mp_2", "target_x": 5.0, "target_y": 5.0, "solid": false},
	}

	switch {
	case r.Method == "GET" && r.URL.Path == "/api/v1/spaces":
		write(200, map[string]any{"spaces": []any{
			map[string]any{"id": "sp_abc", "name": "Lobby Space", "slug": "lobby-space", "settings": map[string]any{"proximity_radius": 5.0}},
		}})
	case r.Method == "POST" && r.URL.Path == "/api/v1/spaces/sp_abc/join":
		write(200, map[string]any{
			"participant": pt("pt_me", "agent", "AgentBot", 10, 10),
			"ticket":      "tk_test", "ws_url": "ws://x/ws?ticket=tk_test", "livekit": nil,
		})
	case r.Method == "POST" && r.URL.Path == "/api/v1/spaces/sp_abc/leave":
		write(200, map[string]any{})
	case r.Method == "GET" && r.URL.Path == "/api/v1/spaces/sp_abc/participants":
		write(200, map[string]any{"participants": participants})
	case r.Method == "GET" && r.URL.Path == "/api/v1/spaces/sp_abc":
		write(200, map[string]any{
			"space": map[string]any{"id": "sp_abc", "name": "Lobby Space", "slug": "lobby-space", "settings": map[string]any{"proximity_radius": 5.0}},
			"maps":  []any{map[string]any{"id": "mp_1", "space_id": "sp_abc", "name": "Lobby", "width": 40, "height": 30}},
		})
	case r.Method == "GET" && r.URL.Path == "/api/v1/spaces/sp_abc/areas":
		write(200, map[string]any{"areas": areas})
	case r.Method == "GET" && r.URL.Path == "/api/v1/spaces/sp_abc/objects":
		write(200, map[string]any{"objects": objects})
	case r.Method == "GET" && r.URL.Path == "/api/v1/spaces/sp_abc/maps/mp_1":
		tiles := make([]int, 40*30)
		for i := range tiles {
			tiles[i] = 1
		}
		for x := 0; x < 40; x++ {
			tiles[x] = 2
			tiles[(30-1)*40+x] = 2
		}
		for y := 0; y < 30; y++ {
			tiles[y*40] = 2
			tiles[y*40+39] = 2
		}
		write(200, map[string]any{"map": map[string]any{
			"id": "mp_1", "space_id": "sp_abc", "name": "Lobby",
			"width": 40, "height": 30, "tiles": tiles,
			"spawn": map[string]any{"x": 10.0, "y": 10.0},
		}})
	case r.Method == "GET" && r.URL.Path == "/api/v1/spaces/sp_abc/chat":
		write(200, map[string]any{"messages": []any{
			map[string]any{"id": "cm_1", "space_id": "sp_abc", "from_participant_id": "pt_near",
				"from_name": "Near Human", "from_kind": "human", "scope": "nearby",
				"text": "hello there", "created_at": "2026-10-16T10:01:00Z"},
		}})
	case r.Method == "POST" && r.URL.Path == "/api/v1/spaces/sp_abc/chat":
		if f.chatStatus != 0 {
			errResp(f.chatStatus, "forbidden", "admin scope required")
			return
		}
		var b map[string]any
		_ = json.Unmarshal(body, &b)
		write(201, map[string]any{"message": map[string]any{
			"id": "cm_9", "space_id": "sp_abc", "from_participant_id": "pt_me",
			"from_name": "AgentBot", "from_kind": "agent", "scope": b["scope"],
			"text": b["text"], "created_at": "2026-10-16T10:02:00Z"}})
	case r.Method == "POST" && r.URL.Path == "/api/v1/participants/me/move":
		var b map[string]any
		_ = json.Unmarshal(body, &b)
		p := pt("pt_me", "agent", "AgentBot", b["x"].(float64), b["y"].(float64))
		write(200, map[string]any{"participant": p})
	case r.Method == "POST" && r.URL.Path == "/api/v1/participants/me/state":
		write(200, map[string]any{})
	case r.Method == "POST" && r.URL.Path == "/api/v1/participants/me/portal":
		p := pt("pt_me", "agent", "AgentBot", 5, 5)
		p["map_id"] = "mp_2"
		write(200, map[string]any{"participant": p})
	case r.Method == "PATCH" && r.URL.Path == "/api/v1/objects/ob_note":
		write(200, map[string]any{})
	case r.Method == "GET" && r.URL.Path == "/api/v1/media/token":
		write(200, map[string]any{"url": "https://livekit.example", "token": "lk_tok"})
	case r.Method == "GET" && r.URL.Path == "/api/v1/stream":
		if f.streamStatus != 200 {
			errResp(f.streamStatus, "forbidden", "bad ticket")
			return
		}
		fmt.Fprintln(w, `{"t":"hello"}`)
	case r.Method == "DELETE" && r.URL.Path == "/api/v1/spaces/sp_abc/participants/pt_x":
		write(200, map[string]any{})
	case r.Method == "GET" && r.URL.Path == "/api/v1/objects/ob_wb/board":
		write(200, map[string]any{"strokes": []any{
			map[string]any{"id": "wbs_1", "object_id": "ob_wb", "participant_id": "pt_near",
				"color": "#000000", "width": 3.0,
				"points": []any{map[string]any{"x": 100.0, "y": 100.0}, map[string]any{"x": 200.0, "y": 200.0}},
				"created_at": "2026-10-23T10:00:00Z"},
		}})
	case r.Method == "POST" && r.URL.Path == "/api/v1/objects/ob_wb/board":
		var b map[string]any
		_ = json.Unmarshal(body, &b)
		write(201, map[string]any{"stroke": map[string]any{
			"id": "wbs_9", "object_id": "ob_wb", "participant_id": "pt_me",
			"color": b["color"], "width": b["width"], "points": b["points"],
			"created_at": "2026-10-23T10:01:00Z"}})
	case r.Method == "DELETE" && r.URL.Path == "/api/v1/objects/ob_wb/board":
		write(200, map[string]any{})
	case r.Method == "DELETE" && r.URL.Path == "/api/v1/objects/ob_locked/board":
		errResp(403, "forbidden", "owner or admin scope required")
		return
	default:
		errResp(404, "not_found", "no stub for "+r.Method+" "+r.URL.Path)
	}
}

func withSpeaking(p map[string]any) map[string]any { p["speaking"] = true; return p }
func withGhost(p map[string]any) map[string]any    { p["ghost"] = true; return p }

// ---- harness ----

func newTestEnv(t *testing.T) (*fakeForge, *MCPServer) {
	t.Helper()
	f := &fakeForge{t: t, streamStatus: 403}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, NewMCPServer(srv.URL, "fspk_test")
}

// callTool invokes a tool via handleToolCall; returns (resultText, isError).
func callTool(t *testing.T, s *MCPServer, name string, args map[string]any) (string, bool) {
	t.Helper()
	argsRaw := json.RawMessage([]byte("{}"))
	if args != nil {
		b, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		argsRaw = b
	}
	params, _ := json.Marshal(map[string]any{"name": name, "arguments": argsRaw})
	id := json.RawMessage(`99`)
	respBytes := s.handleToolCall(&id, params)
	var resp struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		t.Fatalf("unmarshal tool response: %v", err)
	}
	if len(resp.Result.Content) != 1 {
		t.Fatalf("expected 1 content block: %s", respBytes)
	}
	return resp.Result.Content[0].Text, resp.Result.IsError
}

func mustJSON(t *testing.T, text string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("result not JSON: %v\n%s", err, text)
	}
	return out
}

// mustJoin joins the stub space and waits for the SSE stream to settle (the
// stub rejects the ticket with 403, so the stream must reach "dead" without
// retrying).
func mustJoin(t *testing.T, s *MCPServer) {
	t.Helper()
	text, isErr := callTool(t, s, "space_join", map[string]any{"space_id": "sp_abc", "display_name": "AgentBot"})
	if isErr {
		t.Fatalf("space_join failed: %s", text)
	}
	out := mustJSON(t, text)
	p := out["participant"].(map[string]any)
	if p["id"] != "pt_me" {
		t.Fatalf("participant = %v", p)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		st := ""
		if s.sess != nil {
			st = s.sess.state()
		}
		s.mu.Unlock()
		if st == "dead" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("SSE stream did not reach dead state after ticket rejection")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (f *fakeForge) lastCall(method, path string) (recordedCall, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.calls) - 1; i >= 0; i-- {
		if f.calls[i].method == method && f.calls[i].path == path {
			return f.calls[i], true
		}
	}
	return recordedCall{}, false
}

func (f *fakeForge) countCalls(method, path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.method == method && c.path == path {
			n++
		}
	}
	return n
}

func assertBody(t *testing.T, call recordedCall, want map[string]any) {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(call.body, &got); err != nil {
		t.Fatalf("request body not JSON: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("body = %v, want %v", got, want)
	}
}

func idSet(t *testing.T, arr []any, key string) map[string]bool {
	t.Helper()
	m := map[string]bool{}
	for _, item := range arr {
		m[item.(map[string]any)[key].(string)] = true
	}
	return m
}

// ---- tests ----

func TestSpaceList(t *testing.T) {
	f, s := newTestEnv(t)
	text, isErr := callTool(t, s, "space_list", nil)
	if isErr {
		t.Fatalf("space_list failed: %s", text)
	}
	out := mustJSON(t, text)
	spaces := out["spaces"].([]any)
	if len(spaces) != 1 || spaces[0].(map[string]any)["id"] != "sp_abc" {
		t.Fatalf("spaces = %v", spaces)
	}
	call, ok := f.lastCall("GET", "/api/v1/spaces")
	if !ok {
		t.Fatal("GET /api/v1/spaces not called")
	}
	if call.auth != "Bearer fspk_test" {
		t.Fatalf("auth header = %q", call.auth)
	}
}

func TestSpaceJoinValidation(t *testing.T) {
	f, s := newTestEnv(t)
	for _, name := range []string{"", strings.Repeat("x", 31)} {
		_, isErr := callTool(t, s, "space_join", map[string]any{"space_id": "sp_abc", "display_name": name})
		if !isErr {
			t.Fatalf("expected error for display_name %q", name)
		}
	}
	if len(f.calls) != 0 {
		t.Fatalf("expected no HTTP calls on validation failure, got %d", len(f.calls))
	}
}

func TestSpaceJoinRequest(t *testing.T) {
	f, s := newTestEnv(t)
	mustJoin(t, s)
	call, ok := f.lastCall("POST", "/api/v1/spaces/sp_abc/join")
	if !ok {
		t.Fatal("POST /api/v1/spaces/sp_abc/join not called")
	}
	assertBody(t, call, map[string]any{"display_name": "AgentBot"})
	if n := f.countCalls("GET", "/api/v1/stream"); n != 1 {
		t.Fatalf("expected exactly 1 stream attempt (no retry on 403), got %d", n)
	}
}

func TestSpaceLeave(t *testing.T) {
	f, s := newTestEnv(t)
	mustJoin(t, s)
	text, isErr := callTool(t, s, "space_leave", nil)
	if isErr {
		t.Fatalf("space_leave failed: %s", text)
	}
	if _, ok := f.lastCall("POST", "/api/v1/spaces/sp_abc/leave"); !ok {
		t.Fatal("POST /api/v1/spaces/sp_abc/leave not called")
	}
	s.mu.Lock()
	joined := s.sess != nil
	s.mu.Unlock()
	if joined {
		t.Fatal("session not cleared after space_leave")
	}
	_, isErr = callTool(t, s, "me_where", nil)
	if !isErr {
		t.Fatal("expected not-in-space error after leave")
	}
}

func TestMeWhere(t *testing.T) {
	f, s := newTestEnv(t)
	mustJoin(t, s)
	text, isErr := callTool(t, s, "me_where", nil)
	if isErr {
		t.Fatalf("me_where failed: %s", text)
	}
	out := mustJSON(t, text)
	if out["id"] != "pt_me" || out["x"] != 10.0 || out["y"] != 10.0 {
		t.Fatalf("me_where = %v", out)
	}
	if _, ok := f.lastCall("GET", "/api/v1/spaces/sp_abc/participants"); !ok {
		t.Fatal("GET participants not called")
	}
}

func TestMeMoveTo(t *testing.T) {
	f, s := newTestEnv(t)
	mustJoin(t, s)
	text, isErr := callTool(t, s, "me_move_to", map[string]any{"x": 3.0, "y": 4.0})
	if isErr {
		t.Fatalf("me_move_to failed: %s", text)
	}
	call, ok := f.lastCall("POST", "/api/v1/participants/me/move")
	if !ok {
		t.Fatal("POST /api/v1/participants/me/move not called")
	}
	assertBody(t, call, map[string]any{"x": 3.0, "y": 4.0})
	out := mustJSON(t, text)
	if out["x"] != 3.0 || out["y"] != 4.0 {
		t.Fatalf("result = %v", out)
	}
}

func TestMeStep(t *testing.T) {
	f, s := newTestEnv(t)
	mustJoin(t, s)
	// me is at (10,10); step right 2 -> (12,10)
	if _, isErr := callTool(t, s, "me_step", map[string]any{"direction": "right", "tiles": 2}); isErr {
		t.Fatal("me_step right failed")
	}
	call, ok := f.lastCall("POST", "/api/v1/participants/me/move")
	if !ok {
		t.Fatal("move not called")
	}
	assertBody(t, call, map[string]any{"x": 12.0, "y": 10.0})
	// up decreases y: (10,10) -> (10,9)
	if _, isErr := callTool(t, s, "me_step", map[string]any{"direction": "up"}); isErr {
		t.Fatal("me_step up failed")
	}
	call, _ = f.lastCall("POST", "/api/v1/participants/me/move")
	assertBody(t, call, map[string]any{"x": 10.0, "y": 9.0})
	// invalid direction: tool error, no move call
	nBefore := f.countCalls("POST", "/api/v1/participants/me/move")
	if _, isErr := callTool(t, s, "me_step", map[string]any{"direction": "diagonal"}); !isErr {
		t.Fatal("expected error for bad direction")
	}
	if n := f.countCalls("POST", "/api/v1/participants/me/move"); n != nBefore {
		t.Fatal("move called despite invalid direction")
	}
}

func TestMeSayNearby(t *testing.T) {
	f, s := newTestEnv(t)
	mustJoin(t, s)
	text, isErr := callTool(t, s, "me_say", map[string]any{"text": "hi all"})
	if isErr {
		t.Fatalf("me_say failed: %s", text)
	}
	call, ok := f.lastCall("POST", "/api/v1/spaces/sp_abc/chat")
	if !ok {
		t.Fatal("POST chat not called")
	}
	assertBody(t, call, map[string]any{"scope": "nearby", "text": "hi all"})
	out := mustJSON(t, text)
	if out["message"].(map[string]any)["text"] != "hi all" {
		t.Fatalf("result = %v", out)
	}
}

func TestMeSayDM(t *testing.T) {
	f, s := newTestEnv(t)
	mustJoin(t, s)
	_, isErr := callTool(t, s, "me_say", map[string]any{"scope": "dm", "to": "pt_near", "text": "secret"})
	if isErr {
		t.Fatal("me_say dm failed")
	}
	call, _ := f.lastCall("POST", "/api/v1/spaces/sp_abc/chat")
	assertBody(t, call, map[string]any{"scope": "dm", "to": "pt_near", "text": "secret"})
	// dm without recipient is a tool error before any HTTP call
	nBefore := f.countCalls("POST", "/api/v1/spaces/sp_abc/chat")
	if _, isErr := callTool(t, s, "me_say", map[string]any{"scope": "dm", "text": "x"}); !isErr {
		t.Fatal("expected error for dm without to")
	}
	if n := f.countCalls("POST", "/api/v1/spaces/sp_abc/chat"); n != nBefore {
		t.Fatal("chat posted despite missing dm recipient")
	}
	// text over 500 chars is rejected client-side
	if _, isErr := callTool(t, s, "me_say", map[string]any{"text": strings.Repeat("x", 501)}); !isErr {
		t.Fatal("expected error for 501-char text")
	}
}

func TestMeEmoteAndGhost(t *testing.T) {
	f, s := newTestEnv(t)
	mustJoin(t, s)
	if _, isErr := callTool(t, s, "me_emote", map[string]any{"name": "dance"}); isErr {
		t.Fatal("me_emote failed")
	}
	call, _ := f.lastCall("POST", "/api/v1/participants/me/state")
	assertBody(t, call, map[string]any{"emote": "dance"})
	if _, isErr := callTool(t, s, "me_ghost", map[string]any{"on": true}); isErr {
		t.Fatal("me_ghost failed")
	}
	call, _ = f.lastCall("POST", "/api/v1/participants/me/state")
	assertBody(t, call, map[string]any{"ghost": true})
	if _, isErr := callTool(t, s, "me_speaking", map[string]any{"on": true}); isErr {
		t.Fatal("me_speaking failed")
	}
	call, _ = f.lastCall("POST", "/api/v1/participants/me/state")
	assertBody(t, call, map[string]any{"speaking": true})
}

func TestWorldLook(t *testing.T) {
	f, s := newTestEnv(t)
	mustJoin(t, s)
	text, isErr := callTool(t, s, "world_look", nil)
	if isErr {
		t.Fatalf("world_look failed: %s", text)
	}
	for _, p := range []string{"participants", "space", "areas", "objects"} {
		path := "/api/v1/spaces/sp_abc"
		if p != "space" {
			path += "/" + p
		}
		if _, ok := f.lastCall("GET", path); !ok {
			t.Fatalf("GET %s not called", path)
		}
	}
	out := mustJSON(t, text)
	// audible set: pt_near (in radius) + pt_spot (spotlight); NOT pt_far,
	// pt_priv (private isolation), pt_ghost, or self
	gotIDs := idSet(t, out["participants"].([]any), "id")
	want := map[string]bool{"pt_near": true, "pt_spot": true}
	if !reflect.DeepEqual(gotIDs, want) {
		t.Fatalf("audible participants = %v, want %v", gotIDs, want)
	}
	// objects within 2.5 tiles of (10,10): ob_note only
	objIDs := idSet(t, out["objects_in_range"].([]any), "id")
	if !reflect.DeepEqual(objIDs, map[string]bool{"ob_note": true}) {
		t.Fatalf("objects_in_range = %v", objIDs)
	}
	obj := out["objects_in_range"].([]any)[0].(map[string]any)
	if obj["kind"] != "note" || obj["title"] != "Welcome" {
		t.Fatalf("object = %v", obj)
	}
	if out["my_area"] != nil {
		t.Fatalf("my_area = %v, want null", out["my_area"])
	}
	// participant entries carry the §8 fields
	p0 := out["participants"].([]any)[0].(map[string]any)
	for _, k := range []string{"id", "name", "kind", "x", "y", "speaking", "area"} {
		if _, ok := p0[k]; !ok {
			t.Fatalf("participant missing field %q: %v", k, p0)
		}
	}
}

func TestWorldListen(t *testing.T) {
	f, s := newTestEnv(t)
	mustJoin(t, s)
	// inject an SSE event as the background reader would
	s.mu.Lock()
	s.sess.events.add("presence", map[string]any{"t": "presence", "kind": "join"})
	s.mu.Unlock()
	text, isErr := callTool(t, s, "world_listen", map[string]any{"limit": 5})
	if isErr {
		t.Fatalf("world_listen failed: %s", text)
	}
	call, ok := f.lastCall("GET", "/api/v1/spaces/sp_abc/chat")
	if !ok {
		t.Fatal("GET chat not called")
	}
	if call.query.Get("limit") != "5" {
		t.Fatalf("limit query = %q", call.query.Get("limit"))
	}
	out := mustJSON(t, text)
	chat := out["chat"].([]any)
	if len(chat) != 1 || chat[0].(map[string]any)["text"] != "hello there" {
		t.Fatalf("chat = %v", chat)
	}
	events := out["events"].([]any)
	if len(events) != 1 || events[0].(map[string]any)["kind"] != "join" {
		t.Fatalf("events = %v", events)
	}
	if out["stream"] != "dead" {
		t.Fatalf("stream = %v, want dead (stub rejected ticket)", out["stream"])
	}
}

func TestObjectRead(t *testing.T) {
	f, s := newTestEnv(t)
	mustJoin(t, s)
	text, isErr := callTool(t, s, "object_read", map[string]any{"id": "ob_note"})
	if isErr {
		t.Fatalf("object_read failed: %s", text)
	}
	obj := mustJSON(t, text)["object"].(map[string]any)
	if obj["title"] != "Welcome" || obj["body"] != "Hello " {
		t.Fatalf("object = %v", obj)
	}
	if _, ok := f.lastCall("GET", "/api/v1/spaces/sp_abc/objects"); !ok {
		t.Fatal("GET objects not called")
	}
	if _, isErr := callTool(t, s, "object_read", map[string]any{"id": "ob_nope"}); !isErr {
		t.Fatal("expected not-found error")
	}
}

func TestObjectWrite(t *testing.T) {
	f, s := newTestEnv(t)
	mustJoin(t, s)
	// append concatenates onto the existing body "Hello "
	if _, isErr := callTool(t, s, "object_write", map[string]any{"id": "ob_note", "text": "world"}); isErr {
		t.Fatal("object_write append failed")
	}
	call, ok := f.lastCall("PATCH", "/api/v1/objects/ob_note")
	if !ok {
		t.Fatal("PATCH /api/v1/objects/ob_note not called")
	}
	assertBody(t, call, map[string]any{"body": "Hello world"})
	// replace overwrites
	if _, isErr := callTool(t, s, "object_write", map[string]any{"id": "ob_note", "text": "brand new", "mode": "replace"}); isErr {
		t.Fatal("object_write replace failed")
	}
	call, _ = f.lastCall("PATCH", "/api/v1/objects/ob_note")
	assertBody(t, call, map[string]any{"body": "brand new"})
	// bad mode is rejected before any HTTP call
	nBefore := f.countCalls("PATCH", "/api/v1/objects/ob_note")
	if _, isErr := callTool(t, s, "object_write", map[string]any{"id": "ob_note", "text": "x", "mode": "prepend"}); !isErr {
		t.Fatal("expected error for bad mode")
	}
	if n := f.countCalls("PATCH", "/api/v1/objects/ob_note"); n != nBefore {
		t.Fatal("PATCH called despite bad mode")
	}
}

func TestBoardRead(t *testing.T) {
	_, s := newTestEnv(t)
	mustJoin(t, s)
	text, isErr := callTool(t, s, "board_read", map[string]any{"id": "ob_wb"})
	if isErr {
		t.Fatalf("board_read failed: %s", text)
	}
	out := mustJSON(t, text)
	strokes, ok := out["strokes"].([]any)
	if !ok || len(strokes) != 1 {
		t.Fatalf("strokes = %v", out["strokes"])
	}
	st := strokes[0].(map[string]any)
	if st["id"] != "wbs_1" || st["color"] != "#000000" {
		t.Fatalf("stroke = %v", st)
	}
	// missing id is rejected before any HTTP call
	if _, isErr := callTool(t, s, "board_read", map[string]any{}); !isErr {
		t.Fatal("expected error for missing id")
	}
}

func TestBoardDraw(t *testing.T) {
	f, s := newTestEnv(t)
	mustJoin(t, s)
	pts := []any{map[string]any{"x": 10.0, "y": 20.0}, map[string]any{"x": 30.0, "y": 40.0}}
	text, isErr := callTool(t, s, "board_draw", map[string]any{
		"id": "ob_wb", "color": "#ef4444", "width": 5.0, "points": pts,
	})
	if isErr {
		t.Fatalf("board_draw failed: %s", text)
	}
	out := mustJSON(t, text)
	st, ok := out["stroke"].(map[string]any)
	if !ok || st["id"] != "wbs_9" {
		t.Fatalf("stroke = %v", out["stroke"])
	}
	call, ok := f.lastCall("POST", "/api/v1/objects/ob_wb/board")
	if !ok {
		t.Fatal("POST /api/v1/objects/ob_wb/board not called")
	}
	assertBody(t, call, map[string]any{"color": "#ef4444", "width": 5.0, "points": pts})
	// defaults: color #000000, width 3
	if _, isErr := callTool(t, s, "board_draw", map[string]any{"id": "ob_wb", "points": pts}); isErr {
		t.Fatal("board_draw with defaults failed")
	}
	call, _ = f.lastCall("POST", "/api/v1/objects/ob_wb/board")
	assertBody(t, call, map[string]any{"color": "#000000", "width": 3.0, "points": pts})
	// fewer than 2 points is rejected before any HTTP call
	nBefore := f.countCalls("POST", "/api/v1/objects/ob_wb/board")
	if _, isErr := callTool(t, s, "board_draw", map[string]any{
		"id": "ob_wb", "points": []any{map[string]any{"x": 1.0, "y": 1.0}},
	}); !isErr {
		t.Fatal("expected error for single point")
	}
	if n := f.countCalls("POST", "/api/v1/objects/ob_wb/board"); n != nBefore {
		t.Fatal("POST called despite bad points")
	}
}

func TestBoardClear(t *testing.T) {
	f, s := newTestEnv(t)
	mustJoin(t, s)
	text, isErr := callTool(t, s, "board_clear", map[string]any{"id": "ob_wb"})
	if isErr {
		t.Fatalf("board_clear failed: %s", text)
	}
	if _, ok := f.lastCall("DELETE", "/api/v1/objects/ob_wb/board"); !ok {
		t.Fatal("DELETE /api/v1/objects/ob_wb/board not called")
	}
	// 403 from the server surfaces as a tool error (no retry)
	text, isErr = callTool(t, s, "board_clear", map[string]any{"id": "ob_locked"})
	if !isErr {
		t.Fatal("expected error for forbidden clear")
	}
	if !strings.Contains(text, "403") && !strings.Contains(text, "forbidden") {
		t.Fatalf("error should mention 403/forbidden: %s", text)
	}
}

func TestPortalUse(t *testing.T) {
	f, s := newTestEnv(t)
	mustJoin(t, s)
	text, isErr := callTool(t, s, "portal_use", map[string]any{"id": "ob_portal"})
	if isErr {
		t.Fatalf("portal_use failed: %s", text)
	}
	call, ok := f.lastCall("POST", "/api/v1/participants/me/portal")
	if !ok {
		t.Fatal("POST /api/v1/participants/me/portal not called")
	}
	assertBody(t, call, map[string]any{"object_id": "ob_portal"})
	out := mustJSON(t, text)
	if out["map_id"] != "mp_2" || out["x"] != 5.0 || out["y"] != 5.0 {
		t.Fatalf("result = %v", out)
	}
}

func TestParticipantsList(t *testing.T) {
	_, s := newTestEnv(t)
	mustJoin(t, s)
	text, isErr := callTool(t, s, "participants_list", nil)
	if isErr {
		t.Fatalf("participants_list failed: %s", text)
	}
	parts := mustJSON(t, text)["participants"].([]any)
	if len(parts) != 6 {
		t.Fatalf("participants = %d, want 6", len(parts))
	}
}

func TestMapGet(t *testing.T) {
	f, s := newTestEnv(t)
	mustJoin(t, s)
	text, isErr := callTool(t, s, "map_get", nil)
	if isErr {
		t.Fatalf("map_get failed: %s", text)
	}
	if _, ok := f.lastCall("GET", "/api/v1/spaces/sp_abc/maps/mp_1"); !ok {
		t.Fatal("GET map not called")
	}
	m := mustJSON(t, text)["map"].(map[string]any)
	if m["width"] != 40.0 || m["height"] != 30.0 {
		t.Fatalf("map = %v", m)
	}
	if len(m["tiles"].([]any)) != 1200 {
		t.Fatalf("tiles len = %d", len(m["tiles"].([]any)))
	}
}

func TestMediaToken(t *testing.T) {
	f, s := newTestEnv(t)
	mustJoin(t, s)
	text, isErr := callTool(t, s, "media_token", nil)
	if isErr {
		t.Fatalf("media_token failed: %s", text)
	}
	if _, ok := f.lastCall("GET", "/api/v1/media/token"); !ok {
		t.Fatal("GET /api/v1/media/token not called")
	}
	out := mustJSON(t, text)
	if out["url"] != "https://livekit.example" || out["token"] != "lk_tok" {
		t.Fatalf("result = %v", out)
	}
}

func TestAdminBroadcast(t *testing.T) {
	f, s := newTestEnv(t)
	mustJoin(t, s)
	text, isErr := callTool(t, s, "admin_broadcast", map[string]any{"text": "Attention"})
	if isErr {
		t.Fatalf("admin_broadcast failed: %s", text)
	}
	call, ok := f.lastCall("POST", "/api/v1/spaces/sp_abc/chat")
	if !ok {
		t.Fatal("POST chat not called")
	}
	assertBody(t, call, map[string]any{"scope": "space", "announce": true, "text": "Attention"})
	out := mustJSON(t, text)
	if out["message"].(map[string]any)["text"] != "Attention" {
		t.Fatalf("result = %v", out)
	}
}

func TestAdminBroadcast403NoRetry(t *testing.T) {
	f, s := newTestEnv(t)
	f.chatStatus = 403
	mustJoin(t, s)
	text, isErr := callTool(t, s, "admin_broadcast", map[string]any{"text": "Attention"})
	if !isErr {
		t.Fatal("expected error on 403")
	}
	if !strings.Contains(text, "forbidden") || !strings.Contains(text, "admin scope required") {
		t.Fatalf("error should surface server code+message cleanly: %s", text)
	}
	if n := f.countCalls("POST", "/api/v1/spaces/sp_abc/chat"); n != 1 {
		t.Fatalf("expected exactly 1 attempt (no retry on 403), got %d", n)
	}
}

func TestAdminKick(t *testing.T) {
	f, s := newTestEnv(t)
	mustJoin(t, s)
	text, isErr := callTool(t, s, "admin_kick", map[string]any{"participant_id": "pt_x"})
	if isErr {
		t.Fatalf("admin_kick failed: %s", text)
	}
	if _, ok := f.lastCall("DELETE", "/api/v1/spaces/sp_abc/participants/pt_x"); !ok {
		t.Fatal("DELETE participant not called")
	}
	out := mustJSON(t, text)
	if out["ok"] != true || out["participant_id"] != "pt_x" {
		t.Fatalf("result = %v", out)
	}
}

func TestToolsRequireSession(t *testing.T) {
	_, s := newTestEnv(t) // never joined
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"me_where", nil}, {"me_move_to", map[string]any{"x": 1.0, "y": 1.0}},
		{"world_look", nil}, {"participants_list", nil}, {"admin_kick", map[string]any{"participant_id": "pt_x"}},
	} {
		text, isErr := callTool(t, s, tc.name, tc.args)
		if !isErr {
			t.Fatalf("%s: expected not-in-space error", tc.name)
		}
		if !strings.Contains(text, "space_join") {
			t.Fatalf("%s: error should mention space_join: %s", tc.name, text)
		}
	}
}
