package main

// Session state + background SSE reader (CONTRACT §7).
// The SSE stream feeds world_listen with presence/area/emote/chat events.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// sseEvent is one parsed SSE event line.
type sseEvent struct {
	Kind string
	Raw  map[string]any
}

// eventBuffer is a capped ring of recent SSE events (mutex-guarded; written by
// the SSE goroutine, read by tools/call handlers).
type eventBuffer struct {
	mu  sync.Mutex
	evs []sseEvent
	max int
}

func newEventBuffer(max int) *eventBuffer { return &eventBuffer{max: max} }

func (b *eventBuffer) add(kind string, raw map[string]any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.evs = append(b.evs, sseEvent{Kind: kind, Raw: raw})
	if len(b.evs) > b.max {
		b.evs = b.evs[len(b.evs)-b.max:]
	}
}

func (b *eventBuffer) last(n int) []sseEvent {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n > len(b.evs) {
		n = len(b.evs)
	}
	out := make([]sseEvent, n)
	copy(out, b.evs[len(b.evs)-n:])
	return out
}

// eventKind extracts the event discriminator. CONTRACT §7 does not name the
// field, so we accept the candidates in order and fall back to "unknown"
// rather than dropping the event.
func eventKind(raw map[string]any) string {
	for _, k := range []string{"t", "type", "event", "kind"} {
		if v, ok := raw[k].(string); ok && v != "" {
			return v
		}
	}
	return "unknown"
}

// session holds the active space membership. There is at most one: space_join
// replaces any existing session, space_leave clears it.
type session struct {
	spaceID     string
	meID        string
	displayName string
	cancel      context.CancelFunc
	events      *eventBuffer

	mu          sync.Mutex
	streamState string // "connecting" | "connected" | "reconnecting" | "dead"
}

func (s *session) setState(st string) {
	s.mu.Lock()
	s.streamState = st
	s.mu.Unlock()
}

func (s *session) state() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streamState
}

// startSSE launches the background SSE reader for the join ticket. The ticket
// is single-use (CONTRACT §1/§7): it is consumed by this first connection and
// is never stored or reused.
func (s *MCPServer) startSSE(sess *session, ticket string) {
	ctx, cancel := context.WithCancel(context.Background())
	sess.cancel = cancel
	sess.events = newEventBuffer(100)
	sess.setState("connecting")
	go s.sseLoop(ctx, sess, ticket)
}

// stopSSE cancels the background reader. Idempotent.
func stopSSE(sess *session) {
	if sess != nil && sess.cancel != nil {
		sess.cancel()
	}
}

// sseLoop keeps the event stream alive, reconnecting with capped exponential
// backoff. A 401/403 means the single-use ticket was consumed or rejected, so
// the stream is marked dead instead of retrying forever.
func (s *MCPServer) sseLoop(ctx context.Context, sess *session, ticket string) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		authFailed, err := s.readSSEOnce(ctx, sess, ticket)
		if ctx.Err() != nil {
			return
		}
		if authFailed {
			sess.setState("dead")
			logf("sse: ticket rejected, stream dead")
			return
		}
		if err == nil {
			backoff = time.Second // a clean stream ended; reset backoff
		}
		sess.setState("reconnecting")
		if err != nil {
			logf("sse: disconnected (%v), retrying in %s", err, backoff)
		} else {
			logf("sse: stream closed by server, retrying in %s", backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// readSSEOnce opens the stream and blocks until it ends. It returns
// authFailed=true when the server rejects the ticket (401/403) so the caller
// stops retrying.
func (s *MCPServer) readSSEOnce(ctx context.Context, sess *session, ticket string) (authFailed bool, err error) {
	u := s.client.base + "/api/v1/stream?ticket=" + url.QueryEscape(ticket)
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+s.client.key)
	req.Header.Set("Accept", "text/event-stream")
	// No client timeout: this is a long-lived stream. The context (cancelled
	// by space_leave) bounds its lifetime instead.
	streamClient := &http.Client{Timeout: 0}
	resp, err := streamClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return true, fmt.Errorf("stream rejected: HTTP %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("stream: HTTP %d", resp.StatusCode)
	}
	sess.setState("connected")
	br := bufio.NewReader(resp.Body)
	for {
		line, rerr := br.ReadString('\n')
		if len(line) > 0 {
			s.handleSSELine(sess, line)
		}
		if rerr != nil {
			return false, nil // EOF / read error: let the loop reconnect
		}
	}
}

// handleSSELine parses one SSE line per CONTRACT §7: one JSON object per line,
// `event:` omitted, heartbeat comments (":...") every 15s.
func (s *MCPServer) handleSSELine(sess *session, line string) {
	t := strings.TrimRight(line, "\r\n")
	if t == "" || strings.HasPrefix(t, ":") {
		return // heartbeat comment or blank line
	}
	payload := t
	if !json.Valid([]byte(payload)) && strings.HasPrefix(t, "data:") {
		// Tolerate classic SSE framing if a server ever emits it.
		payload = strings.TrimSpace(strings.TrimPrefix(t, "data:"))
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		logf("sse: skipping malformed line: %v", err)
		return
	}
	sess.events.add(eventKind(raw), raw)
}
