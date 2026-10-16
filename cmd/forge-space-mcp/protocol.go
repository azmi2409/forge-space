package main

// Minimal MCP JSON-RPC 2.0 server over stdio (MCP protocol version 2024-11-05).
// Hand-rolled with stdlib only: newline-delimited JSON-RPC messages on stdin,
// one response line per request on stdout. Logs go to stderr (never stdout).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
)

const (
	mcpProtocolVersion = "2024-11-05"
	mcpServerName      = "forge-space-mcp"
	mcpServerVersion   = "0.1.0"
)

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "forge-space-mcp: "+format+"\n", args...)
}

// ---- JSON-RPC types ----

type rpcRequest struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"` // absent for notifications
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params"`
}

// ---- tool registry ----

// ToolHandler runs one MCP tool. It returns the result value, which is
// JSON-marshaled into the text content of the tool result.
type ToolHandler func(ctx context.Context, args json.RawMessage) (any, error)

type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	Handler     ToolHandler
}

// ---- server ----

type MCPServer struct {
	client *Client

	mu   sync.Mutex // guards sess
	sess *session

	tools     map[string]*Tool
	toolOrder []string
}

func NewMCPServer(baseURL, key string) *MCPServer {
	s := &MCPServer{
		client: NewClient(baseURL, key),
		tools:  map[string]*Tool{},
	}
	for _, t := range allTools(s) {
		s.tools[t.Name] = t
		s.toolOrder = append(s.toolOrder, t.Name)
	}
	return s
}

// requireSession returns the active session or a tool error telling the agent
// to call space_join first.
func (s *MCPServer) requireSession() (*session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sess == nil {
		return nil, fmt.Errorf("not in a space — call space_join first")
	}
	return s.sess, nil
}

// clearSession stops the SSE reader and drops the session.
func (s *MCPServer) clearSession() {
	s.mu.Lock()
	defer s.mu.Unlock()
	stopSSE(s.sess)
	s.sess = nil
}

// toolSchemas returns the tools/list payload in registration order.
func (s *MCPServer) toolSchemas() []map[string]any {
	out := make([]map[string]any, 0, len(s.toolOrder))
	for _, name := range s.toolOrder {
		t := s.tools[name]
		out = append(out, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": t.InputSchema,
		})
	}
	return out
}

// ---- stdio loop ----

// Serve reads newline-delimited JSON-RPC messages until EOF, writing one
// response line per request. Notifications produce no output.
func (s *MCPServer) Serve(r io.Reader, w io.Writer) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	bw := bufio.NewWriter(w)
	defer bw.Flush()
	for sc.Scan() {
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if resp := s.handleLine(line); resp != nil {
			bw.Write(resp)
			bw.WriteByte('\n')
			bw.Flush()
		}
	}
	if err := sc.Err(); err != nil {
		logf("stdio read error: %v", err)
	}
}

// handleLine dispatches one JSON-RPC message. It returns nil for notifications.
func (s *MCPServer) handleLine(line []byte) []byte {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		return errBytes(nil, -32600, "invalid request: batch requests not supported")
	}
	var req rpcRequest
	if err := json.Unmarshal(trimmed, &req); err != nil {
		return errBytes(nil, -32700, "parse error: "+err.Error())
	}
	if req.JSONRPC != "2.0" {
		return errBytes(req.ID, -32600, "invalid request: jsonrpc must be \"2.0\"")
	}
	isNotification := req.ID == nil
	switch req.Method {
	case "initialize":
		if isNotification {
			return nil
		}
		return okBytes(req.ID, map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo": map[string]any{
				"name":    mcpServerName,
				"version": mcpServerVersion,
			},
		})
	case "notifications/initialized":
		return nil
	case "ping":
		if isNotification {
			return nil
		}
		return okBytes(req.ID, map[string]any{})
	case "tools/list":
		if isNotification {
			return nil
		}
		return okBytes(req.ID, map[string]any{"tools": s.toolSchemas()})
	case "tools/call":
		if isNotification {
			return nil
		}
		return s.handleToolCall(req.ID, req.Params)
	default:
		if isNotification {
			return nil
		}
		return errBytes(req.ID, -32601, "method not found: "+req.Method)
	}
}

// handleToolCall runs tools/call and wraps the outcome in the MCP result
// shape. Tool failures become {isError:true} results (not JSON-RPC errors),
// so the calling agent sees the message.
func (s *MCPServer) handleToolCall(id *json.RawMessage, params json.RawMessage) []byte {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return errBytes(id, -32602, "invalid params: "+err.Error())
	}
	t := s.tools[p.Name]
	if t == nil {
		return toolError(id, "unknown tool: "+p.Name)
	}
	args := p.Arguments
	if len(args) == 0 {
		args = []byte("{}")
	}
	result, err := t.Handler(context.Background(), args)
	if err != nil {
		return toolError(id, err.Error())
	}
	text, merr := json.Marshal(result)
	if merr != nil {
		return toolError(id, "encode tool result: "+merr.Error())
	}
	return okBytes(id, map[string]any{
		"content": []any{
			map[string]any{"type": "text", "text": string(text)},
		},
	})
}

// ---- response encoding ----

func okBytes(id *json.RawMessage, result any) []byte {
	out, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      rawID(id),
		"result":  result,
	})
	return out
}

func errBytes(id *json.RawMessage, code int, message string) []byte {
	out, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      rawID(id),
		"error":   map[string]any{"code": code, "message": message},
	})
	return out
}

func toolError(id *json.RawMessage, msg string) []byte {
	return okBytes(id, map[string]any{
		"content": []any{
			map[string]any{"type": "text", "text": "error: " + msg},
		},
		"isError": true,
	})
}

func rawID(id *json.RawMessage) json.RawMessage {
	if id == nil {
		return json.RawMessage("null")
	}
	return *id
}
