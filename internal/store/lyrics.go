package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// TrackRef is the minimum needed to locate a track's sidecar files.
type TrackRef struct {
	ID   int64
	Path string
}

// Lyrics is the lyric text stored for one track.
type Lyrics struct {
	TrackID   int64
	Path      string // the .lrc file
	Size      int64
	MtimeNs   int64
	Text      string
	Synced    bool
	LineCount int
}

// LyricsFingerprint identifies the version of an .lrc file that was ingested.
type LyricsFingerprint struct {
	Path    string
	Size    int64
	MtimeNs int64
}

// LyricsHit is one full-text search result.
type LyricsHit struct {
	TrackID int64
	Path    string
	Artist  string
	Title   string
	Snippet string
}

// TrackRefs lists every indexed track.
func (s *Store) TrackRefs(ctx context.Context) ([]TrackRef, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, path FROM tracks ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrackRef
	for rows.Next() {
		var r TrackRef
		if err := rows.Scan(&r.ID, &r.Path); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LyricsFingerprints returns what is currently ingested, by track id.
func (s *Store) LyricsFingerprints(ctx context.Context) (map[int64]LyricsFingerprint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT track_id, path, size, mtime_ns FROM lyrics`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]LyricsFingerprint)
	for rows.Next() {
		var id int64
		var f LyricsFingerprint
		if err := rows.Scan(&id, &f.Path, &f.Size, &f.MtimeNs); err != nil {
			return nil, err
		}
		out[id] = f
	}
	return out, rows.Err()
}

// UpsertLyrics inserts or replaces lyrics in one transaction.
func (s *Store) UpsertLyrics(ctx context.Context, ls []Lyrics) error {
	if len(ls) == 0 {
		return nil
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `
INSERT INTO lyrics (track_id, path, size, mtime_ns, text, synced, line_count, updated_at)
VALUES (?,?,?,?,?,?,?,?)
ON CONFLICT(track_id) DO UPDATE SET path=excluded.path, size=excluded.size,
	mtime_ns=excluded.mtime_ns, text=excluded.text, synced=excluded.synced,
	line_count=excluded.line_count, updated_at=excluded.updated_at`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		now := time.Now().Unix()
		for _, l := range ls {
			if _, err := stmt.ExecContext(ctx, l.TrackID, l.Path, l.Size, l.MtimeNs, l.Text, l.Synced, l.LineCount, now); err != nil {
				return err
			}
			// A new text needs a new language guess (embeddings notice by fingerprint).
			if _, err := tx.ExecContext(ctx, `DELETE FROM lyric_lang WHERE track_id = ?`, l.TrackID); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteLyrics removes the lyrics of the given tracks.
func (s *Store) DeleteLyrics(ctx context.Context, trackIDs []int64) error {
	if len(trackIDs) == 0 {
		return nil
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		for _, id := range trackIDs {
			for _, q := range []string{
				`DELETE FROM lyrics WHERE track_id = ?`,
				`DELETE FROM lyric_lang WHERE track_id = ?`,
				`DELETE FROM lyric_embeddings WHERE track_id = ?`,
			} {
				if _, err := tx.ExecContext(ctx, q, id); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// SearchLyrics finds tracks whose lyrics contain all the given words (or the
// exact phrase). User input is quoted so FTS syntax characters are harmless.
func (s *Store) SearchLyrics(ctx context.Context, query string, phrase bool, limit int) ([]LyricsHit, error) {
	q := ftsQuery(query, phrase)
	if q == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT t.id, t.path, t.artist, t.title, snippet(lyrics_fts, 0, '[', ']', '…', 10)
FROM lyrics_fts JOIN tracks t ON t.id = lyrics_fts.rowid
WHERE lyrics_fts MATCH ? ORDER BY rank LIMIT ?`, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LyricsHit
	for rows.Next() {
		var h LyricsHit
		if err := rows.Scan(&h.TrackID, &h.Path, &h.Artist, &h.Title, &h.Snippet); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func ftsQuery(query string, phrase bool) string {
	words := strings.Fields(query)
	for i, w := range words {
		words[i] = `"` + strings.ReplaceAll(w, `"`, `""`) + `"`
	}
	if phrase && len(words) > 1 {
		return `"` + strings.ReplaceAll(strings.Join(strings.Fields(query), " "), `"`, `""`) + `"`
	}
	return strings.Join(words, " ")
}

// LyricsStats counts tracks with lyrics.
type LyricsStats struct {
	WithLyrics int
	Synced     int
}

// LyricsStats summarises lyric coverage.
func (s *Store) LyricsStats(ctx context.Context) (LyricsStats, error) {
	var l LyricsStats
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(synced), 0) FROM lyrics`).Scan(&l.WithLyrics, &l.Synced)
	return l, err
}
