package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Run status values.
const (
	RunRunning   = "running"
	RunOK        = "ok"
	RunPartial   = "partial"   // some directories could not be read; nothing was removed
	RunCancelled = "cancelled" // interrupted; nothing was removed
	RunAborted   = "aborted"   // refused to remove too many tracks
)

// RunCounts are the totals recorded when a scan run finishes.
type RunCounts struct {
	Seen, Added, Changed, Unchanged, Removed, Errors int
}

// Run is a row of scan_runs.
type Run struct {
	ID         int64
	StartedAt  time.Time
	FinishedAt time.Time // zero while running
	Roots      string
	Status     string
	RunCounts
}

// StartRun records the beginning of a scan and returns its id.
func (s *Store) StartRun(ctx context.Context, roots []string) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO scan_runs (started_at, roots) VALUES (?, ?)`,
		time.Now().Unix(), strings.Join(roots, "\n"))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishRun stores the final status and counters of a run. It uses a fresh
// context so an interrupted run is still recorded.
func (s *Store) FinishRun(runID int64, status string, c RunCounts) error {
	_, err := s.db.ExecContext(context.Background(),
		`UPDATE scan_runs SET finished_at=?, status=?, seen=?, added=?, changed=?, unchanged=?, removed=?, errors=? WHERE id=?`,
		time.Now().Unix(), status, c.Seen, c.Added, c.Changed, c.Unchanged, c.Removed, c.Errors, runID)
	return err
}

// LastRun returns the most recent scan run, or nil if there is none.
func (s *Store) LastRun(ctx context.Context) (*Run, error) {
	var r Run
	var started int64
	var finished sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
SELECT id, started_at, finished_at, roots, status, seen, added, changed, unchanged, removed, errors
FROM scan_runs ORDER BY id DESC LIMIT 1`).Scan(&r.ID, &started, &finished, &r.Roots, &r.Status,
		&r.Seen, &r.Added, &r.Changed, &r.Unchanged, &r.Removed, &r.Errors)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.StartedAt = time.Unix(started, 0)
	if finished.Valid {
		r.FinishedAt = time.Unix(finished.Int64, 0)
	}
	return &r, nil
}
