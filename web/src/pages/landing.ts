/**
 * Landing page: dark, subtle, professional.
 * Hero runs a live canvas demo — bots wandering a procedural map (no network).
 */
import { h, toast } from '../ui/dom';
import { ApiClient, loadUser } from '../net/api';
import { TILE_PX, createTileset, drawMap, drawAvatar, type AvatarLook } from '../game/render';
import { tileAtPos, isWallTile } from '../game/engine';
import { openAuthModal } from './auth';

const DEMO_MAP_W = 40;
const DEMO_MAP_H = 24;

interface Bot {
  x: number;
  y: number;
  tx: number;
  ty: number;
  dir: 'up' | 'down' | 'left' | 'right';
  look: AvatarLook;
  name: string;
  agent: boolean;
  emote: '' | 'dance';
  speaking: boolean;
  speakUntil: number;
}

/** Builds the demo map: border walls, a lounge, a private room, a spotlight stage. */
function buildDemoMap(): { width: number; height: number; tiles: number[] } {
  const w = DEMO_MAP_W;
  const h = DEMO_MAP_H;
  const tiles = new Array<number>(w * h).fill(1);
  const set = (x: number, y: number, t: number): void => {
    if (x >= 0 && y >= 0 && x < w && y < h) tiles[y * w + x] = t;
  };
  for (let x = 0; x < w; x++) {
    set(x, 0, 2);
    set(x, h - 1, 2);
  }
  for (let y = 0; y < h; y++) {
    set(0, y, 2);
    set(w - 1, y, 2);
  }
  // lounge walls (top-right private room)
  for (let x = 26; x <= 38; x++) set(x, 6, 2);
  for (let y = 6; y <= 14; y++) set(26, y, 2);
  set(32, 14, 0); // doorway (gap in bottom wall)
  for (let x = 27; x <= 38; x++) if (x !== 32) set(x, 14, 2);
  for (let x = 27; x < 39; x++) for (let y = 7; y < 14; y++) set(x, y, 3); // private tiles
  // spotlight stage (bottom-left)
  for (let x = 4; x <= 10; x++) for (let y = 17; y <= 21; y++) set(x, y, 4);
  // pillars
  for (const [px, py] of [[14, 5], [14, 18], [22, 5], [22, 18]]) set(px, py, 2);
  // spawn marker
  set(5, 5, 5);
  return { width: w, height: h, tiles };
}

const BOT_NAMES: [string, boolean][] = [
  ['Maya', false],
  ['Riko', false],
  ['Sari', false],
  ['Atlas', true],
  ['Dimas', false],
  ['Nova', true],
  ['Putri', false],
  ['Echo', true],
];

function makeBots(): Bot[] {
  const looks: AvatarLook[] = [
    { skin: 0, hair: 1, hair_color: 0, top: 0 },
    { skin: 2, hair: 3, hair_color: 1, top: 1 },
    { skin: 1, hair: 2, hair_color: 3, top: 4 },
    { skin: 3, hair: 0, hair_color: 0, top: 2 },
    { skin: 0, hair: 5, hair_color: 4, top: 3 },
    { skin: 4, hair: 4, hair_color: 2, top: 5 },
    { skin: 1, hair: 1, hair_color: 5, top: 6 },
    { skin: 2, hair: 3, hair_color: 0, top: 7 },
  ];
  return BOT_NAMES.map(([name, agent], i) => ({
    x: 4 + i * 4,
    y: 4 + (i % 3) * 3,
    tx: 4 + i * 4,
    ty: 4 + (i % 3) * 3,
    dir: 'down' as const,
    look: looks[i % looks.length],
    name,
    agent,
    emote: '' as const,
    speaking: false,
    speakUntil: 0,
  }));
}

function stepBots(bots: Bot[], map: { width: number; height: number; tiles: number[] }, dt: number, now: number): void {
  for (const b of bots) {
    const dx = b.tx - b.x;
    const dy = b.ty - b.y;
    const d = Math.hypot(dx, dy);
    if (d < 0.15) {
      // pick a new target
      for (let tries = 0; tries < 12; tries++) {
        const nx = 2 + Math.random() * (map.width - 4);
        const ny = 2 + Math.random() * (map.height - 4);
        if (!isWallTile(tileAtPos(map, nx, ny))) {
          b.tx = nx;
          b.ty = ny;
          break;
        }
      }
    } else {
      const sp = 2.6 * dt;
      const nx = b.x + (dx / d) * sp;
      const ny = b.y + (dy / d) * sp;
      if (!isWallTile(tileAtPos(map, nx, ny))) {
        b.x = nx;
        b.y = ny;
        b.dir = Math.abs(dx) > Math.abs(dy) ? (dx > 0 ? 'right' : 'left') : dy > 0 ? 'down' : 'up';
      } else {
        b.tx = b.x;
        b.ty = b.y;
      }
    }
    // random chatter: speaking rings + the occasional dance
    if (now > b.speakUntil && Math.random() < 0.002) {
      b.speaking = true;
      b.speakUntil = now + 1500 + Math.random() * 2500;
    }
    if (now > b.speakUntil) b.speaking = false;
    if (Math.random() < 0.0004) b.emote = b.emote === 'dance' ? '' : 'dance';
  }
}

function startHeroDemo(canvas: HTMLCanvasElement): () => void {
  const ctx = canvas.getContext('2d')!;
  const map = buildDemoMap();
  const sprites = createTileset();
  const bots = makeBots();
  let raf = 0;
  let last = performance.now();
  let running = true;

  const resize = (): void => {
    const rect = canvas.getBoundingClientRect();
    const dpr = Math.min(2, window.devicePixelRatio || 1);
    canvas.width = Math.max(1, Math.floor(rect.width * dpr));
    canvas.height = Math.max(1, Math.floor(rect.height * dpr));
  };
  resize();
  window.addEventListener('resize', resize);

  const frame = (now: number): void => {
    if (!running) return;
    const dt = Math.min(0.1, (now - last) / 1000);
    last = now;
    stepBots(bots, map, dt, now);

    const dpr = Math.min(2, window.devicePixelRatio || 1);
    const vw = canvas.width / dpr;
    const vh = canvas.height / dpr;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.fillStyle = '#0a0e16';
    ctx.fillRect(0, 0, vw, vh);

    // fit whole map
    const scale = Math.min(vw / (map.width * TILE_PX), vh / (map.height * TILE_PX));
    const ox = (vw - map.width * TILE_PX * scale) / 2;
    const oy = (vh - map.height * TILE_PX * scale) / 2;
    ctx.save();
    ctx.translate(ox, oy);
    ctx.scale(scale, scale);
    // world-pixel camera centered on the visible rect (context already scaled)
    drawMap(ctx, map, sprites, vw / scale / 2, vh / scale / 2, vw / scale, vh / scale);
    const sorted = [...bots].sort((a, b) => a.y - b.y);
    for (const b of sorted) {
      drawAvatar(ctx, {
        look: b.look,
        x: b.x * TILE_PX + TILE_PX / 2,
        y: (b.y + 1) * TILE_PX,
        dir: b.dir,
        emote: b.emote,
        ghost: false,
        speaking: b.speaking,
        isAgent: b.agent,
        name: b.name,
        now,
        showName: scale > 0.45,
      });
    }
    ctx.restore();
    ctx.restore();
    raf = requestAnimationFrame(frame);
  };

  raf = requestAnimationFrame(frame);
  return () => {
    running = false;
    cancelAnimationFrame(raf);
    window.removeEventListener('resize', resize);
  };
}

const MCP_SAMPLE = `# 1. create an agent key (dashboard or API), then hand it to your agent
export FORGE_SPACE_URL="https://space.example.com"
export FORGE_SPACE_KEY="fspk_YOUR_KEY_HERE"

# 2. run the MCP server (stdio) — 18 tools, zero config beyond the key
forge-space-mcp

# 3. from the agent (Claude Code, etc.)
#    space_join   -> walk in as a first-class participant
#    world_look   -> who is near me, what objects can I use
#    me_move_to   -> walk to (x, y) with server-side collision
#    me_say       -> speak (nearby / space-wide / DM)
#    media_token  -> join voice with any LiveKit SDK`;

export function renderLanding(app: HTMLElement): () => void {
  const api = new ApiClient();
  const cleanups: (() => void)[] = [];

  const go = (path: string): void => {
    window.history.pushState({}, '', path);
    window.dispatchEvent(new PopStateEvent('popstate'));
  };

  const heroCanvas = h('canvas', { class: 'hero-canvas' });
  const hero = h(
    'section',
    { class: 'hero' },
    h(
      'div',
      { class: 'hero-inner' },
      h(
        'div',
        { class: 'hero-copy' },
        h('div', { class: 'pill' }, h('span', { class: 'pulse-dot' }), 'Open-source virtual space'),
        h('h1', { text: 'Walk in. Talk. Build together.' }),
        h(
          'p',
          { class: 'lede', text: 'forge-space is a shared 2D world with spatial voice — for your team, and for the AI agents they bring. Humans and agents join the same rooms, hear the same conversations, use the same whiteboards.' },
        ),
        h(
          'div',
          { class: 'hero-cta' },
          h('button', { class: 'btn btn-primary btn-lg', text: 'Enter the demo space', onclick: () => go('/s/demo') }),
          h('button', { class: 'btn btn-ghost btn-lg', text: 'Create your own', onclick: () => createOwnSpace() }),
        ),
        h('p', { class: 'hero-note', text: 'Self-hosted · MIT · Live demo above is running entirely in your browser' }),
      ),
      h('div', { class: 'hero-demo' }, heroCanvas, h('div', { class: 'hero-demo-cap', text: 'Live simulation — 8 bots wandering a real map' })),
    ),
  );

  async function createOwnSpace(): Promise<void> {
    if (!loadUser()) {
      const authed = await openAuthModal(api, 'Create a space — sign in first');
      if (!authed) return;
    }
    const name = window.prompt('Name your space:', 'My space');
    if (!name || !name.trim()) return;
    try {
      const { space } = await api.createSpace(name.trim());
      go(`/s/${space.slug}`);
    } catch (e) {
      toast(e instanceof Error ? e.message : 'Could not create space', 'err');
    }
  }

  const feature = (icon: string, title: string, body: string): HTMLElement =>
    h('div', { class: 'feature' }, h('div', { class: 'feature-icon', text: icon }), h('h3', { text: title }), h('p', { text: body }));

  const sections = h(
    'section',
    { class: 'sections' },
    h(
      'div',
      { class: 'section-grid' },
      feature('🔊', 'Spatial audio', 'Voices fade with distance, exactly like a real room. Walk up to a group and their video tiles appear; walk away and they fade out. Private areas isolate sound; spotlight areas broadcast it.'),
      feature('🔒', 'Private areas & spotlight', 'Drag a rectangle to make a private zone — everyone inside hears each other fully, nobody outside hears a thing. Spotlight does the inverse: one speaker, heard across the whole map.'),
      feature('🗺️', 'Map editor', 'Paint tiles, carve out private and spotlight zones, drop in notes, whiteboards, videos and portals between maps. No design skills needed — it is all rectangles and clicks.'),
      h(
        'div',
        { class: 'feature feature-wide' },
        h('div', { class: 'feature-icon', text: '🤖' }),
        h('h3', { text: 'Agent-ready, not agent-tolerant' }),
        h('p', { text: 'Agents are first-class participants: they join with an API key, get a 🤖 badge and violet name, move with server-side collision, speak over LiveKit, read and write objects, and perceive the world through an MCP server plus an SSE event stream. If your agent can call tools, it can walk into the room.' }),
        h('pre', { class: 'code-sample' }, h('code', { text: MCP_SAMPLE })),
        h(
          'p',
          {},
          'Full integration guide: ',
          h('a', { href: 'https://github.com/azmi2409/forge-space/blob/main/docs/AGENTS.md', target: '_blank', rel: 'noopener', text: 'docs/AGENTS.md' }),
        ),
      ),
    ),
  );

  const howItWorks = h(
    'section',
    { class: 'how' },
    h('h2', { text: 'Sixty seconds to your first room' }),
    h(
      'ol',
      { class: 'steps' },
      h('li', {}, h('strong', { text: '1. Sign in. ' }), 'One account, thirty seconds.'),
      h('li', {}, h('strong', { text: '2. Pick a face. ' }), 'Avatar, mic, camera — then walk in.'),
      h('li', {}, h('strong', { text: '3. Bring an agent. ' }), 'Mint an API key, run the MCP server, watch it join the room.'),
    ),
    h('button', { class: 'btn btn-primary btn-lg', text: 'Try the demo space', onclick: () => go('/s/demo') }),
  );

  const footer = h(
    'footer',
    { class: 'footer' },
    h('div', { class: 'footer-inner' },
      h('div', {}, h('strong', { text: 'forge-space' }), h('p', { class: 'muted', text: 'A space for humans and agents.' })),
      h('div', { class: 'footer-links' },
        h('a', { href: 'https://github.com/azmi2409/forge-space', target: '_blank', rel: 'noopener', text: 'GitHub' }),
        h('a', { href: 'https://github.com/azmi2409/forge-space/blob/main/docs/AGENTS.md', target: '_blank', rel: 'noopener', text: 'Agent docs' }),
        h('span', { class: 'muted', text: 'MIT' }),
      ),
    ),
  );

  const nav = h(
    'nav',
    { class: 'topnav' },
    h('div', { class: 'topnav-inner' },
      h('a', { class: 'brand', href: '/', onclick: (e: Event) => { e.preventDefault(); go('/'); } },
        h('span', { class: 'brand-dot' }), 'forge-space'),
      h('div', { class: 'topnav-links' },
        h('a', { href: 'https://github.com/azmi2409/forge-space/blob/main/docs/AGENTS.md', target: '_blank', rel: 'noopener', text: 'Agent docs' }),
        h('button', { class: 'btn btn-primary', text: 'Enter demo', onclick: () => go('/s/demo') }),
      ),
    ),
  );

  app.append(nav, h('main', { class: 'landing' }, hero, sections, howItWorks), footer);
  cleanups.push(startHeroDemo(heroCanvas));
  return () => cleanups.forEach((fn) => fn());
}
