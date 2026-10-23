/**
 * Realtime collaborative whiteboard canvas (WHITEBOARD_V1).
 *
 * Board coordinates are normalized 0..1000 on both axes; rendering scales them
 * to the canvas size. Strokes drawn locally show a live preview while the
 * pointer is down; the stroke is committed when the server echoes it back
 * (matched by points hash), and deduped by stroke id so a stroke never
 * renders twice.
 *
 * The pure helpers (normalizePoint, pointsHash, StrokeStore) are DOM-free and
 * covered by vitest; the Whiteboard class needs a browser.
 */
import type { WbPoint, WhiteboardStroke } from '../../game/types';
import { h } from '../../ui/dom';

export const BOARD_MAX = 1000;
export const MAX_POINTS = 500;
const PREVIEW_TIMEOUT_MS = 6000;

export const WB_COLORS = ['#000000', '#ef4444', '#3b82f6', '#22c55e'];
export const WB_WIDTHS = [2, 4, 8];
export const WB_ERASER = { color: '#ffffff', width: 20 };

export interface WbRect {
  left: number;
  top: number;
  width: number;
  height: number;
}

export interface WhiteboardStrokeInput {
  color: string;
  width: number;
  points: WbPoint[];
}

export interface WhiteboardOptions {
  onStrokeEnd: (s: WhiteboardStrokeInput) => void;
  onClear: () => void;
  /** Fires when the widget is removed from the DOM (modal closed). */
  onDetach?: () => void;
}

function clamp(v: number, lo: number, hi: number): number {
  return v < lo ? lo : v > hi ? hi : v;
}

/** Client px -> board coords 0..1000, clamped. Pure — unit tested. */
export function normalizePoint(clientX: number, clientY: number, r: WbRect): WbPoint {
  const w = Math.max(r.width, 1);
  const hgt = Math.max(r.height, 1);
  return {
    x: clamp(((clientX - r.left) / w) * BOARD_MAX, 0, BOARD_MAX),
    y: clamp(((clientY - r.top) / hgt) * BOARD_MAX, 0, BOARD_MAX),
  };
}

/** Stable hash of a stroke's points — used to match our echo back to our preview. Pure. */
export function pointsHash(points: WbPoint[]): string {
  return points.map((p) => `${p.x.toFixed(2)},${p.y.toFixed(2)}`).join(';');
}

/**
 * Rendered-stroke registry. A stroke renders exactly once per id — re-receives
 * (e.g. WS echo after a REST POST, or a re-sent frame) are no-ops.
 */
export class StrokeStore {
  private ids = new Set<string>();
  readonly strokes: WhiteboardStroke[] = [];

  /** @returns true when the stroke was new (caller should render). */
  add(s: WhiteboardStroke): boolean {
    if (this.ids.has(s.id)) return false;
    this.ids.add(s.id);
    this.strokes.push(s);
    return true;
  }

  clear(): void {
    this.ids.clear();
    this.strokes.length = 0;
  }

  get size(): number {
    return this.ids.size;
  }
}

export class Whiteboard {
  readonly root: HTMLElement;

  private canvas: HTMLCanvasElement;
  private ctx: CanvasRenderingContext2D;
  private wrap: HTMLElement;
  private store = new StrokeStore();
  private destroyed = false;

  private color = WB_COLORS[0];
  private width = WB_WIDTHS[1];
  private swatches: HTMLButtonElement[] = [];
  private widthBtns: HTMLButtonElement[] = [];
  private eraserBtn: HTMLButtonElement | null = null;

  private drawing = false;
  private preview: WhiteboardStrokeInput | null = null;
  private pending = new Set<string>();
  private resizeObs: ResizeObserver | null = null;
  private detachObs: MutationObserver | null = null;

  constructor(container: HTMLElement, private opts: WhiteboardOptions) {
    const root = (this.root = h('div', { class: 'wb-wrap' }));
    root.appendChild(this.buildToolbar());

    const wrap = (this.wrap = h('div', { class: 'wb-canvas-wrap' }));
    const canvas = (this.canvas = h('canvas', { class: 'wb-canvas' }) as HTMLCanvasElement);
    wrap.appendChild(canvas);
    root.appendChild(wrap);
    container.appendChild(root);

    this.ctx = canvas.getContext('2d')!;

    canvas.addEventListener('pointerdown', this.onPointerDown);
    canvas.addEventListener('pointermove', this.onPointerMove);
    canvas.addEventListener('pointerup', this.onPointerUp);
    canvas.addEventListener('pointercancel', this.onPointerUp);

    if (typeof ResizeObserver !== 'undefined') {
      this.resizeObs = new ResizeObserver(() => this.fitCanvas());
      this.resizeObs.observe(wrap);
    }
    this.fitCanvas();

    // Self-cleanup when the modal closes (overlay removed from DOM).
    if (typeof MutationObserver !== 'undefined' && typeof document !== 'undefined') {
      this.detachObs = new MutationObserver(() => {
        if (!root.isConnected) this.destroy();
      });
      this.detachObs.observe(document.body, { childList: true, subtree: true });
    }
  }

  // ------------------------------------------------------------ public API ---

  /** Replace contents with the full stroke list (initial load). */
  loadStrokes(strokes: WhiteboardStroke[]): void {
    this.store.clear();
    this.pending.clear();
    this.preview = null;
    for (const s of strokes) this.store.add(s);
    this.redraw();
  }

  /**
   * Render a stroke received from the server. Idempotent by stroke id; if it
   * matches our in-flight preview (own echo), the preview is retired.
   */
  addRemoteStroke(stroke: WhiteboardStroke): void {
    if (!this.store.add(stroke)) return; // duplicate — already rendered
    const hash = pointsHash(stroke.points);
    if (this.pending.delete(hash)) {
      if (this.preview && pointsHash(this.preview.points) === hash) this.preview = null;
    }
    this.redraw();
  }

  clearRemote(): void {
    this.store.clear();
    this.pending.clear();
    this.preview = null;
    this.redraw();
  }

  destroy(): void {
    if (this.destroyed) return;
    this.destroyed = true;
    this.resizeObs?.disconnect();
    this.detachObs?.disconnect();
    this.canvas.removeEventListener('pointerdown', this.onPointerDown);
    this.canvas.removeEventListener('pointermove', this.onPointerMove);
    this.canvas.removeEventListener('pointerup', this.onPointerUp);
    this.canvas.removeEventListener('pointercancel', this.onPointerUp);
    this.opts.onDetach?.();
  }

  // -------------------------------------------------------------- toolbar ---

  private buildToolbar(): HTMLElement {
    const bar = h('div', { class: 'wb-toolbar', role: 'toolbar', 'aria-label': 'Whiteboard tools' });

    for (const c of WB_COLORS) {
      const b = h('button', {
        class: 'wb-swatch',
        title: c,
        'aria-label': `Color ${c}`,
        onclick: () => this.pickTool(c, this.width === WB_ERASER.width ? WB_WIDTHS[1] : this.width),
      }) as HTMLButtonElement;
      b.style.background = c;
      this.swatches.push(b);
      bar.appendChild(b);
    }

    const sep1 = h('span', { class: 'wb-sep' });
    bar.appendChild(sep1);

    for (const w of WB_WIDTHS) {
      const b = h('button', {
        class: 'wb-width',
        title: `${w}px`,
        'aria-label': `Width ${w}px`,
        onclick: () => this.pickTool(this.color === WB_ERASER.color ? WB_COLORS[0] : this.color, w),
      }) as HTMLButtonElement;
      b.appendChild(h('span', { class: 'wb-width-line' }));
      (b.firstChild as HTMLElement).style.height = `${Math.min(w, 8)}px`;
      this.widthBtns.push(b);
      bar.appendChild(b);
    }

    const sep2 = h('span', { class: 'wb-sep' });
    bar.appendChild(sep2);

    this.eraserBtn = h('button', {
      class: 'wb-tool',
      text: '🧽',
      title: 'Eraser',
      'aria-label': 'Eraser',
      onclick: () => this.pickTool(WB_ERASER.color, WB_ERASER.width),
    }) as HTMLButtonElement;
    bar.appendChild(this.eraserBtn);

    const clearBtn = h('button', {
      class: 'wb-tool wb-clear',
      text: 'Clear',
      title: 'Clear the whole board',
      onclick: () => {
        if (window.confirm('Clear the whole whiteboard? Everyone will see it cleared.')) {
          this.opts.onClear();
        }
      },
    }) as HTMLButtonElement;
    bar.appendChild(clearBtn);

    this.syncToolbar();
    return bar;
  }

  private pickTool(color: string, width: number): void {
    this.color = color;
    this.width = width;
    this.syncToolbar();
  }

  private syncToolbar(): void {
    const erasing = this.color === WB_ERASER.color && this.width === WB_ERASER.width;
    this.swatches.forEach((b, i) => b.classList.toggle('active', !erasing && WB_COLORS[i] === this.color));
    this.widthBtns.forEach((b, i) => b.classList.toggle('active', !erasing && WB_WIDTHS[i] === this.width));
    this.eraserBtn?.classList.toggle('active', erasing);
  }

  // -------------------------------------------------------------- drawing ---

  private onPointerDown = (e: PointerEvent): void => {
    if (e.button !== 0 && e.pointerType === 'mouse') return;
    e.preventDefault();
    try {
      this.canvas.setPointerCapture(e.pointerId);
    } catch {
      /* noop */
    }
    const r = this.canvas.getBoundingClientRect();
    this.drawing = true;
    this.preview = { color: this.color, width: this.width, points: [normalizePoint(e.clientX, e.clientY, r)] };
    this.redraw();
  };

  private onPointerMove = (e: PointerEvent): void => {
    if (!this.drawing || !this.preview) return;
    e.preventDefault();
    const r = this.canvas.getBoundingClientRect();
    const p = normalizePoint(e.clientX, e.clientY, r);
    const last = this.preview.points[this.preview.points.length - 1];
    if (Math.abs(p.x - last.x) < 0.5 && Math.abs(p.y - last.y) < 0.5) return; // skip dupes
    if (this.preview.points.length >= MAX_POINTS) return;
    this.preview.points.push(p);
    this.redraw();
  };

  private onPointerUp = (e: PointerEvent): void => {
    if (!this.drawing) return;
    this.drawing = false;
    try {
      this.canvas.releasePointerCapture(e.pointerId);
    } catch {
      /* noop */
    }
    const stroke = this.preview;
    if (stroke && stroke.points.length >= 2) {
      this.pending.add(pointsHash(stroke.points));
      this.opts.onStrokeEnd({ color: stroke.color, width: stroke.width, points: stroke.points });
      // Retire a preview that never got an echo (send failed silently over WS).
      window.setTimeout(() => {
        if (this.preview === stroke && !this.destroyed) {
          this.preview = null;
          this.redraw();
        }
      }, PREVIEW_TIMEOUT_MS);
    } else {
      this.preview = null; // single tap — not a stroke
      this.redraw();
    }
  };

  // ------------------------------------------------------------- rendering ---

  private fitCanvas(): void {
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    const w = this.wrap.clientWidth;
    const h = this.wrap.clientHeight;
    if (w === 0 || h === 0) return;
    this.canvas.width = Math.round(w * dpr);
    this.canvas.height = Math.round(h * dpr);
    this.ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    this.redraw();
  }

  private redraw(): void {
    if (this.destroyed) return;
    const w = this.wrap.clientWidth;
    const h = this.wrap.clientHeight;
    if (w === 0 || h === 0) return;
    const ctx = this.ctx;
    ctx.clearRect(0, 0, w, h);
    ctx.fillStyle = '#ffffff';
    ctx.fillRect(0, 0, w, h);
    ctx.lineCap = 'round';
    ctx.lineJoin = 'round';
    const scale = w / BOARD_MAX;
    for (const s of this.store.strokes) this.drawStroke(ctx, s.color, s.width, s.points, w, h, scale);
    if (this.preview) this.drawStroke(ctx, this.preview.color, this.preview.width, this.preview.points, w, h, scale);
  }

  private drawStroke(
    ctx: CanvasRenderingContext2D,
    color: string,
    width: number,
    points: WbPoint[],
    w: number,
    h: number,
    scale: number,
  ): void {
    if (points.length === 0) return;
    ctx.strokeStyle = color;
    ctx.lineWidth = Math.max(1, width * scale);
    ctx.beginPath();
    ctx.moveTo((points[0].x / BOARD_MAX) * w, (points[0].y / BOARD_MAX) * h);
    for (let i = 1; i < points.length; i++) {
      ctx.lineTo((points[i].x / BOARD_MAX) * w, (points[i].y / BOARD_MAX) * h);
    }
    ctx.stroke();
  }
}
