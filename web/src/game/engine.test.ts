import { describe, it, expect } from 'vitest';
import {
  audibleVolumes,
  lerpFactor,
  lerpPos,
  tileAt,
  tileAtPos,
  isWallTile,
  dirFromDelta,
  nearestObject,
  clampCamera,
  inRect,
  areaAt,
  Tile,
  type EngineParticipant,
  type EngineArea,
} from './engine';

const R = 5; // default proximity radius

function p(id: string, x: number, y: number, extra: Partial<EngineParticipant> = {}): EngineParticipant {
  return { id, map_id: 'mp_1', x, y, ghost: false, ...extra };
}

function privateArea(id: string, x: number, y: number, w: number, h: number): EngineArea {
  return { id, map_id: 'mp_1', kind: 'private', x, y, w, h };
}

function spotArea(id: string, x: number, y: number, w: number, h: number): EngineArea {
  return { id, map_id: 'mp_1', kind: 'spotlight', x, y, w, h };
}

describe('proximity (§5 mirror)', () => {
  it('falls off linearly with distance', () => {
    const me = p('a', 0, 0);
    const near = p('b', R / 2, 0);
    const edge = p('c', R, 0);
    const far = p('d', R + 0.01, 0);
    const vols = audibleVolumes(me, [near, edge, far], [], R);
    expect(vols.get('b')).toBeCloseTo(0.5, 6);
    expect(vols.get('c')).toBeCloseTo(0, 6); // dist == R -> max(0, 1-1) = 0
    expect(vols.has('d')).toBe(false);
  });

  it('zero distance gives full volume', () => {
    const vols = audibleVolumes(p('a', 3, 3), [p('b', 3, 3)], [], R);
    expect(vols.get('b')).toBe(1);
  });

  it('excludes self, other maps, and includes nobody else', () => {
    const me = p('a', 0, 0);
    const vols = audibleVolumes(me, [me, p('b', 1, 0, { map_id: 'mp_2' })], [], R);
    expect(vols.size).toBe(0);
  });

  it('ghost hears no one and is heard by no one', () => {
    const me = p('a', 0, 0, { ghost: true });
    expect(audibleVolumes(me, [p('b', 1, 0)], [], R).size).toBe(0);
    const vols = audibleVolumes(p('a', 0, 0), [p('b', 1, 0, { ghost: true })], [], R);
    expect(vols.has('b')).toBe(false);
  });

  it('same private area -> vol 1.0 even beyond radius', () => {
    const areas = [privateArea('ar_1', 10, 10, 4, 4)];
    const me = p('a', 11, 11);
    const mate = p('b', 13, 13); // dist ~2.83 but also test far corner
    const far = p('c', 13.9, 13.9);
    const vols = audibleVolumes(me, [mate, far], areas, 1); // tiny radius
    expect(vols.get('b')).toBe(1);
    expect(vols.get('c')).toBe(1);
  });

  it('different private areas are fully isolated (no distance falloff)', () => {
    // A private area isolates its occupants: they hear ONLY their own area,
    // even if another private area is adjacent or within radius.
    const areas = [privateArea('ar_1', 0, 0, 3, 3), privateArea('ar_2', 10, 10, 3, 3)];
    expect(audibleVolumes(p('a', 1, 1), [p('b', 11, 11)], areas, 100).has('b')).toBe(false);
    // adjacent pods, 1 tile apart: still isolated
    const adjacent = [privateArea('ar_1', 0, 0, 3, 3), privateArea('ar_2', 3, 0, 3, 3)];
    expect(audibleVolumes(p('a', 2.9, 1), [p('b', 3.1, 1)], adjacent, 100).has('b')).toBe(false);
  });

  it('private XOR -> excluded (one inside, one outside)', () => {
    const areas = [privateArea('ar_1', 0, 0, 3, 3)];
    const volsIn = audibleVolumes(p('a', 1, 1), [p('b', 1.5, 1.5 + 10)], areas, 100);
    expect(volsIn.has('b')).toBe(false);
    const volsOut = audibleVolumes(p('a', 20, 20), [p('b', 1, 1)], areas, 100);
    expect(volsOut.has('b')).toBe(false);
  });

  it('spotlight: occupant is broadcast to the whole map at 1.0', () => {
    const areas = [spotArea('ar_s', 10, 10, 3, 3)];
    const vols = audibleVolumes(p('a', 0, 0), [p('b', 11, 11)], areas, R);
    expect(vols.get('b')).toBe(1); // dist ~15.5 > R, still audible
  });

  it('spotlight: listener in spotlight hears everyone at 1.0', () => {
    const areas = [spotArea('ar_s', 0, 0, 3, 3)];
    const vols = audibleVolumes(p('a', 1, 1), [p('b', 30, 30)], areas, R);
    expect(vols.get('b')).toBe(1);
  });

  it('private area beats distance: outsider beyond radius hears nothing', () => {
    const areas = [privateArea('ar_1', 0, 0, 3, 3)];
    const vols = audibleVolumes(p('a', 1, 1), [p('b', 50, 50)], areas, R);
    expect(vols.has('b')).toBe(false);
  });
});

describe('interpolation', () => {
  it('lerpFactor(0) = 0, grows with dt', () => {
    expect(lerpFactor(0)).toBe(0);
    expect(lerpFactor(-5)).toBe(0);
    const f100 = lerpFactor(100);
    expect(f100).toBeCloseTo(1 - Math.exp(-1), 6); // ~0.632
    expect(lerpFactor(50)).toBeLessThan(f100);
    expect(lerpFactor(1000)).toBeGreaterThan(0.9999);
  });

  it('lerpPos moves toward the target proportionally', () => {
    const cur = { x: 0, y: 0 };
    const target = { x: 10, y: 0 };
    const next = lerpPos(cur, target, 100);
    expect(next.x).toBeCloseTo(10 * (1 - Math.exp(-1)), 6);
    expect(next.y).toBe(0);
  });

  it('converges to the target over many frames', () => {
    let cur = { x: 0, y: 0 };
    const target = { x: 8, y: 6 };
    for (let i = 0; i < 120; i++) cur = lerpPos(cur, target, 16.7);
    expect(Math.hypot(cur.x - target.x, cur.y - target.y)).toBeLessThan(0.05);
  });

  it('snaps on huge dt and holds on zero dt', () => {
    expect(lerpPos({ x: 1, y: 1 }, { x: 9, y: 9 }, 100000)).toEqual({ x: 9, y: 9 });
    expect(lerpPos({ x: 1, y: 1 }, { x: 9, y: 9 }, 0)).toEqual({ x: 1, y: 1 });
  });
});

describe('tile helpers', () => {
  const map = { width: 4, height: 3, tiles: [0, 1, 2, 1, 1, 1, 1, 1, 1, 1, 1, 5] };

  it('tileAt reads row-major and clamps OOB to void', () => {
    expect(tileAt(map, 0, 0)).toBe(Tile.Void);
    expect(tileAt(map, 2, 0)).toBe(Tile.Wall);
    expect(tileAt(map, 3, 2)).toBe(Tile.Spawn);
    expect(tileAt(map, -1, 0)).toBe(Tile.Void);
    expect(tileAt(map, 4, 0)).toBe(Tile.Void);
    expect(tileAt(map, 0, 3)).toBe(Tile.Void);
  });

  it('tileAtPos floors float coords', () => {
    expect(tileAtPos(map, 2.9, 0.1)).toBe(Tile.Wall);
  });

  it('isWallTile only matches walls', () => {
    expect(isWallTile(Tile.Wall)).toBe(true);
    expect(isWallTile(Tile.Floor)).toBe(false);
    expect(isWallTile(Tile.Void)).toBe(false);
  });

  it('dirFromDelta picks the dominant axis', () => {
    expect(dirFromDelta(1, 0)).toBe('right');
    expect(dirFromDelta(-1, 0.2)).toBe('left');
    expect(dirFromDelta(0.1, -1)).toBe('up');
    expect(dirFromDelta(0, 1)).toBe('down');
  });

  it('nearestObject respects the interact radius', () => {
    const objs = [
      { id: 'o1', x: 5, y: 5 },
      { id: 'o2', x: 0, y: 0 },
    ];
    expect(nearestObject(objs, 5.5, 5.5)?.id).toBe('o1');
    expect(nearestObject(objs, 50, 50)).toBe(null);
  });

  it('inRect is half-open', () => {
    const r = { x: 2, y: 2, w: 3, h: 3 };
    expect(inRect(2, 2, r)).toBe(true);
    expect(inRect(4.99, 4.99, r)).toBe(true);
    expect(inRect(5, 2, r)).toBe(false);
    expect(inRect(1.99, 2, r)).toBe(false);
  });

  it('areaAt returns the first matching area on the map', () => {
    const areas = [privateArea('ar_1', 0, 0, 3, 3)];
    expect(areaAt(areas, 'mp_1', 1, 1)?.id).toBe('ar_1');
    expect(areaAt(areas, 'mp_1', 9, 9)).toBe(null);
    expect(areaAt(areas, 'mp_2', 1, 1)).toBe(null);
  });

  it('clampCamera keeps the view on the map', () => {
    const c = clampCamera(0, 0, 10, 8, 40, 30);
    expect(c.x).toBe(5);
    expect(c.y).toBe(4);
    const big = clampCamera(20, 15, 100, 100, 40, 30);
    expect(big.x).toBe(20);
    expect(big.y).toBe(15);
  });
});
