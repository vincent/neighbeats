package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Taste is what one Jellyfin user's history says about one track.
type Taste struct {
	TrackID    int64
	Plays      int
	Favourite  bool
	LastPlayed int64 // unix seconds, 0 = never
}

// ReplaceTaste makes rows the whole taste snapshot of a user in one
// transaction, so a failed sync leaves the previous one untouched.
func (s *Store) ReplaceTaste(ctx context.Context, user string, rows []Taste) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM taste WHERE user_id = ?`, user); err != nil {
			return err
		}
		stmt, err := tx.PrepareContext(ctx,
			`INSERT INTO taste (user_id, track_id, plays, favourite, last_played, synced_at) VALUES (?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		now := time.Now().Unix()
		for _, r := range rows {
			if _, err := stmt.ExecContext(ctx, user, r.TrackID, r.Plays, r.Favourite, r.LastPlayed, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// LoadTaste returns a user's snapshot by track id.
func (s *Store) LoadTaste(ctx context.Context, user string) (map[int64]Taste, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT track_id, plays, favourite, last_played FROM taste WHERE user_id = ?`, user)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]Taste)
	for rows.Next() {
		var t Taste
		if err := rows.Scan(&t.TrackID, &t.Plays, &t.Favourite, &t.LastPlayed); err != nil {
			return nil, err
		}
		out[t.TrackID] = t
	}
	return out, rows.Err()
}

// PathIndex maps every indexed file path to its track id.
func (s *Store) PathIndex(ctx context.Context) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path, id FROM tracks`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int64)
	for rows.Next() {
		var p string
		var id int64
		if err := rows.Scan(&p, &id); err != nil {
			return nil, err
		}
		out[p] = id
	}
	return out, rows.Err()
}

// RecordingIndex maps a recording MBID to its track id. An MBID shared by
// several tracks (duplicates, compilations) is left out: it would be a guess.
func (s *Store) RecordingIndex(ctx context.Context) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT mbid_recording, MIN(id) FROM tracks
		WHERE mbid_recording <> '' GROUP BY mbid_recording HAVING COUNT(*) = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int64)
	for rows.Next() {
		var m string
		var id int64
		if err := rows.Scan(&m, &id); err != nil {
			return nil, err
		}
		out[m] = id
	}
	return out, rows.Err()
}

// ItemTracks returns the cached Jellyfin item id -> track id matches; 0 means
// the item was looked up and has no indexed track.
func (s *Store) ItemTracks(ctx context.Context) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT item_id, COALESCE(track_id, 0) FROM jellyfin_items`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int64)
	for rows.Next() {
		var k string
		var id int64
		if err := rows.Scan(&k, &id); err != nil {
			return nil, err
		}
		out[k] = id
	}
	return out, rows.Err()
}

// SaveItemTracks stores item matches (track id 0 = no match).
func (s *Store) SaveItemTracks(ctx context.Context, m map[string]int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		for item, id := range m {
			var v any
			if id != 0 {
				v = id
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO jellyfin_items (item_id, track_id) VALUES (?,?)
				 ON CONFLICT(item_id) DO UPDATE SET track_id = excluded.track_id`, item, v); err != nil {
				return err
			}
		}
		return nil
	})
}

// ItemTrack returns the cached track of one Jellyfin item; ok is false when
// the item was never seen or matched no indexed track.
func (s *Store) ItemTrack(ctx context.Context, itemID string) (id int64, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT track_id FROM jellyfin_items
		WHERE item_id = ? AND track_id IS NOT NULL`, itemID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}

// TrackItems returns a Jellyfin item id for each of the tracks that has one.
// When several items are the same file, the smallest id is taken so the
// answer is stable.
func (s *Store) TrackItems(ctx context.Context, ids []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(ids))
	const chunk = 500 // stay well below SQLite's variable limit
	for start := 0; start < len(ids); start += chunk {
		part := ids[start:min(start+chunk, len(ids))]
		args := make([]any, len(part))
		for i, id := range part {
			args[i] = id
		}
		rows, err := s.db.QueryContext(ctx, `SELECT track_id, MIN(item_id) FROM jellyfin_items
			WHERE track_id IN (?`+strings.Repeat(",?", len(part)-1)+`) GROUP BY track_id`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id int64
			var item string
			if err := rows.Scan(&id, &item); err != nil {
				rows.Close()
				return nil, err
			}
			out[id] = item
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// TrackByPath returns the id of the track stored under this exact path.
func (s *Store) TrackByPath(ctx context.Context, path string) (id int64, ok bool, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT id FROM tracks WHERE path = ?`, path).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}

// PlayEvent is one listening event from Playback Reporting.
type PlayEvent struct {
	AtMs    int64
	ItemID  string
	TrackID int64 // 0 = unmatched
	Seconds float64
}

// AddPlayEvents stores events, ignoring ones already present.
func (s *Store) AddPlayEvents(ctx context.Context, user string, ev []PlayEvent) (added int, err error) {
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		for _, e := range ev {
			var tid any
			if e.TrackID != 0 {
				tid = e.TrackID
			}
			res, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO play_events (user_id, at_ms, item_id, track_id, seconds) VALUES (?,?,?,?,?)`,
				user, e.AtMs, e.ItemID, tid, e.Seconds)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				added++
			}
		}
		return nil
	})
	return added, err
}

// LastPlayEventMs is the time of the newest stored event of the user (0 if none).
func (s *Store) LastPlayEventMs(ctx context.Context, user string) (int64, error) {
	var ms sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT MAX(at_ms) FROM play_events WHERE user_id = ?`, user).Scan(&ms)
	return ms.Int64, err
}

// PlayEvents returns the user's matched events at or after sinceMs.
func (s *Store) PlayEvents(ctx context.Context, user string, sinceMs int64) ([]PlayEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT at_ms, item_id, track_id, seconds FROM play_events
		WHERE user_id = ? AND at_ms >= ? AND track_id IS NOT NULL ORDER BY at_ms`, user, sinceMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PlayEvent
	for rows.Next() {
		var e PlayEvent
		if err := rows.Scan(&e.AtMs, &e.ItemID, &e.TrackID, &e.Seconds); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
