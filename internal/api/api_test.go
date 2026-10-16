package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"nhooyr.io/websocket"

	"github.com/azmi2409/forge-space/internal/config"
	"github.com/azmi2409/forge-space/internal/livekitx"
	"github.com/azmi2409/forge-space/internal/realtime"
	"github.com/azmi2409/forge-space/internal/store"
)

// ---------- harness ----------

type testStack struct {
	ts    *httptest.Server
	srv   *Server
	store *store.Store
	hub   *realtime.Hub
}

func newTestStack(t *testing.T) *testStack {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Load()
	hub := realtime.New(st)
	go hub.Run()
	lk := &livekitx.Client{} // media disabled
	srv := New(st, hub, lk, cfg)
	ts := httptest.NewServer(srv.Router())
	t.Cleanup(func() {
		ts.Close()
		hub.Close()
		st.Close()
	})
	return &testStack{ts: ts, srv: srv, store: st, hub: hub}
}

func doJSON(t *testing.T, method, url, token string, body any, out any) int {
	t.Helper()
	var rdr *strings.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = strings.NewReader(string(b))
	} else {
		rdr = strings.NewReader("")
	}
	req, _ := http.NewRequest(method, url, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func register(t *testing.T, ts *testStack, email, name string) string {
	t.Helper()
	var out map[string]any
	code := doJSON(t, "POST", ts.ts.URL+"/api/v1/auth/register",
		"", map[string]string{"email": email, "password": "password123", "display_name": name}, &out)
	if code != 201 {
		t.Fatalf("register → %d: %v", code, out)
	}
	code = doJSON(t, "POST", ts.ts.URL+"/api/v1/auth/login",
		"", map[string]string{"email": email, "password": "password123"}, &out)
	if code != 200 {
		t.Fatalf("login → %d: %v", code, out)
	}
	tok, _ := out["token"].(string)
	if tok == "" {
		t.Fatal("no token returned")
	}
	return tok
}

func createSpace(t *testing.T, ts *testStack, token, name string) string {
	t.Helper()
	var out map[string]any
	code := doJSON(t, "POST", ts.ts.URL+"/api/v1/spaces", token, map[string]string{"name": name}, &out)
	if code != 201 {
		t.Fatalf("create space → %d: %v", code, out)
	}
	sp := out["space"].(map[string]any)
	return sp["id"].(string)
}

type joinResult struct {
	participant map[string]any
	ticket      string
	wsURL       string
}

func join(t *testing.T, ts *testStack, token, spaceID, name string) joinResult {
	t.Helper()
	var out map[string]any
	code := doJSON(t, "POST", ts.ts.URL+"/api/v1/spaces/"+spaceID+"/join", token,
		map[string]string{"display_name": name}, &out)
	if code != 200 {
		t.Fatalf("join → %d: %v", code, out)
	}
	return joinResult{
		participant: out["participant"].(map[string]any),
		ticket:      out["ticket"].(string),
		wsURL:       out["ws_url"].(string),
	}
}

func wsDial(t *testing.T, url string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	return conn
}

// readMsg reads one text message with a timeout.
func readMsg(t *testing.T, conn *websocket.Conn, timeout time.Duration) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}
	if typ != websocket.MessageText {
		t.Fatalf("expected text message, got %v", typ)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	return m
}

func wsWrite(t *testing.T, conn *websocket.Conn, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("ws write: %v", err)
	}
}

func wsClose(conn *websocket.Conn) {
	_ = conn.Close(websocket.StatusNormalClosure, "test done")
}

// waitForMsg skips messages until one with t==want arrives (or timeout).
func waitForMsg(t *testing.T, conn *websocket.Conn, want string, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		m := readMsg(t, conn, time.Until(deadline))
		if m["t"] == want {
			return m
		}
	}
	t.Fatalf("timed out waiting for %q message", want)
	return nil
}

// ---------- tests ----------

func TestRegisterLoginFlow(t *testing.T) {
	ts := newTestStack(t)
	var out map[string]any

	// duplicate email → 409
	register(t, ts, "a@x.com", "Alice")
	code := doJSON(t, "POST", ts.ts.URL+"/api/v1/auth/register", "",
		map[string]string{"email": "a@x.com", "password": "password123", "display_name": "Alice2"}, &out)
	if code != 409 {
		t.Fatalf("duplicate register → %d, want 409", code)
	}
	// wrong password → 401
	code = doJSON(t, "POST", ts.ts.URL+"/api/v1/auth/login", "",
		map[string]string{"email": "a@x.com", "password": "wrongpass1"}, &out)
	if code != 401 {
		t.Fatalf("wrong password → %d, want 401", code)
	}
	// error shape
	if _, ok := out["error"]; !ok {
		t.Fatalf("error shape missing: %v", out)
	}
}

func TestSpaceDefaultMap(t *testing.T) {
	ts := newTestStack(t)
	tok := register(t, ts, "a@x.com", "Alice")
	spaceID := createSpace(t, ts, tok, "Test Space")

	var out map[string]any
	code := doJSON(t, "GET", ts.ts.URL+"/api/v1/spaces/"+spaceID, tok, nil, &out)
	if code != 200 {
		t.Fatalf("get space → %d", code)
	}
	maps := out["maps"].([]any)
	if len(maps) != 1 {
		t.Fatalf("expected 1 default map, got %d", len(maps))
	}
	mapID := maps[0].(map[string]any)["id"].(string)

	code = doJSON(t, "GET", ts.ts.URL+"/api/v1/spaces/"+spaceID+"/maps/"+mapID, tok, nil, &out)
	if code != 200 {
		t.Fatalf("get map → %d", code)
	}
	m := out["map"].(map[string]any)
	if int(m["width"].(float64)) != 40 || int(m["height"].(float64)) != 30 {
		t.Fatalf("default map should be 40x30, got %vx%v", m["width"], m["height"])
	}
	tiles := m["tiles"].([]any)
	if len(tiles) != 40*30 {
		t.Fatalf("tiles len = %d, want 1200", len(tiles))
	}
	// border walls
	if int(tiles[0].(float64)) != 2 || int(tiles[39].(float64)) != 2 || int(tiles[29*40].(float64)) != 2 {
		t.Fatal("border tiles should be walls (2)")
	}
	// center: spawn tile (5), spawn coords match
	if int(tiles[15*40+20].(float64)) != 5 {
		t.Fatalf("center tile should be spawn (5), got %v", tiles[15*40+20])
	}
	spawn := m["spawn"].(map[string]any)
	if spawn["x"].(float64) != 20 || spawn["y"].(float64) != 15 {
		t.Fatalf("spawn should be (20,15), got %v", spawn)
	}
}

func TestAgentKeyScopes(t *testing.T) {
	ts := newTestStack(t)
	tok := register(t, ts, "owner@x.com", "Owner")
	spaceID := createSpace(t, ts, tok, "Scope Space")

	// create agent-scoped key
	var out map[string]any
	code := doJSON(t, "POST", ts.ts.URL+"/api/v1/agent-keys", tok,
		map[string]any{"name": "bot", "scopes": []string{"agent"}}, &out)
	if code != 201 {
		t.Fatalf("create agent key → %d: %v", code, out)
	}
	secret := out["secret"].(string)
	if len(secret) < 10 || secret[:5] != "fspk_" {
		t.Fatalf("secret should start with fspk_, got %q", secret[:8])
	}
	// secret shown once: list must not include it
	var listOut map[string]any
	code = doJSON(t, "GET", ts.ts.URL+"/api/v1/agent-keys", tok, nil, &listOut)
	if code != 200 {
		t.Fatalf("list keys → %d", code)
	}
	if strings.Contains(string(mustJSON(listOut)), secret) {
		t.Fatal("secret leaked in key list")
	}

	// agent key can join (kind=agent)
	jr := join(t, ts, secret, spaceID, "Bot")
	if jr.participant["kind"] != "agent" {
		t.Fatalf("participant kind = %v, want agent", jr.participant["kind"])
	}
	// agent key cannot create spaces
	code = doJSON(t, "POST", ts.ts.URL+"/api/v1/spaces", secret, map[string]string{"name": "Nope"}, nil)
	if code != 403 {
		t.Fatalf("agent creating space → %d, want 403", code)
	}
	// agent key cannot manage keys
	code = doJSON(t, "GET", ts.ts.URL+"/api/v1/agent-keys", secret, nil, nil)
	if code != 403 {
		t.Fatalf("agent listing keys → %d, want 403", code)
	}

	// admin-scoped key can create spaces
	code = doJSON(t, "POST", ts.ts.URL+"/api/v1/agent-keys", tok,
		map[string]any{"name": "admin-bot", "scopes": []string{"admin"}}, &out)
	if code != 201 {
		t.Fatalf("create admin key → %d", code)
	}
	adminSecret := out["secret"].(string)
	code = doJSON(t, "POST", ts.ts.URL+"/api/v1/spaces", adminSecret, map[string]string{"name": "Admin Space"}, nil)
	if code != 201 {
		t.Fatalf("admin key creating space → %d, want 201", code)
	}
}

func TestObjectWriteAgentScope(t *testing.T) {
	ts := newTestStack(t)
	tok := register(t, ts, "owner@x.com", "Owner")
	spaceID := createSpace(t, ts, tok, "Obj Space")

	var out map[string]any
	code := doJSON(t, "POST", ts.ts.URL+"/api/v1/agent-keys", tok,
		map[string]any{"name": "bot", "scopes": []string{"agent"}}, &out)
	secret := out["secret"].(string)

	// owner creates an object
	var maps map[string]any
	_ = doJSON(t, "GET", ts.ts.URL+"/api/v1/spaces/"+spaceID, tok, nil, &maps)
	mapID := maps["maps"].([]any)[0].(map[string]any)["id"].(string)
	code = doJSON(t, "POST", ts.ts.URL+"/api/v1/spaces/"+spaceID+"/objects", tok, map[string]any{
		"map_id": mapID, "kind": "note", "x": 10, "y": 10, "w": 2, "h": 2, "title": "Note", "body": "v1",
	}, &out)
	if code != 201 {
		t.Fatalf("create object → %d: %v", code, out)
	}
	objID := out["object"].(map[string]any)["id"].(string)

	// agent: body-only patch OK
	code = doJSON(t, "PATCH", ts.ts.URL+"/api/v1/objects/"+objID, secret,
		map[string]string{"body": "v2 by agent"}, &out)
	if code != 200 {
		t.Fatalf("agent body patch → %d: %v", code, out)
	}
	if out["object"].(map[string]any)["body"] != "v2 by agent" {
		t.Fatal("body not updated")
	}
	// agent: any other field → 403
	code = doJSON(t, "PATCH", ts.ts.URL+"/api/v1/objects/"+objID, secret,
		map[string]string{"title": "hijack"}, nil)
	if code != 403 {
		t.Fatalf("agent title patch → %d, want 403", code)
	}
	code = doJSON(t, "PATCH", ts.ts.URL+"/api/v1/objects/"+objID, secret,
		map[string]string{"body": "x", "title": "hijack"}, nil)
	if code != 403 {
		t.Fatalf("agent body+title patch → %d, want 403", code)
	}
	// owner: full patch OK
	code = doJSON(t, "PATCH", ts.ts.URL+"/api/v1/objects/"+objID, tok,
		map[string]string{"title": "Renamed"}, &out)
	if code != 200 {
		t.Fatalf("owner patch → %d", code)
	}
}

func TestChatScoping(t *testing.T) {
	ts := newTestStack(t)
	tokA := register(t, ts, "a@x.com", "Alice")
	tokB := register(t, ts, "b@x.com", "Bob")
	tokC := register(t, ts, "c@x.com", "Cara")
	spaceID := createSpace(t, ts, tokA, "Chat Space")

	ja := join(t, ts, tokA, spaceID, "Alice")
	jb := join(t, ts, tokB, spaceID, "Bob")
	jc := join(t, ts, tokC, spaceID, "Cara")
	idA := ja.participant["id"].(string)
	idB := jb.participant["id"].(string)
	idC := jc.participant["id"].(string)

	// move Cara away: dx=10 → (30,15), dist 10 > R=5 (out of earshot)
	var out map[string]any
	code := doJSON(t, "POST", ts.ts.URL+"/api/v1/participants/me/move", tokC,
		map[string]float64{"dx": 10, "dy": 0}, &out)
	if code != 200 {
		t.Fatalf("move → %d: %v", code, out)
	}

	// Alice sends nearby
	code = doJSON(t, "POST", ts.ts.URL+"/api/v1/spaces/"+spaceID+"/chat", tokA,
		map[string]string{"scope": "nearby", "text": "hello nearby"}, &out)
	if code != 201 {
		t.Fatalf("send chat → %d: %v", code, out)
	}

	// Bob (near) sees it; Cara (far) doesn't
	code = doJSON(t, "GET", ts.ts.URL+"/api/v1/spaces/"+spaceID+"/chat?scope=nearby", tokB, nil, &out)
	msgs := out["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("Bob should see 1 nearby message, got %d", len(msgs))
	}
	code = doJSON(t, "GET", ts.ts.URL+"/api/v1/spaces/"+spaceID+"/chat?scope=nearby", tokC, nil, &out)
	msgs = out["messages"].([]any)
	if len(msgs) != 0 {
		t.Fatalf("Cara should see 0 nearby messages, got %d", len(msgs))
	}

	// DM: Alice → Bob. Only Bob (and Alice) see it.
	code = doJSON(t, "POST", ts.ts.URL+"/api/v1/spaces/"+spaceID+"/chat", tokA,
		map[string]string{"scope": "dm", "to": idB, "text": "secret"}, &out)
	if code != 201 {
		t.Fatalf("send dm → %d", code)
	}
	for _, tc := range []struct {
		tok  string
		name string
		want int
	}{
		{tokB, "Bob", 1}, {tokA, "Alice", 1}, {tokC, "Cara", 0},
	} {
		_ = doJSON(t, "GET", ts.ts.URL+"/api/v1/spaces/"+spaceID+"/chat?scope=dm", tc.tok, nil, &out)
		msgs := out["messages"].([]any)
		if len(msgs) != tc.want {
			t.Fatalf("%s should see %d dm(s), got %d", tc.name, tc.want, len(msgs))
		}
	}

	// envelope keys must be exactly {messages:[...]} etc.
	var raw map[string]json.RawMessage
	_ = doJSON(t, "GET", ts.ts.URL+"/api/v1/spaces/"+spaceID+"/chat", tokA, nil, &raw)
	if _, ok := raw["messages"]; !ok || len(raw) != 1 {
		t.Fatalf("chat envelope should be exactly {messages}, got keys %v", keysOf(raw))
	}
	raw = map[string]json.RawMessage{}
	_ = doJSON(t, "GET", ts.ts.URL+"/api/v1/spaces/"+spaceID+"/areas", tokA, nil, &raw)
	if _, ok := raw["areas"]; !ok || len(raw) != 1 {
		t.Fatalf("areas envelope should be exactly {areas}, got keys %v", keysOf(raw))
	}
	raw = map[string]json.RawMessage{}
	_ = doJSON(t, "GET", ts.ts.URL+"/api/v1/spaces/"+spaceID+"/objects", tokA, nil, &raw)
	if _, ok := raw["objects"]; !ok || len(raw) != 1 {
		t.Fatalf("objects envelope should be exactly {objects}, got keys %v", keysOf(raw))
	}
	raw = map[string]json.RawMessage{}
	_ = doJSON(t, "GET", ts.ts.URL+"/api/v1/spaces/"+spaceID+"/participants", tokA, nil, &raw)
	if _, ok := raw["participants"]; !ok || len(raw) != 1 {
		t.Fatalf("participants envelope should be exactly {participants}, got keys %v", keysOf(raw))
	}

	_ = idA
	_ = idC
}

func keysOf(m map[string]json.RawMessage) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func TestChatRateLimit(t *testing.T) {
	ts := newTestStack(t)
	tok := register(t, ts, "a@x.com", "Alice")
	spaceID := createSpace(t, ts, tok, "Rate Space")
	join(t, ts, tok, spaceID, "Alice")

	for i := 0; i < 5; i++ {
		code := doJSON(t, "POST", ts.ts.URL+"/api/v1/spaces/"+spaceID+"/chat", tok,
			map[string]string{"scope": "nearby", "text": "spam"}, nil)
		if code != 201 {
			t.Fatalf("chat %d → %d, want 201", i, code)
		}
	}
	code := doJSON(t, "POST", ts.ts.URL+"/api/v1/spaces/"+spaceID+"/chat", tok,
		map[string]string{"scope": "nearby", "text": "spam6"}, nil)
	if code != 429 {
		t.Fatalf("6th chat in 5s → %d, want 429", code)
	}
}

func TestTicketSingleUse(t *testing.T) {
	ts := newTestStack(t)
	tok := register(t, ts, "a@x.com", "Alice")
	spaceID := createSpace(t, ts, tok, "Ticket Space")
	jr := join(t, ts, tok, spaceID, "Alice")

	// first redeem works
	c1 := wsDial(t, jr.wsURL)
	m := readMsg(t, c1, 5*time.Second)
	if m["t"] != "hello" {
		t.Fatalf("first message should be hello, got %v", m["t"])
	}
	defer wsClose(c1)

	// second redeem of the same ticket → 401
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := websocket.Dial(ctx, jr.wsURL, nil)
	if err == nil {
		t.Fatal("second ticket redeem should fail")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected 401, got %v", err)
	}
}

func TestWSBroadcastSmoke(t *testing.T) {
	ts := newTestStack(t)
	tokA := register(t, ts, "a@x.com", "Alice")
	tokB := register(t, ts, "b@x.com", "Bob")
	spaceID := createSpace(t, ts, tokA, "WS Space")
	ja := join(t, ts, tokA, spaceID, "Alice")
	jb := join(t, ts, tokB, spaceID, "Bob")
	idA := ja.participant["id"].(string)

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

	// A moves right for ~0.6s
	wsWrite(t, ca, map[string]any{"t": "input", "seq": 1, "dx": 1, "dy": 0})
	time.Sleep(600 * time.Millisecond)
	wsWrite(t, ca, map[string]any{"t": "input", "seq": 2, "dx": 0, "dy": 0})

	// B should see state with A's x increased, and an audio event naming A
	var sawMoved bool
	var sawAudio bool
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) && !(sawMoved && sawAudio) {
		m := readMsg(t, cb, time.Until(deadline))
		switch m["t"] {
		case "state":
			for _, p := range m["participants"].([]any) {
				pm := p.(map[string]any)
				if pm["id"] == idA && pm["x"].(float64) > 20.5 {
					sawMoved = true
				}
			}
		case "audio":
			if vols, ok := m["volumes"].(map[string]any); ok {
				if _, ok := vols[idA]; ok {
					sawAudio = true
				}
			}
		}
	}
	if !sawMoved {
		t.Fatal("B never saw A move in state broadcasts")
	}
	if !sawAudio {
		t.Fatal("B never got an audio event naming A")
	}

	// malformed input → error message, connection stays alive
	wsWrite(t, ca, map[string]any{"t": "bogus"})
	if m := waitForMsg(t, ca, "error", 3*time.Second); m["code"] != "bad_request" {
		t.Fatalf("expected bad_request error, got %v", m)
	}
	wsWrite(t, ca, map[string]any{"t": "ping", "at": 123})
	if m := waitForMsg(t, ca, "pong", 3*time.Second); m["at"].(float64) != 123 {
		t.Fatalf("expected pong, got %v", m)
	}
}

func TestKick(t *testing.T) {
	ts := newTestStack(t)
	tokOwner := register(t, ts, "owner@x.com", "Owner")
	tokB := register(t, ts, "b@x.com", "Bob")
	spaceID := createSpace(t, ts, tokOwner, "Kick Space")
	join(t, ts, tokOwner, spaceID, "Owner")
	jb := join(t, ts, tokB, spaceID, "Bob")
	idB := jb.participant["id"].(string)

	cb := wsDial(t, jb.wsURL)
	defer wsClose(cb)
	if m := readMsg(t, cb, 5*time.Second); m["t"] != "hello" {
		t.Fatalf("hello: %v", m["t"])
	}

	var out map[string]any
	code := doJSON(t, "DELETE", ts.ts.URL+"/api/v1/spaces/"+spaceID+"/participants/"+idB, tokOwner, nil, &out)
	if code != 200 {
		t.Fatalf("kick → %d: %v", code, out)
	}
	// kicked socket receives the kick event
	m := waitForMsg(t, cb, "event", 3*time.Second)
	if m["kind"] != "kick" {
		t.Fatalf("expected kick event, got %v", m)
	}
	// Bob is gone from the participant list
	code = doJSON(t, "GET", ts.ts.URL+"/api/v1/spaces/"+spaceID+"/participants", tokOwner, nil, &out)
	parts := out["participants"].([]any)
	for _, p := range parts {
		if p.(map[string]any)["id"] == idB {
			t.Fatal("kicked participant still listed")
		}
	}
	// non-owner cannot kick: Cara tries to kick the owner → 403
	tokC := register(t, ts, "c@x.com", "Cara")
	join(t, ts, tokC, spaceID, "Cara")
	code = doJSON(t, "GET", ts.ts.URL+"/api/v1/spaces/"+spaceID+"/participants", tokOwner, nil, &out)
	parts = out["participants"].([]any)
	var ownerPid string
	for _, p := range parts {
		if p.(map[string]any)["display_name"] == "Owner" {
			ownerPid = p.(map[string]any)["id"].(string)
		}
	}
	if ownerPid == "" {
		t.Fatal("owner participant not found")
	}
	code = doJSON(t, "DELETE", ts.ts.URL+"/api/v1/spaces/"+spaceID+"/participants/"+ownerPid, tokC, nil, nil)
	if code != 403 {
		t.Fatalf("non-owner kick → %d, want 403", code)
	}
}

func TestMeMove(t *testing.T) {
	ts := newTestStack(t)
	tok := register(t, ts, "a@x.com", "Alice")
	spaceID := createSpace(t, ts, tok, "Move Space")
	jr := join(t, ts, tok, spaceID, "Alice")

	// absolute move to open floor
	var out map[string]any
	code := doJSON(t, "POST", ts.ts.URL+"/api/v1/participants/me/move", tok,
		map[string]float64{"x": 25, "y": 20}, &out)
	if code != 200 {
		t.Fatalf("me/move → %d: %v", code, out)
	}
	// unreachable: inside the border wall
	code = doJSON(t, "POST", ts.ts.URL+"/api/v1/participants/me/move", tok,
		map[string]float64{"x": 0.5, "y": 0.5}, nil)
	if code != 400 {
		t.Fatalf("move into wall → %d, want 400", code)
	}
	// relative nudge
	code = doJSON(t, "POST", ts.ts.URL+"/api/v1/participants/me/move", tok,
		map[string]float64{"dx": 2, "dy": 0}, &out)
	if code != 200 {
		t.Fatalf("nudge → %d", code)
	}
	_ = jr
}

func TestMediaDisabled(t *testing.T) {
	ts := newTestStack(t)
	tok := register(t, ts, "a@x.com", "Alice")
	spaceID := createSpace(t, ts, tok, "Media Space")
	jr := join(t, ts, tok, spaceID, "Alice")
	if jr.wsURL == "" {
		t.Fatal("ws_url missing")
	}
	// join response must carry livekit:null — check via raw join
	var out map[string]any
	tokB := register(t, ts, "b@x.com", "Bob")
	code := doJSON(t, "POST", ts.ts.URL+"/api/v1/spaces/"+spaceID+"/join", tokB,
		map[string]string{"display_name": "Bob"}, &out)
	if code != 200 {
		t.Fatalf("join → %d", code)
	}
	if _, ok := out["livekit"]; !ok || out["livekit"] != nil {
		t.Fatalf("livekit should be null when disabled, got %v", out["livekit"])
	}
	// media token → 401
	code = doJSON(t, "GET", ts.ts.URL+"/api/v1/media/token", tokB, nil, &out)
	if code != 401 {
		t.Fatalf("media token when disabled → %d, want 401", code)
	}
	// health
	code = doJSON(t, "GET", ts.ts.URL+"/api/v1/health", "", nil, &out)
	if code != 200 || out["ok"] != true || out["media"] != false {
		t.Fatalf("health = %v", out)
	}
}

func TestJoinLeave(t *testing.T) {
	ts := newTestStack(t)
	tok := register(t, ts, "a@x.com", "Alice")
	spaceID := createSpace(t, ts, tok, "JL Space")

	// one login → one participant per space
	join(t, ts, tok, spaceID, "Alice")
	code := doJSON(t, "POST", ts.ts.URL+"/api/v1/spaces/"+spaceID+"/join", tok,
		map[string]string{"display_name": "Alice2"}, nil)
	if code != 409 {
		t.Fatalf("double join → %d, want 409", code)
	}

	var out map[string]any
	code = doJSON(t, "POST", ts.ts.URL+"/api/v1/spaces/"+spaceID+"/leave", tok, nil, &out)
	if code != 200 {
		t.Fatalf("leave → %d", code)
	}
	// can rejoin after leaving
	join(t, ts, tok, spaceID, "Alice")
}

func TestSSEStream(t *testing.T) {
	ts := newTestStack(t)
	tokA := register(t, ts, "a@x.com", "Alice")
	tokB := register(t, ts, "b@x.com", "Bob")
	spaceID := createSpace(t, ts, tokA, "SSE Space")
	ja := join(t, ts, tokA, spaceID, "Alice")

	// open SSE with Alice's ticket
	req, _ := http.NewRequest("GET", ts.ts.URL+"/api/v1/stream?ticket="+ja.ticket, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("SSE → %d", resp.StatusCode)
	}
	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()
	readLine := func() map[string]any {
		t.Helper()
		select {
		case l := <-lines:
			var m map[string]any
			if err := json.Unmarshal([]byte(l), &m); err != nil {
				t.Fatalf("SSE line not JSON: %q", l)
			}
			if _, ok := m["t"]; !ok {
				t.Fatalf("SSE line missing t discriminator: %q", l)
			}
			return m
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for SSE line")
			return nil
		}
	}
	// hello first
	if m := readLine(); m["t"] != "hello" {
		t.Fatalf("first SSE line should be hello, got %v", m["t"])
	}
	// Bob joins → presence event
	join(t, ts, tokB, spaceID, "Bob")
	m := readLine()
	if m["t"] != "presence" || m["kind"] != "join" {
		t.Fatalf("expected presence/join, got %v", m)
	}
	if _, ok := m["participant"]; !ok {
		t.Fatalf("presence missing participant: %v", m)
	}
}
