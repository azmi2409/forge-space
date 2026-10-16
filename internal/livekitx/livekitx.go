// Package livekitx mints LiveKit access tokens for voice/video.
// Token minting uses github.com/livekit/protocol/auth (the same package the
// official server-sdk-go/v2 delegates to — verified via go doc; the module
// path in CONTRACT.md §0 has been corrected, see handoff notes).
// When LIVEKIT_URL is unset, media is disabled: join returns livekit:null and
// /media/token answers 401.
package livekitx

import (
	"time"

	"github.com/livekit/protocol/auth"
)

// TokenTTL is 6h per CONTRACT.md §6.
const TokenTTL = 6 * time.Hour

// Client holds LiveKit credentials.
type Client struct {
	URL    string
	Key    string
	Secret string
}

// Enabled reports whether LiveKit is fully configured.
func (c *Client) Enabled() bool { return c != nil && c.URL != "" && c.Key != "" && c.Secret != "" }

// RoomName builds the room name: fs_<spaceID>_<mapID> (CONTRACT.md §6).
// IDs are prefix + base36, already a safe charset.
func RoomName(spaceID, mapID string) string {
	return "fs_" + spaceID + "_" + mapID
}

func boolPtr(b bool) *bool { return &b }

// MintToken mints a participant token: identity=participant ID, grant
// roomJoin + room, canPublish + canSubscribe, TTL 6h.
func (c *Client) MintToken(participantID, displayName, room string) (string, error) {
	at := auth.NewAccessToken(c.Key, c.Secret)
	at.SetIdentity(participantID).
		SetName(displayName).
		SetValidFor(TokenTTL).
		SetVideoGrant(&auth.VideoGrant{
			RoomJoin:     true,
			Room:         room,
			CanPublish:   boolPtr(true),
			CanSubscribe: boolPtr(true),
		})
	return at.ToJWT()
}
