package main

// Tests for the MCP JSON-RPC 2.0 framing over stdio.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func newBareServer() *MCPServer {
	// No network is touched by these tests; the client is never used.
	return NewMCPServer("http://localhost:8080", "fspk_test")
}

func TestInitialize(t *testing.T) {
	s := newBareServer()
	resp := s.handleLine([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","clientInfo":{"name":"test"}}}`))
	var out struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
			Capabilities    struct {
				Tools map[string]any `json:"tools"`
			} `json:"capabilities"`
			ServerInfo struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Result.ProtocolVersion != "2024-11-05" {
		t.Fatalf("protocolVersion = %q", out.Result.ProtocolVersion)
	}
	if out.Result.ServerInfo.Name != "forge-space-mcp" {
		t.Fatalf("server name = %q", out.Result.ServerInfo.Name)
	}
	if out.Result.Capabilities.Tools == nil {
		t.Fatal("capabilities.tools missing")
	}
}

func TestPing(t *testing.T) {
	s := newBareServer()
	resp := s.handleLine([]byte(`{"jsonrpc":"2.0","id":"a","method":"ping"}`))
	var out struct {
		ID     string         `json:"id"`
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ID != "a" || out.Result == nil {
		t.Fatalf("bad ping response: %s", resp)
	}
}

func TestUnknownMethod(t *testing.T) {
	s := newBareServer()
	resp := s.handleLine([]byte(`{"jsonrpc":"2.0","id":7,"method":"nope"}`))
	var out struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Error.Code != -32601 {
		t.Fatalf("code = %d, want -32601", out.Error.Code)
	}
}

func TestNotificationProducesNoResponse(t *testing.T) {
	s := newBareServer()
	if resp := s.handleLine([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`)); resp != nil {
		t.Fatalf("expected nil response, got %s", resp)
	}
	// Unknown notifications are also silent.
	if resp := s.handleLine([]byte(`{"jsonrpc":"2.0","method":"notifications/cancelled"}`)); resp != nil {
		t.Fatalf("expected nil response, got %s", resp)
	}
}

func TestParseError(t *testing.T) {
	s := newBareServer()
	resp := s.handleLine([]byte(`{not json`))
	var out struct {
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Error.Code != -32700 {
		t.Fatalf("code = %d, want -32700", out.Error.Code)
	}
}

// TestToolSchemasValid checks every tool has a name, description, and a valid
// JSON Schema object with required fields present in properties.
func TestToolSchemasValid(t *testing.T) {
	s := newBareServer()
	if len(s.toolOrder) != 23 {
		t.Fatalf("expected 23 tools, got %d", len(s.toolOrder))
	}
	wantOrder := []string{
		"space_list", "space_join", "space_leave", "me_where", "me_move_to",
		"me_step", "me_say", "me_emote", "me_ghost", "me_speaking", "world_look", "world_listen",
		"object_read", "object_write", "board_read", "board_draw", "board_clear",
		"portal_use", "participants_list",
		"map_get", "media_token", "admin_broadcast", "admin_kick",
	}
	for i, name := range wantOrder {
		if s.toolOrder[i] != name {
			t.Fatalf("tool order[%d] = %q, want %q", i, s.toolOrder[i], name)
		}
	}
	for _, name := range s.toolOrder {
		tool := s.tools[name]
		if tool.Name == "" {
			t.Errorf("tool %q: empty name", name)
		}
		if tool.Description == "" {
			t.Errorf("tool %q: empty description", name)
		}
		schema := tool.InputSchema
		if schema["type"] != "object" {
			t.Errorf("tool %q: inputSchema type = %v, want object", name, schema["type"])
		}
		props, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Errorf("tool %q: inputSchema.properties not an object", name)
			continue
		}
		for pname, pschema := range props {
			pm, ok := pschema.(map[string]any)
			if !ok || pm["type"] == nil || pm["description"] == nil {
				t.Errorf("tool %q prop %q: schema must have type and description", name, pname)
			}
		}
		if req, ok := schema["required"].([]string); ok {
			for _, r := range req {
				if _, exists := props[r]; !exists {
					t.Errorf("tool %q: required %q not in properties", name, r)
				}
			}
		}
	}
}

func TestToolsList(t *testing.T) {
	s := newBareServer()
	resp := s.handleLine([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`))
	var out struct {
		Result struct {
			Tools []map[string]any `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Result.Tools) != 23 {
		t.Fatalf("tools/list returned %d tools, want 23", len(out.Result.Tools))
	}
	for _, tool := range out.Result.Tools {
		if tool["name"] == nil || tool["description"] == nil || tool["inputSchema"] == nil {
			t.Fatalf("tool missing fields: %v", tool)
		}
	}
}

func TestToolsCallUnknownTool(t *testing.T) {
	s := newBareServer()
	resp := s.handleLine([]byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"bogus"}}`))
	var out struct {
		Result struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !out.Result.IsError {
		t.Fatal("expected isError=true for unknown tool")
	}
	if !strings.Contains(out.Result.Content[0].Text, "unknown tool") {
		t.Fatalf("unexpected text: %q", out.Result.Content[0].Text)
	}
}

// TestServeRoundtrip exercises the full stdio loop: initialize ->
// notifications/initialized -> tools/list -> tools/call(space_list) -> EOF.
func TestServeRoundtrip(t *testing.T) {
	f, s := newTestEnv(t)
	_ = f
	in := new(bytes.Buffer)
	in.WriteString("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{}}\n")
	in.WriteString("{\"jsonrpc\":\"2.0\",\"method\":\"notifications/initialized\",\"params\":{}}\n")
	in.WriteString("{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/list\",\"params\":{}}\n")
	in.WriteString("{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"tools/call\",\"params\":{\"name\":\"space_list\",\"arguments\":{}}}\n")
	var out bytes.Buffer
	s.Serve(in, &out)

	sc := bufio.NewScanner(&out)
	sc.Buffer(make([]byte, 1024*1024), 4*1024*1024)
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	if len(lines) != 3 {
		t.Fatalf("expected 3 response lines (notification is silent), got %d", len(lines))
	}
	var r1 struct {
		ID     int `json:"id"`
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &r1); err != nil || r1.Result.ProtocolVersion != "2024-11-05" {
		t.Fatalf("bad initialize response: %s", lines[0])
	}
	var r3 struct {
		ID     int `json:"id"`
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[2]), &r3); err != nil {
		t.Fatalf("bad tools/call response: %v", err)
	}
	var payload struct {
		Spaces []struct {
			ID string `json:"id"`
		} `json:"spaces"`
	}
	if err := json.Unmarshal([]byte(r3.Result.Content[0].Text), &payload); err != nil {
		t.Fatalf("bad space_list payload: %v", err)
	}
	if len(payload.Spaces) != 1 || payload.Spaces[0].ID != "sp_abc" {
		t.Fatalf("unexpected spaces: %+v", payload.Spaces)
	}
}

func TestLoadConfig(t *testing.T) {
	t.Setenv("FORGE_SPACE_URL", "")
	t.Setenv("FORGE_SPACE_KEY", "")
	if _, err := loadConfig(); err == nil {
		t.Fatal("expected error when FORGE_SPACE_KEY is missing")
	}
	t.Setenv("FORGE_SPACE_KEY", "badkey")
	if _, err := loadConfig(); err == nil {
		t.Fatal("expected error when key lacks fspk_ prefix")
	}
	t.Setenv("FORGE_SPACE_KEY", "fspk_test")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.BaseURL != "http://localhost:8080" {
		t.Fatalf("default base URL = %q", cfg.BaseURL)
	}
	t.Setenv("FORGE_SPACE_URL", "https://space.example.com/")
	cfg, _ = loadConfig()
	if cfg.BaseURL != "https://space.example.com/" {
		t.Fatalf("base URL = %q", cfg.BaseURL)
	}
}
