// Shared JSON shapes — mirrors CONTRACT.md §2 and §4 exactly.
// Field names must match what the server sends; do not add invented fields.

export interface SpaceSettings {
  proximity_radius: number;
}

export interface Space {
  id: string;
  name: string;
  slug: string;
  owner_user_id: string;
  settings: SpaceSettings;
  created_at: string;
}

export interface MapSpawn {
  x: number;
  y: number;
}

export interface GameMap {
  id: string;
  space_id: string;
  name: string;
  width: number;
  height: number;
  /** row-major, width*height ints: 0=void 1=floor 2=wall 3=private 4=spotlight 5=spawn */
  tiles: number[];
  spawn: MapSpawn;
}

export interface MapSummary {
  id: string;
  space_id: string;
  name: string;
  width: number;
  height: number;
}

export interface Area {
  id: string;
  map_id: string;
  kind: 'private' | 'spotlight';
  name: string;
  x: number;
  y: number;
  w: number;
  h: number;
}

export type ObjectKind = 'note' | 'whiteboard' | 'url' | 'video' | 'poster' | 'portal';

export interface SpaceObject {
  id: string;
  map_id: string;
  kind: ObjectKind;
  x: number;
  y: number;
  w: number;
  h: number;
  title: string;
  body: string;
  url?: string;
  target_map_id?: string;
  target_x?: number;
  target_y?: number;
  solid: boolean;
}

export type Direction = 'up' | 'down' | 'left' | 'right';
export type Emote = '' | 'dance' | 'wave';

export interface AvatarSpec {
  skin: number;
  hair: number;
  hair_color: number;
  top: number;
}

export interface Participant {
  id: string;
  space_id: string;
  map_id: string;
  kind: 'human' | 'agent';
  display_name: string;
  avatar: AvatarSpec;
  /** float tile coords */
  x: number;
  y: number;
  dir: Direction;
  emote: Emote;
  ghost: boolean;
  muted: boolean;
  camera_off: boolean;
  speaking: boolean;
  status: string;
  area_id: string | null;
  updated_at: string;
}

/** Partial participant row from the 10Hz `state` event. */
export interface ParticipantStateRow {
  id: string;
  x: number;
  y: number;
  dir: Direction;
  emote: Emote;
  ghost: boolean;
  muted: boolean;
  camera_off: boolean;
  speaking: boolean;
  status: string;
  area_id: string | null;
}

export type ChatScope = 'nearby' | 'space' | 'dm';

export interface ChatMessage {
  id: string;
  space_id: string;
  from_participant_id: string;
  from_name: string;
  from_kind: 'human' | 'agent';
  scope: ChatScope;
  to_participant_id?: string;
  text: string;
  created_at: string;
}

export interface JoinResult {
  participant: Participant;
  ticket: string;
  ws_url: string;
  livekit: { url: string; token: string } | null;
  ice_note?: string;
}

export interface AuthUser {
  id: string;
  email: string;
  display_name: string;
  created_at: string;
}

// --- WebSocket messages (CONTRACT §4) ---

export interface ClientInputMsg {
  t: 'input';
  seq: number;
  dx: -1 | 0 | 1;
  dy: -1 | 0 | 1;
}

export interface ClientChatMsg {
  t: 'chat';
  scope: ChatScope;
  to?: string;
  text: string;
}

export interface ClientInteractMsg {
  t: 'interact';
  object_id: string;
}

export interface ClientPingMsg {
  t: 'ping';
  at: number;
}

export type ClientMsg = ClientInputMsg | ClientChatMsg | ClientInteractMsg | ClientPingMsg;

export interface ServerHelloMsg {
  t: 'hello';
  you: string;
  participants: Participant[];
  areas: Area[];
  objects: SpaceObject[];
  map: GameMap;
  now: number;
}

export interface ServerStateMsg {
  t: 'state';
  seq: number;
  participants: ParticipantStateRow[];
}

export interface ServerAudioMsg {
  t: 'audio';
  volumes: Record<string, number>;
}

export interface ServerChatMsg {
  t: 'chat';
  message: ChatMessage;
}

export type SpaceEventKind = 'join' | 'leave' | 'emote' | 'area' | 'kick';

export interface ServerEventMsg {
  t: 'event';
  kind: SpaceEventKind;
  participant_id: string;
  data: Record<string, unknown>;
}

export interface ServerObjectMsg {
  t: 'object';
  object: SpaceObject;
}

export interface ServerErrorMsg {
  t: 'error';
  code: string;
  message: string;
}

export type ServerMsg =
  | ServerHelloMsg
  | ServerStateMsg
  | ServerAudioMsg
  | ServerChatMsg
  | ServerEventMsg
  | ServerObjectMsg
  | ServerErrorMsg;

export interface ApiErrorBody {
  error: { code: string; message: string };
}
