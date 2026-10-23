package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
)

// Whiteboard v1 (WHITEBOARD_V1.md): stroke validation and persistence.
// The FK in schema.sql points at objects(id) ON DELETE CASCADE.

// hexColor matches #rrggbb.
var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// ValidateStroke checks a stroke's drawable fields per the whiteboard v1
// contract: color #rrggbb, width 1..20, 2..500 points, each 0..1000.
// Used by both the REST handlers and the WS path.
func ValidateStroke(color string, width float64, points []WhiteboardPoint) error {
	if !hexColor.MatchString(color) {
		return fmt.Errorf("color must be #rrggbb hex")
	}
	if width < 1 || width > 20 {
		return fmt.Errorf("width must be 1..20")
	}
	if len(points) < 2 || len(points) > 500 {
		return fmt.Errorf("points must be 2..500")
	}
	for i, pt := range points {
		if pt.X < 0 || pt.X > 1000 || pt.Y < 0 || pt.Y > 1000 {
			return fmt.Errorf("point %d out of range 0..1000", i)
		}
	}
	return nil
}

// CreateStroke persists a validated stroke.
func (s *Store) CreateStroke(st *WhiteboardStroke) error {
	_, err := s.db.Exec(`INSERT INTO whiteboard_strokes(id,object_id,participant_id,color,width,points,created_at) VALUES(?,?,?,?,?,?,?)`,
		st.ID, st.ObjectID, st.ParticipantID, st.Color, st.Width, mustJSON(st.Points), st.CreatedAt)
	return err
}

func scanStroke(row *sql.Row) (*WhiteboardStroke, error) {
	st := &WhiteboardStroke{}
	var points string
	err := row.Scan(&st.ID, &st.ObjectID, &st.ParticipantID, &st.Color, &st.Width, &points, &st.CreatedAt)
	if err != nil {
		return nil, mapNotFound(err)
	}
	if err := json.Unmarshal([]byte(points), &st.Points); err != nil {
		return nil, err
	}
	return st, nil
}

// ListStrokes returns all strokes on an object, oldest first.
// Returns an empty (non-nil) slice when there are none.
func (s *Store) ListStrokes(objectID string) ([]*WhiteboardStroke, error) {
	rows, err := s.db.Query(`SELECT id,object_id,participant_id,color,width,points,created_at FROM whiteboard_strokes WHERE object_id=? ORDER BY created_at, rowid`, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*WhiteboardStroke{}
	for rows.Next() {
		st := &WhiteboardStroke{}
		var points string
		if err := rows.Scan(&st.ID, &st.ObjectID, &st.ParticipantID, &st.Color, &st.Width, &points, &st.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(points), &st.Points); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// ClearStrokes deletes every stroke on an object.
func (s *Store) ClearStrokes(objectID string) error {
	_, err := s.db.Exec(`DELETE FROM whiteboard_strokes WHERE object_id=?`, objectID)
	return err
}
