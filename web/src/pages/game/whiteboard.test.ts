import { describe, it, expect } from 'vitest';
import {
  BOARD_MAX,
  MAX_POINTS,
  WB_COLORS,
  WB_WIDTHS,
  WB_ERASER,
  normalizePoint,
  pointsHash,
  StrokeStore,
  type WbRect,
} from './whiteboard';
import type { WhiteboardStroke } from '../../game/types';

const RECT: WbRect = { left: 10, top: 20, width: 200, height: 100 };

function stroke(id: string, pts: { x: number; y: number }[] = [{ x: 0, y: 0 }, { x: 10, y: 10 }]): WhiteboardStroke {
  return {
    id,
    object_id: 'ob_1',
    participant_id: 'pt_1',
    color: '#000000',
    width: 4,
    points: pts,
    created_at: new Date().toISOString(),
  };
}

describe('normalizePoint', () => {
  it('maps rect corners to 0..1000', () => {
    expect(normalizePoint(10, 20, RECT)).toEqual({ x: 0, y: 0 });
    expect(normalizePoint(210, 120, RECT)).toEqual({ x: 1000, y: 1000 });
  });

  it('maps the center to 500,500', () => {
    const p = normalizePoint(110, 70, RECT);
    expect(p.x).toBeCloseTo(500, 6);
    expect(p.y).toBeCloseTo(500, 6);
  });

  it('clamps out-of-bounds pointers to 0..1000', () => {
    expect(normalizePoint(-500, -500, RECT)).toEqual({ x: 0, y: 0 });
    expect(normalizePoint(9999, 9999, RECT)).toEqual({ x: BOARD_MAX, y: BOARD_MAX });
  });

  it('handles a zero-size rect without NaN', () => {
    const p = normalizePoint(10, 20, { left: 10, top: 20, width: 0, height: 0 });
    expect(Number.isNaN(p.x)).toBe(false);
    expect(Number.isNaN(p.y)).toBe(false);
    expect(p.x).toBeGreaterThanOrEqual(0);
    expect(p.x).toBeLessThanOrEqual(BOARD_MAX);
  });
});

describe('pointsHash', () => {
  it('is stable for identical points', () => {
    const a = [{ x: 1.234, y: 5.678 }, { x: 9, y: 10 }];
    expect(pointsHash(a)).toBe(pointsHash([{ x: 1.234, y: 5.678 }, { x: 9, y: 10 }]));
  });

  it('differs for different points', () => {
    expect(pointsHash([{ x: 1, y: 1 }])).not.toBe(pointsHash([{ x: 2, y: 1 }]));
  });
});

describe('StrokeStore (render dedupe)', () => {
  it('renders a stroke exactly once — re-receives are no-ops', () => {
    const store = new StrokeStore();
    const s = stroke('wbs_abc');
    expect(store.add(s)).toBe(true);
    expect(store.add(s)).toBe(false); // duplicate echo
    expect(store.add({ ...s })).toBe(false); // same id, new object
    expect(store.strokes).toHaveLength(1);
    expect(store.size).toBe(1);
  });

  it('accepts distinct strokes', () => {
    const store = new StrokeStore();
    expect(store.add(stroke('wbs_1'))).toBe(true);
    expect(store.add(stroke('wbs_2'))).toBe(true);
    expect(store.size).toBe(2);
  });

  it('clear empties the store and allows re-adding', () => {
    const store = new StrokeStore();
    store.add(stroke('wbs_1'));
    store.clear();
    expect(store.size).toBe(0);
    expect(store.strokes).toHaveLength(0);
    expect(store.add(stroke('wbs_1'))).toBe(true); // id forgotten after clear
  });
});

describe('whiteboard constants', () => {
  it('exposes the contract toolbar palette', () => {
    expect(WB_COLORS).toEqual(['#000000', '#ef4444', '#3b82f6', '#22c55e']);
    expect(WB_WIDTHS).toEqual([2, 4, 8]);
    expect(WB_ERASER).toEqual({ color: '#ffffff', width: 20 });
    expect(MAX_POINTS).toBe(500);
  });
});
