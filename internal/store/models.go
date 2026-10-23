package store

// Models mirror the JSON shapes in CONTRACT.md §2 exactly.

type Avatar struct {
	Skin      int `json:"skin"`
	Hair      int `json:"hair"`
	HairColor int `json:"hair_color"`
	Top       int `json:"top"`
}

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

type Spawn struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type Map struct {
	ID        string `json:"id"`
	SpaceID   string `json:"space_id"`
	Name      string `json:"name"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Tiles     []int  `json:"tiles,omitempty"`
	Spawn     Spawn  `json:"spawn"`
	CreatedAt string `json:"created_at"`
}

// MapSummary is a Map without tile data, for space detail responses.
type MapSummary struct {
	ID      string `json:"id"`
	SpaceID string `json:"space_id"`
	Name    string `json:"name"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
}

func (m *Map) Summary() MapSummary {
	return MapSummary{ID: m.ID, SpaceID: m.SpaceID, Name: m.Name, Width: m.Width, Height: m.Height}
}

type Area struct {
	ID        string `json:"id"`
	MapID     string `json:"map_id"`
	Kind      string `json:"kind"` // private|spotlight
	Name      string `json:"name"`
	X         int    `json:"x"`
	Y         int    `json:"y"`
	W         int    `json:"w"`
	H         int    `json:"h"`
	CreatedAt string `json:"created_at,omitempty"`
}

type SpaceObject struct {
	ID        string  `json:"id"`
	MapID     string  `json:"map_id"`
	Kind      string  `json:"kind"` // note|whiteboard|url|video|poster|portal
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	W         float64 `json:"w"`
	H         float64 `json:"h"`
	Title     string  `json:"title"`
	Body      string  `json:"body"`
	URL       string  `json:"url,omitempty"`
	TargetMap string  `json:"target_map_id,omitempty"`
	TargetX   float64 `json:"target_x,omitempty"`
	TargetY   float64 `json:"target_y,omitempty"`
	Solid     bool    `json:"solid"`
	CreatedAt string  `json:"created_at,omitempty"`
	UpdatedAt string  `json:"updated_at,omitempty"`
}

type Participant struct {
	ID          string  `json:"id"`
	SpaceID     string  `json:"space_id"`
	MapID       string  `json:"map_id"`
	Kind        string  `json:"kind"` // human|agent
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
	// Owner fields are internal (never serialized to clients).
	OwnerUserID     string `json:"-"`
	OwnerAgentKeyID string `json:"-"`
	CreatedAt       string `json:"-"`
}

type ChatMessage struct {
	ID                string `json:"id"`
	SpaceID           string `json:"space_id"`
	FromParticipantID string `json:"from_participant_id"`
	FromName          string `json:"from_name"`
	FromKind          string `json:"from_kind"`
	Scope             string `json:"scope"` // nearby|space|dm
	ToParticipantID   string `json:"to_participant_id,omitempty"`
	Text              string `json:"text"`
	Announce          bool   `json:"announce,omitempty"`
	CreatedAt         string `json:"created_at"`
	// Recipients is internal: participant IDs that may see a nearby message.
	Recipients []string `json:"-"`
}

type User struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	CreatedAt   string `json:"created_at"`
}

type AgentKey struct {
	ID        string   `json:"id"`
	UserID    string   `json:"user_id,omitempty"`
	Name      string   `json:"name"`
	Scopes    []string `json:"scopes"`
	CreatedAt string   `json:"created_at"`
}

type Ticket struct {
	ID            string `json:"id"`
	ParticipantID string `json:"participant_id"`
	CreatedAt     string `json:"created_at"`
	// ExpiresAt/Used/Hash are internal.
	ExpiresAt string
	Used      bool
}

// WhiteboardPoint is one pen point in board coords (0..1000 normalized).
type WhiteboardPoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// WhiteboardStroke is one pen stroke on a kind=whiteboard object
// (WHITEBOARD_V1.md amendment).
type WhiteboardStroke struct {
	ID            string            `json:"id"`
	ObjectID      string            `json:"object_id"`
	ParticipantID string            `json:"participant_id"`
	Color         string            `json:"color"`
	Width         float64           `json:"width"`
	Points        []WhiteboardPoint `json:"points"`
	CreatedAt     string            `json:"created_at"`
}
