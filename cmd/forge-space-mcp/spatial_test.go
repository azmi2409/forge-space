package main

// Table tests for CONTRACT §5 proximity (mirrors internal/spatial semantics).

import "testing"

func mkP(id string, x, y float64) Participant {
	return Participant{ID: id, MapID: "mp_1", Kind: "agent", DisplayName: id, X: x, Y: y}
}

func ids(ps []Participant) map[string]bool {
	m := map[string]bool{}
	for _, p := range ps {
		m[p.ID] = true
	}
	return m
}

func TestAudibleSet(t *testing.T) {
	privArea := Area{ID: "ar_priv", MapID: "mp_1", Kind: "private", Name: "Room", X: 18, Y: 18, W: 4, H: 4}
	spotArea := Area{ID: "ar_spot", MapID: "mp_1", Kind: "spotlight", Name: "Stage", X: 28, Y: 28, W: 4, H: 4}
	areas := []Area{privArea, spotArea}

	tests := []struct {
		name   string
		me     Participant
		others []Participant
		radius float64
		want   []string // ids expected in the audible set
	}{
		{
			name:   "within radius is audible",
			me:     mkP("me", 10, 10),
			others: []Participant{mkP("near", 12, 10)},
			radius: 5,
			want:   []string{"near"},
		},
		{
			name:   "beyond radius is not audible",
			me:     mkP("me", 10, 10),
			others: []Participant{mkP("far", 20, 10)},
			radius: 5,
			want:   []string{},
		},
		{
			name:   "exactly at radius boundary is audible (dist <= R)",
			me:     mkP("me", 10, 10),
			others: []Participant{mkP("edge", 15, 10)},
			radius: 5,
			want:   []string{"edge"},
		},
		{
			name:   "ghost other is excluded",
			me:     mkP("me", 10, 10),
			others: []Participant{{ID: "g", MapID: "mp_1", X: 11, Y: 10, Ghost: true}},
			radius: 5,
			want:   []string{},
		},
		{
			name:   "ghost me hears no one",
			me:     Participant{ID: "me", MapID: "mp_1", X: 10, Y: 10, Ghost: true},
			others: []Participant{mkP("near", 11, 10)},
			radius: 5,
			want:   []string{},
		},
		{
			name:   "same private area hears across distance",
			me:     mkP("me", 19, 19),
			others: []Participant{mkP("mate", 21, 21)},
			radius: 5,
			want:   []string{"mate"},
		},
		{
			name:   "me in private, other outside and close is excluded",
			me:     mkP("me", 19, 19),
			others: []Participant{mkP("out", 17.5, 19)}, // 1.5 tiles away but outside
			radius: 5,
			want:   []string{},
		},
		{
			name:   "other in private, me outside is excluded",
			me:     mkP("me", 10, 10),
			others: []Participant{mkP("inpriv", 20, 20)},
			radius: 50, // even huge radius does not leak
			want:   []string{},
		},
		{
			name:   "spotlight occupant is audible map-wide",
			me:     mkP("me", 10, 10),
			others: []Participant{mkP("star", 30, 30)},
			radius: 5,
			want:   []string{"star"},
		},
		{
			name:   "me in spotlight hears far others",
			me:     mkP("me", 30, 30),
			others: []Participant{mkP("far", 10, 10)},
			radius: 5,
			want:   []string{"far"},
		},
		{
			name:   "different map is excluded",
			me:     mkP("me", 10, 10),
			others: []Participant{{ID: "other", MapID: "mp_2", X: 10, Y: 10}},
			radius: 5,
			want:   []string{},
		},
		{
			name:   "self is excluded",
			me:     mkP("me", 10, 10),
			others: []Participant{mkP("me", 10, 10)},
			radius: 5,
			want:   []string{},
		},
		{
			name:   "zero radius falls back to default 5",
			me:     mkP("me", 10, 10),
			others: []Participant{mkP("near", 14, 10)},
			radius: 0,
			want:   []string{"near"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := audibleSet(tc.me, tc.others, areas, tc.radius)
			gotIDs := ids(got)
			if len(gotIDs) != len(tc.want) {
				t.Fatalf("audible set = %v, want %v", gotIDs, tc.want)
			}
			for _, w := range tc.want {
				if !gotIDs[w] {
					t.Fatalf("audible set = %v, want %v", gotIDs, tc.want)
				}
			}
		})
	}
}

func TestAreaContaining(t *testing.T) {
	areas := []Area{
		{ID: "ar_1", MapID: "mp_1", Kind: "private", Name: "Room", X: 18, Y: 18, W: 4, H: 4},
	}
	if a := areaContaining(areas, "mp_1", 19, 19); a == nil || a.ID != "ar_1" {
		t.Fatalf("expected ar_1, got %+v", a)
	}
	if a := areaContaining(areas, "mp_1", 10, 10); a != nil {
		t.Fatalf("expected nil, got %+v", a)
	}
	if a := areaContaining(areas, "mp_2", 19, 19); a != nil {
		t.Fatalf("expected nil for other map, got %+v", a)
	}
	// boundary: rect is [X, X+W) x [Y, Y+H)
	if a := areaContaining(areas, "mp_1", 22, 19); a != nil {
		t.Fatalf("expected nil on exclusive edge, got %+v", a)
	}
}

func TestRectDist(t *testing.T) {
	o := SpaceObject{X: 11, Y: 11, W: 1, H: 1}
	if d := rectDist(10, 10, o); d < 1.41 || d > 1.42 {
		t.Fatalf("expected ~1.414, got %v", d)
	}
	if d := rectDist(11.5, 11.5, o); d != 0 {
		t.Fatalf("expected 0 inside, got %v", d)
	}
	if d := rectDist(11.5, 9, o); d != 2 {
		t.Fatalf("expected 2 above, got %v", d)
	}
}
