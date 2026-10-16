// Package realtime is the authoritative simulation hub: 15Hz tick, 10Hz
// state broadcasts, WS connections (CONTRACT.md §4), chat fanout with
// proximity filtering, and the SSE agent stream (CONTRACT.md §7).
package realtime

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"

	"nhooyr.io/websocket"

	"github.com/azmi2409/forge-space/internal/auth"
	"github.com/azmi2409/forge-space/internal/spatial"
	"github.com/azmi2409/forge-space/internal/store"
)

// InteractRadius is how close a participant must be to use an object.
const InteractRadius = 2.5

type inputState struct {
	dx, dy float64
	seq    int
}

type pathState struct {
	waypoints [][2]int // tile path, head = next waypoint
	targetX   float64
	targetY   float64
	stuck     int // ticks without progress
	lastX     float64
	lastY     float64
}

type partRT struct {
	p store.Participant
}

type mapRT struct {
	spatial *spatial.Map
	areas   []spatial.Area
	objects []*store.SpaceObject
	solids  []spatial.Rect
	parts   map[string]*partRT // participantID -> runtime
}

type spaceRT struct {
	settings store.SpaceSettings
	maps     map[string]*mapRT
}

// Hub is the central simulation. All state mutations go through its mutex;
// outbound socket writes never hold the mutex (per-conn send channels).
type Hub struct {
	store *store.Store

	mu        sync.Mutex
	spaces    map[string]*spaceRT
	conns     map[string]*Conn
	inputs    map[string]inputState
	paths     map[string]*pathState
	lastAudio map[string]map[string]float64
	lastArea  map[string]string
	dirty     map[string]bool
	chatWin   map[string][]time.Time
	seq       uint64

	closed chan struct{}
	wg     sync.WaitGroup
}

// New creates a Hub. Call Run to start the tick loops.
func New(st *store.Store) *Hub {
	return &Hub{
		store:     st,
		spaces:    map[string]*spaceRT{},
		conns:     map[string]*Conn{},
		inputs:    map[string]inputState{},
		paths:     map[string]*pathState{},
		lastAudio: map[string]map[string]float64{},
		lastArea:  map[string]string{},
		dirty:     map[string]bool{},
		chatWin:   map[string][]time.Time{},
		closed:    make(chan struct{}),
	}
}

// Run starts the 15Hz tick and 10Hz broadcast loops. Blocks until Close.
func (h *Hub) Run() {
	tick := time.NewTicker(time.Second / spatial.TickHz)
	defer tick.Stop()
	bcast := time.NewTicker(time.Second / spatial.BroadcastHz)
	defer bcast.Stop()
	flush := time.NewTicker(5 * time.Second)
	defer flush.Stop()
	for {
		select {
		case <-h.closed:
			return
		case <-tick.C:
			h.tick()
		case <-bcast.C:
			h.broadcast()
		case <-flush.C:
			h.flushPositions()
		}
	}
}

// Close stops the hub loops.
func (h *Hub) Close() {
	select {
	case <-h.closed:
	default:
		close(h.closed)
	}
	h.wg.Wait()
}

// ---------- space runtime cache ----------

func (h *Hub) loadSpace(spaceID string) (*spaceRT, error) {
	sp, err := h.store.GetSpace(spaceID)
	if err != nil {
		return nil, err
	}
	rt := &spaceRT{settings: sp.Settings, maps: map[string]*mapRT{}}
	maps, err := h.store.ListMapsBySpace(spaceID)
	if err != nil {
		return nil, err
	}
	for _, m := range maps {
		full, err := h.store.GetMap(m.ID)
		if err != nil {
			return nil, err
		}
		mrt := &mapRT{
			spatial: &spatial.Map{Width: full.Width, Height: full.Height, Tiles: full.Tiles},
			parts:   map[string]*partRT{},
		}
		if err := h.loadMapExtras(mrt, full.ID); err != nil {
			return nil, err
		}
		rt.maps[full.ID] = mrt
	}
	parts, err := h.store.ListParticipantsBySpace(spaceID)
	if err != nil {
		return nil, err
	}
	for _, p := range parts {
		if mrt := rt.maps[p.MapID]; mrt != nil {
			mrt.parts[p.ID] = &partRT{p: *p}
			h.lastArea[p.ID] = p.AreaID
		}
	}
	return rt, nil
}

func (h *Hub) loadMapExtras(mrt *mapRT, mapID string) error {
	areas, err := h.store.ListAreasByMap(mapID)
	if err != nil {
		return err
	}
	mrt.areas = mrt.areas[:0]
	for _, a := range areas {
		mrt.areas = append(mrt.areas, spatial.Area{ID: a.ID, Kind: a.Kind, X: a.X, Y: a.Y, W: a.W, H: a.H})
	}
	objects, err := h.store.ListObjectsByMap(mapID)
	if err != nil {
		return err
	}
	mrt.objects = objects
	mrt.solids = mrt.solids[:0]
	for _, o := range objects {
		if o.Solid {
			mrt.solids = append(mrt.solids, spatial.Rect{X: o.X, Y: o.Y, W: o.W, H: o.H})
		}
	}
	return nil
}

// getSpace returns the cached runtime, loading it on first use. Caller must
// NOT hold h.mu (it takes it).
func (h *Hub) getSpace(spaceID string) (*spaceRT, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.getSpaceLocked(spaceID)
}

func (h *Hub) getSpaceLocked(spaceID string) (*spaceRT, error) {
	if rt, ok := h.spaces[spaceID]; ok {
		return rt, nil
	}
	rt, err := h.loadSpace(spaceID)
	if err != nil {
		return nil, err
	}
	h.spaces[spaceID] = rt
	return rt, nil
}

// RefreshSpace reloads a space's maps/areas/objects/settings after owner edits.
func (h *Hub) RefreshSpace(spaceID string) {
	h.mu.Lock()
	delete(h.spaces, spaceID)
	conns := h.connsForSpaceLocked(spaceID)
	h.mu.Unlock()
	// Push fresh hello data to connected clients so editors' changes appear.
	for _, c := range conns {
		if hello, err := h.buildHello(c.pid); err == nil {
			h.sendTo(c, hello)
		}
	}
}

// ---------- participant lifecycle ----------

// JoinParticipant registers a newly joined participant in the runtime and
// announces it. The participant row must already exist in the store.
func (h *Hub) JoinParticipant(p *store.Participant) error {
	rt, err := h.getSpace(p.SpaceID)
	if err != nil {
		return err
	}
	h.mu.Lock()
	mrt := rt.maps[p.MapID]
	if mrt == nil {
		h.mu.Unlock()
		return fmt.Errorf("realtime: map %s not loaded", p.MapID)
	}
	mrt.parts[p.ID] = &partRT{p: *p}
	h.lastArea[p.ID] = p.AreaID
	others := h.connsForSpaceLocked(p.SpaceID)
	h.mu.Unlock()

	payload := mustJSON(map[string]any{
		"t": "event", "kind": "join", "participant_id": p.ID,
		"data": map[string]any{"participant": p},
	})
	for _, c := range others {
		if c.kind == connWS {
			h.sendTo(c, payload)
		}
	}
	h.broadcastPresence(p, "join")
	return nil
}

// LeaveParticipant removes a participant everywhere: runtime, store, socket.
// Safe to call for unknown IDs.
func (h *Hub) LeaveParticipant(pid string) {
	h.mu.Lock()
	var p *store.Participant
	var spaceID string
	for _, rt := range h.spaces {
		for _, mrt := range rt.maps {
			if prt, ok := mrt.parts[pid]; ok {
				cp := prt.p
				p = &cp
				spaceID = prt.p.SpaceID
				delete(mrt.parts, pid)
			}
		}
	}
	c := h.conns[pid]
	delete(h.conns, pid)
	delete(h.inputs, pid)
	delete(h.paths, pid)
	delete(h.lastAudio, pid)
	delete(h.lastArea, pid)
	delete(h.dirty, pid)
	delete(h.chatWin, pid)
	others := h.connsForSpaceLocked(spaceID)
	h.mu.Unlock()

	if c != nil {
		c.shutdown()
	}
	_ = h.store.DeleteTicketsByParticipant(pid)
	_ = h.store.DeleteParticipant(pid)
	if p == nil {
		return
	}
	payload := mustJSON(map[string]any{
		"t": "event", "kind": "leave", "participant_id": pid,
		"data": map[string]any{"participant": p},
	})
	for _, co := range others {
		if co.kind == connWS {
			h.sendTo(co, payload)
		}
	}
	h.broadcastPresence(p, "leave")
}

// KickParticipant forcibly removes a participant: the kicked socket first
// receives {"t":"event","kind":"kick"}, then closes. The kick is written
// synchronously so it is guaranteed on the wire before the close handshake.
func (h *Hub) KickParticipant(pid string) {
	h.mu.Lock()
	c := h.conns[pid]
	h.mu.Unlock()
	if c != nil && c.kind == connWS {
		msg := mustJSON(map[string]any{
			"t": "event", "kind": "kick", "participant_id": pid,
			"data": map[string]any{"reason": "kicked"},
		})
		wctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		c.writeMu.Lock()
		_ = c.ws.Write(wctx, websocket.MessageText, msg)
		c.writeMu.Unlock()
		cancel()
	}
	h.LeaveParticipant(pid)
}

// GetRuntime returns the in-memory participant copy, or nil.
func (h *Hub) GetRuntime(pid string) *store.Participant {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, rt := range h.spaces {
		for _, mrt := range rt.maps {
			if prt, ok := mrt.parts[pid]; ok {
				cp := prt.p
				return &cp
			}
		}
	}
	return nil
}

// ---------- movement ----------

// SetInput records a client's movement input for the next ticks.
func (h *Hub) SetInput(pid string, dx, dy float64, seq int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if dx != 0 || dy != 0 {
		delete(h.paths, pid) // live input cancels path-following
	}
	h.inputs[pid] = inputState{dx: dx, dy: dy, seq: seq}
}

// SetPath installs a BFS waypoint path for a participant.
func (h *Hub) SetPath(pid string, waypoints [][2]int, tx, ty float64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.paths[pid] = &pathState{waypoints: waypoints, targetX: tx, targetY: ty}
}

// NudgeMove applies an immediate relative move with collision slide.
func (h *Hub) NudgeMove(pid string, dx, dy float64) (*store.Participant, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	prt, mrt := h.findPartLocked(pid)
	if prt == nil {
		return nil, fmt.Errorf("realtime: unknown participant")
	}
	nx, ny := spatial.Move(mrt.spatial, mrt.solids, prt.p.X, prt.p.Y, dx, dy, prt.p.Ghost)
	if d := spatial.DirFromDelta(nx-prt.p.X, ny-prt.p.Y); d != "" {
		prt.p.Dir = d
	}
	prt.p.X, prt.p.Y = nx, ny
	prt.p.AreaID = areaIDAt(mrt.areas, nx, ny)
	h.dirty[pid] = true
	cp := prt.p
	return &cp, nil
}

// Teleport moves a participant to another map/coords (portal use).
func (h *Hub) Teleport(pid, mapID string, x, y float64) (*store.Participant, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	prt, mrt := h.findPartLocked(pid)
	if prt == nil {
		return nil, fmt.Errorf("realtime: unknown participant")
	}
	src := h.spaceOfLocked(pid)
	target := src.maps[mapID]
	if target == nil {
		return nil, fmt.Errorf("realtime: target map not in space")
	}
	x = math.Max(0, math.Min(x, float64(target.spatial.Width)))
	y = math.Max(0, math.Min(y, float64(target.spatial.Height)))
	delete(mrt.parts, pid)
	prt.p.MapID = mapID
	prt.p.X, prt.p.Y = x, y
	prt.p.AreaID = areaIDAt(target.areas, x, y)
	target.parts[pid] = prt
	delete(h.paths, pid)
	delete(h.lastAudio, pid) // force audio recompute on new map
	h.dirty[pid] = true
	cp := prt.p
	return &cp, nil
}

func (h *Hub) spaceOfLocked(pid string) *spaceRT {
	for _, rt := range h.spaces {
		for _, mrt := range rt.maps {
			if _, ok := mrt.parts[pid]; ok {
				return rt
			}
		}
	}
	return nil
}

func (h *Hub) findPartLocked(pid string) (*partRT, *mapRT) {
	for _, rt := range h.spaces {
		for _, mrt := range rt.maps {
			if prt, ok := mrt.parts[pid]; ok {
				return prt, mrt
			}
		}
	}
	return nil, nil
}

// UpdateState applies a state patch (emote/ghost/muted/camera_off/status/speaking).
func (h *Hub) UpdateState(pid string, patch map[string]any) (*store.Participant, error) {
	h.mu.Lock()
	prt, mrt := h.findPartLocked(pid)
	if prt == nil {
		h.mu.Unlock()
		return nil, fmt.Errorf("realtime: unknown participant")
	}
	emoteChanged := false
	if v, ok := patch["emote"].(string); ok && v != prt.p.Emote {
		prt.p.Emote = v
		emoteChanged = true
	}
	if v, ok := patch["ghost"].(bool); ok {
		prt.p.Ghost = v
	}
	if v, ok := patch["muted"].(bool); ok {
		prt.p.Muted = v
	}
	if v, ok := patch["camera_off"].(bool); ok {
		prt.p.CameraOff = v
	}
	if v, ok := patch["speaking"].(bool); ok {
		prt.p.Speaking = v
	}
	if v, ok := patch["status"].(string); ok {
		prt.p.Status = v
	}
	if v, ok := patch["dir"].(string); ok {
		switch v {
		case "up", "down", "left", "right":
			prt.p.Dir = v
		}
	}
	prt.p.AreaID = areaIDAt(mrt.areas, prt.p.X, prt.p.Y)
	h.dirty[pid] = true
	cp := prt.p
	others := h.connsForSpaceLocked(prt.p.SpaceID)
	h.mu.Unlock()

	if emoteChanged {
		payload := mustJSON(map[string]any{
			"t": "event", "kind": "emote", "participant_id": pid,
			"data": map[string]any{"emote": cp.Emote},
		})
		for _, c := range others {
			if c.kind == connWS {
				h.sendTo(c, payload)
			}
		}
		h.broadcastEmoteSSE(&cp)
	}
	return &cp, nil
}

// InteractObject returns the full object if within interact range.
func (h *Hub) InteractObject(pid, objectID string) (*store.SpaceObject, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	prt, mrt := h.findPartLocked(pid)
	if prt == nil {
		return nil, fmt.Errorf("realtime: unknown participant")
	}
	for _, o := range mrt.objects {
		if o.ID == objectID {
			cx, cy := o.X+o.W/2, o.Y+o.H/2
			if spatial.Dist(prt.p.X, prt.p.Y, cx, cy) <= InteractRadius {
				return o, nil
			}
			return nil, fmt.Errorf("realtime: object out of range")
		}
	}
	return nil, fmt.Errorf("realtime: object not found")
}

// ---------- tick ----------

func (h *Hub) tick() {
	dt := 1.0 / spatial.TickHz
	type areaEvent struct {
		pid  string
		area *spatial.Area
		kind string // enter|exit
	}
	var events []areaEvent

	h.mu.Lock()
	for _, rt := range h.spaces {
		for _, mrt := range rt.maps {
			for pid, prt := range mrt.parts {
				moved := false
				if inp, ok := h.inputs[pid]; ok && (inp.dx != 0 || inp.dy != 0) {
					ddx, ddy := spatial.Integrate(inp.dx, inp.dy, dt)
					nx, ny := spatial.Move(mrt.spatial, mrt.solids, prt.p.X, prt.p.Y, ddx, ddy, prt.p.Ghost)
					if nx != prt.p.X || ny != prt.p.Y {
						if d := spatial.DirFromDelta(nx-prt.p.X, ny-prt.p.Y); d != "" {
							prt.p.Dir = d
						}
						prt.p.X, prt.p.Y = nx, ny
						moved = true
					}
				} else if ps, ok := h.paths[pid]; ok {
					if h.followPath(pid, mrt, prt, ps, dt) {
						moved = true
					}
				}
				if moved {
					h.dirty[pid] = true
				}
				// area transitions
				newArea := spatial.AreaAt(mrt.areas, prt.p.X, prt.p.Y)
				newID := ""
				if newArea != nil {
					newID = newArea.ID
				}
				if oldID := h.lastArea[pid]; oldID != newID {
					if oldID != "" {
						events = append(events, areaEvent{pid: pid, kind: "exit", area: &spatial.Area{ID: oldID}})
					}
					if newArea != nil {
						events = append(events, areaEvent{pid: pid, kind: "enter", area: newArea})
					}
					h.lastArea[pid] = newID
					prt.p.AreaID = newID
					h.dirty[pid] = true
				}
			}
		}
	}
	h.mu.Unlock()

	for _, ev := range events {
		h.emitAreaEvent(ev.pid, ev.area, ev.kind)
	}
}

// followPath advances a participant along BFS waypoints. Returns moved.
func (h *Hub) followPath(pid string, mrt *mapRT, prt *partRT, ps *pathState, dt float64) bool {
	if len(ps.waypoints) == 0 {
		delete(h.paths, pid)
		return false
	}
	wp := ps.waypoints[0]
	tx, ty := float64(wp[0])+0.5, float64(wp[1])+0.5
	dx, dy := tx-prt.p.X, ty-prt.p.Y
	dist := math.Hypot(dx, dy)
	if dist < 0.15 {
		ps.waypoints = ps.waypoints[1:]
		if len(ps.waypoints) == 0 {
			// snap to exact target, path complete
			prt.p.X, prt.p.Y = ps.targetX, ps.targetY
			delete(h.paths, pid)
			return true
		}
		return false
	}
	step := spatial.MaxSpeed * dt
	if dist < step {
		step = dist
	}
	ddx, ddy := dx/dist*step, dy/dist*step
	nx, ny := spatial.Move(mrt.spatial, mrt.solids, prt.p.X, prt.p.Y, ddx, ddy, prt.p.Ghost)
	if math.Hypot(nx-ps.lastX, ny-ps.lastY) < 1e-9 {
		ps.stuck++
		if ps.stuck > spatial.TickHz*3 { // stuck 3s → give up
			delete(h.paths, pid)
		}
		return false
	}
	ps.stuck = 0
	ps.lastX, ps.lastY = nx, ny
	if d := spatial.DirFromDelta(nx-prt.p.X, ny-prt.p.Y); d != "" {
		prt.p.Dir = d
	}
	prt.p.X, prt.p.Y = nx, ny
	return true
}

func (h *Hub) emitAreaEvent(pid string, area *spatial.Area, kind string) {
	h.mu.Lock()
	prt, _ := h.findPartLocked(pid)
	if prt == nil {
		h.mu.Unlock()
		return
	}
	spaceID := prt.p.SpaceID
	others := h.connsForSpaceLocked(spaceID)
	var areaJSON *store.Area
	if kind == "enter" {
		if a, err := h.store.GetArea(area.ID); err == nil {
			areaJSON = a
		}
	}
	h.mu.Unlock()

	payload := mustJSON(map[string]any{
		"t": "event", "kind": "area", "participant_id": pid,
		"data": map[string]any{"kind": kind, "area": areaJSON, "area_id": area.ID},
	})
	for _, c := range others {
		if c.kind == connWS {
			h.sendTo(c, payload)
		}
	}
	// SSE area event goes to the participant themselves (their own transitions).
	if c := h.connFor(pid); c != nil && c.kind == connSSE && areaJSON != nil {
		h.sendTo(c, mustJSON(map[string]any{"t": "area", "kind": kind, "area": areaJSON}))
	}
}

// ---------- broadcast (10Hz) ----------

type stateParticipant struct {
	ID        string  `json:"id"`
	X         float64 `json:"x"`
	Y         float64 `json:"y"`
	Dir       string  `json:"dir"`
	Emote     string  `json:"emote"`
	Ghost     bool    `json:"ghost"`
	Muted     bool    `json:"muted"`
	CameraOff bool    `json:"camera_off"`
	Speaking  bool    `json:"speaking"`
	Status    string  `json:"status"`
	AreaID    string  `json:"area_id"`
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

func (h *Hub) broadcast() {
	h.mu.Lock()
	h.seq++
	seq := h.seq
	type job struct {
		c        *Conn
		state    []byte
		audio    []byte
		newAudio map[string]float64
	}
	var jobs []job
	for _, rt := range h.spaces {
		for _, mrt := range rt.maps {
			// snapshot participants on this map
			bodies := make([]spatial.Body, 0, len(mrt.parts))
			snap := make([]stateParticipant, 0, len(mrt.parts))
			for _, prt := range mrt.parts {
				p := &prt.p
				bodies = append(bodies, spatial.Body{ID: p.ID, MapID: p.MapID, X: p.X, Y: p.Y, Ghost: p.Ghost})
				snap = append(snap, stateParticipant{
					ID: p.ID, X: round2(p.X), Y: round2(p.Y), Dir: p.Dir,
					Emote: p.Emote, Ghost: p.Ghost, Muted: p.Muted,
					CameraOff: p.CameraOff, Speaking: p.Speaking,
					Status: p.Status, AreaID: p.AreaID,
				})
			}
			stateMsg := mustJSON(map[string]any{"t": "state", "seq": seq, "participants": snap})
			// per-viewer audio diffs
			for _, c := range h.conns {
				if c.kind != connWS {
					continue
				}
				prt, ok := mrt.parts[c.pid]
				if !ok {
					continue
				}
				me := spatial.Body{ID: prt.p.ID, MapID: prt.p.MapID, X: prt.p.X, Y: prt.p.Y, Ghost: prt.p.Ghost}
				vols := spatial.AudibleSet(me, bodies, mrt.areas, rt.settings.ProximityRadius)
				if audioEqual(h.lastAudio[c.pid], vols) {
					continue
				}
				h.lastAudio[c.pid] = vols
				jobs = append(jobs, job{
					c:        c,
					state:    stateMsg,
					audio:    mustJSON(map[string]any{"t": "audio", "volumes": vols}),
					newAudio: vols,
				})
			}
			// conns on this map without audio change still get state
			for _, c := range h.conns {
				if c.kind != connWS {
					continue
				}
				if _, ok := mrt.parts[c.pid]; !ok {
					continue
				}
				found := false
				for _, j := range jobs {
					if j.c == c {
						found = true
						break
					}
				}
				if !found {
					jobs = append(jobs, job{c: c, state: stateMsg})
				}
			}
		}
	}
	h.mu.Unlock()

	for _, j := range jobs {
		h.sendTo(j.c, j.state)
		if j.audio != nil {
			h.sendTo(j.c, j.audio)
		}
	}
}

func audioEqual(a, b map[string]float64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || math.Abs(bv-v) > 1e-9 {
			return false
		}
	}
	return true
}

// ---------- chat ----------

// CheckChatRate enforces 5 messages per 5s per participant (sliding window).
func (h *Hub) CheckChatRate(pid string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	cut := now.Add(-5 * time.Second)
	win := h.chatWin[pid][:0]
	for _, t := range h.chatWin[pid] {
		if t.After(cut) {
			win = append(win, t)
		}
	}
	if len(win) >= 5 {
		h.chatWin[pid] = win
		return false
	}
	h.chatWin[pid] = append(win, now)
	return true
}

// SendChat persists a message and fans it out. Returns the stored message.
func (h *Hub) SendChat(sender *store.Participant, scope, toID, text string, announce bool) (*store.ChatMessage, error) {
	rt, err := h.getSpace(sender.SpaceID)
	if err != nil {
		return nil, err
	}
	h.mu.Lock()
	mrt := rt.maps[sender.MapID]
	var recipients []string
	switch scope {
	case "nearby":
		bodies := make([]spatial.Body, 0)
		for _, prt := range mrt.parts {
			bodies = append(bodies, spatial.Body{ID: prt.p.ID, MapID: prt.p.MapID, X: prt.p.X, Y: prt.p.Y, Ghost: prt.p.Ghost})
		}
		// recipients = Q such that sender is audible to Q (vol > 0)
		for _, prt := range mrt.parts {
			if prt.p.ID == sender.ID {
				continue
			}
			q := spatial.Body{ID: prt.p.ID, MapID: prt.p.MapID, X: prt.p.X, Y: prt.p.Y, Ghost: prt.p.Ghost}
			vols := spatial.AudibleSet(q, bodies, mrt.areas, rt.settings.ProximityRadius)
			if v, ok := vols[sender.ID]; ok && v > 0 {
				recipients = append(recipients, prt.p.ID)
			}
		}
	case "dm":
		if _, ok := mrt.parts[toID]; !ok {
			// recipient may be on another map; check whole space
			found := false
			for _, m := range rt.maps {
				if _, ok := m.parts[toID]; ok {
					found = true
					break
				}
			}
			if !found {
				h.mu.Unlock()
				return nil, fmt.Errorf("realtime: dm recipient not in space")
			}
		}
		recipients = []string{toID}
	case "space":
		for _, m := range rt.maps {
			for pid := range m.parts {
				recipients = append(recipients, pid)
			}
		}
	default:
		h.mu.Unlock()
		return nil, fmt.Errorf("realtime: bad scope")
	}
	h.mu.Unlock()

	msg := &store.ChatMessage{
		ID:                store.NewID(store.PrefixChatMessage),
		SpaceID:           sender.SpaceID,
		FromParticipantID: sender.ID,
		FromName:          sender.DisplayName,
		FromKind:          sender.Kind,
		Scope:             scope,
		ToParticipantID:   toID,
		Text:              text,
		Announce:          announce,
		Recipients:        recipients,
		CreatedAt:         store.Now(),
	}
	if err := h.store.CreateChatMessage(msg); err != nil {
		return nil, err
	}
	payload := mustJSON(map[string]any{"t": "chat", "message": msg})
	for _, pid := range recipients {
		if c := h.connFor(pid); c != nil {
			h.sendTo(c, payload)
		}
	}
	// echo to sender
	if c := h.connFor(sender.ID); c != nil {
		h.sendTo(c, payload)
	}
	return msg, nil
}

// PlanMove validates that (tx,ty) is reachable from the participant's
// position via BFS (≤ MaxPathLen tiles, CONTRACT.md §3) and installs a
// waypoint path the tick loop follows at 4.5 t/s with collision slide.
// Ghosts glide straight (they walk through walls).
func (h *Hub) PlanMove(pid string, tx, ty float64) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	prt, mrt := h.findPartLocked(pid)
	if prt == nil {
		return fmt.Errorf("unknown participant")
	}
	if tx < 0 || ty < 0 || tx >= float64(mrt.spatial.Width) || ty >= float64(mrt.spatial.Height) {
		return fmt.Errorf("target out of map bounds")
	}
	ps := &pathState{targetX: tx, targetY: ty, lastX: prt.p.X, lastY: prt.p.Y}
	if prt.p.Ghost {
		ps.waypoints = [][2]int{{int(math.Floor(tx)), int(math.Floor(ty))}}
	} else {
		path := spatial.FindPath(mrt.spatial, mrt.solids, prt.p.X, prt.p.Y, tx, ty, spatial.MaxPathLen)
		if path == nil {
			return fmt.Errorf("target unreachable: no path within 40 tiles")
		}
		if len(path) > 1 {
			path = path[1:] // skip the tile we're standing on
		}
		ps.waypoints = path
	}
	h.paths[pid] = ps
	return nil
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic("realtime: marshal: " + err.Error())
	}
	return b
}

func areaIDAt(areas []spatial.Area, x, y float64) string {
	if a := spatial.AreaAt(areas, x, y); a != nil {
		return a.ID
	}
	return ""
}

// sha256hex delegates to auth to avoid duplicating the hash helper.
func sha256hex(s string) string { return auth.SHA256Hex(s) }
