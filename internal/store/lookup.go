package store

import (
	"context"
	"strings"
)

// TrackRow is a track as shown to the user.
type TrackRow struct {
	ID            int64
	Path          string
	Title         string
	Artist        string
	Album         string
	MBIDRecording string
	// ArtistKey groups tracks by artist: the artist MBID when known, else the
	// lowercased artist name, else empty.
	ArtistKey string
}

const trackCols = `t.id, t.path, t.title, t.artist, t.album, t.mbid_recording,
	CASE WHEN t.mbid_artist <> '' THEN t.mbid_artist ELSE lower(t.artist) END`

func scanTrackRow(sc interface{ Scan(...any) error }) (TrackRow, error) {
	var r TrackRow
	err := sc.Scan(&r.ID, &r.Path, &r.Title, &r.Artist, &r.Album, &r.MBIDRecording, &r.ArtistKey)
	return r, err
}

// TracksByIDs returns the rows for the given ids.
func (s *Store) TracksByIDs(ctx context.Context, ids []int64) (map[int64]TrackRow, error) {
	out := make(map[int64]TrackRow, len(ids))
	const chunk = 500 // stay well below SQLite's variable limit
	for start := 0; start < len(ids); start += chunk {
		part := ids[start:min(start+chunk, len(ids))]
		args := make([]any, len(part))
		for i, id := range part {
			args[i] = id
		}
		rows, err := s.db.QueryContext(ctx,
			`SELECT `+trackCols+` FROM tracks t WHERE t.id IN (?`+strings.Repeat(",?", len(part)-1)+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			r, err := scanTrackRow(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			out[r.ID] = r
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

// FindTracks resolves a user query to tracks that have an embedding for
// model: an exact file path, a recording MBID, or words that must all appear
// (case-insensitive, ASCII) in artist, title or album. Results are ordered by
// artist, album, title and capped at limit.
func (s *Store) FindTracks(ctx context.Context, model, query string, limit int) ([]TrackRow, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	base := `SELECT ` + trackCols + ` FROM tracks t JOIN embeddings e ON e.track_id = t.id AND e.model = ?`
	for _, exact := range []string{`t.path = ?`, `t.mbid_recording = ?`} {
		rows, err := s.queryTracks(ctx, base+` WHERE `+exact+` ORDER BY t.id LIMIT ?`, model, query, limit)
		if err != nil || len(rows) > 0 {
			return rows, err
		}
	}
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	where := make([]string, 0, 4)
	args := []any{model}
	for _, w := range strings.Fields(query) {
		where = append(where, `(t.artist || ' ' || t.title || ' ' || t.album) LIKE ? ESCAPE '\'`)
		args = append(args, "%"+esc.Replace(w)+"%")
	}
	args = append(args, limit)
	return s.queryTracks(ctx, base+` WHERE `+strings.Join(where, " AND ")+
		` ORDER BY t.artist, t.album, t.track_no, t.title LIMIT ?`, args...)
}

func (s *Store) queryTracks(ctx context.Context, q string, args ...any) ([]TrackRow, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrackRow
	for rows.Next() {
		r, err := scanTrackRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ArtistKeys maps every track id to its TrackRow.ArtistKey.
func (s *Store) ArtistKeys(ctx context.Context) (map[int64]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,
		CASE WHEN mbid_artist <> '' THEN mbid_artist ELSE lower(artist) END FROM tracks`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]string)
	for rows.Next() {
		var id int64
		var k string
		if err := rows.Scan(&id, &k); err != nil {
			return nil, err
		}
		out[id] = k
	}
	return out, rows.Err()
}

// DedupeKeys maps each track id to a key shared by copies of the same song:
// the recording MBID, else "artist|title" lowercased. Tracks with neither
// get no key.
func (s *Store) DedupeKeys(ctx context.Context) (map[int64]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,
		CASE WHEN mbid_recording <> '' THEN mbid_recording
		     WHEN title <> '' THEN lower(artist) || '|' || lower(title)
		     ELSE '' END FROM tracks`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]string)
	for rows.Next() {
		var id int64
		var k string
		if err := rows.Scan(&id, &k); err != nil {
			return nil, err
		}
		if k != "" {
			out[id] = k
		}
	}
	return out, rows.Err()
}

// TrackIDsByArtist returns the ids of tracks whose artist contains the text
// (case-insensitive).
func (s *Store) TrackIDsByArtist(ctx context.Context, text string) ([]int64, error) {
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM tracks WHERE artist LIKE ? ESCAPE '\'`, "%"+esc.Replace(text)+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
