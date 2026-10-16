package main

// Pure spatial logic mirroring CONTRACT §5 (proximity) exactly.
// No I/O here — trivially unit-testable.

import "math"

// areaContaining returns the first area rect on the given map containing (x, y),
// or nil. Rects are [X, X+W) x [Y, Y+H), matching tile-rect semantics.
func areaContaining(areas []Area, mapID string, x, y float64) *Area {
	for i := range areas {
		a := &areas[i]
		if a.MapID != mapID {
			continue
		}
		if x >= a.X && x < a.X+a.W && y >= a.Y && y < a.Y+a.H {
			return a
		}
	}
	return nil
}

// privateAreaOf returns the private area containing p, or nil.
func privateAreaOf(p Participant, areas []Area) *Area {
	if a := areaContaining(areas, p.MapID, p.X, p.Y); a != nil && a.Kind == "private" {
		return a
	}
	return nil
}

// inSpotlight reports whether p stands on a spotlight tile area.
func inSpotlight(p Participant, areas []Area) bool {
	a := areaContaining(areas, p.MapID, p.X, p.Y)
	return a != nil && a.Kind == "spotlight"
}

// audibleSet implements CONTRACT §5 exactly:
//
//	audible(P) := { Q on same map, Q≠P, Q not ghost, P not ghost,
//	  AND ( samePrivateArea(P,Q) → vol 1.0
//	     OR inPrivate(P) OR inPrivate(Q) → EXCLUDE   // private areas are fully
//	                                                 // isolated: occupants hear
//	                                                 // ONLY their own area
//	     OR Q in spotlight OR P in spotlight → vol 1.0
//	     OR dist(P,Q) ≤ R → vol = max(0, 1 - dist/R) ) }
//
// Volume is not returned here; the MCP world_look tool only needs the set.
func audibleSet(me Participant, others []Participant, areas []Area, radius float64) []Participant {
	if radius <= 0 {
		radius = 5 // CONTRACT §2 default proximity_radius
	}
	out := []Participant{}
	mePriv := privateAreaOf(me, areas)
	meSpot := inSpotlight(me, areas)
	for _, q := range others {
		if q.ID == me.ID || q.MapID != me.MapID {
			continue
		}
		if q.Ghost || me.Ghost {
			continue // ghosts hear no one and are heard by no one
		}
		qPriv := privateAreaOf(q, areas)
		switch {
		case mePriv != nil && qPriv != nil && mePriv.ID == qPriv.ID:
			out = append(out, q) // same private area: full volume
		case mePriv != nil || qPriv != nil:
			continue // private-area isolation: occupants hear ONLY their own area,
			// even in adjacent or different private areas
		case meSpot || inSpotlight(q, areas):
			out = append(out, q) // spotlight: broadcast to the whole map
		default:
			if math.Hypot(me.X-q.X, me.Y-q.Y) <= radius {
				out = append(out, q)
			}
		}
	}
	return out
}

// rectDist returns the Euclidean distance from point (px, py) to the object
// rect (0 when the point is inside). Used for the 2.5-tile interact radius in
// world_look.
func rectDist(px, py float64, o SpaceObject) float64 {
	dx := 0.0
	if px < o.X {
		dx = o.X - px
	} else if px > o.X+o.W {
		dx = px - (o.X + o.W)
	}
	dy := 0.0
	if py < o.Y {
		dy = o.Y - py
	} else if py > o.Y+o.H {
		dy = py - (o.Y + o.H)
	}
	return math.Hypot(dx, dy)
}
