// Package store owns all SQL. SQLite via modernc.org/sqlite (pure Go, no CGO),
// WAL mode, schema.sql applied on boot. No other package may import database/sql
// for application data — everything goes through Store.
package store

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("store: not found")

// Store is the database handle. All methods are safe for concurrent use;
// callers needing atomicity across methods should use transactions via DB().
type Store struct {
	db *sql.DB
}

// Session is an opaque login session (internal fields included).
type Session struct {
	TokenHash string
	UserID    string
	ExpiresAt string
	CreatedAt string
}

// AgentKeyFull includes the secret hash for validation (never serialized).
type AgentKeyFull struct {
	AgentKey
	SecretHash string
}

// TicketFull includes validation fields.
type TicketFull struct {
	Ticket
	TicketHash string
}

// Open opens (creating parent dirs) the SQLite database, enables WAL mode,
// and applies schema.sql.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("store: mkdir: %w", err)
		}
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite: single writer; serialized access.
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("store: %s: %w", pragma, err)
		}
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: schema: %w", err)
	}
	return &Store{db: db}, nil
}

// DB exposes the raw handle for transactions (use sparingly).
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
func i2b(i int) bool { return i != 0 }

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic("store: marshal: " + err.Error())
	}
	return string(b)
}

func mapNotFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// ---------- users ----------

func (s *Store) CreateUser(id, email, passwordHash, displayName, createdAt string) (*User, error) {
	u := &User{ID: id, Email: email, DisplayName: displayName, CreatedAt: createdAt}
	_, err := s.db.Exec(`INSERT INTO users(id,email,password_hash,display_name,created_at) VALUES(?,?,?,?,?)`,
		id, email, passwordHash, displayName, createdAt)
	return u, err
}

// GetUserByEmail returns the user plus password hash (for login verification).
func (s *Store) GetUserByEmail(email string) (*User, string, error) {
	u := &User{}
	var hash string
	err := s.db.QueryRow(`SELECT id,email,password_hash,display_name,created_at FROM users WHERE email=?`, email).
		Scan(&u.ID, &u.Email, &hash, &u.DisplayName, &u.CreatedAt)
	if err != nil {
		return nil, "", mapNotFound(err)
	}
	return u, hash, nil
}

func (s *Store) GetUserByID(id string) (*User, error) {
	u := &User{}
	err := s.db.QueryRow(`SELECT id,email,display_name,created_at FROM users WHERE id=?`, id).
		Scan(&u.ID, &u.Email, &u.DisplayName, &u.CreatedAt)
	if err != nil {
		return nil, mapNotFound(err)
	}
	return u, nil
}

// ---------- sessions ----------

func (s *Store) CreateSession(tokenHash, userID, expiresAt, createdAt string) error {
	_, err := s.db.Exec(`INSERT INTO sessions(token_hash,user_id,expires_at,created_at) VALUES(?,?,?,?)`,
		tokenHash, userID, expiresAt, createdAt)
	return err
}

func (s *Store) GetSession(tokenHash string) (*Session, error) {
	se := &Session{}
	err := s.db.QueryRow(`SELECT token_hash,user_id,expires_at,created_at FROM sessions WHERE token_hash=?`, tokenHash).
		Scan(&se.TokenHash, &se.UserID, &se.ExpiresAt, &se.CreatedAt)
	if err != nil {
		return nil, mapNotFound(err)
	}
	return se, nil
}

func (s *Store) DeleteSession(tokenHash string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash=?`, tokenHash)
	return err
}

// ---------- agent keys ----------

func (s *Store) CreateAgentKey(id, userID, name, secretHash string, scopes []string, createdAt string) (*AgentKey, error) {
	k := &AgentKey{ID: id, UserID: userID, Name: name, Scopes: scopes, CreatedAt: createdAt}
	_, err := s.db.Exec(`INSERT INTO agent_keys(id,user_id,name,secret_hash,scopes,created_at) VALUES(?,?,?,?,?,?)`,
		id, userID, name, secretHash, mustJSON(scopes), createdAt)
	return k, err
}

func scanAgentKey(row *sql.Row) (*AgentKeyFull, error) {
	k := &AgentKeyFull{}
	var scopes string
	err := row.Scan(&k.ID, &k.UserID, &k.Name, &k.SecretHash, &scopes, &k.CreatedAt)
	if err != nil {
		return nil, mapNotFound(err)
	}
	if err := json.Unmarshal([]byte(scopes), &k.Scopes); err != nil {
		return nil, err
	}
	return k, nil
}

func (s *Store) GetAgentKeyBySecretHash(hash string) (*AgentKeyFull, error) {
	return scanAgentKey(s.db.QueryRow(`SELECT id,user_id,name,secret_hash,scopes,created_at FROM agent_keys WHERE secret_hash=?`, hash))
}

func (s *Store) GetAgentKey(id, userID string) (*AgentKey, error) {
	k := &AgentKey{}
	var scopes string
	err := s.db.QueryRow(`SELECT id,name,scopes,created_at FROM agent_keys WHERE id=? AND user_id=?`, id, userID).
		Scan(&k.ID, &k.Name, &scopes, &k.CreatedAt)
	if err != nil {
		return nil, mapNotFound(err)
	}
	if err := json.Unmarshal([]byte(scopes), &k.Scopes); err != nil {
		return nil, err
	}
	return k, nil
}

func (s *Store) ListAgentKeys(userID string) ([]*AgentKey, error) {
	rows, err := s.db.Query(`SELECT id,name,scopes,created_at FROM agent_keys WHERE user_id=? ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AgentKey
	for rows.Next() {
		k := &AgentKey{}
		var scopes string
		if err := rows.Scan(&k.ID, &k.Name, &scopes, &k.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(scopes), &k.Scopes); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) DeleteAgentKey(id, userID string) error {
	res, err := s.db.Exec(`DELETE FROM agent_keys WHERE id=? AND user_id=?`, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- spaces ----------

func scanSpace(row *sql.Row) (*Space, error) {
	sp := &Space{}
	var settings string
	err := row.Scan(&sp.ID, &sp.Name, &sp.Slug, &sp.OwnerUserID, &settings, &sp.CreatedAt)
	if err != nil {
		return nil, mapNotFound(err)
	}
	if err := json.Unmarshal([]byte(settings), &sp.Settings); err != nil {
		return nil, err
	}
	return sp, nil
}

func (s *Store) CreateSpace(sp *Space) error {
	_, err := s.db.Exec(`INSERT INTO spaces(id,name,slug,owner_user_id,settings,created_at) VALUES(?,?,?,?,?,?)`,
		sp.ID, sp.Name, sp.Slug, sp.OwnerUserID, mustJSON(sp.Settings), sp.CreatedAt)
	return err
}

func (s *Store) GetSpace(id string) (*Space, error) {
	return scanSpace(s.db.QueryRow(`SELECT id,name,slug,owner_user_id,settings,created_at FROM spaces WHERE id=?`, id))
}

func (s *Store) GetSpaceBySlug(slug string) (*Space, error) {
	return scanSpace(s.db.QueryRow(`SELECT id,name,slug,owner_user_id,settings,created_at FROM spaces WHERE slug=?`, slug))
}

func (s *Store) ListSpaces() ([]*Space, error) {
	rows, err := s.db.Query(`SELECT id,name,slug,owner_user_id,settings,created_at FROM spaces ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Space
	for rows.Next() {
		sp := &Space{}
		var settings string
		if err := rows.Scan(&sp.ID, &sp.Name, &sp.Slug, &sp.OwnerUserID, &settings, &sp.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(settings), &sp.Settings); err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

func (s *Store) UpdateSpace(sp *Space) error {
	_, err := s.db.Exec(`UPDATE spaces SET name=?, settings=? WHERE id=?`,
		sp.Name, mustJSON(sp.Settings), sp.ID)
	return err
}

func (s *Store) DeleteSpace(id string) error {
	_, err := s.db.Exec(`DELETE FROM spaces WHERE id=?`, id)
	return err
}

// ---------- maps ----------

func scanMap(row *sql.Row, withTiles bool) (*Map, error) {
	m := &Map{}
	var tiles string
	var err error
	if withTiles {
		err = row.Scan(&m.ID, &m.SpaceID, &m.Name, &m.Width, &m.Height, &tiles, &m.Spawn.X, &m.Spawn.Y, &m.CreatedAt)
	} else {
		err = row.Scan(&m.ID, &m.SpaceID, &m.Name, &m.Width, &m.Height, &m.Spawn.X, &m.Spawn.Y, &m.CreatedAt)
	}
	if err != nil {
		return nil, mapNotFound(err)
	}
	if withTiles {
		if err := json.Unmarshal([]byte(tiles), &m.Tiles); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (s *Store) CreateMap(m *Map) error {
	_, err := s.db.Exec(`INSERT INTO maps(id,space_id,name,width,height,tiles,spawn_x,spawn_y,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		m.ID, m.SpaceID, m.Name, m.Width, m.Height, mustJSON(m.Tiles), m.Spawn.X, m.Spawn.Y, m.CreatedAt)
	return err
}

func (s *Store) GetMap(id string) (*Map, error) {
	return scanMap(s.db.QueryRow(`SELECT id,space_id,name,width,height,tiles,spawn_x,spawn_y,created_at FROM maps WHERE id=?`, id), true)
}

func (s *Store) ListMapsBySpace(spaceID string) ([]*Map, error) {
	rows, err := s.db.Query(`SELECT id,space_id,name,width,height,spawn_x,spawn_y,created_at FROM maps WHERE space_id=? ORDER BY created_at`, spaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Map
	for rows.Next() {
		m, err := scanMapRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func scanMapRow(rows *sql.Rows) (*Map, error) {
	m := &Map{}
	err := rows.Scan(&m.ID, &m.SpaceID, &m.Name, &m.Width, &m.Height, &m.Spawn.X, &m.Spawn.Y, &m.CreatedAt)
	return m, err
}

func (s *Store) UpdateMap(m *Map) error {
	_, err := s.db.Exec(`UPDATE maps SET name=?, width=?, height=?, tiles=?, spawn_x=?, spawn_y=? WHERE id=?`,
		m.Name, m.Width, m.Height, mustJSON(m.Tiles), m.Spawn.X, m.Spawn.Y, m.ID)
	return err
}

// ---------- areas ----------

func scanArea(row *sql.Row) (*Area, error) {
	a := &Area{}
	err := row.Scan(&a.ID, &a.MapID, &a.Kind, &a.Name, &a.X, &a.Y, &a.W, &a.H, &a.CreatedAt)
	if err != nil {
		return nil, mapNotFound(err)
	}
	return a, nil
}

func (s *Store) CreateArea(a *Area) error {
	_, err := s.db.Exec(`INSERT INTO areas(id,map_id,kind,name,x,y,w,h,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		a.ID, a.MapID, a.Kind, a.Name, a.X, a.Y, a.W, a.H, a.CreatedAt)
	return err
}

func (s *Store) GetArea(id string) (*Area, error) {
	return scanArea(s.db.QueryRow(`SELECT id,map_id,kind,name,x,y,w,h,created_at FROM areas WHERE id=?`, id))
}

func (s *Store) ListAreasByMap(mapID string) ([]*Area, error) {
	rows, err := s.db.Query(`SELECT id,map_id,kind,name,x,y,w,h,created_at FROM areas WHERE map_id=? ORDER BY created_at`, mapID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Area
	for rows.Next() {
		a := &Area{}
		if err := rows.Scan(&a.ID, &a.MapID, &a.Kind, &a.Name, &a.X, &a.Y, &a.W, &a.H, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) DeleteArea(id string) error {
	res, err := s.db.Exec(`DELETE FROM areas WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- objects ----------

func scanObject(row *sql.Row) (*SpaceObject, error) {
	o := &SpaceObject{}
	var solid int
	err := row.Scan(&o.ID, &o.MapID, &o.Kind, &o.X, &o.Y, &o.W, &o.H, &o.Title, &o.Body,
		&o.URL, &o.TargetMap, &o.TargetX, &o.TargetY, &solid, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return nil, mapNotFound(err)
	}
	o.Solid = i2b(solid)
	return o, nil
}

const objectCols = `id,map_id,kind,x,y,w,h,title,body,url,target_map_id,target_x,target_y,solid,created_at,updated_at`

func (s *Store) CreateObject(o *SpaceObject) error {
	_, err := s.db.Exec(`INSERT INTO objects(`+objectCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		o.ID, o.MapID, o.Kind, o.X, o.Y, o.W, o.H, o.Title, o.Body, o.URL,
		o.TargetMap, o.TargetX, o.TargetY, b2i(o.Solid), o.CreatedAt, o.UpdatedAt)
	return err
}

func (s *Store) GetObject(id string) (*SpaceObject, error) {
	return scanObject(s.db.QueryRow(`SELECT `+objectCols+` FROM objects WHERE id=?`, id))
}

func (s *Store) ListObjectsByMap(mapID string) ([]*SpaceObject, error) {
	rows, err := s.db.Query(`SELECT `+objectCols+` FROM objects WHERE map_id=? ORDER BY created_at`, mapID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*SpaceObject
	for rows.Next() {
		o := &SpaceObject{}
		var solid int
		if err := rows.Scan(&o.ID, &o.MapID, &o.Kind, &o.X, &o.Y, &o.W, &o.H, &o.Title, &o.Body,
			&o.URL, &o.TargetMap, &o.TargetX, &o.TargetY, &solid, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		o.Solid = i2b(solid)
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) UpdateObject(o *SpaceObject) error {
	_, err := s.db.Exec(`UPDATE objects SET kind=?,x=?,y=?,w=?,h=?,title=?,body=?,url=?,target_map_id=?,target_x=?,target_y=?,solid=?,updated_at=? WHERE id=?`,
		o.Kind, o.X, o.Y, o.W, o.H, o.Title, o.Body, o.URL, o.TargetMap, o.TargetX, o.TargetY, b2i(o.Solid), o.UpdatedAt, o.ID)
	return err
}

func (s *Store) DeleteObject(id string) error {
	res, err := s.db.Exec(`DELETE FROM objects WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- participants ----------

const participantCols = `id,space_id,map_id,kind,display_name,avatar,x,y,dir,emote,ghost,muted,camera_off,speaking,status,area_id,owner_user_id,owner_agent_key_id,created_at,updated_at`

func scanParticipant(row *sql.Row) (*Participant, error) {
	p := &Participant{}
	var avatar string
	var ghost, muted, cameraOff, speaking int
	err := row.Scan(&p.ID, &p.SpaceID, &p.MapID, &p.Kind, &p.DisplayName, &avatar,
		&p.X, &p.Y, &p.Dir, &p.Emote, &ghost, &muted, &cameraOff, &speaking,
		&p.Status, &p.AreaID, &p.OwnerUserID, &p.OwnerAgentKeyID, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, mapNotFound(err)
	}
	if err := json.Unmarshal([]byte(avatar), &p.Avatar); err != nil {
		return nil, err
	}
	p.Ghost, p.Muted, p.CameraOff, p.Speaking = i2b(ghost), i2b(muted), i2b(cameraOff), i2b(speaking)
	return p, nil
}

func (s *Store) CreateParticipant(p *Participant) error {
	_, err := s.db.Exec(`INSERT INTO participants(`+participantCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ID, p.SpaceID, p.MapID, p.Kind, p.DisplayName, mustJSON(p.Avatar),
		p.X, p.Y, p.Dir, p.Emote, b2i(p.Ghost), b2i(p.Muted), b2i(p.CameraOff), b2i(p.Speaking),
		p.Status, p.AreaID, p.OwnerUserID, p.OwnerAgentKeyID, p.CreatedAt, p.UpdatedAt)
	return err
}

func (s *Store) GetParticipant(id string) (*Participant, error) {
	return scanParticipant(s.db.QueryRow(`SELECT `+participantCols+` FROM participants WHERE id=?`, id))
}

func (s *Store) ListParticipantsBySpace(spaceID string) ([]*Participant, error) {
	rows, err := s.db.Query(`SELECT `+participantCols+` FROM participants WHERE space_id=? ORDER BY created_at`, spaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Participant
	for rows.Next() {
		p := &Participant{}
		var avatar string
		var ghost, muted, cameraOff, speaking int
		if err := rows.Scan(&p.ID, &p.SpaceID, &p.MapID, &p.Kind, &p.DisplayName, &avatar,
			&p.X, &p.Y, &p.Dir, &p.Emote, &ghost, &muted, &cameraOff, &speaking,
			&p.Status, &p.AreaID, &p.OwnerUserID, &p.OwnerAgentKeyID, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(avatar), &p.Avatar); err != nil {
			return nil, err
		}
		p.Ghost, p.Muted, p.CameraOff, p.Speaking = i2b(ghost), i2b(muted), i2b(cameraOff), i2b(speaking)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListParticipantsByOwner returns all participants held by a login.
func (s *Store) ListParticipantsByOwner(userID, agentKeyID string) ([]*Participant, error) {
	rows, err := s.db.Query(`SELECT `+participantCols+` FROM participants WHERE owner_user_id=? AND owner_agent_key_id=? ORDER BY updated_at DESC`, userID, agentKeyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Participant
	for rows.Next() {
		p := &Participant{}
		var avatar string
		var ghost, muted, cameraOff, speaking int
		if err := rows.Scan(&p.ID, &p.SpaceID, &p.MapID, &p.Kind, &p.DisplayName, &avatar,
			&p.X, &p.Y, &p.Dir, &p.Emote, &ghost, &muted, &cameraOff, &speaking,
			&p.Status, &p.AreaID, &p.OwnerUserID, &p.OwnerAgentKeyID, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(avatar), &p.Avatar); err != nil {
			return nil, err
		}
		p.Ghost, p.Muted, p.CameraOff, p.Speaking = i2b(ghost), i2b(muted), i2b(cameraOff), i2b(speaking)
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetParticipantByOwner finds the single participant a login holds in a space.
func (s *Store) GetParticipantByOwner(spaceID, userID, agentKeyID string) (*Participant, error) {
	return scanParticipant(s.db.QueryRow(`SELECT `+participantCols+` FROM participants WHERE space_id=? AND owner_user_id=? AND owner_agent_key_id=?`,
		spaceID, userID, agentKeyID))
}

func (s *Store) UpdateParticipant(p *Participant) error {
	_, err := s.db.Exec(`UPDATE participants SET map_id=?,display_name=?,avatar=?,x=?,y=?,dir=?,emote=?,ghost=?,muted=?,camera_off=?,speaking=?,status=?,area_id=?,updated_at=? WHERE id=?`,
		p.MapID, p.DisplayName, mustJSON(p.Avatar), p.X, p.Y, p.Dir, p.Emote,
		b2i(p.Ghost), b2i(p.Muted), b2i(p.CameraOff), b2i(p.Speaking), p.Status, p.AreaID, p.UpdatedAt, p.ID)
	return err
}

// UpdateParticipantPosition is the hot-path tick write (position + area only).
func (s *Store) UpdateParticipantPosition(id string, x, y float64, dir, areaID, updatedAt string) error {
	_, err := s.db.Exec(`UPDATE participants SET x=?,y=?,dir=?,area_id=?,updated_at=? WHERE id=?`,
		x, y, dir, areaID, updatedAt, id)
	return err
}

func (s *Store) DeleteParticipant(id string) error {
	_, err := s.db.Exec(`DELETE FROM participants WHERE id=?`, id)
	return err
}

// ---------- tickets ----------

func (s *Store) CreateTicket(id, participantID, ticketHash, expiresAt, createdAt string) error {
	_, err := s.db.Exec(`INSERT INTO tickets(id,participant_id,ticket_hash,expires_at,used,created_at) VALUES(?,?,?,?,0,?)`,
		id, participantID, ticketHash, expiresAt, createdAt)
	return err
}

func (s *Store) GetTicketByHash(hash string) (*TicketFull, error) {
	t := &TicketFull{}
	var used int
	err := s.db.QueryRow(`SELECT id,participant_id,ticket_hash,expires_at,used,created_at FROM tickets WHERE ticket_hash=?`, hash).
		Scan(&t.ID, &t.ParticipantID, &t.TicketHash, &t.ExpiresAt, &used, &t.CreatedAt)
	if err != nil {
		return nil, mapNotFound(err)
	}
	t.Used = i2b(used)
	return t, nil
}

func (s *Store) MarkTicketUsed(id string) error {
	_, err := s.db.Exec(`UPDATE tickets SET used=1 WHERE id=?`, id)
	return err
}

// ClaimTicket atomically marks a ticket used; returns false if already used.
func (s *Store) ClaimTicket(id string) (bool, error) {
	res, err := s.db.Exec(`UPDATE tickets SET used=1 WHERE id=? AND used=0`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (s *Store) DeleteTicketsByParticipant(participantID string) error {
	_, err := s.db.Exec(`DELETE FROM tickets WHERE participant_id=?`, participantID)
	return err
}

// ---------- chat ----------

const chatCols = `id,space_id,from_participant_id,from_name,from_kind,scope,to_participant_id,text,announce,recipients,created_at`

func (s *Store) CreateChatMessage(m *ChatMessage) error {
	_, err := s.db.Exec(`INSERT INTO chat_messages(`+chatCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.SpaceID, m.FromParticipantID, m.FromName, m.FromKind, m.Scope,
		m.ToParticipantID, m.Text, b2i(m.Announce), mustJSON(m.Recipients), m.CreatedAt)
	return err
}

func scanChatMessage(rows *sql.Rows) (*ChatMessage, error) {
	m := &ChatMessage{}
	var announce int
	var recipients string
	err := rows.Scan(&m.ID, &m.SpaceID, &m.FromParticipantID, &m.FromName, &m.FromKind,
		&m.Scope, &m.ToParticipantID, &m.Text, &announce, &recipients, &m.CreatedAt)
	if err != nil {
		return nil, err
	}
	m.Announce = i2b(announce)
	if recipients != "" && recipients != "null" {
		_ = json.Unmarshal([]byte(recipients), &m.Recipients)
	}
	return m, nil
}

// ListChatMessages returns the most recent messages, oldest-first.
func (s *Store) ListChatMessages(spaceID string, limit int) ([]*ChatMessage, error) {
	rows, err := s.db.Query(`SELECT `+chatCols+` FROM chat_messages WHERE space_id=? ORDER BY created_at DESC, rowid DESC LIMIT ?`, spaceID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ChatMessage
	for rows.Next() {
		m, err := scanChatMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// reverse to oldest-first
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}
