package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

// PendingTrack is a track that still needs an embedding.
type PendingTrack struct {
	ID      int64
	Path    string
	Size    int64
	MtimeNs int64
}

// Embedding is the stored vector of one track.
type Embedding struct {
	TrackID   int64
	Model     string
	Vector    []float32
	Patches   int
	ClipStart float64
	ClipLen   float64
	Duration  float64
	Size      int64
	MtimeNs   int64
}

// PendingEmbeddings lists tracks without an up-to-date embedding for model,
// in id order. Tracks whose last attempt failed on the same file version are
// skipped unless retryErrors is set.
func (s *Store) PendingEmbeddings(ctx context.Context, model string, retryErrors bool) ([]PendingTrack, error) {
	q := `
SELECT t.id, t.path, t.size, t.mtime_ns FROM tracks t
LEFT JOIN embeddings e ON e.track_id = t.id AND e.model = ?1
LEFT JOIN embedding_errors x ON x.track_id = t.id AND x.model = ?1
WHERE (e.track_id IS NULL OR e.size <> t.size OR e.mtime_ns <> t.mtime_ns)`
	if !retryErrors {
		q += ` AND (x.track_id IS NULL OR x.size <> t.size OR x.mtime_ns <> t.mtime_ns)`
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY t.id`, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingTrack
	for rows.Next() {
		var p PendingTrack
		if err := rows.Scan(&p.ID, &p.Path, &p.Size, &p.MtimeNs); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SaveEmbedding stores a vector and clears any earlier failure.
func (s *Store) SaveEmbedding(ctx context.Context, e Embedding) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO embeddings (track_id, model, vector, dim, patches, clip_start, clip_len, duration, size, mtime_ns, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(track_id, model) DO UPDATE SET
	vector=excluded.vector, dim=excluded.dim, patches=excluded.patches, clip_start=excluded.clip_start,
	clip_len=excluded.clip_len, duration=excluded.duration, size=excluded.size,
	mtime_ns=excluded.mtime_ns, updated_at=excluded.updated_at`,
			e.TrackID, e.Model, EncodeVector(e.Vector), len(e.Vector), e.Patches, e.ClipStart, e.ClipLen,
			e.Duration, e.Size, e.MtimeNs, time.Now().Unix()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM embedding_errors WHERE track_id = ? AND model = ?`, e.TrackID, e.Model)
		return err
	})
}

// SaveEmbeddingError records that a track could not be embedded.
func (s *Store) SaveEmbeddingError(ctx context.Context, t PendingTrack, model, msg string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO embedding_errors (track_id, model, error, size, mtime_ns, updated_at) VALUES (?,?,?,?,?,?)
ON CONFLICT(track_id, model) DO UPDATE SET
	error=excluded.error, size=excluded.size, mtime_ns=excluded.mtime_ns, updated_at=excluded.updated_at`,
		t.ID, model, msg, t.Size, t.MtimeNs, time.Now().Unix())
	return err
}

// EmbeddingStats counts embedded and failed tracks for a model.
func (s *Store) EmbeddingStats(ctx context.Context, model string) (embedded, failed int, err error) {
	err = s.db.QueryRowContext(ctx, `
SELECT (SELECT COUNT(*) FROM embeddings WHERE model = ?1),
       (SELECT COUNT(*) FROM embedding_errors WHERE model = ?1)`, model).Scan(&embedded, &failed)
	return
}

// Embeddings returns every stored vector for a model.
func (s *Store) Embeddings(ctx context.Context, model string) ([]Embedding, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT track_id, vector, patches, clip_start, clip_len, duration, size, mtime_ns
FROM embeddings WHERE model = ? ORDER BY track_id`, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Embedding
	for rows.Next() {
		e := Embedding{Model: model}
		var blob []byte
		if err := rows.Scan(&e.TrackID, &blob, &e.Patches, &e.ClipStart, &e.ClipLen, &e.Duration, &e.Size, &e.MtimeNs); err != nil {
			return nil, err
		}
		if e.Vector, err = DecodeVector(blob); err != nil {
			return nil, fmt.Errorf("track %d: %w", e.TrackID, err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// EncodeVector packs float32 values little-endian.
func EncodeVector(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(x))
	}
	return b
}

// DecodeVector reverses EncodeVector.
func DecodeVector(b []byte) ([]float32, error) {
	if len(b)%4 != 0 {
		return nil, fmt.Errorf("vector blob has %d bytes, not a multiple of 4", len(b))
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return v, nil
}
