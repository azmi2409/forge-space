/**
 * REST client for /api/v1 — field names match CONTRACT.md §3 exactly.
 * Same origin by default (the server also serves web/dist); dev proxy handles vite.
 */
import type {
  ApiErrorBody,
  Area,
  AuthUser,
  AvatarSpec,
  ChatMessage,
  ChatScope,
  GameMap,
  JoinResult,
  MapSummary,
  Participant,
  Space,
  SpaceObject,
} from '../game/types';

export class ApiError extends Error {
  code: string;
  status: number;
  constructor(code: string, message: string, status: number) {
    super(message);
    this.code = code;
    this.status = status;
  }
}

const TOKEN_KEY = 'forge-space:token';
const USER_KEY = 'forge-space:user';

export function loadToken(): string | null {
  return localStorage.getItem(TOKEN_KEY);
}
export function saveToken(token: string | null): void {
  if (token) localStorage.setItem(TOKEN_KEY, token);
  else localStorage.removeItem(TOKEN_KEY);
}
export function loadUser(): AuthUser | null {
  try {
    const raw = localStorage.getItem(USER_KEY);
    return raw ? (JSON.parse(raw) as AuthUser) : null;
  } catch {
    return null;
  }
}
export function saveUser(user: AuthUser | null): void {
  if (user) localStorage.setItem(USER_KEY, JSON.stringify(user));
  else localStorage.removeItem(USER_KEY);
}

export class ApiClient {
  private token: string | null = loadToken();

  setToken(t: string | null): void {
    this.token = t;
    saveToken(t);
  }
  getToken(): string | null {
    return this.token;
  }

  private async req<T>(method: string, path: string, body?: unknown): Promise<T> {
    const headers: Record<string, string> = { 'Content-Type': 'application/json' };
    if (this.token) headers['Authorization'] = `Bearer ${this.token}`;
    let res: Response;
    try {
      res = await fetch(`/api/v1${path}`, {
        method,
        headers,
        body: body === undefined ? undefined : JSON.stringify(body),
      });
    } catch (e) {
      throw new ApiError('network_error', 'Could not reach the server. Is it running?', 0);
    }
    const text = await res.text();
    let data: unknown = null;
    try {
      data = text ? JSON.parse(text) : null;
    } catch {
      data = null;
    }
    if (!res.ok) {
      const eb = data as ApiErrorBody | null;
      throw new ApiError(eb?.error?.code ?? 'request_failed', eb?.error?.message ?? `Request failed (${res.status})`, res.status);
    }
    return data as T;
  }

  // --- auth ---
  async register(email: string, password: string, display_name: string): Promise<{ user: AuthUser }> {
    return this.req('POST', '/auth/register', { email, password, display_name });
  }
  async login(email: string, password: string): Promise<{ token: string; user: AuthUser }> {
    const r = await this.req<{ token: string; user: AuthUser }>('POST', '/auth/login', { email, password });
    this.setToken(r.token);
    saveUser(r.user);
    return r;
  }
  logout(): void {
    this.setToken(null);
    saveUser(null);
  }

  // --- spaces ---
  async listSpaces(): Promise<{ spaces: Space[] }> {
    return this.req('GET', '/spaces');
  }
  async createSpace(name: string): Promise<{ space: Space }> {
    return this.req('POST', '/spaces', { name });
  }
  async getSpace(id: string): Promise<{ space: Space; maps: MapSummary[] }> {
    return this.req('GET', `/spaces/${id}`);
  }
  async spaceBySlug(slug: string): Promise<Space | null> {
    const { spaces } = await this.listSpaces();
    return spaces.find((s) => s.slug === slug) ?? null;
  }

  // --- maps / areas / objects ---
  async getMap(spaceId: string, mapId: string): Promise<{ map: GameMap }> {
    return this.req('GET', `/spaces/${spaceId}/maps/${mapId}`);
  }
  async putMap(spaceId: string, mapId: string, patch: Partial<Pick<GameMap, 'name' | 'tiles' | 'width' | 'height' | 'spawn'>>): Promise<{ map: GameMap }> {
    return this.req('PUT', `/spaces/${spaceId}/maps/${mapId}`, patch);
  }
  async getAreas(spaceId: string): Promise<{ areas: Area[] }> {
    return this.req('GET', `/spaces/${spaceId}/areas`);
  }
  async createArea(spaceId: string, a: Omit<Area, 'id' | 'map_id'> & { map_id: string }): Promise<{ area: Area }> {
    return this.req('POST', `/spaces/${spaceId}/areas`, a);
  }
  async deleteArea(areaId: string): Promise<void> {
    await this.req('DELETE', `/areas/${areaId}`);
  }
  async getObjects(spaceId: string): Promise<{ objects: SpaceObject[] }> {
    return this.req('GET', `/spaces/${spaceId}/objects`);
  }
  async createObject(spaceId: string, o: Omit<SpaceObject, 'id'>): Promise<{ object: SpaceObject }> {
    return this.req('POST', `/spaces/${spaceId}/objects`, o);
  }
  async patchObject(objectId: string, patch: Partial<SpaceObject>): Promise<{ object: SpaceObject }> {
    return this.req('PATCH', `/objects/${objectId}`, patch);
  }
  async deleteObject(objectId: string): Promise<void> {
    await this.req('DELETE', `/objects/${objectId}`);
  }

  // --- presence ---
  async getParticipants(spaceId: string): Promise<{ participants: Participant[] }> {
    return this.req('GET', `/spaces/${spaceId}/participants`);
  }
  async join(spaceId: string, display_name?: string, avatar?: AvatarSpec): Promise<JoinResult> {
    return this.req('POST', `/spaces/${spaceId}/join`, { display_name, avatar });
  }
  async leave(spaceId: string): Promise<void> {
    await this.req('POST', `/spaces/${spaceId}/leave`);
  }

  // --- chat ---
  async sendChat(spaceId: string, scope: ChatScope, to: string | undefined, text: string, announce = false): Promise<{ message: ChatMessage }> {
    return this.req('POST', `/spaces/${spaceId}/chat`, { scope, to, text, ...(announce ? { announce: true } : {}) });
  }
  async getChat(spaceId: string, limit = 50): Promise<{ messages: ChatMessage[] }> {
    return this.req('GET', `/spaces/${spaceId}/chat?limit=${limit}`);
  }

  // --- me ---
  async moveMe(target: { x?: number; y?: number; dx?: number; dy?: number }): Promise<{ participant: Participant }> {
    return this.req('POST', '/participants/me/move', target);
  }
  async portalMe(object_id: string): Promise<void> {
    await this.req('POST', '/participants/me/portal', { object_id });
  }
  async setMyState(patch: { emote?: string; ghost?: boolean; muted?: boolean; camera_off?: boolean; status?: string; speaking?: boolean }): Promise<void> {
    await this.req('POST', '/participants/me/state', patch);
  }
  async mediaToken(): Promise<{ url: string; token: string }> {
    return this.req('GET', '/media/token');
  }
  async health(): Promise<{ ok: boolean; media: boolean }> {
    const res = await fetch('/api/v1/health');
    return (await res.json()) as { ok: boolean; media: boolean };
  }
}

export interface ChatMessageList {
  messages: ChatMessage[];
}
