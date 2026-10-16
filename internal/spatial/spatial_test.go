package spatial

import (
	"math"
	"testing"
)

func openMap(w, h int) *Map {
	tiles := make([]int, w*h)
	for i := range tiles {
		tiles[i] = TileFloor
	}
	return &Map{Width: w, Height: h, Tiles: tiles}
}

func body(id, mapID string, x, y float64) Body {
	return Body{ID: id, MapID: mapID, X: x, Y: y}
}

// ---------- proximity: basics ----------

func TestAudibleSetBasic(t *testing.T) {
	m := "mp_test"
	me := body("pt_me", m, 0, 0)
	others := []Body{
		body("pt_near", m, 3, 0),  // dist 3, R=5 → 0.4
		body("pt_edge", m, 5, 0),  // dist 5 → vol 0
		body("pt_far", m, 5.1, 0), // beyond R → excluded
		body("pt_self", m, 0, 0),  // not me (different id, same spot) → vol 1.0
		body("pt_other_map", "mp_x", 0, 0),
	}
	others[3].ID = "pt_zero"
	got := AudibleSet(me, others, nil, 5)
	if v := got["pt_near"]; math.Abs(v-0.4) > 1e-9 {
		t.Fatalf("pt_near vol = %v, want 0.4", v)
	}
	if v, ok := got["pt_edge"]; !ok || v != 0 {
		t.Fatalf("pt_edge = %v,%v want (0,true)", v, ok)
	}
	if _, ok := got["pt_far"]; ok {
		t.Fatal("pt_far should be excluded")
	}
	if v := got["pt_zero"]; v != 1.0 {
		t.Fatalf("pt_zero vol = %v, want 1.0", v)
	}
	if _, ok := got["pt_other_map"]; ok {
		t.Fatal("other-map participant should be excluded")
	}
	if _, ok := got["pt_me"]; ok {
		t.Fatal("self should be excluded")
	}
}

func TestAudibleSetGhost(t *testing.T) {
	m := "mp_test"
	// ghost Q excluded from everyone's set
	me := body("pt_me", m, 0, 0)
	ghost := body("pt_ghost", m, 1, 0)
	ghost.Ghost = true
	if got := AudibleSet(me, []Body{ghost}, nil, 5); len(got) != 0 {
		t.Fatalf("ghost Q should be excluded, got %v", got)
	}
	// ghost P hears no one
	meGhost := body("pt_me", m, 0, 0)
	meGhost.Ghost = true
	if got := AudibleSet(meGhost, []Body{body("pt_q", m, 1, 0)}, nil, 5); len(got) != 0 {
		t.Fatalf("ghost P should hear no one, got %v", got)
	}
}

func TestAudibleSetNonTransitive(t *testing.T) {
	m := "mp_test"
	a := body("pt_a", m, 0, 0)
	b := body("pt_b", m, 4, 0)
	c := body("pt_c", m, 8, 0)
	all := []Body{a, b, c}
	ha := AudibleSet(a, all, nil, 5)
	hb := AudibleSet(b, all, nil, 5)
	hc := AudibleSet(c, all, nil, 5)
	if _, ok := ha["pt_b"]; !ok {
		t.Fatal("A should hear B")
	}
	if _, ok := ha["pt_c"]; ok {
		t.Fatal("A should NOT hear C (non-transitive)")
	}
	if _, ok := hb["pt_a"]; !ok {
		t.Fatal("B should hear A")
	}
	if _, ok := hb["pt_c"]; !ok {
		t.Fatal("B should hear C")
	}
	if _, ok := hc["pt_b"]; !ok {
		t.Fatal("C should hear B")
	}
	if _, ok := hc["pt_a"]; ok {
		t.Fatal("C should NOT hear A")
	}
}

// ---------- proximity: private areas ----------

func TestAudibleSetPrivate(t *testing.T) {
	m := "mp_test"
	privA := Area{ID: "ar_a", Kind: "private", X: 0, Y: 0, W: 4, H: 4}
	privB := Area{ID: "ar_b", Kind: "private", X: 4, Y: 0, W: 4, H: 4} // adjacent pod
	areas := []Area{privA, privB}

	t.Run("same area → 1.0 regardless of distance", func(t *testing.T) {
		p := body("pt_p", m, 0.5, 0.5)
		q := body("pt_q", m, 3.5, 3.5) // same area (privA), distance irrelevant
		got := AudibleSet(p, []Body{q}, areas, 5)
		if v := got["pt_q"]; v != 1.0 {
			t.Fatalf("same private area vol = %v, want 1.0", v)
		}
	})
	t.Run("inside vs outside → excluded both ways", func(t *testing.T) {
		p := body("pt_p", m, 1, 1)   // in privA
		q := body("pt_q", m, 10, 10) // outside
		if got := AudibleSet(p, []Body{q}, areas, 5); len(got) != 0 {
			t.Fatalf("private→outside should be excluded, got %v", got)
		}
		if got := AudibleSet(q, []Body{p}, areas, 5); len(got) != 0 {
			t.Fatalf("outside→private should be excluded, got %v", got)
		}
	})
	t.Run("adjacent different areas do not leak", func(t *testing.T) {
		// P at x=3.9 (privA), Q at x=4.1 (privB): 0.2 tiles apart, different pods
		p := body("pt_p", m, 3.9, 2)
		q := body("pt_q", m, 4.1, 2)
		if got := AudibleSet(p, []Body{q}, areas, 5); len(got) != 0 {
			t.Fatalf("adjacent private pods must not leak audio, got %v", got)
		}
	})
	t.Run("rect is half-open", func(t *testing.T) {
		// x=4.0 is NOT inside privA [0,4)
		if a := AreaAt(areas, 4.0, 2); a != nil && a.ID == "ar_a" {
			t.Fatal("x=4.0 should be outside privA (half-open)")
		}
		if a := AreaAt(areas, 3.999, 2); a == nil || a.ID != "ar_a" {
			t.Fatal("x=3.999 should be inside privA")
		}
		if a := AreaAt(areas, 4.0, 2); a == nil || a.ID != "ar_b" {
			t.Fatal("x=4.0 should be inside privB")
		}
	})
}

// ---------- proximity: spotlight ----------

func TestAudibleSetSpotlight(t *testing.T) {
	m := "mp_test"
	spot := Area{ID: "ar_s", Kind: "spotlight", X: 10, Y: 10, W: 4, H: 4}
	areas := []Area{spot}
	t.Run("spotlight occupant broadcast to whole map", func(t *testing.T) {
		q := body("pt_q", m, 11, 11) // in spotlight
		p := body("pt_p", m, 0, 0)   // 15+ tiles away
		got := AudibleSet(p, []Body{q}, areas, 5)
		if v := got["pt_q"]; v != 1.0 {
			t.Fatalf("spotlight vol = %v, want 1.0", v)
		}
	})
	t.Run("spotlight occupant hears everyone", func(t *testing.T) {
		p := body("pt_p", m, 11, 11) // in spotlight
		q := body("pt_q", m, 0, 0)
		got := AudibleSet(p, []Body{q}, areas, 5)
		if v := got["pt_q"]; v != 1.0 {
			t.Fatalf("spotlight occupant vol = %v, want 1.0", v)
		}
	})
	t.Run("private isolation beats spotlight", func(t *testing.T) {
		priv := Area{ID: "ar_p", Kind: "private", X: 0, Y: 0, W: 4, H: 4}
		areas2 := []Area{priv, spot}
		p := body("pt_p", m, 1, 1)   // in private
		q := body("pt_q", m, 11, 11) // in spotlight
		if got := AudibleSet(p, []Body{q}, areas2, 5); len(got) != 0 {
			t.Fatalf("private member must not hear spotlight, got %v", got)
		}
	})
}

// ---------- collision & movement ----------

func TestCollisionSlide(t *testing.T) {
	// vertical wall at x=5
	m := openMap(20, 20)
	for y := 0; y < 20; y++ {
		m.Tiles[y*20+5] = TileWall
	}
	// straight into wall: x blocked
	nx, ny := Move(m, nil, 4.0, 10.0, 2.0, 0, false)
	if nx >= 4.7 {
		t.Fatalf("walked through wall: x=%v", nx)
	}
	if ny != 10.0 {
		t.Fatalf("y should be unchanged, got %v", ny)
	}
	// diagonal into wall: slides along y
	nx, ny = Move(m, nil, 4.0, 10.0, 2.0, 1.5, false)
	if nx >= 4.7 {
		t.Fatalf("walked through wall diagonally: x=%v", nx)
	}
	if ny <= 10.0 {
		t.Fatalf("should have slid along y, got y=%v", ny)
	}
	// open space: full move
	nx, ny = Move(m, nil, 1.0, 1.0, 1.0, 1.0, false)
	if nx != 2.0 || ny != 2.0 {
		t.Fatalf("open move = (%v,%v), want (2,2)", nx, ny)
	}
	// ghost walks through
	nx, _ = Move(m, nil, 4.0, 10.0, 2.0, 0, true)
	if nx != 6.0 {
		t.Fatalf("ghost should pass through wall, x=%v", nx)
	}
	// solid object footprint blocks
	solids := []Rect{{X: 8, Y: 8, W: 2, H: 2}}
	nx, _ = Move(m, solids, 7.0, 9.0, 2.0, 0, false)
	if nx >= 7.7 {
		t.Fatalf("walked through solid object: x=%v", nx)
	}
}

func TestSpeedCap(t *testing.T) {
	dx, dy := Integrate(1, 0, 1.0/TickHz)
	if math.Abs(math.Hypot(dx, dy)-MaxSpeed/TickHz) > 1e-9 {
		t.Fatalf("axis step = %v, want %v", math.Hypot(dx, dy), MaxSpeed/TickHz)
	}
	// diagonal input is normalized: total speed still capped
	dx, dy = Integrate(1, 1, 1.0/TickHz)
	if math.Abs(math.Hypot(dx, dy)-MaxSpeed/TickHz) > 1e-9 {
		t.Fatalf("diagonal step = %v, want capped %v", math.Hypot(dx, dy), MaxSpeed/TickHz)
	}
}

func TestDirFromDelta(t *testing.T) {
	cases := map[[2]float64]string{
		{0, -1}:   "up", // y grows downward → up is -y
		{0, 1}:    "down",
		{1, 0}:    "right",
		{-1, 0}:   "left",
		{0.5, -2}: "up", // dominant axis wins
		{3, 1}:    "right",
	}
	for d, want := range cases {
		if got := DirFromDelta(d[0], d[1]); got != want {
			t.Fatalf("DirFromDelta(%v) = %q, want %q", d, got, want)
		}
	}
	if got := DirFromDelta(0, 0); got != "" {
		t.Fatalf("zero delta should return empty, got %q", got)
	}
}

// ---------- BFS ----------

func TestReachable(t *testing.T) {
	m := openMap(50, 10)
	// open field: reachable
	if !Reachable(m, nil, 1, 1, 40, 1, MaxPathLen) {
		t.Fatal("open field should be reachable")
	}
	// wall across the whole map: unreachable
	for y := 0; y < 10; y++ {
		m.Tiles[y*50+20] = TileWall
	}
	if Reachable(m, nil, 1, 1, 40, 1, MaxPathLen) {
		t.Fatal("walled-off target should be unreachable")
	}
	// target inside wall: unreachable
	if Reachable(m, nil, 1, 1, 20, 1, MaxPathLen) {
		t.Fatal("target inside wall should be unreachable")
	}
	// >40 tiles: unreachable even on open map
	m2 := openMap(100, 10)
	if Reachable(m2, nil, 1, 1, 90, 1, MaxPathLen) {
		t.Fatal("target >40 tiles away should be unreachable")
	}
	if path := FindPath(m2, nil, 1, 1, 30, 1, MaxPathLen); path == nil {
		t.Fatal("30-tile path should exist")
	} else if len(path) > MaxPathLen+1 {
		t.Fatalf("path too long: %d", len(path))
	}
	// solid object corridor: path exists around it
	m3 := openMap(20, 20)
	solids := []Rect{{X: 5, Y: 0, W: 2, H: 18}} // wall-like furniture, gap at bottom
	if !Reachable(m3, solids, 1, 1, 10, 19, MaxPathLen) {
		t.Fatal("should path around solid object via gap")
	}
}
