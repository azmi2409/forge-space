-- forge-space SQLite schema. Applied on boot (CREATE TABLE IF NOT EXISTS).
-- Timestamps are RFC3339 UTC TEXT. IDs are prefixed base36 (see ids.go).

CREATE TABLE IF NOT EXISTS users (
  id            TEXT PRIMARY KEY,
  email         TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  display_name  TEXT NOT NULL,
  created_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
  token_hash TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);

CREATE TABLE IF NOT EXISTS agent_keys (
  id          TEXT PRIMARY KEY,
  user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name        TEXT NOT NULL,
  secret_hash TEXT NOT NULL UNIQUE,
  scopes      TEXT NOT NULL,
  created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_agent_keys_user ON agent_keys(user_id);

CREATE TABLE IF NOT EXISTS spaces (
  id            TEXT PRIMARY KEY,
  name          TEXT NOT NULL,
  slug          TEXT NOT NULL UNIQUE,
  owner_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  settings      TEXT NOT NULL,
  created_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS maps (
  id         TEXT PRIMARY KEY,
  space_id   TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  width      INTEGER NOT NULL,
  height     INTEGER NOT NULL,
  tiles      TEXT NOT NULL,
  spawn_x    REAL NOT NULL,
  spawn_y    REAL NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_maps_space ON maps(space_id);

CREATE TABLE IF NOT EXISTS areas (
  id         TEXT PRIMARY KEY,
  map_id     TEXT NOT NULL REFERENCES maps(id) ON DELETE CASCADE,
  kind       TEXT NOT NULL,
  name       TEXT NOT NULL,
  x          INTEGER NOT NULL,
  y          INTEGER NOT NULL,
  w          INTEGER NOT NULL,
  h          INTEGER NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_areas_map ON areas(map_id);

CREATE TABLE IF NOT EXISTS objects (
  id            TEXT PRIMARY KEY,
  map_id        TEXT NOT NULL REFERENCES maps(id) ON DELETE CASCADE,
  kind          TEXT NOT NULL,
  x             REAL NOT NULL,
  y             REAL NOT NULL,
  w             REAL NOT NULL,
  h             REAL NOT NULL,
  title         TEXT NOT NULL DEFAULT '',
  body          TEXT NOT NULL DEFAULT '',
  url           TEXT NOT NULL DEFAULT '',
  target_map_id TEXT NOT NULL DEFAULT '',
  target_x      REAL NOT NULL DEFAULT 0,
  target_y      REAL NOT NULL DEFAULT 0,
  solid         INTEGER NOT NULL DEFAULT 0,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_objects_map ON objects(map_id);

CREATE TABLE IF NOT EXISTS participants (
  id               TEXT PRIMARY KEY,
  space_id         TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  map_id           TEXT NOT NULL REFERENCES maps(id) ON DELETE CASCADE,
  kind             TEXT NOT NULL,
  display_name     TEXT NOT NULL,
  avatar           TEXT NOT NULL,
  x                REAL NOT NULL,
  y                REAL NOT NULL,
  dir              TEXT NOT NULL DEFAULT 'down',
  emote            TEXT NOT NULL DEFAULT '',
  ghost            INTEGER NOT NULL DEFAULT 0,
  muted            INTEGER NOT NULL DEFAULT 0,
  camera_off       INTEGER NOT NULL DEFAULT 0,
  speaking         INTEGER NOT NULL DEFAULT 0,
  status           TEXT NOT NULL DEFAULT '',
  area_id          TEXT NOT NULL DEFAULT '',
  owner_user_id    TEXT NOT NULL DEFAULT '',
  owner_agent_key_id TEXT NOT NULL DEFAULT '',
  created_at       TEXT NOT NULL,
  updated_at       TEXT NOT NULL,
  UNIQUE(space_id, owner_user_id, owner_agent_key_id)
);
CREATE INDEX IF NOT EXISTS idx_participants_space ON participants(space_id);
CREATE INDEX IF NOT EXISTS idx_participants_owner ON participants(space_id, owner_user_id, owner_agent_key_id);

CREATE TABLE IF NOT EXISTS tickets (
  id             TEXT PRIMARY KEY,
  participant_id TEXT NOT NULL REFERENCES participants(id) ON DELETE CASCADE,
  ticket_hash    TEXT NOT NULL UNIQUE,
  expires_at     TEXT NOT NULL,
  used           INTEGER NOT NULL DEFAULT 0,
  created_at     TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tickets_participant ON tickets(participant_id);

CREATE TABLE IF NOT EXISTS chat_messages (
  id                  TEXT PRIMARY KEY,
  space_id            TEXT NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
  from_participant_id TEXT NOT NULL,
  from_name           TEXT NOT NULL,
  from_kind           TEXT NOT NULL,
  scope               TEXT NOT NULL,
  to_participant_id   TEXT NOT NULL DEFAULT '',
  text                TEXT NOT NULL,
  announce            INTEGER NOT NULL DEFAULT 0,
  recipients          TEXT NOT NULL DEFAULT '',
  created_at          TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_chat_space ON chat_messages(space_id, created_at);

-- Whiteboard v1 (WHITEBOARD_V1.md): strokes live on kind=whiteboard objects.
CREATE TABLE IF NOT EXISTS whiteboard_strokes (
  id             TEXT PRIMARY KEY,
  object_id      TEXT NOT NULL REFERENCES objects(id) ON DELETE CASCADE,
  participant_id TEXT NOT NULL,
  color          TEXT NOT NULL,
  width          REAL NOT NULL,
  points         TEXT NOT NULL,
  created_at     TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_wb_strokes_object ON whiteboard_strokes(object_id);
