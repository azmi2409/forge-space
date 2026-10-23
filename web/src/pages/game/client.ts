/**
 * Game client for /s/:slug — full-viewport canvas, WS protocol per CONTRACT §4,
 * LiveKit voice wired to the server's `audio` events (§6), interpolated movement.
 */
import type {
  Area,
  ChatMessage,
  GameMap,
  Participant,
  ParticipantStateRow,
  ServerMsg,
  Space,
  SpaceObject,
} from '../../game/types';
import {
  audibleVolumes,
  clampCamera,
  lerpPos,
  nearestObject,
  type EngineArea,
  type EngineParticipant,
} from '../../game/engine';
import {
  TILE_PX,
  createTileset,
  drawAvatar,
  drawBubble,
  drawMap,
} from '../../game/render';
import { ApiClient } from '../../net/api';
import { GameSocket } from '../../net/socket';
import { MediaManager, type VideoKind } from '../../net/media';
import { clear, fmtTime, h, isModalOpen, openModal, toast } from '../../ui/dom';
import { loadDevicePrefs, saveDevicePrefs, type DevicePrefs, type JoinContext } from './join';
import { Whiteboard, type WhiteboardStrokeInput } from './whiteboard';

const TUTORIAL_KEY = 'forge-space:tutorial:v1';
const BUBBLE_MS = 3000;
const INTERACT_RADIUS = 2.5;

interface PView extends Participant {
  rx: number;
  ry: number;
  bubble: { text: string; until: number } | null;
  videos: Map<VideoKind, HTMLVideoElement>;
  liveSpeaking: boolean;
}

type ChatTab = 'nearby' | 'space' | 'dm';

function toEngine(p: { id: string; map_id: string; x: number; y: number; ghost: boolean }): EngineParticipant {
  return { id: p.id, map_id: p.map_id, x: p.x, y: p.y, ghost: p.ghost };
}
function areaToEngine(a: Area): EngineArea {
  return { id: a.id, map_id: a.map_id, kind: a.kind, x: a.x, y: a.y, w: a.w, h: a.h };
}

export class GameClient {
  private app: HTMLElement;
  private ctx: JoinContext;
  private api: ApiClient;
  private space: Space;
  private devices: DevicePrefs;

  private socket: GameSocket | null = null;
  private media = new MediaManager();
  private meId = '';
  private map: GameMap | null = null;
  private areas: Area[] = [];
  private objects: SpaceObject[] = [];
  private parts = new Map<string, PView>();
  private engineAreas: EngineArea[] = [];
  private radius = 5;

  private sprites = createTileset();
  private cam = { x: 0, y: 0 };
  private keys = new Set<string>();
  private inputSeq = 0;
  private lastInputSent = 0;
  private lastInput = { dx: 0 as -1 | 0 | 1, dy: 0 as -1 | 0 | 1 };
  private tapTarget: { x: number; y: number } | null = null;
  private followedId: string | null = null;
  private locateId: string | null = null;
  private locateUntil = 0;
  private myAreaId: string | null | undefined = undefined;
  private pinnedId: string | null = null;
  private dockIds: string[] = [];
  private lastSpeakState = false;
  private lastSpeakSent = 0;
  private destroyed = false;
  private helloReceived = false;
  private pingTimer: number | null = null;

  // DOM
  private root!: HTMLElement;
  private canvas!: HTMLCanvasElement;
  private g!: CanvasRenderingContext2D;
  private minimap!: HTMLCanvasElement;
  private minimapWrap!: HTMLElement;
  private dock!: HTMLElement;
  private panel!: HTMLElement;
  private chatList!: HTMLElement;
  private chatInput!: HTMLInputElement;
  private tabBar!: HTMLElement;
  private peopleList!: HTMLElement;
  private peopleSearch!: HTMLInputElement;
  private agentOnly!: HTMLInputElement;
  private toolbar!: HTMLElement;
  private banner!: HTMLElement;
  private promptEl!: HTMLElement;
  private voiceBadge!: HTMLElement;
  private sideTab: 'chat' | 'people' = 'chat';

  private chat: Record<'nearby' | 'space', ChatMessage[]> = { nearby: [], space: [] };
  private dms = new Map<string, ChatMessage[]>();
  private unread: Record<ChatTab, number> = { nearby: 0, space: 0, dm: 0 };
  private activeTab: ChatTab = 'nearby';
  private activeDm: string | null = null;

  private micOn: boolean;
  private camOn: boolean;
  private screenOn = false;
  private ghostOn = false;
  private activeBoard: { objectId: string; wb: Whiteboard } | null = null;

  constructor(app: HTMLElement, ctx: JoinContext) {
    this.app = app;
    this.ctx = ctx;
    this.api = ctx.api;
    this.space = ctx.space;
    this.devices = ctx.devices;
    this.micOn = ctx.devices.micOn;
    this.camOn = ctx.devices.camOn;
    this.radius = ctx.space.settings?.proximity_radius ?? 5;
  }

  /** Build DOM, connect socket + media, start the loop. */
  async start(): Promise<void> {
    this.buildDom();
    this.wireSocket();
    this.socket!.connect();
    this.pingTimer = window.setInterval(() => this.socket?.sendPing(Date.now()), 20000);
    this.loop(performance.now());
    window.setTimeout(() => {
      if (!this.helloReceived && !this.destroyed) {
        toast('Still connecting — the server may be starting up.', 'info');
      }
    }, 8000);
  }

  destroy(): void {
    this.destroyed = true;
    if (this.pingTimer !== null) {
      window.clearInterval(this.pingTimer);
      this.pingTimer = null;
    }
    window.removeEventListener('keydown', this.onKeyDown);
    window.removeEventListener('keyup', this.onKeyUp);
    this.socket?.close();
    void this.media.disconnect();
    this.root?.remove();
  }

  // ------------------------------------------------------------------ DOM ---

  private buildDom(): void {
    const c = (this.canvas = h('canvas', { class: 'game-canvas' }) as HTMLCanvasElement);
    this.g = c.getContext('2d')!;

    this.banner = h('div', { class: 'spotlight-banner hidden' });
    this.promptEl = h('div', { class: 'object-prompt hidden' });
    this.voiceBadge = h('div', { class: 'voice-badge hidden', text: 'Voice disabled (dev)' });

    this.minimap = h('canvas', { class: 'minimap-canvas', width: 176, height: 132 }) as HTMLCanvasElement;
    this.minimapWrap = h('div', { class: 'minimap-wrap' }, this.minimap);

    this.dock = h('div', { class: 'video-dock' });

    this.panel = h('div', { class: 'side-panel' });
    this.buildPanel();

    this.toolbar = h('div', { class: 'toolbar' });
    this.buildToolbar();

    const panelToggles = h(
      'div',
      { class: 'panel-toggles' },
      h('button', { class: 'panel-toggle', text: '💬', title: 'Chat', 'aria-label': 'Chat', onclick: () => this.togglePanel('chat') }),
      h('button', { class: 'panel-toggle', text: '👥', title: 'People', 'aria-label': 'People', onclick: () => this.togglePanel('people') }),
    );

    this.root = h(
      'div',
      { class: 'game' },
      c,
      this.banner,
      this.voiceBadge,
      this.promptEl,
      this.minimapWrap,
      this.dock,
      this.panel,
      panelToggles,
      this.toolbar,
      h('div', { class: 'loading-veil' }, h('div', { class: 'spinner' }), h('p', { text: 'Walking in…' })),
    );
    this.app.appendChild(this.root);

    new ResizeObserver(() => this.fitCanvas()).observe(this.root);
    this.fitCanvas();

    window.addEventListener('keydown', this.onKeyDown);
    window.addEventListener('keyup', this.onKeyUp);

    // tap-to-move (touch only — desktop uses keys)
    c.addEventListener('pointerdown', (e) => {
      if (e.pointerType !== 'touch' || !this.map) return;
      const rect = c.getBoundingClientRect();
      const wx = this.cam.x * TILE_PX + (e.clientX - rect.left - rect.width / 2);
      const wy = this.cam.y * TILE_PX + (e.clientY - rect.top - rect.height / 2);
      this.tapTarget = { x: wx / TILE_PX, y: wy / TILE_PX };
      this.followedId = null;
    });
  }

  private fitCanvas(): void {
    const rect = this.root.getBoundingClientRect();
    const dpr = Math.min(2, window.devicePixelRatio || 1);
    this.canvas.width = Math.max(1, Math.floor(rect.width * dpr));
    this.canvas.height = Math.max(1, Math.floor(rect.height * dpr));
  }

  // --------------------------------------------------------------- socket ---

  private wireSocket(): void {
    const { join } = this.ctx;
    const sock = (this.socket = new GameSocket(join.ws_url, join.ticket));
    sock.onOpen = () => {};
    sock.onMessage = (m) => this.handleServer(m);
    sock.onClose = (intentional) => {
      if (this.destroyed || intentional) return;
      this.showDisconnect();
    };
  }

  private handleServer(m: ServerMsg): void {
    switch (m.t) {
      case 'hello':
        this.onHello(m.you, m.participants, m.areas, m.objects, m.map);
        break;
      case 'state':
        this.onState(m.participants);
        break;
      case 'audio':
        this.media.applyAudible(m.volumes);
        break;
      case 'chat':
        this.onChat(m.message, false);
        break;
      case 'event':
        this.onEvent(m.kind, m.participant_id, m.data);
        break;
      case 'object':
        this.openObjectModal(m.object);
        break;
      case 'wb_stroke':
        if (this.activeBoard && m.object_id === this.activeBoard.objectId) {
          this.activeBoard.wb.addRemoteStroke(m.stroke);
        }
        break;
      case 'wb_clear':
        if (this.activeBoard && m.object_id === this.activeBoard.objectId) {
          this.activeBoard.wb.clearRemote();
        }
        break;
      case 'error':
        toast(m.message || 'Server error', 'err');
        break;
    }
  }

  private onHello(you: string, participants: Participant[], areas: Area[], objects: SpaceObject[], map: GameMap): void {
    this.meId = you;
    this.map = map;
    this.areas = areas;
    this.objects = objects;
    this.engineAreas = areas.map(areaToEngine);
    this.parts.clear();
    for (const p of participants) {
      this.parts.set(p.id, { ...p, rx: p.x, ry: p.y, bubble: null, videos: new Map(), liveSpeaking: false });
    }
    const me = this.parts.get(you);
    if (me) {
      this.cam.x = me.x;
      this.cam.y = me.y;
      this.ghostOn = me.ghost;
      this.syncToolbar();
    }
    this.helloReceived = true;
    this.root.querySelector('.loading-veil')?.remove();
    void this.connectMedia();
    void this.loadHistory();
    this.refreshPeople();
    if (!localStorage.getItem(TUTORIAL_KEY)) this.showTutorial();
    // owner shortcut to the map editor
    if (this.space.owner_user_id && this.isOwner()) this.addEditorButton();
  }

  private isOwner(): boolean {
    try {
      const raw = localStorage.getItem('forge-space:user');
      const u = raw ? (JSON.parse(raw) as { id: string }) : null;
      return !!u && u.id === this.space.owner_user_id;
    } catch {
      return false;
    }
  }

  private onState(rows: ParticipantStateRow[]): void {
    let needSync = false;
    for (const r of rows) {
      const p = this.parts.get(r.id);
      if (!p) {
        needSync = true;
        continue;
      }
      const areaChanged = p.area_id !== r.area_id;
      Object.assign(p, {
        x: r.x,
        y: r.y,
        dir: r.dir,
        emote: r.emote,
        ghost: r.ghost,
        muted: r.muted,
        camera_off: r.camera_off,
        speaking: r.speaking,
        status: r.status,
        area_id: r.area_id,
      });
      if (r.id === this.meId && areaChanged) this.onMyAreaChange(r.area_id);
      if (r.id === this.meId) {
        if (this.ghostOn !== r.ghost) {
          this.ghostOn = r.ghost;
          this.syncToolbar();
        }
      }
    }
    if (needSync) void this.syncParticipants();
    this.refreshDockSoon();
  }

  private async syncParticipants(): Promise<void> {
    try {
      const { participants } = await this.api.getParticipants(this.space.id);
      for (const p of participants) {
        if (!this.parts.has(p.id)) {
          this.parts.set(p.id, { ...p, rx: p.x, ry: p.y, bubble: null, videos: new Map(), liveSpeaking: false });
        }
      }
      this.refreshPeople();
    } catch {
      /* best effort */
    }
  }

  private onEvent(kind: string, participantId: string, data: Record<string, unknown>): void {
    if (kind === 'join') {
      const p = data['participant'] as Participant | undefined;
      if (p && !this.parts.has(p.id)) {
        this.parts.set(p.id, { ...p, rx: p.x, ry: p.y, bubble: null, videos: new Map(), liveSpeaking: false });
        toast(`${p.display_name} walked in`, 'info');
        this.refreshPeople();
      } else {
        void this.syncParticipants();
      }
    } else if (kind === 'leave') {
      const p = this.parts.get(participantId);
      if (p) {
        toast(`${p.display_name} left`, 'info');
        this.parts.delete(participantId);
        this.dockIds = this.dockIds.filter((id) => id !== participantId);
        if (this.pinnedId === participantId) this.pinnedId = null;
        this.refreshPeople();
        this.refreshDock();
      }
    } else if (kind === 'emote') {
      const p = this.parts.get(participantId);
      const emote = data['emote'] as string | undefined;
      if (p && (emote === 'dance' || emote === 'wave' || emote === '')) p.emote = emote;
    } else if (kind === 'area') {
      const p = this.parts.get(participantId);
      const areaId = (data['area_id'] as string | null | undefined) ?? null;
      if (p) p.area_id = areaId;
    } else if (kind === 'kick') {
      if (participantId === this.meId) {
        toast('You were removed from this space.', 'err');
        this.leave(true);
      } else {
        this.parts.delete(participantId);
        this.refreshPeople();
        this.refreshDock();
      }
    }
  }

  private async connectMedia(): Promise<void> {
    const { livekit } = this.ctx.join;
    if (!livekit) {
      this.voiceBadge.classList.remove('hidden');
      return;
    }
    const media = this.media;
    media.onVideoTrack = (identity, el, kind) => {
      const p = this.parts.get(identity);
      if (p) {
        p.videos.set(kind, el);
        this.refreshDock();
      }
    };
    media.onVideoLost = (identity, kind) => {
      this.parts.get(identity)?.videos.delete(kind);
      this.refreshDock();
    };
    media.onSpeaking = (identity, speaking) => {
      const p = this.parts.get(identity);
      if (p) p.liveSpeaking = speaking;
    };
    media.onLocalLevel = (level) => {
      const speaking = level > 0.12;
      const now = Date.now();
      if (speaking !== this.lastSpeakState && now - this.lastSpeakSent > 1200 && this.micOn) {
        this.lastSpeakState = speaking;
        this.lastSpeakSent = now;
        this.api.setMyState({ speaking }).catch(() => {});
      }
    };
    try {
      await media.connect(livekit.url, livekit.token, {
        mic: this.micOn,
        camera: this.camOn,
        micDeviceId: this.devices.micId || undefined,
        cameraDeviceId: this.devices.camId || undefined,
      });
      if (this.devices.speakerId) media.setOutputDevice(this.devices.speakerId);
      if (!this.micOn) await this.api.setMyState({ muted: true }).catch(() => {});
    } catch (e) {
      toast('Voice failed to connect — you can still explore and chat.', 'err');
      this.voiceBadge.classList.remove('hidden');
      this.voiceBadge.textContent = 'Voice unavailable';
    }
  }

  private showDisconnect(): void {
    const overlay = h(
      'div',
      { class: 'disconnect-veil' },
      h('div', { class: 'disconnect-card' },
        h('h3', { text: 'Disconnected' }),
        h('p', { class: 'muted', text: 'The connection to this space dropped.' }),
        h('button', { class: 'btn btn-primary', text: 'Rejoin', onclick: () => window.location.reload() }),
        h('button', { class: 'btn btn-ghost', text: 'Back home', onclick: () => (window.location.href = '/') }),
      ),
    );
    this.root.appendChild(overlay);
  }

  async leave(navigate = true): Promise<void> {
    this.destroy();
    try {
      await this.api.leave(this.space.id);
    } catch {
      /* already gone */
    }
    if (navigate) window.location.href = '/';
  }

  // ----------------------------------------------------------------- loop ---

  private loop = (nowMs: number): void => {
    if (this.destroyed) return;
    const dt = Math.min(0.1, (nowMs - (this.lastFrame || nowMs)) / 1000 || 0.016);
    this.lastFrame = nowMs;

    this.sendInput(nowMs);
    // interpolate every participant toward the server's authoritative position
    for (const p of this.parts.values()) {
      const n = lerpPos({ x: p.rx, y: p.ry }, { x: p.x, y: p.y }, dt * 1000);
      p.rx = n.x;
      p.ry = n.y;
      if (p.bubble && nowMs > p.bubble.until) p.bubble = null;
    }

    // camera follows me (or a followed participant)
    const focus = this.followedId ? this.parts.get(this.followedId) : this.parts.get(this.meId);
    if (focus && this.map) {
      this.cam.x += (focus.rx - this.cam.x) * Math.min(1, dt * 6);
      this.cam.y += (focus.ry - this.cam.y) * Math.min(1, dt * 6);
      const vw = this.canvas.width / Math.min(2, window.devicePixelRatio || 1) / TILE_PX;
      const vh = this.canvas.height / Math.min(2, window.devicePixelRatio || 1) / TILE_PX;
      const c = clampCamera(this.cam.x, this.cam.y, vw, vh, this.map.width, this.map.height);
      this.cam.x = c.x;
      this.cam.y = c.y;
    }

    this.draw(nowMs);
    this.updatePrompt();
    this.drawMinimapSoon(nowMs);
    requestAnimationFrame(this.loop);
  };
  private lastFrame = 0;

  private sendInput(nowMs: number): void {
    if (!this.socket?.connected || !this.map) return;
    let dx: -1 | 0 | 1 = 0;
    let dy: -1 | 0 | 1 = 0;
    const k = this.keys;
    if (k.has('arrowleft') || k.has('a')) dx = -1;
    else if (k.has('arrowright') || k.has('d')) dx = 1;
    if (k.has('arrowup') || k.has('w')) dy = -1;
    else if (k.has('arrowdown') || k.has('s')) dy = 1;

    // tap-to-move (touch): steer toward the target until close
    if (dx === 0 && dy === 0 && this.tapTarget) {
      const me = this.parts.get(this.meId);
      if (me) {
        const ddx = this.tapTarget.x - me.rx;
        const ddy = this.tapTarget.y - me.ry;
        if (Math.hypot(ddx, ddy) < 0.25) {
          this.tapTarget = null;
        } else {
          dx = Math.abs(ddx) > 0.12 ? (ddx > 0 ? 1 : -1) : 0;
          dy = Math.abs(ddy) > 0.12 ? (ddy > 0 ? 1 : -1) : 0;
        }
      }
    }

    const changed = dx !== this.lastInput.dx || dy !== this.lastInput.dy;
    if (changed || ((dx !== 0 || dy !== 0) && nowMs - this.lastInputSent > 120)) {
      this.inputSeq++;
      this.socket.sendInput(this.inputSeq, dx, dy);
      this.lastInput = { dx, dy };
      this.lastInputSent = nowMs;
    }
  }

  private onKeyDown = (e: KeyboardEvent): void => {
    if (this.destroyed) return;
    const tag = (e.target as HTMLElement | null)?.tagName;
    if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || isModalOpen()) {
      if (e.key === 'Escape') (e.target as HTMLElement)?.blur?.();
      return;
    }
    const key = e.key.toLowerCase();
    if (['arrowup', 'arrowdown', 'arrowleft', 'arrowright', ' '].includes(key)) e.preventDefault();

    if (key === 'x') {
      this.interactNearest();
      return;
    }
    if (key === 'z') {
      void this.toggleEmote('dance');
      return;
    }
    if (key === 'g') {
      void this.toggleGhost();
      return;
    }
    if (key === 'm') {
      void this.toggleMic();
      return;
    }
    if (key === 'escape') {
      this.followedId = null;
      this.locateId = null;
      this.tapTarget = null;
      return;
    }
    this.keys.add(key);
    if (this.tapTarget && ['arrowup', 'arrowdown', 'arrowleft', 'arrowright', 'w', 'a', 's', 'd'].includes(key)) {
      this.tapTarget = null; // keys take over from tap-to-move
    }
  };

  private onKeyUp = (e: KeyboardEvent): void => {
    this.keys.delete(e.key.toLowerCase());
  };

  // --------------------------------------------------------------- render ---

  private draw(nowMs: number): void {
    const ctx = this.g;
    const dpr = Math.min(2, window.devicePixelRatio || 1);
    const vw = this.canvas.width / dpr;
    const vh = this.canvas.height / dpr;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.fillStyle = '#0a0e16';
    ctx.fillRect(0, 0, vw, vh);
    if (!this.map) return;

    const camPxX = this.cam.x * TILE_PX;
    const camPxY = this.cam.y * TILE_PX;
    ctx.save();
    ctx.translate(vw / 2 - camPxX, vh / 2 - camPxY);

    drawMap(ctx, this.map, this.sprites, camPxX, camPxY, vw, vh);
    this.drawAreaOverlays(ctx);
    this.drawObjects(ctx, nowMs);

    const sorted = [...this.parts.values()].sort((a, b) => a.ry - b.ry);
    for (const p of sorted) {
      const px = p.rx * TILE_PX + TILE_PX / 2;
      const py = (p.ry + 1) * TILE_PX;
      drawAvatar(ctx, {
        look: p.avatar,
        x: px,
        y: py,
        dir: p.dir,
        emote: p.emote,
        ghost: p.ghost,
        speaking: p.speaking || p.liveSpeaking,
        isAgent: p.kind === 'agent',
        name: p.display_name,
        now: nowMs,
      });
      if (p.bubble && nowMs <= p.bubble.until) drawBubble(ctx, px, py - 8, p.bubble);
      if (p.muted && !p.ghost) {
        ctx.save();
        ctx.font = '11px sans-serif';
        ctx.fillText('🔇', px + 12, py - 48);
        ctx.restore();
      }
    }

    // locate arrow: pulsing ring + direction line from me to the target
    if (this.locateId && nowMs < this.locateUntil) {
      const t = this.parts.get(this.locateId);
      const me = this.parts.get(this.meId);
      if (t && me) {
        const tx = t.rx * TILE_PX + TILE_PX / 2;
        const ty = t.ry * TILE_PX + TILE_PX / 2;
        const pulse = 1 + Math.sin(nowMs / 200) * 0.15;
        ctx.strokeStyle = '#10b981';
        ctx.lineWidth = 2.5;
        ctx.beginPath();
        ctx.arc(tx, ty, 22 * pulse, 0, Math.PI * 2);
        ctx.stroke();
        const mx = me.rx * TILE_PX + TILE_PX / 2;
        const my = me.ry * TILE_PX + TILE_PX / 2;
        const a = Math.atan2(ty - my, tx - mx);
        ctx.strokeStyle = 'rgba(16,185,129,0.5)';
        ctx.setLineDash([6, 6]);
        ctx.beginPath();
        ctx.moveTo(mx + Math.cos(a) * 30, my + Math.sin(a) * 30);
        ctx.lineTo(tx - Math.cos(a) * 30, ty - Math.sin(a) * 30);
        ctx.stroke();
        ctx.setLineDash([]);
      }
    }
    ctx.restore();
  }

  private drawAreaOverlays(ctx: CanvasRenderingContext2D): void {
    for (const a of this.areas) {
      const x = a.x * TILE_PX;
      const y = a.y * TILE_PX;
      const w = a.w * TILE_PX;
      const h = a.h * TILE_PX;
      const color = a.kind === 'private' ? '16,185,129' : '245,158,11';
      ctx.fillStyle = `rgba(${color},0.07)`;
      ctx.fillRect(x, y, w, h);
      ctx.strokeStyle = `rgba(${color},0.55)`;
      ctx.lineWidth = 1.5;
      ctx.setLineDash([8, 5]);
      ctx.strokeRect(x + 1, y + 1, w - 2, h - 2);
      ctx.setLineDash([]);
      ctx.save();
      ctx.font = '600 10px -apple-system, sans-serif';
      ctx.fillStyle = `rgba(${color},0.9)`;
      ctx.fillText(a.kind === 'private' ? `🔒 ${a.name}` : `🔦 ${a.name}`, x + 6, y + 14);
      ctx.restore();
    }
  }

  private drawObjects(ctx: CanvasRenderingContext2D, nowMs: number): void {
    for (const o of this.objects) {
      const cx = (o.x + o.w / 2) * TILE_PX;
      const cy = (o.y + o.h / 2) * TILE_PX;
      const bob = Math.sin(nowMs / 600 + o.x) * 2;
      ctx.save();
      ctx.fillStyle = 'rgba(10,14,22,0.6)';
      ctx.beginPath();
      ctx.ellipse(cx, cy + 10, 12, 4, 0, 0, Math.PI * 2);
      ctx.fill();
      ctx.font = '20px sans-serif';
      ctx.textAlign = 'center';
      ctx.fillText(this.objectIcon(o.kind), cx, cy + 7 + bob);
      if (o.solid) {
        ctx.strokeStyle = 'rgba(139,146,173,0.5)';
        ctx.lineWidth = 1.5;
        ctx.strokeRect(o.x * TILE_PX + 2, o.y * TILE_PX + 2, o.w * TILE_PX - 4, o.h * TILE_PX - 4);
      }
      ctx.restore();
    }
  }

  private objectIcon(kind: SpaceObject['kind']): string {
    switch (kind) {
      case 'note':
        return '📝';
      case 'whiteboard':
        return '🧑‍🏫';
      case 'url':
        return '🔗';
      case 'video':
        return '🎬';
      case 'poster':
        return '🖼️';
      case 'portal':
        return '🌀';
    }
  }

  // ---------------------------------------------------------- interaction ---

  private nearestInteractable(): SpaceObject | null {
    const me = this.parts.get(this.meId);
    if (!me) return null;
    return nearestObject(this.objects, me.rx, me.ry, INTERACT_RADIUS);
  }

  private updatePrompt(): void {
    const o = this.nearestInteractable();
    if (o) {
      this.promptEl.classList.remove('hidden');
      this.promptEl.textContent = `Press X — ${o.title || this.objectIcon(o.kind) + ' ' + o.kind}`;
    } else {
      this.promptEl.classList.add('hidden');
    }
  }

  private interactNearest(): void {
    const o = this.nearestInteractable();
    if (o) this.socket?.sendInteract(o.id);
  }

  private openObjectModal(o: SpaceObject): void {
    const body = h('div', { class: 'object-modal' });
    const kindLabel = { note: 'Note', whiteboard: 'Whiteboard', url: 'Link', video: 'Video', poster: 'Poster', portal: 'Portal' }[o.kind];
    body.appendChild(h('p', { class: 'eyebrow', text: kindLabel }));
    if (o.kind === 'note' || o.kind === 'poster') {
      body.appendChild(h('p', { class: 'object-body', text: o.body || '(empty)' }));
    } else if (o.kind === 'whiteboard') {
      this.openWhiteboard(o, body);
    } else if (o.kind === 'url') {
      if (o.url) body.appendChild(h('a', { class: 'btn btn-primary', href: o.url, target: '_blank', rel: 'noopener', text: 'Open link ↗' }));
      else body.appendChild(h('p', { class: 'muted', text: 'No URL set on this object.' }));
    } else if (o.kind === 'video') {
      if (o.url) {
        const v = h('video', { class: 'object-video', src: o.url, controls: '' }) as HTMLVideoElement;
        body.appendChild(v);
      } else {
        body.appendChild(h('p', { class: 'muted', text: 'No video URL set on this object.' }));
      }
    } else if (o.kind === 'portal') {
      body.appendChild(h('p', { class: 'muted', text: 'Step through to another map.' }));
      const btn = h('button', { class: 'btn btn-primary', text: 'Teleport 🌀' });
      btn.onclick = () => {
        close();
        this.api.portalMe(o.id).catch((e: unknown) => toast(e instanceof Error ? e.message : 'Portal failed', 'err'));
      };
      body.appendChild(btn);
    }
    const close = openModal(o.title || kindLabel, body);
  }

  // ------------------------------------------------------- whiteboard v1 ---

  private openWhiteboard(o: SpaceObject, body: HTMLElement): void {
    const wb = new Whiteboard(body, {
      onStrokeEnd: (s) => this.sendBoardStroke(o.id, s),
      onClear: () => this.sendBoardClear(o.id),
      onDetach: () => {
        if (this.activeBoard?.wb === wb) this.activeBoard = null;
      },
    });
    this.activeBoard = { objectId: o.id, wb };
    void this.api
      .getBoard(o.id)
      .then(({ strokes }) => {
        if (this.activeBoard?.wb === wb) wb.loadStrokes(strokes);
      })
      .catch((e: unknown) => toast(e instanceof Error ? e.message : 'Could not load whiteboard', 'err'));
  }

  private sendBoardStroke(objectId: string, s: WhiteboardStrokeInput): void {
    if (this.socket?.connected) {
      // Server persists + broadcasts; our echo renders via handleServer (deduped by id).
      this.socket.sendWbStroke(objectId, s);
      return;
    }
    // WS not ready — fall back to REST; the returned stroke carries the server id.
    const wb = this.activeBoard?.wb;
    void this.api
      .postBoard(objectId, s)
      .then(({ stroke }) => {
        if (this.activeBoard?.objectId === objectId) wb?.addRemoteStroke(stroke);
      })
      .catch((e: unknown) => toast(e instanceof Error ? e.message : 'Stroke failed', 'err'));
  }

  private sendBoardClear(objectId: string): void {
    if (this.socket?.connected) {
      // Server clears + broadcasts; our echo clears via handleServer.
      this.socket.sendWbClear(objectId);
      return;
    }
    const wb = this.activeBoard?.wb;
    void this.api
      .clearBoard(objectId)
      .then(() => {
        if (this.activeBoard?.objectId === objectId) wb?.clearRemote();
      })
      .catch((e: unknown) => toast(e instanceof Error ? e.message : 'Clear failed', 'err'));
  }

  private onMyAreaChange(areaId: string | null): void {
    const prev = this.myAreaId;
    this.myAreaId = areaId;
    if (prev === undefined) {
      this.syncBanner(areaId); // initial — no toast
      return;
    }
    if (areaId === prev) return;
    const area = this.areas.find((a) => a.id === areaId) ?? null;
    const prevArea = this.areas.find((a) => a.id === prev) ?? null;
    if (prevArea) toast(`You left ${prevArea.name}`, 'info');
    if (area) {
      if (area.kind === 'private') toast(`🔒 You entered ${area.name} — private. Only people inside can hear you.`, 'ok');
      else toast(`🔦 You entered ${area.name} — spotlight. Everyone on this map can hear you.`, 'ok');
    }
    this.syncBanner(areaId);
  }

  private syncBanner(areaId: string | null): void {
    const area = this.areas.find((a) => a.id === areaId) ?? null;
    if (area?.kind === 'spotlight') {
      this.banner.classList.remove('hidden');
      this.banner.textContent = `🔦 You are in the spotlight (${area.name}) — everyone on this map can see and hear you`;
    } else {
      this.banner.classList.add('hidden');
    }
  }

  // -------------------------------------------------------------- minimap ---

  private lastMinimapDraw = 0;
  private drawMinimapSoon(nowMs: number): void {
    if (nowMs - this.lastMinimapDraw < 300 || !this.map || this.minimapWrap.classList.contains('hidden')) return;
    this.lastMinimapDraw = nowMs;
    const ctx = this.minimap.getContext('2d')!;
    const W = this.minimap.width;
    const H = this.minimap.height;
    const sx = W / this.map.width;
    const sy = H / this.map.height;
    ctx.fillStyle = '#0a0e16';
    ctx.fillRect(0, 0, W, H);
    for (let y = 0; y < this.map.height; y++) {
      for (let x = 0; x < this.map.width; x++) {
        const t = this.map.tiles[y * this.map.width + x];
        if (t === 0) continue;
        ctx.fillStyle = t === 2 ? '#2e3a5c' : t === 3 ? 'rgba(16,185,129,0.5)' : t === 4 ? 'rgba(245,158,11,0.5)' : '#1a2233';
        ctx.fillRect(x * sx, y * sy, Math.ceil(sx), Math.ceil(sy));
      }
    }
    for (const p of this.parts.values()) {
      ctx.fillStyle = p.id === this.meId ? '#ffffff' : p.kind === 'agent' ? '#a78bfa' : '#10b981';
      ctx.beginPath();
      ctx.arc(p.rx * sx, p.ry * sy, p.id === this.meId ? 3 : 2.2, 0, Math.PI * 2);
      ctx.fill();
    }
  }

  toggleMinimap(): void {
    this.minimapWrap.classList.toggle('hidden');
  }

  // ----------------------------------------------------------------- chat ---

  private buildPanel(): void {
    const chatTabBtn = h('button', { class: 'side-tab active', text: 'Chat' });
    const peopleTabBtn = h('button', { class: 'side-tab', text: 'People' });
    chatTabBtn.onclick = () => this.setSideTab('chat');
    peopleTabBtn.onclick = () => this.setSideTab('people');

    // --- chat view ---
    this.tabBar = h('div', { class: 'chat-tabs' });
    this.chatList = h('div', { class: 'chat-list' });
    this.chatInput = h('input', { class: 'input chat-input', placeholder: 'Message nearby…', maxlength: '500' }) as HTMLInputElement;
    this.chatInput.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') this.sendChatFromInput();
    });
    const sendBtn = h('button', { class: 'btn btn-primary btn-sm', text: 'Send', onclick: () => this.sendChatFromInput() });
    const chatView = h('div', { class: 'chat-view' }, this.tabBar, this.chatList, h('div', { class: 'chat-compose' }, this.chatInput, sendBtn));

    // --- people view ---
    this.peopleSearch = h('input', { class: 'input', placeholder: 'Search people…', 'aria-label': 'Search people' }) as HTMLInputElement;
    this.peopleSearch.addEventListener('input', () => this.refreshPeople());
    this.agentOnly = h('input', { type: 'checkbox', id: 'agent-only' }) as HTMLInputElement;
    this.agentOnly.addEventListener('change', () => this.refreshPeople());
    this.peopleList = h('div', { class: 'people-list' });
    const peopleView = h(
      'div',
      { class: 'people-view hidden' },
      h('div', { class: 'people-tools' }, this.peopleSearch, h('label', { class: 'check' }, this.agentOnly, h('span', { text: '🤖 agents only' }))),
      this.peopleList,
    );

    this.panel.append(
      h('div', { class: 'side-tabs' }, chatTabBtn, peopleTabBtn),
      chatView,
      peopleView,
    );
    this.chatView = chatView;
    this.peopleView = peopleView;
    this.chatTabBtn = chatTabBtn;
    this.peopleTabBtn = peopleTabBtn;
    this.renderChatTabs();
    this.renderChat();
  }
  private chatView!: HTMLElement;
  private peopleView!: HTMLElement;
  private chatTabBtn!: HTMLElement;
  private peopleTabBtn!: HTMLElement;

  /** Mobile: the side panel is an overlay toggled by floating buttons. */
  private togglePanel(t: 'chat' | 'people'): void {
    const willOpen = !this.panel.classList.contains('open') || this.sideTab !== t;
    this.setSideTab(t);
    this.panel.classList.toggle('open', willOpen);
  }

  private setSideTab(t: 'chat' | 'people'): void {
    this.sideTab = t;
    this.chatTabBtn.classList.toggle('active', t === 'chat');
    this.peopleTabBtn.classList.toggle('active', t === 'people');
    this.chatView.classList.toggle('hidden', t !== 'chat');
    this.peopleView.classList.toggle('hidden', t !== 'people');
    if (t === 'chat') this.renderChat();
  }

  private dmLabel(otherId: string): string {
    const p = this.parts.get(otherId);
    return p ? p.display_name : '…';
  }

  private renderChatTabs(): void {
    clear(this.tabBar);
    const tabs: { id: ChatTab; label: string }[] = [
      { id: 'nearby', label: 'Nearby' },
      { id: 'space', label: 'Space' },
      { id: 'dm', label: 'DMs' },
    ];
    for (const t of tabs) {
      const badge = this.unread[t.id] > 0 ? h('span', { class: 'badge', text: String(this.unread[t.id]) }) : null;
      const btn = h(
        'button',
        { class: `chat-tab${this.activeTab === t.id ? ' active' : ''}`, onclick: () => this.setActiveTab(t.id) },
        t.label,
        badge,
      );
      this.tabBar.appendChild(btn);
    }
    // DM thread picker
    if (this.activeTab === 'dm') {
      const sel = h('select', { class: 'input dm-select', 'aria-label': 'DM thread' }) as HTMLSelectElement;
      sel.appendChild(new Option('Pick someone…', ''));
      const ids = [...this.dms.keys()].filter((id) => id !== this.meId);
      for (const id of ids) {
        const opt = new Option(this.dmLabel(id), id);
        if (id === this.activeDm) opt.selected = true;
        sel.appendChild(opt);
      }
      sel.onchange = () => {
        this.activeDm = sel.value || null;
        this.renderChat();
      };
      this.tabBar.appendChild(sel);
    }
    this.chatInput.placeholder =
      this.activeTab === 'nearby' ? 'Message nearby…' : this.activeTab === 'space' ? 'Message the whole space…' : 'Pick a DM thread…';
  }

  private setActiveTab(t: ChatTab): void {
    this.activeTab = t;
    this.unread[t] = 0;
    this.renderChatTabs();
    this.renderChat();
  }

  private renderChat(): void {
    clear(this.chatList);
    const frag = document.createDocumentFragment();
    if (this.activeTab === 'dm') {
      const msgs = this.activeDm ? this.dms.get(this.activeDm) ?? [] : [];
      if (!this.activeDm) {
        frag.appendChild(h('p', { class: 'chat-empty', text: 'Pick someone from the People tab → Message, or choose a thread above.' }));
      }
      for (const m of msgs) frag.appendChild(this.chatBubbleEl(m));
    } else {
      const msgs = this.chat[this.activeTab];
      if (msgs.length === 0) frag.appendChild(h('p', { class: 'chat-empty', text: this.activeTab === 'nearby' ? 'Quiet around here. Walk up to someone and say hi.' : 'No space-wide messages yet.' }));
      for (const m of msgs) frag.appendChild(this.chatBubbleEl(m));
    }
    this.chatList.appendChild(frag);
    this.chatList.scrollTop = this.chatList.scrollHeight;
  }

  private chatBubbleEl(m: ChatMessage): HTMLElement {
    const mine = m.from_participant_id === this.meId;
    const name = mine ? 'You' : m.from_name;
    return h(
      'div',
      { class: `chat-msg${mine ? ' mine' : ''}` },
      h('div', { class: 'chat-meta' },
        h('span', { class: `chat-name${m.from_kind === 'agent' ? ' agent' : ''}`, text: (m.from_kind === 'agent' && !mine ? '🤖 ' : '') + name }),
        h('span', { class: 'chat-time', text: fmtTime(m.created_at) }),
      ),
      h('p', { class: 'chat-text', text: m.text }),
    );
  }

  private async loadHistory(): Promise<void> {
    try {
      const { messages } = await this.api.getChat(this.space.id, 50);
      for (const m of messages) this.routeMessage(m);
      this.renderChatTabs();
      this.renderChat();
    } catch {
      /* chat still works live */
    }
  }

  private onChat(m: ChatMessage, _local: boolean): void {
    this.routeMessage(m);
    // speech bubble for nearby + DM messages from others (3s)
    if (m.from_participant_id !== this.meId && (m.scope === 'nearby' || m.scope === 'dm')) {
      const p = this.parts.get(m.from_participant_id);
      if (p) p.bubble = { text: m.text, until: performance.now() + BUBBLE_MS };
    }
    this.renderChatTabs();
    if (this.sideTab === 'chat') this.renderChat();
  }

  private routeMessage(m: ChatMessage): void {
    if (m.scope === 'dm') {
      const other = m.from_participant_id === this.meId ? m.to_participant_id ?? '' : m.from_participant_id;
      if (!other) return;
      if (!this.dms.has(other)) this.dms.set(other, []);
      const thread = this.dms.get(other)!;
      if (!thread.some((x) => x.id === m.id)) thread.push(m);
      if (!(this.activeTab === 'dm' && this.activeDm === other && this.sideTab === 'chat')) this.unread.dm++;
    } else {
      const list = this.chat[m.scope];
      if (!list.some((x) => x.id === m.id)) list.push(m);
      if (list.length > 200) list.splice(0, list.length - 200);
      if (!(this.activeTab === m.scope && this.sideTab === 'chat')) this.unread[m.scope]++;
    }
  }

  private announceChecked = false;

  private sendChatFromInput(): void {
    const text = this.chatInput.value.trim();
    if (!text || !this.socket?.connected) return;
    if (this.activeTab === 'dm') {
      if (!this.activeDm) {
        toast('Pick a DM thread first.', 'info');
        return;
      }
      this.socket.sendChat('dm', this.activeDm, text);
    } else if (this.activeTab === 'space' && this.announceChecked && this.isOwner()) {
      // announcements go over REST (WS chat has no announce flag) — owner only
      this.api.sendChat(this.space.id, 'space', undefined, text, true).catch((e: unknown) => {
        toast(e instanceof Error ? e.message : 'Announcement failed', 'err');
      });
    } else {
      this.socket.sendChat(this.activeTab, undefined, text);
    }
    this.chatInput.value = '';
  }

  // --------------------------------------------------------------- people ---

  private refreshPeople(): void {
    if (!this.peopleList) return;
    clear(this.peopleList);
    const q = this.peopleSearch.value.trim().toLowerCase();
    const onlyAgents = this.agentOnly.checked;
    const list = [...this.parts.values()]
      .filter((p) => p.id !== this.meId)
      .filter((p) => !onlyAgents || p.kind === 'agent')
      .filter((p) => !q || p.display_name.toLowerCase().includes(q) || (p.status || '').toLowerCase().includes(q))
      .sort((a, b) => a.display_name.localeCompare(b.display_name));

    const count = h('p', { class: 'people-count', text: `${list.length} here${onlyAgents ? ' (agents)' : ''}` });
    this.peopleList.appendChild(count);

    for (const p of list) {
      const row = h('div', { class: 'person' });
      const mini = h('canvas', { class: 'person-avatar', width: '36', height: '36' }) as HTMLCanvasElement;
      const mctx = mini.getContext('2d')!;
      drawAvatar(mctx, {
        look: p.avatar, x: 18, y: 34, dir: p.dir, emote: '', ghost: p.ghost,
        speaking: false, isAgent: p.kind === 'agent', name: '', now: 0, scale: 0.75, showName: false,
      });
      const info = h(
        'div',
        { class: 'person-info' },
        h('div', { class: `person-name${p.kind === 'agent' ? ' agent' : ''}`, text: (p.kind === 'agent' ? '🤖 ' : '') + p.display_name }),
        p.status ? h('div', { class: 'person-status', text: p.status }) : null,
      );
      const actions = h('div', { class: 'person-actions' });
      const locateBtn = h('button', { class: 'btn btn-ghost btn-xs', text: 'Locate', title: 'Show an arrow to them' });
      locateBtn.onclick = () => {
        this.locateId = p.id;
        this.locateUntil = performance.now() + 4000;
        toast(`Arrow pointing at ${p.display_name}`, 'info');
      };
      const followBtn = h('button', {
        class: `btn btn-ghost btn-xs${this.followedId === p.id ? ' active' : ''}`,
        text: this.followedId === p.id ? 'Following' : 'Follow',
        title: 'Camera follows them',
      });
      followBtn.onclick = () => {
        this.followedId = this.followedId === p.id ? null : p.id;
        this.refreshPeople();
      };
      const dmBtn = h('button', { class: 'btn btn-ghost btn-xs', text: 'Message' });
      dmBtn.onclick = () => {
        if (!this.dms.has(p.id)) this.dms.set(p.id, []);
        this.activeDm = p.id;
        this.setSideTab('chat');
        this.setActiveTab('dm');
        this.chatInput.focus();
      };
      actions.append(locateBtn, followBtn, dmBtn);
      row.append(mini, info, actions);
      this.peopleList.appendChild(row);
    }
  }

  // ------------------------------------------------------------ video dock ---

  private dockTimer = 0;
  private refreshDockSoon(): void {
    const now = performance.now();
    if (now - this.dockTimer < 500) return;
    this.dockTimer = now;
    this.refreshDock();
  }

  /** Audible set (CONTRACT §5 visible set) drives the dock — audible participants only. */
  private refreshDock(): void {
    const me = this.parts.get(this.meId);
    if (!me) return;
    const vols = audibleVolumes(toEngine(me), [...this.parts.values()].map(toEngine), this.engineAreas, this.radius);
    const ids = [...vols.keys()].filter((id) => vols.get(id)! > 0);
    const same = ids.length === this.dockIds.length && ids.every((id, i) => this.dockIds[i] === id);
    if (same) return;
    this.dockIds = ids;
    clear(this.dock);
    if (this.pinnedId && !ids.includes(this.pinnedId)) this.pinnedId = null;

    const pinned = this.pinnedId ? this.parts.get(this.pinnedId) : null;
    if (pinned) this.dock.appendChild(this.videoTile(pinned, true));
    for (const id of ids) {
      if (id === this.pinnedId) continue;
      const p = this.parts.get(id);
      if (p) this.dock.appendChild(this.videoTile(p, false));
    }
    this.dock.classList.toggle('hidden', ids.length === 0);
  }

  private videoTile(p: PView, large: boolean): HTMLElement {
    const tile = h('div', { class: `video-tile${large ? ' pinned' : ''}`, title: `${p.display_name} — click to ${large ? 'unpin' : 'pin'}` });
    tile.onclick = () => {
      this.pinnedId = large ? null : p.id;
      this.dockIds = [];
      this.refreshDock();
    };
    const video = p.videos.get('screen') ?? p.videos.get('camera');
    if (video && !p.camera_off) {
      video.classList.add('video-el');
      tile.appendChild(video);
    } else {
      const mini = h('canvas', { class: 'video-placeholder', width: '96', height: '72' }) as HTMLCanvasElement;
      const mctx = mini.getContext('2d')!;
      mctx.fillStyle = '#151c2e';
      mctx.fillRect(0, 0, 96, 72);
      drawAvatar(mctx, {
        look: p.avatar, x: 48, y: 68, dir: p.dir, emote: '', ghost: false,
        speaking: false, isAgent: p.kind === 'agent', name: '', now: 0, scale: 1.4, showName: false,
      });
      tile.appendChild(mini);
    }
    tile.appendChild(
      h('div', { class: `video-name${p.kind === 'agent' ? ' agent' : ''}`, text: (p.kind === 'agent' ? '🤖 ' : '') + p.display_name }),
    );
    if (p.muted) tile.appendChild(h('div', { class: 'video-muted', text: '🔇' }));
    return tile;
  }

  // --------------------------------------------------------------- toolbar ---

  private micBtn!: HTMLButtonElement;
  private camBtn!: HTMLButtonElement;
  private screenBtn!: HTMLButtonElement;
  private ghostBtn!: HTMLButtonElement;

  private buildToolbar(): void {
    const btn = (icon: string, label: string, fn: () => void): HTMLButtonElement => {
      const b = h('button', { class: 'tool-btn', title: label, 'aria-label': label, text: icon, onclick: fn }) as HTMLButtonElement;
      this.toolbar.appendChild(b);
      return b;
    };
    this.micBtn = btn('🎙️', 'Mute/unmute (M)', () => void this.toggleMic());
    this.camBtn = btn('📷', 'Camera on/off', () => void this.toggleCamera());
    this.screenBtn = btn('🖥️', 'Share screen', () => void this.toggleScreen());
    btn('😊', 'Emote (Z = dance)', () => this.openEmotePicker());
    this.ghostBtn = btn('👻', 'Ghost mode (G) — walk through walls, silent', () => void this.toggleGhost());
    btn('🗺️', 'Toggle minimap', () => this.toggleMinimap());
    btn('⟲', 'Reset position (back to spawn)', () => void this.resetPosition());
    btn('⚙️', 'Settings', () => this.openSettings());
    btn('🚪', 'Leave space', () => void this.leave());
    this.syncToolbar();
  }

  private syncToolbar(): void {
    if (!this.micBtn) return;
    this.micBtn.classList.toggle('off', !this.micOn);
    this.camBtn.classList.toggle('off', !this.camOn);
    this.screenBtn.classList.toggle('active', this.screenOn);
    this.ghostBtn.classList.toggle('active', this.ghostOn);
  }

  private async toggleMic(): Promise<void> {
    this.micOn = !this.micOn;
    try {
      await this.media.setMic(this.micOn);
      await this.api.setMyState({ muted: !this.micOn });
    } catch (e) {
      toast(e instanceof Error ? e.message : 'Mic toggle failed', 'err');
      this.micOn = !this.micOn;
    }
    this.syncToolbar();
  }

  private async toggleCamera(): Promise<void> {
    this.camOn = !this.camOn;
    try {
      await this.media.setCamera(this.camOn);
      await this.api.setMyState({ camera_off: !this.camOn });
    } catch (e) {
      toast(e instanceof Error ? e.message : 'Camera toggle failed', 'err');
      this.camOn = !this.camOn;
    }
    this.syncToolbar();
  }

  private async toggleScreen(): Promise<void> {
    this.screenOn = !this.screenOn;
    try {
      await this.media.setScreenShare(this.screenOn);
    } catch {
      toast('Screen share was cancelled or failed.', 'err');
      this.screenOn = !this.screenOn;
    }
    this.syncToolbar();
  }

  private async toggleGhost(): Promise<void> {
    const next = !this.ghostOn;
    try {
      await this.api.setMyState({ ghost: next });
      this.ghostOn = next;
      toast(next ? '👻 Ghost mode — you are silent and walk through walls.' : 'Back to solid. People can hear you again.', 'info');
    } catch (e) {
      toast(e instanceof Error ? e.message : 'Ghost toggle failed', 'err');
    }
    this.syncToolbar();
  }

  private async toggleEmote(name: 'dance' | 'wave'): Promise<void> {
    const me = this.parts.get(this.meId);
    const next = me && me.emote === name ? '' : name;
    try {
      await this.api.setMyState({ emote: next });
      if (me) me.emote = next as '' | 'dance' | 'wave';
    } catch (e) {
      toast(e instanceof Error ? e.message : 'Emote failed', 'err');
    }
  }

  private openEmotePicker(): void {
    const body = h('div', { class: 'emote-picker' });
    const close = openModal('Emote', body);
    const pick = (label: string, name: 'dance' | 'wave' | ''): void => {
      const b = h('button', { class: 'btn btn-ghost btn-block', text: label });
      b.onclick = () => {
        close();
        if (name === '') this.api.setMyState({ emote: '' }).catch(() => {});
        else void this.toggleEmote(name);
      };
      body.appendChild(b);
    };
    pick('💃 Dance (Z)', 'dance');
    pick('👋 Wave', 'wave');
    pick('Clear emote', '');
  }

  private async resetPosition(): Promise<void> {
    if (!this.map) return;
    try {
      await this.api.moveMe({ x: this.map.spawn.x, y: this.map.spawn.y });
      toast('Heading back to spawn…', 'info');
    } catch (e) {
      toast(e instanceof Error ? e.message : 'Reset failed', 'err');
    }
  }

  // -------------------------------------------------------------- settings ---

  private openSettings(): void {
    const body = h('div', { class: 'settings' });
    const devices = loadDevicePrefs();

    const micSel = h('select', { class: 'input' }) as HTMLSelectElement;
    const camSel = h('select', { class: 'input' }) as HTMLSelectElement;
    const spkSel = h('select', { class: 'input' }) as HTMLSelectElement;
    const fill = async (): Promise<void> => {
      try {
        const all = await navigator.mediaDevices.enumerateDevices();
        const fillSel = (sel: HTMLSelectElement, kind: MediaDeviceKind, cur: string, none: string): void => {
          sel.innerHTML = '';
          sel.appendChild(new Option(none, ''));
          for (const d of all.filter((x) => x.kind === kind)) sel.appendChild(new Option(d.label || `${kind} ${sel.length}`, d.deviceId));
          sel.value = cur;
        };
        fillSel(micSel, 'audioinput', devices.micId, 'Default microphone');
        fillSel(camSel, 'videoinput', devices.camId, 'Default camera');
        fillSel(spkSel, 'audiooutput', devices.speakerId, 'Default speaker');
      } catch {
        /* keep defaults */
      }
    };
    void fill();

    const applyDevices = h('button', { class: 'btn btn-primary btn-sm', text: 'Apply devices' });
    applyDevices.onclick = () => {
      devices.micId = micSel.value;
      devices.camId = camSel.value;
      devices.speakerId = spkSel.value;
      saveDevicePrefs(devices);
      this.media.setDevices(devices.micId || undefined, devices.camId || undefined).catch(() => {});
      this.media.setOutputDevice(devices.speakerId || null);
      toast('Devices updated.', 'ok');
    };

    const statusInput = h('input', { class: 'input', placeholder: 'Status — e.g. “heads down”', maxlength: '80', value: this.parts.get(this.meId)?.status ?? '' }) as HTMLInputElement;
    const saveStatus = h('button', { class: 'btn btn-primary btn-sm', text: 'Set status' });
    saveStatus.onclick = () => {
      this.api.setMyState({ status: statusInput.value.trim() }).then(() => toast('Status updated.', 'ok')).catch((e: unknown) => toast(e instanceof Error ? e.message : 'Failed', 'err'));
    };

    body.append(
      h('h4', { text: 'Status' }),
      h('div', { class: 'row' }, statusInput, saveStatus),
      h('h4', { text: 'Devices' }),
      h('label', { class: 'field' }, 'Microphone', micSel),
      h('label', { class: 'field' }, 'Camera', camSel),
      h('label', { class: 'field' }, 'Speaker', spkSel),
      applyDevices,
      h('h4', { text: 'Space' }),
      h('p', { class: 'muted', text: `Proximity radius: ${this.radius} tiles (set by the space owner)` }),
      h('h4', { text: 'Keyboard' }),
      h(
        'ul',
        { class: 'keybinds' },
        h('li', {}, h('kbd', { text: 'WASD / ←↑↓→' }), ' move'),
        h('li', {}, h('kbd', { text: 'X' }), ' interact with nearby object'),
        h('li', {}, h('kbd', { text: 'Z' }), ' dance'),
        h('li', {}, h('kbd', { text: 'G' }), ' ghost mode'),
        h('li', {}, h('kbd', { text: 'M' }), ' mute/unmute'),
        h('li', {}, h('kbd', { text: 'Esc' }), ' close panels / stop following'),
      ),
    );
    if (this.isOwner()) {
      const editBtn = h('button', { class: 'btn btn-ghost btn-block', text: '🗺️ Edit this map' });
      editBtn.onclick = () => {
        window.location.href = `/s/${this.space.slug}/edit`;
      };
      body.appendChild(editBtn);
    }
    if (this.isOwner()) {
      const ann = h('input', { type: 'checkbox' }) as HTMLInputElement;
      ann.checked = this.announceChecked;
      ann.onchange = () => (this.announceChecked = ann.checked);
      body.append(h('h4', { text: 'Owner' }), h('label', { class: 'check' }, ann, h('span', { text: 'Send Space-tab messages as announcements' })));
    }
    openModal('Settings', body);
  }

  private addEditorButton(): void {
    // owner shortcut lives in settings; also add a small floating button
    const b = h('button', { class: 'editor-fab', text: '🗺️ Edit map', title: 'Open the map editor (owner)' });
    b.onclick = () => (window.location.href = `/s/${this.space.slug}/edit`);
    this.root.appendChild(b);
  }

  // -------------------------------------------------------------- tutorial ---

  private showTutorial(): void {
    const body = h(
      'div',
      { class: 'tutorial' },
      h('p', { class: 'muted', text: 'First time here? Sixty seconds:' }),
      h(
        'ul',
        { class: 'keybinds' },
        h('li', {}, h('kbd', { text: 'WASD' }), ' or ', h('kbd', { text: '←↑↓→' }), ' — walk around'),
        h('li', {}, h('kbd', { text: 'X' }), ' — use a nearby object (notes, portals…)'),
        h('li', {}, h('kbd', { text: 'Z' }), ' — dance'),
        h('li', {}, h('kbd', { text: 'G' }), ' — ghost: silent, walk through walls'),
        h('li', {}, h('kbd', { text: 'M' }), ' — mute yourself'),
      ),
      h('p', { class: 'muted', text: 'Walk close to people to hear them — voices fade with distance. Green zones are private, amber zones are spotlight.' }),
      h('button', { class: 'btn btn-primary btn-block', text: 'Start exploring', onclick: () => {
        localStorage.setItem(TUTORIAL_KEY, '1');
        close();
      } }),
    );
    const close = openModal(`Welcome to ${this.space.name} 👋`, body, { lock: true });
  }
}

/** Route entry: assumes join already completed. Returns a cleanup function. */
export async function startGame(app: HTMLElement, ctx: JoinContext): Promise<() => void> {
  const client = new GameClient(app, ctx);
  await client.start();
  return () => client.destroy();
}
