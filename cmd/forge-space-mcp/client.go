package main

// REST client for the forge-space server (CONTRACT §3).
// Stdlib only: net/http + encoding/json.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ---- API shapes (mirror CONTRACT §2) ----

type SpaceSettings struct {
	ProximityRadius float64 `json:"proximity_radius"`
}

type Space struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Slug        string        `json:"slug"`
	OwnerUserID string        `json:"owner_user_id"`
	Settings    SpaceSettings `json:"settings"`
	CreatedAt   string        `json:"created_at"`
}

type MapSummary struct {
	ID      string `json:"id"`
	SpaceID string `json:"space_id"`
	Name    string `json:"name"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
}

type XY struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type GameMap struct {
	ID      string `json:"id"`
	SpaceID string `json:"space_id"`
	Name    string `json:"name"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Tiles   []int  `json:"tiles"`
	Spawn   XY     `json:"spawn"`
}

type Area struct {
	ID    string  `json:"id"`
	MapID string  `json:"map_id"`
	Kind  string  `json:"kind"` // "private" | "spotlight"
	Name  string  `json:"name"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	W     float64 `json:"w"`
	H     float64 `json:"h"`
}

type SpaceObject struct {
	ID          string   `json:"id"`
	MapID       string   `json:"map_id"`
	Kind        string   `json:"kind"` // note|whiteboard|url|video|poster|portal
	X           float64  `json:"x"`
	Y           float64  `json:"y"`
	W           float64  `json:"w"`
	H           float64  `json:"h"`
	Title       string   `json:"title"`
	Body        string   `json:"body"`
	URL         string   `json:"url,omitempty"`
	TargetMapID string   `json:"target_map_id,omitempty"`
	TargetX     *float64 `json:"target_x,omitempty"`
	TargetY     *float64 `json:"target_y,omitempty"`
	Solid       bool     `json:"solid"`
}

type Avatar struct {
	Skin      string `json:"skin"`
	Hair      string `json:"hair"`
	HairColor string `json:"hair_color"`
	Top       string `json:"top"`
}

type Participant struct {
	ID          string  `json:"id"`
	SpaceID     string  `json:"space_id"`
	MapID       string  `json:"map_id"`
	Kind        string  `json:"kind"` // "human" | "agent"
	DisplayName string  `json:"display_name"`
	Avatar      Avatar  `json:"avatar"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
	Dir         string  `json:"dir"`
	Emote       string  `json:"emote"`
	Ghost       bool    `json:"ghost"`
	Muted       bool    `json:"muted"`
	CameraOff   bool    `json:"camera_off"`
	Speaking    bool    `json:"speaking"`
	Status      string  `json:"status"`
	AreaID      string  `json:"area_id"`
	UpdatedAt   string  `json:"updated_at"`
}

type ChatMessage struct {
	ID                string `json:"id"`
	SpaceID           string `json:"space_id"`
	FromParticipantID string `json:"from_participant_id"`
	FromName          string `json:"from_name"`
	FromKind          string `json:"from_kind"`
	Scope             string `json:"scope"` // nearby | space | dm
	ToParticipantID   string `json:"to_participant_id"`
	Text              string `json:"text"`
	CreatedAt         string `json:"created_at"`
}

type LiveKitInfo struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// JoinResponse mirrors POST /api/v1/spaces/:id/join (CONTRACT §3).
type JoinResponse struct {
	Participant Participant  `json:"participant"`
	Ticket      string       `json:"ticket"`
	WsURL       string       `json:"ws_url"`
	LiveKit     *LiveKitInfo `json:"livekit"`
	IceNote     string       `json:"ice_note,omitempty"`
}

// ---- client ----

// APIError is a parsed server error envelope {error:{code,message}} (CONTRACT §10).
type APIError struct {
	Status  int
	Code    string
	Message string
}

func (e *APIError) Error() string {
	code := e.Code
	if code == "" {
		code = "http_error"
	}
	return fmt.Sprintf("server error %s: %s (HTTP %d)", code, e.Message, e.Status)
}

// Client talks to the forge-space REST API with an agent key.
type Client struct {
	base string
	key  string
	http *http.Client
}

func NewClient(baseURL, key string) *Client {
	return &Client{
		base: strings.TrimSuffix(baseURL, "/"),
		key:  key,
		http: &http.Client{Timeout: 20 * time.Second},
	}
}

// do performs one REST call. Path is appended to the base URL; query holds
// URL query params; body (if non-nil) is JSON-encoded; out (if non-nil)
// receives the decoded JSON response. Non-2xx responses return *APIError.
func (c *Client) do(ctx context.Context, method, path string, query map[string]string, body any, out any) error {
	u := c.base + path
	if len(query) > 0 {
		q := url.Values{}
		for k, v := range query {
			q.Set(k, v)
		}
		u += "?" + q.Encode()
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		apiErr := &APIError{Status: resp.StatusCode}
		if json.Unmarshal(data, &e) == nil && e.Error.Code != "" {
			apiErr.Code = e.Error.Code
			apiErr.Message = e.Error.Message
		} else {
			apiErr.Message = strings.TrimSpace(string(data))
			if apiErr.Message == "" {
				apiErr.Message = resp.Status
			}
		}
		return apiErr
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
