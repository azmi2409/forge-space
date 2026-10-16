/**
 * Procedural rendering — every pixel is drawn in code.
 * 32px tileset + procedural avatars. ZERO external image/font assets.
 */
import { Tile } from './engine';

export const TILE_PX = 32;

export interface GameMapLike {
  width: number;
  height: number;
  tiles: number[];
}

export interface AvatarLook {
  skin: number;
  hair: number;
  hair_color: number;
  top: number;
}

export const SKIN_COLORS = ['#f1c27d', '#e0ac69', '#c68642', '#8d5524', '#5c3a1e'];
export const HAIR_COLORS = ['#20242e', '#4a3226', '#8a5a2b', '#d9a441', '#b8bcc8', '#c2503e'];
export const TOP_COLORS = ['#10b981', '#38bdf8', '#8b5cf6', '#f59e0b', '#f43f5e', '#64748b', '#14b8a6', '#6366f1'];
export const HAIR_NAMES = ['Buzz', 'Spiky', 'Long', 'Bob', 'Ponytail', 'Curly'];
export const TOP_NAMES = ['Emerald', 'Sky', 'Violet', 'Amber', 'Rose', 'Slate', 'Teal', 'Indigo'];

/** Deterministic 2D hash -> [0,1) for texture variation. */
export function hash2(x: number, y: number): number {
  let h = (x * 374761393 + y * 668265263) | 0;
  h = (h ^ (h >> 13)) * 1274126177;
  return ((h ^ (h >> 16)) >>> 0) / 4294967295;
}

function shade(hex: string, amt: number): string {
  const n = parseInt(hex.slice(1), 16);
  const f = (v: number): number => Math.min(255, Math.max(0, Math.round(v + amt)));
  const r = f((n >> 16) & 255);
  const g = f((n >> 8) & 255);
  const b = f(n & 255);
  return `rgb(${r},${g},${b})`;
}

/** Pre-rendered tile sprites. Index: tile * 4 + variant (floor has 4 visual variants). */
export function createTileset(): HTMLCanvasElement[] {
  const sprites: HTMLCanvasElement[] = [];
  const mk = (): [HTMLCanvasElement, CanvasRenderingContext2D] => {
    const c = document.createElement('canvas');
    c.width = TILE_PX;
    c.height = TILE_PX;
    const ctx = c.getContext('2d')!;
    return [c, ctx];
  };

  // 0 — void: deep background with faint speckle
  {
    const [c, ctx] = mk();
    ctx.fillStyle = '#0a0e16';
    ctx.fillRect(0, 0, TILE_PX, TILE_PX);
    ctx.fillStyle = 'rgba(255,255,255,0.025)';
    for (let i = 0; i < 6; i++) {
      ctx.fillRect(Math.floor(hash2(i, 7) * 30), Math.floor(hash2(7, i) * 30), 2, 2);
    }
    sprites[Tile.Void * 4] = c;
  }

  // 1 — floor: 4 subtle variants
  for (let v = 0; v < 4; v++) {
    const [c, ctx] = mk();
    const base = v % 2 === 0 ? '#161d2e' : '#181f32';
    ctx.fillStyle = base;
    ctx.fillRect(0, 0, TILE_PX, TILE_PX);
    ctx.strokeStyle = 'rgba(0,0,0,0.35)';
    ctx.lineWidth = 1;
    ctx.strokeRect(0.5, 0.5, TILE_PX - 1, TILE_PX - 1);
    ctx.fillStyle = 'rgba(255,255,255,0.03)';
    const sx = Math.floor(hash2(v, 1) * 24) + 2;
    const sy = Math.floor(hash2(1, v) * 24) + 2;
    ctx.fillRect(sx, sy, 3, 3);
    ctx.fillRect(sx + 8, sy + 12, 2, 2);
    sprites[Tile.Floor * 4 + v] = c;
  }

  // 2 — wall: raised block with lit top edge
  {
    const [c, ctx] = mk();
    ctx.fillStyle = '#232c44';
    ctx.fillRect(0, 0, TILE_PX, TILE_PX);
    ctx.fillStyle = '#2e3a5c';
    ctx.fillRect(0, 0, TILE_PX, 10);
    ctx.fillStyle = 'rgba(255,255,255,0.10)';
    ctx.fillRect(0, 0, TILE_PX, 2);
    ctx.fillStyle = 'rgba(0,0,0,0.45)';
    ctx.fillRect(0, TILE_PX - 4, TILE_PX, 4);
    ctx.fillStyle = 'rgba(255,255,255,0.04)';
    ctx.fillRect(4, 14, 8, 8);
    ctx.fillRect(18, 18, 6, 6);
    sprites[Tile.Wall * 4] = c;
  }

  // 3 — private: floor + emerald tint
  {
    const [c, ctx] = mk();
    ctx.drawImage(sprites[Tile.Floor * 4], 0, 0);
    ctx.fillStyle = 'rgba(16,185,129,0.16)';
    ctx.fillRect(0, 0, TILE_PX, TILE_PX);
    ctx.strokeStyle = 'rgba(16,185,129,0.35)';
    ctx.setLineDash([4, 3]);
    ctx.strokeRect(1.5, 1.5, TILE_PX - 3, TILE_PX - 3);
    ctx.setLineDash([]);
    sprites[Tile.Private * 4] = c;
  }

  // 4 — spotlight: floor + amber tint
  {
    const [c, ctx] = mk();
    ctx.drawImage(sprites[Tile.Floor * 4 + 1], 0, 0);
    ctx.fillStyle = 'rgba(245,158,11,0.16)';
    ctx.fillRect(0, 0, TILE_PX, TILE_PX);
    const g = ctx.createRadialGradient(16, 16, 2, 16, 16, 16);
    g.addColorStop(0, 'rgba(245,158,11,0.25)');
    g.addColorStop(1, 'rgba(245,158,11,0)');
    ctx.fillStyle = g;
    ctx.fillRect(0, 0, TILE_PX, TILE_PX);
    sprites[Tile.Spotlight * 4] = c;
  }

  // 5 — spawn: floor + emerald marker
  {
    const [c, ctx] = mk();
    ctx.drawImage(sprites[Tile.Floor * 4 + 2], 0, 0);
    ctx.strokeStyle = 'rgba(16,185,129,0.7)';
    ctx.lineWidth = 2;
    ctx.beginPath();
    ctx.arc(16, 16, 9, 0, Math.PI * 2);
    ctx.stroke();
    ctx.fillStyle = 'rgba(16,185,129,0.9)';
    ctx.beginPath();
    ctx.arc(16, 16, 3, 0, Math.PI * 2);
    ctx.fill();
    sprites[Tile.Spawn * 4] = c;
  }
  return sprites;
}

function spriteFor(sprites: HTMLCanvasElement[], tile: number, tx: number, ty: number): HTMLCanvasElement {
  if (tile === Tile.Floor) {
    const v = Math.floor(hash2(tx, ty) * 4);
    return sprites[Tile.Floor * 4 + v];
  }
  return sprites[tile * 4] ?? sprites[Tile.Void * 4];
}

/** Draw the visible map region. camX/camY = camera center in pixels. */
export function drawMap(
  ctx: CanvasRenderingContext2D,
  map: GameMapLike,
  sprites: HTMLCanvasElement[],
  camX: number,
  camY: number,
  viewW: number,
  viewH: number,
): void {
  const x0 = Math.max(0, Math.floor((camX - viewW / 2) / TILE_PX));
  const y0 = Math.max(0, Math.floor((camY - viewH / 2) / TILE_PX));
  const x1 = Math.min(map.width - 1, Math.ceil((camX + viewW / 2) / TILE_PX));
  const y1 = Math.min(map.height - 1, Math.ceil((camY + viewH / 2) / TILE_PX));
  for (let ty = y0; ty <= y1; ty++) {
    for (let tx = x0; tx <= x1; tx++) {
      const t = map.tiles[ty * map.width + tx] ?? 0;
      ctx.drawImage(spriteFor(sprites, t, tx, ty), tx * TILE_PX, ty * TILE_PX);
    }
  }
}

export interface DrawAvatarOpts {
  look: AvatarLook;
  /** center-bottom (feet) position in pixels */
  x: number;
  y: number;
  dir: 'up' | 'down' | 'left' | 'right';
  emote: '' | 'dance' | 'wave';
  ghost: boolean;
  speaking: boolean;
  isAgent: boolean;
  name: string;
  /** ms timestamp for animation */
  now: number;
  /** pixel scale (1 = 32px tile) */
  scale?: number;
  /** draw name tag */
  showName?: boolean;
}

export function drawAvatar(ctx: CanvasRenderingContext2D, o: DrawAvatarOpts): void {
  const s = o.scale ?? 1;
  const dancing = o.emote === 'dance';
  const waving = o.emote === 'wave';
  const bounce = dancing ? Math.abs(Math.sin(o.now / 160)) * -6 * s : 0;
  const sway = dancing ? Math.sin(o.now / 160) * 3 * s : 0;

  ctx.save();
  ctx.translate(o.x + sway, o.y + bounce);
  ctx.scale(s, s);
  if (o.ghost) ctx.globalAlpha = 0.45;

  const skin = SKIN_COLORS[o.look.skin % SKIN_COLORS.length];
  const hairC = HAIR_COLORS[o.look.hair_color % HAIR_COLORS.length];
  const topC = TOP_COLORS[o.look.top % TOP_COLORS.length];

  // speaking ring
  if (o.speaking) {
    ctx.strokeStyle = '#10b981';
    ctx.lineWidth = 2.5;
    ctx.beginPath();
    ctx.ellipse(0, -18, 15, 20, 0, 0, Math.PI * 2);
    ctx.stroke();
  }

  // shadow
  ctx.fillStyle = 'rgba(0,0,0,0.35)';
  ctx.beginPath();
  ctx.ellipse(0, 0, 10, 4, 0, 0, Math.PI * 2);
  ctx.fill();

  // body
  ctx.fillStyle = shade(topC, -18);
  roundRect(ctx, -9, -22, 18, 22, 7);
  ctx.fill();
  ctx.fillStyle = topC;
  roundRect(ctx, -9, -24, 18, 20, 7);
  ctx.fill();
  // arms (wave raises one)
  ctx.fillStyle = shade(topC, -30);
  const armUp = waving ? Math.sin(o.now / 200) * 4 - 10 : 0;
  roundRect(ctx, -13, -22 + (waving ? armUp : 0), 5, 14, 2.5);
  ctx.fill();
  roundRect(ctx, 8, -22, 5, 14, 2.5);
  ctx.fill();

  // head
  ctx.fillStyle = skin;
  ctx.beginPath();
  ctx.arc(0, -30, 9, 0, Math.PI * 2);
  ctx.fill();

  // hair by style
  ctx.fillStyle = hairC;
  const hs = o.look.hair % 6;
  if (hs === 0) {
    // buzz
    ctx.beginPath();
    ctx.arc(0, -31, 9, Math.PI, Math.PI * 2);
    ctx.fill();
  } else if (hs === 1) {
    // spiky
    ctx.beginPath();
    ctx.moveTo(-9, -30);
    for (let i = 0; i <= 6; i++) {
      const px = -9 + (18 / 6) * i;
      ctx.lineTo(px, -30);
      ctx.lineTo(px + 1.5, -40 - (i % 2) * 3);
    }
    ctx.lineTo(9, -30);
    ctx.closePath();
    ctx.fill();
  } else if (hs === 2) {
    // long
    ctx.beginPath();
    ctx.arc(0, -31, 9.5, Math.PI * 0.95, Math.PI * 2.05);
    ctx.fill();
    roundRect(ctx, -10.5, -34, 5, 18, 2.5);
    ctx.fill();
    roundRect(ctx, 5.5, -34, 5, 18, 2.5);
    ctx.fill();
  } else if (hs === 3) {
    // bob
    ctx.beginPath();
    ctx.arc(0, -30, 10, Math.PI * 0.9, Math.PI * 2.1);
    ctx.fill();
    roundRect(ctx, -11, -32, 4, 12, 2);
    ctx.fill();
    roundRect(ctx, 7, -32, 4, 12, 2);
    ctx.fill();
  } else if (hs === 4) {
    // ponytail
    ctx.beginPath();
    ctx.arc(0, -31, 9, Math.PI, Math.PI * 2);
    ctx.fill();
    ctx.beginPath();
    ctx.ellipse(10, -26, 3.5, 7, 0.4, 0, Math.PI * 2);
    ctx.fill();
  } else {
    // curly
    for (let i = 0; i < 7; i++) {
      const a = Math.PI + (Math.PI / 6) * i;
      ctx.beginPath();
      ctx.arc(Math.cos(a) * 8, -31 + Math.sin(a) * 8, 4, 0, Math.PI * 2);
      ctx.fill();
    }
  }

  // face: eyes shift with direction
  const ex = o.dir === 'left' ? -2.5 : o.dir === 'right' ? 2.5 : 0;
  const ey = o.dir === 'up' ? -1.5 : 1;
  ctx.fillStyle = '#1c2333';
  if (o.dir !== 'up') {
    ctx.beginPath();
    ctx.arc(-3 + ex, -29 + ey, 1.4, 0, Math.PI * 2);
    ctx.arc(3 + ex, -29 + ey, 1.4, 0, Math.PI * 2);
    ctx.fill();
  }

  ctx.restore();

  // name tag (unscaled text, drawn in screen space)
  if (o.showName !== false) {
    ctx.save();
    ctx.font = '600 11px -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif';
    ctx.textAlign = 'center';
    const label = o.isAgent ? `🤖 ${o.name}` : o.name;
    const w = ctx.measureText(label).width + 12;
    const bx = o.x + sway;
    const by = o.y + bounce - 52 * s;
    ctx.fillStyle = 'rgba(10,14,22,0.72)';
    roundRect(ctx, bx - w / 2, by - 15, w, 19, 6);
    ctx.fill();
    ctx.fillStyle = o.isAgent ? '#a78bfa' : '#eef2fa';
    ctx.fillText(label, bx, by - 1.5);
    ctx.restore();
  }
}

function roundRect(ctx: CanvasRenderingContext2D, x: number, y: number, w: number, h: number, r: number): void {
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}

export interface Bubble {
  text: string;
  until: number;
}

/** Speech bubble above an avatar; auto-expires after 3s (caller checks `until`). */
export function drawBubble(ctx: CanvasRenderingContext2D, x: number, y: number, b: Bubble): void {
  ctx.save();
  ctx.font = '12px -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif';
  const maxW = 180;
  const words = b.text.split(' ');
  const lines: string[] = [];
  let line = '';
  for (const w of words) {
    const t = line ? line + ' ' + w : w;
    if (ctx.measureText(t).width > maxW && line) {
      lines.push(line);
      line = w;
    } else {
      line = t;
    }
    if (lines.length >= 3) break;
  }
  if (line) lines.push(line);
  const w = Math.min(maxW, Math.max(...lines.map((l) => ctx.measureText(l).width))) + 16;
  const h = lines.length * 16 + 12;
  const bx = x - w / 2;
  const by = y - 62 - h;
  ctx.fillStyle = 'rgba(238,242,250,0.96)';
  roundRect(ctx, bx, by, w, h, 8);
  ctx.fill();
  ctx.fillStyle = 'rgba(238,242,250,0.96)';
  ctx.beginPath();
  ctx.moveTo(x - 5, by + h - 1);
  ctx.lineTo(x + 5, by + h - 1);
  ctx.lineTo(x, by + h + 7);
  ctx.closePath();
  ctx.fill();
  ctx.fillStyle = '#141a28';
  ctx.textAlign = 'left';
  lines.forEach((l, i) => ctx.fillText(l, bx + 8, by + 17 + i * 16));
  ctx.restore();
}
