/**
 * PURE game engine math — no DOM, no network, no side effects.
 *
 * The proximity function mirrors CONTRACT.md §5 EXACTLY and must stay in lockstep
 * with the Go implementation in `internal/spatial`:
 *
 *   R = space.settings.proximity_radius (default 5)
 *   audible(P) := { Q on same map, Q != P, Q not ghost, P not ghost,
 *     AND ( samePrivateArea(P,Q) -> vol 1.0
 *        OR inPrivate(P) OR inPrivate(Q) -> EXCLUDE   // private areas are fully
 *                                                    // isolated: occupants hear
 *                                                    // ONLY their own area
 *        OR Q in spotlight OR P in spotlight -> vol 1.0
 *        OR dist(P,Q) <= R -> vol = max(0, 1 - dist/R) ) }
 *   visible(P) := same set (video shown when vol > 0)
 *   Ghost: excluded from everyone's sets; ghost hears no one.
 *
 * Coordinate conventions shared with the server:
 * - positions are float tile coords (tile (tx,ty) spans [tx,tx+1) x [ty,ty+1))
 * - area rects are half-open: x in [r.x, r.x+r.w), y in [r.y, r.y+r.h)
 */

export interface EngineParticipant {
  id: string;
  map_id: string;
  x: number;
  y: number;
  ghost: boolean;
}

export interface EngineArea {
  id: string;
  map_id: string;
  kind: 'private' | 'spotlight';
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface EngineMap {
  width: number;
  height: number;
  tiles: number[];
}

export interface Vec {
  x: number;
  y: number;
}

/** Tile values per CONTRACT §2. */
export const Tile = {
  Void: 0,
  Floor: 1,
  Wall: 2,
  Private: 3,
  Spotlight: 4,
  Spawn: 5,
} as const;

export function dist(ax: number, ay: number, bx: number, by: number): number {
  return Math.hypot(ax - bx, ay - by);
}

/** Half-open rect test. */
export function inRect(x: number, y: number, r: { x: number; y: number; w: number; h: number }): boolean {
  return x >= r.x && x < r.x + r.w && y >= r.y && y < r.y + r.h;
}

/** First area (any kind) on this map containing the point, or null. */
export function areaAt(areas: EngineArea[], mapId: string, x: number, y: number): EngineArea | null {
  for (const a of areas) {
    if (a.map_id !== mapId) continue;
    if (inRect(x, y, a)) return a;
  }
  return null;
}

/** First private area on this map containing the point, or null. */
export function privateAreaAt(areas: EngineArea[], mapId: string, x: number, y: number): EngineArea | null {
  for (const a of areas) {
    if (a.map_id !== mapId || a.kind !== 'private') continue;
    if (inRect(x, y, a)) return a;
  }
  return null;
}

export function inPrivate(areas: EngineArea[], mapId: string, x: number, y: number): boolean {
  return privateAreaAt(areas, mapId, x, y) !== null;
}

export function inSpotlight(areas: EngineArea[], mapId: string, x: number, y: number): boolean {
  for (const a of areas) {
    if (a.map_id !== mapId || a.kind !== 'spotlight') continue;
    if (inRect(x, y, a)) return true;
  }
  return false;
}

/**
 * Who P can hear, with per-participant volume 0..1.
 * Implements CONTRACT §5 verbatim: private-area isolation, spotlight broadcast,
 * distance falloff, ghost exclusion.
 */
export function audibleVolumes(
  me: EngineParticipant,
  others: EngineParticipant[],
  areas: EngineArea[],
  radius: number,
): Map<string, number> {
  const out = new Map<string, number>();
  if (me.ghost) return out; // ghost hears no one

  const myPrivate = privateAreaAt(areas, me.map_id, me.x, me.y);
  const mySpotlight = inSpotlight(areas, me.map_id, me.x, me.y);

  for (const q of others) {
    if (q.id === me.id) continue;
    if (q.map_id !== me.map_id) continue;
    if (q.ghost) continue;

    const qPrivate = privateAreaAt(areas, q.map_id, q.x, q.y);
    if (myPrivate && qPrivate && myPrivate.id === qPrivate.id) {
      out.set(q.id, 1.0); // same private area -> full volume
      continue;
    }
    if (myPrivate !== null || qPrivate !== null) {
      continue; // private-area isolation: occupants hear ONLY their own area,
                // even in adjacent or different private areas
    }
    if (mySpotlight || inSpotlight(areas, q.map_id, q.x, q.y)) {
      out.set(q.id, 1.0); // spotlight broadcast
      continue;
    }
    const d = dist(me.x, me.y, q.x, q.y);
    if (d <= radius) {
      out.set(q.id, Math.max(0, 1 - d / radius));
    }
  }
  return out;
}

/** Frame-rate independent exponential smoothing with a ~100ms time constant. */
export function lerpFactor(dtMs: number, timeConstantMs = 100): number {
  if (dtMs <= 0) return 0;
  return 1 - Math.exp(-dtMs / timeConstantMs);
}

export function lerpPos(cur: Vec, target: Vec, dtMs: number, timeConstantMs = 100): Vec {
  const t = lerpFactor(dtMs, timeConstantMs);
  return {
    x: cur.x + (target.x - cur.x) * t,
    y: cur.y + (target.y - cur.y) * t,
  };
}

/** Tile value at integer coords; out of bounds reads as void (0). */
export function tileAt(map: EngineMap, tx: number, ty: number): number {
  if (tx < 0 || ty < 0 || tx >= map.width || ty >= map.height) return Tile.Void;
  return map.tiles[ty * map.width + tx] ?? Tile.Void;
}

/** Tile value under a float position. */
export function tileAtPos(map: EngineMap, x: number, y: number): number {
  return tileAt(map, Math.floor(x), Math.floor(y));
}

export function isWallTile(t: number): boolean {
  return t === Tile.Wall;
}

/** Facing direction from a movement delta (dominant axis wins). */
export function dirFromDelta(dx: number, dy: number): 'up' | 'down' | 'left' | 'right' {
  if (Math.abs(dx) >= Math.abs(dy)) return dx >= 0 ? 'right' : 'left';
  return dy >= 0 ? 'down' : 'up';
}

export interface EngineObject {
  id: string;
  x: number;
  y: number;
}

/**
 * Nearest object within maxDist tiles (object center = x+0.5, y+0.5).
 * The interact radius is 2.5 tiles (CONTRACT §8 world_look range).
 */
export function nearestObject<T extends EngineObject>(
  objects: T[],
  x: number,
  y: number,
  maxDist = 2.5,
): T | null {
  let best: T | null = null;
  let bestD = maxDist;
  for (const o of objects) {
    const d = dist(x, y, o.x + 0.5, o.y + 0.5);
    if (d <= bestD) {
      best = o;
      bestD = d;
    }
  }
  return best;
}

/** Clamp a camera center so the view stays inside (or covers) the map. */
export function clampCamera(cx: number, cy: number, viewTilesW: number, viewTilesH: number, mapW: number, mapH: number): Vec {
  const clamp = (c: number, view: number, size: number): number => {
    if (view >= size) return size / 2;
    return Math.min(Math.max(c, view / 2), size - view / 2);
  };
  return { x: clamp(cx, viewTilesW, mapW), y: clamp(cy, viewTilesH, mapH) };
}
