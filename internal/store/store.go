// Package store owns the SQLite database.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // registers the "sqlite" driver (pure Go, no cgo)
)

// migrations are applied in order; the index+1 is stored in PRAGMA
// user_version. Never edit an applied entry, append a new one.
var migrations = []string{
	`CREATE TABLE scan_runs (
		id          INTEGER PRIMARY KEY,
		started_at  INTEGER NOT NULL,
		finished_at INTEGER,
		roots       TEXT    NOT NULL,
		status      TEXT    NOT NULL DEFAULT 'running',
		seen        INTEGER NOT NULL DEFAULT 0,
		added       INTEGER NOT NULL DEFAULT 0,
		changed     INTEGER NOT NULL DEFAULT 0,
		unchanged   INTEGER NOT NULL DEFAULT 0,
		removed     INTEGER NOT NULL DEFAULT 0,
		errors      INTEGER NOT NULL DEFAULT 0
	);
	CREATE TABLE tracks (
		id                 INTEGER PRIMARY KEY,
		path               TEXT    NOT NULL UNIQUE,
		size               INTEGER NOT NULL,
		mtime_ns           INTEGER NOT NULL,
		format             TEXT    NOT NULL,
		title              TEXT    NOT NULL DEFAULT '',
		artist             TEXT    NOT NULL DEFAULT '',
		album              TEXT    NOT NULL DEFAULT '',
		album_artist       TEXT    NOT NULL DEFAULT '',
		genre              TEXT    NOT NULL DEFAULT '',
		year               INTEGER NOT NULL DEFAULT 0,
		track_no           INTEGER NOT NULL DEFAULT 0,
		disc_no            INTEGER NOT NULL DEFAULT 0,
		mbid_recording     TEXT    NOT NULL DEFAULT '',
		mbid_release_track TEXT    NOT NULL DEFAULT '',
		mbid_artist        TEXT    NOT NULL DEFAULT '',
		mbid_album_artist  TEXT    NOT NULL DEFAULT '',
		mbid_release       TEXT    NOT NULL DEFAULT '',
		mbid_release_group TEXT    NOT NULL DEFAULT '',
		mbid_work          TEXT    NOT NULL DEFAULT '',
		tag_error          TEXT    NOT NULL DEFAULT '',
		seen_run           INTEGER NOT NULL,
		updated_at         INTEGER NOT NULL
	);
	CREATE INDEX tracks_mbid_recording ON tracks(mbid_recording) WHERE mbid_recording <> '';
	CREATE INDEX tracks_mbid_artist    ON tracks(mbid_artist)    WHERE mbid_artist <> '';
	CREATE INDEX tracks_artist         ON tracks(artist);`,

	// v2: lyrics from sibling .lrc files, with full-text search. The FTS table
	// is an external-content index kept in sync by triggers.
	`CREATE TABLE lyrics (
		track_id   INTEGER PRIMARY KEY REFERENCES tracks(id) ON DELETE CASCADE,
		path       TEXT    NOT NULL,
		size       INTEGER NOT NULL,
		mtime_ns   INTEGER NOT NULL,
		text       TEXT    NOT NULL,
		synced     INTEGER NOT NULL,
		line_count INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE VIRTUAL TABLE lyrics_fts USING fts5(
		text, content='lyrics', content_rowid='track_id', tokenize='unicode61 remove_diacritics 2'
	);
	CREATE TRIGGER lyrics_ai AFTER INSERT ON lyrics BEGIN
		INSERT INTO lyrics_fts(rowid, text) VALUES (new.track_id, new.text);
	END;
	CREATE TRIGGER lyrics_ad AFTER DELETE ON lyrics BEGIN
		INSERT INTO lyrics_fts(lyrics_fts, rowid, text) VALUES ('delete', old.track_id, old.text);
	END;
	CREATE TRIGGER lyrics_au AFTER UPDATE ON lyrics BEGIN
		INSERT INTO lyrics_fts(lyrics_fts, rowid, text) VALUES ('delete', old.track_id, old.text);
		INSERT INTO lyrics_fts(rowid, text) VALUES (new.track_id, new.text);
	END;`,

	// v3: one embedding per track and model. size/mtime_ns copy the audio
	// file's fingerprint so a changed file is re-embedded. Failures are kept
	// so a re-run does not retry undecodable files forever.
	`CREATE TABLE embeddings (
		track_id   INTEGER NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
		model      TEXT    NOT NULL,
		vector     BLOB    NOT NULL,
		dim        INTEGER NOT NULL,
		patches    INTEGER NOT NULL,
		clip_start REAL    NOT NULL,
		clip_len   REAL    NOT NULL,
		duration   REAL    NOT NULL,
		size       INTEGER NOT NULL,
		mtime_ns   INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (track_id, model)
	);
	CREATE TABLE embedding_errors (
		track_id   INTEGER NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
		model      TEXT    NOT NULL,
		error      TEXT    NOT NULL,
		size       INTEGER NOT NULL,
		mtime_ns   INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (track_id, model)
	);`,

	// v4: listening history from Jellyfin, per Jellyfin user id. taste is a
	// snapshot replaced by each sync; play_events accumulate (Playback
	// Reporting) and are what skips are derived from; jellyfin_items caches
	// which indexed track an item id is (NULL = no match, so it is not
	// looked up again).
	`CREATE TABLE taste (
		user_id     TEXT    NOT NULL,
		track_id    INTEGER NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
		plays       INTEGER NOT NULL,
		favourite   INTEGER NOT NULL,
		last_played INTEGER NOT NULL,
		synced_at   INTEGER NOT NULL,
		PRIMARY KEY (user_id, track_id)
	);
	CREATE TABLE play_events (
		user_id  TEXT    NOT NULL,
		at_ms    INTEGER NOT NULL,
		item_id  TEXT    NOT NULL,
		track_id INTEGER REFERENCES tracks(id) ON DELETE CASCADE,
		seconds  REAL    NOT NULL,
		PRIMARY KEY (user_id, at_ms, item_id)
	);
	CREATE TABLE jellyfin_items (
		item_id  TEXT PRIMARY KEY,
		track_id INTEGER REFERENCES tracks(id) ON DELETE CASCADE
	);`,

	// v5: what is derived from lyrics. lyric_lang is the detected language
	// (a separate table: updating lyrics itself would rewrite its full-text
	// index); lyric_embeddings holds one text vector per track and model, with
	// the .lrc fingerprint it was computed from so a changed file is
	// re-embedded.
	`CREATE TABLE lyric_lang (
		track_id   INTEGER PRIMARY KEY REFERENCES tracks(id) ON DELETE CASCADE,
		lang       TEXT    NOT NULL,
		confidence REAL    NOT NULL
	);
	CREATE TABLE lyric_embeddings (
		track_id   INTEGER NOT NULL REFERENCES tracks(id) ON DELETE CASCADE,
		model      TEXT    NOT NULL,
		vector     BLOB    NOT NULL,
		dim        INTEGER NOT NULL,
		tokens     INTEGER NOT NULL,
		size       INTEGER NOT NULL,
		mtime_ns   INTEGER NOT NULL,
		updated_at INTEGER NOT NULL,
		PRIMARY KEY (track_id, model)
	);`,

	// v6: `serve` answers with Jellyfin item ids, so the item cache is also
	// read from the track side.
	`CREATE INDEX jellyfin_items_track ON jellyfin_items(track_id) WHERE track_id IS NOT NULL;`,
}

// Store is a handle on the database.
type Store struct {
	db *sql.DB
}

// Open creates the parent directory if needed, opens the database in WAL mode
// and applies pending migrations.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Add("_pragma", "foreign_keys(1)")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	// One writer at a time; WAL still allows concurrent readers.
	db.SetMaxOpenConns(4)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	var v int
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	if v > len(migrations) {
		return fmt.Errorf("database schema v%d is newer than this binary (v%d)", v, len(migrations))
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		// PRAGMA does not accept bound parameters.
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
