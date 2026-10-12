package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// LyricText is the lyric text of one track.
type LyricText struct {
	TrackID int64
	Text    string
}

// LyricLang is the detected language of one track's lyrics.
type LyricLang struct {
	TrackID    int64
	Lang       string // ISO 639-3, or "und" when unsure
	Confidence float64
}

// LangCount is how many tracks have lyrics in a language.
type LangCount struct {
	Lang  string
	Count int
}

// LyricsWithoutLang returns the lyrics that have no language yet.
func (s *Store) LyricsWithoutLang(ctx context.Context) ([]LyricText, error) {
	return s.lyricTexts(ctx, `SELECT l.track_id, l.text FROM lyrics l
		LEFT JOIN lyric_lang g ON g.track_id = l.track_id WHERE g.track_id IS NULL ORDER BY l.track_id`)
}

// LyricTexts returns the lyric text of the given tracks (those without lyrics are absent).
func (s *Store) LyricTexts(ctx context.Context, ids []int64) (map[int64]string, error) {
	out := make(map[int64]string, len(ids))
	for _, id := range ids {
		var text string
		err := s.db.QueryRowContext(ctx, `SELECT text FROM lyrics WHERE track_id = ?`, id).Scan(&text)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[id] = text
	}
	return out, nil
}

// EachLyric calls fn with every stored lyric, in track id order.
func (s *Store) EachLyric(ctx context.Context, fn func(id int64, text string)) error {
	rows, err := s.db.QueryContext(ctx, `SELECT track_id, text FROM lyrics ORDER BY track_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var text string
		if err := rows.Scan(&id, &text); err != nil {
			return err
		}
		fn(id, text)
	}
	return rows.Err()
}

func (s *Store) lyricTexts(ctx context.Context, q string, args ...any) ([]LyricText, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LyricText
	for rows.Next() {
		var l LyricText
		if err := rows.Scan(&l.TrackID, &l.Text); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// SetLyricLangs stores language guesses.
func (s *Store) SetLyricLangs(ctx context.Context, ls []LyricLang) error {
	if len(ls) == 0 {
		return nil
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO lyric_lang (track_id, lang, confidence) VALUES (?,?,?)
			ON CONFLICT(track_id) DO UPDATE SET lang=excluded.lang, confidence=excluded.confidence`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, l := range ls {
			if _, err := stmt.ExecContext(ctx, l.TrackID, l.Lang, l.Confidence); err != nil {
				return err
			}
		}
		return nil
	})
}

// LyricLangs maps tracks to their detected language (including "und").
func (s *Store) LyricLangs(ctx context.Context) (map[int64]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT track_id, lang FROM lyric_lang`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[int64]string)
	for rows.Next() {
		var id int64
		var l string
		if err := rows.Scan(&id, &l); err != nil {
			return nil, err
		}
		out[id] = l
	}
	return out, rows.Err()
}

// LangCounts lists languages by number of tracks, most common first.
func (s *Store) LangCounts(ctx context.Context) ([]LangCount, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT lang, COUNT(*) FROM lyric_lang GROUP BY lang ORDER BY 2 DESC, 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LangCount
	for rows.Next() {
		var c LangCount
		if err := rows.Scan(&c.Lang, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// LangSample is a track picked to check a language guess by eye.
type LangSample struct {
	TrackID    int64
	Artist     string
	Title      string
	Confidence float64
	Excerpt    string
}

// LangSamples returns up to n tracks of a language, spread evenly over its
// track ids, with the start of the lyrics.
func (s *Store) LangSamples(ctx context.Context, lang string, n int) ([]LangSample, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT t.id, t.artist, t.title, g.confidence, substr(l.text, 1, 160)
FROM lyric_lang g JOIN lyrics l ON l.track_id = g.track_id JOIN tracks t ON t.id = g.track_id
WHERE g.lang = ?1 AND (g.track_id * 2654435761) % (SELECT MAX(1, COUNT(*) / ?2) FROM lyric_lang WHERE lang = ?1) = 0
ORDER BY t.id LIMIT ?2`, lang, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LangSample
	for rows.Next() {
		var x LangSample
		if err := rows.Scan(&x.TrackID, &x.Artist, &x.Title, &x.Confidence, &x.Excerpt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// PendingLyric is a lyric that still needs an embedding for a model.
type PendingLyric struct {
	TrackID int64
	Text    string
	Size    int64
	MtimeNs int64
}

// PendingLyricEmbeddings lists lyrics without an up-to-date embedding for model.
func (s *Store) PendingLyricEmbeddings(ctx context.Context, model string) ([]PendingLyric, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT l.track_id, l.text, l.size, l.mtime_ns FROM lyrics l
LEFT JOIN lyric_embeddings e ON e.track_id = l.track_id AND e.model = ?
WHERE e.track_id IS NULL OR e.size <> l.size OR e.mtime_ns <> l.mtime_ns ORDER BY l.track_id`, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingLyric
	for rows.Next() {
		var p PendingLyric
		if err := rows.Scan(&p.TrackID, &p.Text, &p.Size, &p.MtimeNs); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// LyricEmbedding is the stored text vector of one track's lyrics.
type LyricEmbedding struct {
	TrackID int64
	Vector  []float32
	Tokens  int
	Size    int64
	MtimeNs int64
}

// SaveLyricEmbeddings stores vectors for a model in one transaction.
func (s *Store) SaveLyricEmbeddings(ctx context.Context, model string, es []LyricEmbedding) error {
	if len(es) == 0 {
		return nil
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `
INSERT INTO lyric_embeddings (track_id, model, vector, dim, tokens, size, mtime_ns, updated_at) VALUES (?,?,?,?,?,?,?,?)
ON CONFLICT(track_id, model) DO UPDATE SET vector=excluded.vector, dim=excluded.dim, tokens=excluded.tokens,
	size=excluded.size, mtime_ns=excluded.mtime_ns, updated_at=excluded.updated_at`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		now := time.Now().Unix()
		for _, e := range es {
			if _, err := stmt.ExecContext(ctx, e.TrackID, model, EncodeVector(e.Vector), len(e.Vector), e.Tokens, e.Size, e.MtimeNs, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// LyricEmbeddings returns every stored lyric vector for a model.
func (s *Store) LyricEmbeddings(ctx context.Context, model string) ([]LyricEmbedding, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT track_id, vector, tokens, size, mtime_ns
		FROM lyric_embeddings WHERE model = ? ORDER BY track_id`, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LyricEmbedding
	for rows.Next() {
		var e LyricEmbedding
		var blob []byte
		if err := rows.Scan(&e.TrackID, &blob, &e.Tokens, &e.Size, &e.MtimeNs); err != nil {
			return nil, err
		}
		if e.Vector, err = DecodeVector(blob); err != nil {
			return nil, fmt.Errorf("track %d: %w", e.TrackID, err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// LyricEmbeddingCount counts lyric vectors stored for a model.
func (s *Store) LyricEmbeddingCount(ctx context.Context, model string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM lyric_embeddings WHERE model = ?`, model).Scan(&n)
	return n, err
}
