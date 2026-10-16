// Package spatial is PURE world logic: no I/O, no DB, no network.
// Proximity implements CONTRACT.md §5 EXACTLY; the web client mirrors it in
// TypeScript (web/src/game). Coordinate convention: y grows DOWNWARD
// (up = -y), x grows rightward. Positions are float tile coordinates.
package spatial

import "math"

// Tile values per CONTRACT.md §2.
const (
	TileVoid      = 0
	TileFloor     = 1
	TileWall      = 2
	TilePrivate   = 3
	TileSpotlight = 4
	TileSpawn     = 5
)

// Physics constants per CONTRACT.md §4.
const (
	TickHz      = 15
	BroadcastHz = 10
	MaxSpeed    = 4.5 // tiles per second
	BodyRadius  = 0.3 // participant collision radius in tiles
	MaxPathLen  = 40  // BFS step limit for me_move_to
)

// Map is the tile grid a body moves on.
type Map struct {
	Width  int
	Height int
	Tiles  []int // row-major, len == Width*Height
}

// Area is a rect zone in tiles.
type Area struct {
	ID   string
	Kind string // "private" | "spotlight"
	X, Y int
	W, H int
}

// Body is a participant's spatial state for proximity/collision.
type Body struct {
	ID    string
	MapID string
	X, Y  float64
	Ghost bool
}

// Rect is a solid object footprint in tile coordinates.
type Rect struct {
	X, Y, W, H float64
}

// TileAt returns the tile value at integer coords; out-of-bounds is a wall.
func TileAt(m *Map, tx, ty int) int {
	if tx < 0 || ty < 0 || tx >= m.Width || ty >= m.Height {
		return TileWall
	}
	return m.Tiles[ty*m.Width+tx]
}

// AreaAt returns the first area containing point (x,y), or nil.
// Lookup is by position, never by tile value alone (CONTRACT.md §2).
func AreaAt(areas []Area, x, y float64) *Area {
	for i := range areas {
		a := &areas[i]
		if x >= float64(a.X) && x < float64(a.X+a.W) &&
			y >= float64(a.Y) && y < float64(a.Y+a.H) {
			return a
		}
	}
	return nil
}

func privateAreaAt(areas []Area, x, y float64) *Area {
	if a := AreaAt(areas, x, y); a != nil && a.Kind == "private" {
		return a
	}
	return nil
}

func inSpotlight(areas []Area, x, y float64) bool {
	if a := AreaAt(areas, x, y); a != nil && a.Kind == "spotlight" {
		return true
	}
	return false
}

// Dist is Euclidean distance in tiles.
func Dist(ax, ay, bx, by float64) float64 {
	return math.Hypot(ax-bx, ay-by)
}

// AudibleSet computes who me hears, EXACTLY per CONTRACT.md §5 (as amended):
//
//	R = proximity_radius (default 5)
//	audible(P) := { Q on same map, Q≠P, Q not ghost, P not ghost, AND (
//	  samePrivateArea(P,Q)            → vol 1.0
//	  OR inPrivate(P) OR inPrivate(Q) → EXCLUDE (private areas fully isolated)
//	  OR Q in spotlight OR P in spotlight → vol 1.0
//	  OR dist(P,Q) ≤ R                → vol = max(0, 1 - dist/R) ) }
//
// A private area is fully isolated: if either party is in ANY private area and
// they are not in the same one, they are excluded — no distance falloff leaks
// between adjacent meeting pods. Returns participant ID → volume (0.0–1.0).
// Ghost hears no one.
func AudibleSet(me Body, others []Body, areas []Area, radius float64) map[string]float64 {
	out := make(map[string]float64)
	if me.Ghost {
		return out
	}
	mePriv := privateAreaAt(areas, me.X, me.Y)
	meSpot := inSpotlight(areas, me.X, me.Y)
	for _, q := range others {
		if q.ID == me.ID || q.MapID != me.MapID || q.Ghost {
			continue
		}
		qPriv := privateAreaAt(areas, q.X, q.Y)
		qSpot := inSpotlight(areas, q.X, q.Y)
		switch {
		case mePriv != nil && qPriv != nil && mePriv.ID == qPriv.ID:
			out[q.ID] = 1.0
		case mePriv != nil || qPriv != nil:
			// either in a private area (but not the same one): fully isolated
		case meSpot || qSpot:
			out[q.ID] = 1.0
		default:
			d := Dist(me.X, me.Y, q.X, q.Y)
			if d <= radius {
				v := 1.0
				if radius > 0 {
					v = 1 - d/radius
					if v < 0 {
						v = 0
					}
				}
				out[q.ID] = v
			}
		}
	}
	return out
}

// circleRectOverlap reports whether a circle overlaps an axis-aligned rect.
func circleRectOverlap(cx, cy, r, rx, ry, rw, rh float64) bool {
	nx := math.Max(rx, math.Min(cx, rx+rw))
	ny := math.Max(ry, math.Min(cy, ry+rh))
	dx, dy := cx-nx, cy-ny
	return dx*dx+dy*dy < r*r
}

// rectsOverlap reports whether two axis-aligned rects overlap.
func rectsOverlap(ax, ay, aw, ah, bx, by, bw, bh float64) bool {
	return ax < bx+bw && ax+aw > bx && ay < by+bh && ay+ah > by
}

// Collides reports whether a body circle at (x,y) hits a wall tile or a
// solid footprint. Ghosts never collide (handled by the caller via Move).
func Collides(m *Map, solids []Rect, x, y float64) bool {
	minTX := int(math.Floor(x - BodyRadius))
	maxTX := int(math.Floor(x + BodyRadius))
	minTY := int(math.Floor(y - BodyRadius))
	maxTY := int(math.Floor(y + BodyRadius))
	for ty := minTY; ty <= maxTY; ty++ {
		for tx := minTX; tx <= maxTX; tx++ {
			if TileAt(m, tx, ty) == TileWall &&
				circleRectOverlap(x, y, BodyRadius, float64(tx), float64(ty), 1, 1) {
				return true
			}
		}
	}
	for _, s := range solids {
		if circleRectOverlap(x, y, BodyRadius, s.X, s.Y, s.W, s.H) {
			return true
		}
	}
	return false
}

// Move applies an axis-separated move: X first, then Y, each reverted on
// collision — this yields wall sliding. Ghosts pass through everything but
// are clamped to the map bounds.
func Move(m *Map, solids []Rect, x, y, dx, dy float64, ghost bool) (float64, float64) {
	nx := x + dx
	if !ghost && Collides(m, solids, nx, y) {
		nx = x
	}
	ny := y + dy
	if !ghost && Collides(m, solids, nx, ny) {
		ny = y
	}
	nx = clamp(nx, 0, float64(m.Width))
	ny = clamp(ny, 0, float64(m.Height))
	return nx, ny
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Integrate moves a body along (dx,dy) for dt seconds, normalizing diagonal
// input and capping speed at MaxSpeed tiles/s. Returns the raw (uncollided)
// delta — the caller applies it via Move for collision.
func Integrate(dx, dy, dt float64) (float64, float64) {
	if l := math.Hypot(dx, dy); l > 1 {
		dx, dy = dx/l, dy/l
	}
	step := MaxSpeed * dt
	return dx * step, dy * step
}

// DirFromDelta maps a movement delta to a facing direction (y grows downward,
// so dy<0 is "up"). Returns "" when there is no movement.
func DirFromDelta(dx, dy float64) string {
	if dx == 0 && dy == 0 {
		return ""
	}
	if math.Abs(dx) >= math.Abs(dy) {
		if dx > 0 {
			return "right"
		}
		return "left"
	}
	if dy > 0 {
		return "down"
	}
	return "up"
}

// tileWalkable reports whether integer tile (tx,ty) can be pathed through.
func tileWalkable(m *Map, solids []Rect, tx, ty int) bool {
	if TileAt(m, tx, ty) == TileWall {
		return false
	}
	for _, s := range solids {
		if rectsOverlap(float64(tx), float64(ty), 1, 1, s.X, s.Y, s.W, s.H) {
			return false
		}
	}
	return true
}

// FindPath runs BFS on the tile grid from (fx,fy) to (tx,ty) and returns the
// tile path (inclusive of both ends), or nil when unreachable within maxSteps.
// Used by me_move_to: the hub follows the waypoints with collision slide.
func FindPath(m *Map, solids []Rect, fx, fy, tx, ty float64, maxSteps int) [][2]int {
	sx, sy := int(math.Floor(fx)), int(math.Floor(fy))
	gx, gy := int(math.Floor(tx)), int(math.Floor(ty))
	if !tileWalkable(m, solids, sx, sy) || !tileWalkable(m, solids, gx, gy) {
		return nil
	}
	if Collides(m, solids, tx, ty) {
		return nil // exact target point is inside a wall/footprint
	}
	if sx == gx && sy == gy {
		return [][2]int{{sx, sy}}
	}
	type node struct{ x, y int }
	prev := map[node]node{}
	dist := map[node]int{{sx, sy}: 0}
	queue := []node{{sx, sy}}
	dirs := [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.x == gx && cur.y == gy {
			// reconstruct
			var path [][2]int
			for n := cur; ; {
				path = append([][2]int{{n.x, n.y}}, path...)
				p, ok := prev[n]
				if !ok {
					break
				}
				n = p
			}
			return path
		}
		if dist[cur] >= maxSteps {
			continue
		}
		for _, d := range dirs {
			nx, ny := cur.x+d[0], cur.y+d[1]
			n := node{nx, ny}
			if _, seen := dist[n]; seen {
				continue
			}
			if !tileWalkable(m, solids, nx, ny) {
				continue
			}
			dist[n] = dist[cur] + 1
			prev[n] = cur
			queue = append(queue, n)
		}
	}
	return nil
}

// Reachable is FindPath != nil.
func Reachable(m *Map, solids []Rect, fx, fy, tx, ty float64, maxSteps int) bool {
	return FindPath(m, solids, fx, fy, tx, ty, maxSteps) != nil
}
