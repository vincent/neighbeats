package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/vincent/neighbeats/internal/tags"
)

// Track is one indexed audio file.
type Track struct {
	Path     string
	Size     int64
	MtimeNs  int64
	Format   string // lowercase extension without the dot
	TagError string // non-empty when the tags could not be read
	tags.Info
}

// Fingerprint identifies a file version without reading it.
type Fingerprint struct {
	Size     int64
	MtimeNs  int64
	TagError string
}

// Fingerprints returns every indexed path with its fingerprint.
func (s *Store) Fingerprints(ctx context.Context) (map[string]Fingerprint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT path, size, mtime_ns, tag_error FROM tracks`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]Fingerprint)
	for rows.Next() {
		var p string
		var f Fingerprint
		if err := rows.Scan(&p, &f.Size, &f.MtimeNs, &f.TagError); err != nil {
			return nil, err
		}
		out[p] = f
	}
	return out, rows.Err()
}

// UpsertTracks inserts or updates tracks in one transaction and marks them
// seen by the given run.
func (s *Store) UpsertTracks(ctx context.Context, runID int64, tracks []Track) error {
	if len(tracks) == 0 {
		return nil
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `
INSERT INTO tracks (path, size, mtime_ns, format, title, artist, album, album_artist, genre,
	year, track_no, disc_no, mbid_recording, mbid_release_track, mbid_artist, mbid_album_artist,
	mbid_release, mbid_release_group, mbid_work, tag_error, seen_run, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(path) DO UPDATE SET
	size=excluded.size, mtime_ns=excluded.mtime_ns, format=excluded.format,
	title=excluded.title, artist=excluded.artist, album=excluded.album,
	album_artist=excluded.album_artist, genre=excluded.genre, year=excluded.year,
	track_no=excluded.track_no, disc_no=excluded.disc_no,
	mbid_recording=excluded.mbid_recording, mbid_release_track=excluded.mbid_release_track,
	mbid_artist=excluded.mbid_artist, mbid_album_artist=excluded.mbid_album_artist,
	mbid_release=excluded.mbid_release, mbid_release_group=excluded.mbid_release_group,
	mbid_work=excluded.mbid_work, tag_error=excluded.tag_error,
	seen_run=excluded.seen_run, updated_at=excluded.updated_at`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		now := time.Now().Unix()
		for _, t := range tracks {
			if _, err := stmt.ExecContext(ctx, t.Path, t.Size, t.MtimeNs, t.Format, t.Title, t.Artist,
				t.Album, t.AlbumArtist, t.Genre, t.Year, t.TrackNo, t.DiscNo, t.MBIDRecording,
				t.MBIDReleaseTrack, t.MBIDArtist, t.MBIDAlbumArtist, t.MBIDRelease,
				t.MBIDReleaseGroup, t.MBIDWork, t.TagError, runID, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// MarkSeen records that unchanged files were still present during a run.
func (s *Store) MarkSeen(ctx context.Context, runID int64, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `UPDATE tracks SET seen_run = ? WHERE path = ?`)
		if err != nil {
			return err
		}
		defer stmt.Close()
		for _, p := range paths {
			if _, err := stmt.ExecContext(ctx, runID, p); err != nil {
				return err
			}
		}
		return nil
	})
}

// UnseenCount counts tracks under the roots that the run did not see.
func (s *Store) UnseenCount(ctx context.Context, runID int64, roots []string) (int, error) {
	where, args := underRoots(roots)
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tracks WHERE seen_run < ? AND (`+where+`)`,
		append([]any{runID}, args...)...).Scan(&n)
	return n, err
}

// DeleteUnseen removes tracks under the roots that the run did not see.
func (s *Store) DeleteUnseen(ctx context.Context, runID int64, roots []string) (int, error) {
	where, args := underRoots(roots)
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM tracks WHERE seen_run < ? AND (`+where+`)`,
		append([]any{runID}, args...)...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// underRoots builds a WHERE fragment matching paths at or below any root.
// LIKE wildcards in the root are escaped.
func underRoots(roots []string) (string, []any) {
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	parts := make([]string, 0, len(roots))
	args := make([]any, 0, len(roots))
	for _, r := range roots {
		parts = append(parts, `path LIKE ? ESCAPE '\'`)
		args = append(args, esc.Replace(strings.TrimRight(r, "/"))+"/%")
	}
	if len(parts) == 0 {
		return "0", nil
	}
	return strings.Join(parts, " OR "), args
}

func (s *Store) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
