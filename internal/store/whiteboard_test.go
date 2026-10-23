package store

import (
	"testing"
)

// ---------- validation ----------

func goodPoints() []WhiteboardPoint {
	return []WhiteboardPoint{{X: 0, Y: 0}, {X: 1000, Y: 1000}}
}

func TestValidateStroke(t *testing.T) {
	cases := []struct {
		name    string
		color   string
		width   float64
		points  []WhiteboardPoint
		wantErr bool
	}{
		{"valid", "#ff0000", 3, goodPoints(), false},
		{"valid uppercase hex", "#A1B2C3", 1, goodPoints(), false},
		{"valid max width", "#000000", 20, goodPoints(), false},
		{"bad color name", "red", 3, goodPoints(), true},
		{"bad color short", "#fff", 3, goodPoints(), true},
		{"bad color no hash", "ff0000", 3, goodPoints(), true},
		{"bad color non-hex", "#gggggg", 3, goodPoints(), true},
		{"bad color 8 digits", "#ff0000ff", 3, goodPoints(), true},
		{"bad width 0", "#ff0000", 0, goodPoints(), true},
		{"bad width 21", "#ff0000", 21, goodPoints(), true},
		{"bad width negative", "#ff0000", -1, goodPoints(), true},
		{"too few points", "#ff0000", 3, []WhiteboardPoint{{X: 1, Y: 1}}, true},
		{"zero points", "#ff0000", 3, nil, true},
		{"point x negative", "#ff0000", 3, []WhiteboardPoint{{X: -1, Y: 1}, {X: 2, Y: 2}}, true},
		{"point x too big", "#ff0000", 3, []WhiteboardPoint{{X: 1001, Y: 1}, {X: 2, Y: 2}}, true},
		{"point y negative", "#ff0000", 3, []WhiteboardPoint{{X: 1, Y: -0.5}, {X: 2, Y: 2}}, true},
		{"point y too big", "#ff0000", 3, []WhiteboardPoint{{X: 1, Y: 1}, {X: 2, Y: 2000}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateStroke(tc.color, tc.width, tc.points)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateStroke(%q,%v,%d pts) err=%v, wantErr=%v",
					tc.color, tc.width, len(tc.points), err, tc.wantErr)
			}
		})
	}
	// 501 points → error; 500 → ok
	many := make([]WhiteboardPoint, 501)
	for i := range many {
		many[i] = WhiteboardPoint{X: 1, Y: 1}
	}
	if err := ValidateStroke("#ff0000", 3, many); err == nil {
		t.Fatal("501 points should fail")
	}
	if err := ValidateStroke("#ff0000", 3, many[:500]); err != nil {
		t.Fatalf("500 points should pass: %v", err)
	}
}

// ---------- persistence ----------

// seedBoardSpace creates user/space/map/whiteboard object for board tests.
func seedBoardSpace(t *testing.T, s *Store) (objID string) {
	t.Helper()
	u := &User{ID: NewID(PrefixUser), Email: "wb@x.com", DisplayName: "WB", CreatedAt: Now()}
	if _, err := s.CreateUser(u.ID, u.Email, "hash", u.DisplayName, u.CreatedAt); err != nil {
		t.Fatal(err)
	}
	sp := &Space{ID: NewID(PrefixSpace), Name: "WB Space", Slug: "wb-space",
		OwnerUserID: u.ID, Settings: SpaceSettings{ProximityRadius: 5}, CreatedAt: Now()}
	if err := s.CreateSpace(sp); err != nil {
		t.Fatal(err)
	}
	m := &Map{ID: NewID(PrefixMap), SpaceID: sp.ID, Name: "Lobby", Width: 40, Height: 30,
		Tiles: []int{1}, Spawn: Spawn{X: 20, Y: 15}, CreatedAt: Now()}
	if err := s.CreateMap(m); err != nil {
		t.Fatal(err)
	}
	o := &SpaceObject{ID: NewID(PrefixObject), MapID: m.ID, Kind: "whiteboard",
		X: 5, Y: 5, W: 4, H: 3, Title: "Board", CreatedAt: Now(), UpdatedAt: Now()}
	if err := s.CreateObject(o); err != nil {
		t.Fatal(err)
	}
	return o.ID
}

func TestStrokeCRUD(t *testing.T) {
	s, err := Open(t.TempDir() + "/wb.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	objID := seedBoardSpace(t, s)

	// empty board → empty slice, no error
	got, err := s.ListStrokes(objID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("expected empty non-nil list, got %v", got)
	}

	mk := func(color string) *WhiteboardStroke {
		return &WhiteboardStroke{
			ID: NewID(PrefixWhiteboardStroke), ObjectID: objID, ParticipantID: "pt_x",
			Color: color, Width: 3,
			Points:    []WhiteboardPoint{{X: 10, Y: 10}, {X: 20, Y: 20}},
			CreatedAt: Now(),
		}
	}
	a, b := mk("#ff0000"), mk("#00ff00")
	if err := s.CreateStroke(a); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateStroke(b); err != nil {
		t.Fatal(err)
	}
	got, err = s.ListStrokes(objID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 strokes, got %d", len(got))
	}
	if got[0].ID != a.ID || got[1].ID != b.ID {
		t.Fatal("strokes not in created_at order")
	}
	if got[0].Color != "#ff0000" || len(got[0].Points) != 2 || got[0].Points[1].X != 20 {
		t.Fatalf("stroke round-trip mismatch: %+v", got[0])
	}
	if got[0].ID[:4] != PrefixWhiteboardStroke {
		t.Fatalf("stroke id prefix = %q, want wbs_", got[0].ID[:4])
	}

	// clear
	if err := s.ClearStrokes(objID); err != nil {
		t.Fatal(err)
	}
	got, err = s.ListStrokes(objID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 strokes after clear, got %d", len(got))
	}
}

func TestStrokeCascadeOnObjectDelete(t *testing.T) {
	s, err := Open(t.TempDir() + "/wb.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	objID := seedBoardSpace(t, s)
	st := &WhiteboardStroke{
		ID: NewID(PrefixWhiteboardStroke), ObjectID: objID, ParticipantID: "pt_x",
		Color: "#0000ff", Width: 2,
		Points:    []WhiteboardPoint{{X: 1, Y: 1}, {X: 2, Y: 2}},
		CreatedAt: Now(),
	}
	if err := s.CreateStroke(st); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteObject(objID); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListStrokes(objID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("strokes should cascade-delete with the object, got %d", len(got))
	}
}
