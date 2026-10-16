package livekitx

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRoomName(t *testing.T) {
	if got := RoomName("sp_abc", "mp_def"); got != "fs_sp_abc_mp_def" {
		t.Fatalf("room name = %q", got)
	}
}

// TestMintTokenStructure mints a real token with the LiveKit SDK and checks
// the JWT payload carries identity, name, room grant and a ~6h expiry.
// (No live server needed — this validates the claim shape.)
func TestMintTokenStructure(t *testing.T) {
	c := &Client{URL: "ws://localhost:7880", Key: "testkey", Secret: "testsecret"}
	tok, err := c.MintToken("pt_abc123", "Alice", "fs_sp_x_mp_y")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %d segments", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Sub      string `json:"sub"`
		Identity string `json:"identity"`
		Name     string `json:"name"`
		Exp      int64  `json:"exp"`
		Video    struct {
			RoomJoin     bool   `json:"roomJoin"`
			Room         string `json:"room"`
			CanPublish   bool   `json:"canPublish"`
			CanSubscribe bool   `json:"canSubscribe"`
		} `json:"video"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	if claims.Identity != "pt_abc123" || claims.Sub != "pt_abc123" {
		t.Fatalf("identity/sub = %q/%q", claims.Identity, claims.Sub)
	}
	if claims.Name != "Alice" {
		t.Fatalf("name = %q", claims.Name)
	}
	if !claims.Video.RoomJoin || claims.Video.Room != "fs_sp_x_mp_y" {
		t.Fatalf("bad video grant: %+v", claims.Video)
	}
	if !claims.Video.CanPublish || !claims.Video.CanSubscribe {
		t.Fatalf("publish/subscribe not granted: %+v", claims.Video)
	}
	ttl := time.Until(time.Unix(claims.Exp, 0))
	if ttl < 5*time.Hour || ttl > 7*time.Hour {
		t.Fatalf("token TTL = %v, want ~6h", ttl)
	}
}

func TestDisabled(t *testing.T) {
	var c *Client
	if c.Enabled() {
		t.Fatal("nil client should be disabled")
	}
	c = &Client{}
	if c.Enabled() {
		t.Fatal("empty client should be disabled")
	}
}
