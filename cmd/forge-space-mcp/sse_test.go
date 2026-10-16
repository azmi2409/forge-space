package main

// Tests for SSE line parsing (CONTRACT §7) and the single-use ticket lifecycle.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleSSELine(t *testing.T) {
	s := NewMCPServer("http://localhost:8080", "fspk_test")
	sess := &session{events: newEventBuffer(10)}

	// JSON object per line, `event:` omitted.
	s.handleSSELine(sess, `{"t":"presence","kind":"join","participant":{"id":"pt_1"}}`+"\n")
	// Heartbeat comment and blank lines are skipped.
	s.handleSSELine(sess, ": heartbeat\n")
	s.handleSSELine(sess, "\n")
	// Malformed lines are skipped, not fatal.
	s.handleSSELine(sess, `not json at all`+"\n")
	// Classic SSE data: framing is tolerated.
	s.handleSSELine(sess, `data: {"t":"chat","message":{"text":"hi"}}`+"\n")
	// CRLF endings are handled.
	s.handleSSELine(sess, "{\"t\":\"emote\",\"participant_id\":\"pt_1\",\"emote\":\"dance\"}\r\n")

	evs := sess.events.last(10)
	if len(evs) != 3 {
		t.Fatalf("expected 3 buffered events, got %d", len(evs))
	}
	if evs[0].Kind != "presence" || evs[1].Kind != "chat" || evs[2].Kind != "emote" {
		t.Fatalf("unexpected kinds: %+v", evs)
	}
}

func TestEventKindFallback(t *testing.T) {
	if k := eventKind(map[string]any{"kind": "area"}); k != "area" {
		t.Fatalf("got %q", k)
	}
	if k := eventKind(map[string]any{"foo": 1}); k != "unknown" {
		t.Fatalf("got %q", k)
	}
}

// TestReadSSEOnceBuffersEvents serves a real stream from a stub server and
// verifies events land in the buffer, then the stream ending triggers a clean
// EOF (no auth failure).
func TestReadSSEOnceBuffersEvents(t *testing.T) {
	var gotTicket string
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTicket = r.URL.Query().Get("ticket")
		if r.Header.Get("Authorization") != "Bearer fspk_test" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `{"t":"hello","you":"pt_me"}`)
		fmt.Fprintln(w, `: heartbeat`)
		fmt.Fprintln(w, `{"t":"presence","kind":"join","participant":{"id":"pt_2"}}`)
		// return: handler ends -> EOF on the client
	}))
	defer stub.Close()

	s := NewMCPServer(stub.URL, "fspk_test")
	sess := &session{events: newEventBuffer(10)}
	authFailed, err := s.readSSEOnce(t.Context(), sess, "tk_abc123")
	if err != nil {
		t.Fatalf("readSSEOnce: %v", err)
	}
	if authFailed {
		t.Fatal("authFailed should be false for a good stream")
	}
	if gotTicket != "tk_abc123" {
		t.Fatalf("ticket not passed as query param, got %q", gotTicket)
	}
	evs := sess.events.last(10)
	if len(evs) != 2 || evs[0].Kind != "hello" || evs[1].Kind != "presence" {
		t.Fatalf("unexpected buffered events: %+v", evs)
	}
	if sess.state() != "connected" {
		t.Fatalf("expected state connected, got %q", sess.state())
	}
}

// TestSSEAuthFailureMarksDead verifies a rejected ticket (single-use, expired)
// marks the stream dead instead of retrying forever.
func TestSSEAuthFailureMarksDead(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(403)
		fmt.Fprintln(w, `{"error":{"code":"forbidden","message":"bad ticket"}}`)
	}))
	defer stub.Close()

	s := NewMCPServer(stub.URL, "fspk_test")
	sess := &session{events: newEventBuffer(10)}
	authFailed, _ := s.readSSEOnce(t.Context(), sess, "tk_used")
	if !authFailed {
		t.Fatal("expected authFailed=true on 403")
	}
}

// TestEventBufferCap verifies the ring drops the oldest events past capacity.
func TestEventBufferCap(t *testing.T) {
	b := newEventBuffer(3)
	for i := 0; i < 5; i++ {
		b.add("e", map[string]any{"i": i})
	}
	evs := b.last(10)
	if len(evs) != 3 {
		t.Fatalf("expected 3, got %d", len(evs))
	}
	if evs[0].Raw["i"] != 2 {
		t.Fatalf("expected oldest kept to be i=2, got %v", evs[0].Raw["i"])
	}
}
