// Package scan walks the music folders and keeps the track index in sync.
//
// The scan is incremental: a file whose size and mtime match the index is not
// re-read. Files that disappeared are removed, but only when the whole walk
// succeeded, so an unmounted share can never wipe the index.
package scan

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vincentlark/neighbeats/internal/store"
	"github.com/vincentlark/neighbeats/internal/tags"
)

const (
	batchSize     = 500
	flushInterval = time.Second
	maxTagErrLen  = 200
)

// Options configures a scan.
type Options struct {
	Roots      []string
	Extensions map[string]bool // lowercase, with leading dot
	Workers    int
	// AllowMassRemove lets a scan delete more than half of the tracks under
	// the roots. Without it such a scan is aborted as a safety net.
	AllowMassRemove bool
	// Logf receives progress lines; nil silences them.
	Logf func(format string, args ...any)
	// Progress receives the running totals, often (every 250 ms). Nil means
	// they go to Logf every 5 s.
	Progress func(format string, args ...any)
}

// Result summarises a finished scan.
type Result struct {
	RunID  int64
	Status string
	store.RunCounts
}

// ErrMassRemove is returned when a scan would remove most of the index.
var ErrMassRemove = errors.New("refusing to remove more than half of the indexed tracks; check that the music share is mounted, or pass --allow-mass-remove")

type event struct {
	touch string       // path of an unchanged file
	track *store.Track // new or changed file
}

type counters struct {
	seen, added, changed, unchanged, errors atomic.Int64
}

// Run scans the roots and updates the store.
func Run(ctx context.Context, st *store.Store, opt Options) (Result, error) {
	logf := opt.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if len(opt.Roots) == 0 {
		return Result{}, errors.New("no roots to scan")
	}
	if opt.Workers < 1 {
		opt.Workers = 1
	}
	for _, r := range opt.Roots {
		fi, err := os.Stat(r)
		if err != nil {
			return Result{}, fmt.Errorf("root %s: %w", r, err)
		}
		if !fi.IsDir() {
			return Result{}, fmt.Errorf("root %s is not a directory", r)
		}
	}

	known, err := st.Fingerprints(ctx)
	if err != nil {
		return Result{}, err
	}
	runID, err := st.StartRun(ctx, opt.Roots)
	if err != nil {
		return Result{}, err
	}

	var c counters
	var walkIncomplete atomic.Bool
	var writeErr error

	jobs := make(chan *store.Track, opt.Workers*2)
	events := make(chan event, 2*batchSize)

	// Single writer: SQLite handles one writer at a time, so batching here is
	// both simpler and faster than writing from the workers.
	var writerDone sync.WaitGroup
	writerDone.Add(1)
	go func() {
		defer writerDone.Done()
		writeErr = writeLoop(ctx, st, runID, events)
	}()

	var workers sync.WaitGroup
	for i := 0; i < opt.Workers; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for t := range jobs {
				info, err := tags.Read(t.Path)
				t.Info = info
				if err != nil {
					t.TagError = truncate(err.Error(), maxTagErrLen)
					c.errors.Add(1)
				}
				if _, existed := known[t.Path]; existed {
					c.changed.Add(1)
				} else {
					c.added.Add(1)
				}
				events <- event{track: t}
			}
		}()
	}

	progress, every := opt.Progress, 250*time.Millisecond
	if progress == nil {
		progress, every = func(f string, a ...any) { logf(f, a...) }, 5*time.Second
	}
	stopProgress := make(chan struct{})
	go func() {
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			select {
			case <-tick.C:
				progress("scanning: %d files seen (%d new, %d changed)", c.seen.Load(), c.added.Load(), c.changed.Load())
			case <-stopProgress:
				return
			}
		}
	}()

	// The walk runs in this goroutine; it feeds jobs and touch events.
	walkErr := walk(ctx, opt, known, &c, &walkIncomplete, jobs, events, logf)
	close(jobs)
	workers.Wait()
	close(events)
	writerDone.Wait()
	close(stopProgress)

	counts := store.RunCounts{
		Seen: int(c.seen.Load()), Added: int(c.added.Load()), Changed: int(c.changed.Load()),
		Unchanged: int(c.unchanged.Load()), Errors: int(c.errors.Load()),
	}
	finish := func(status string, err error) (Result, error) {
		if ferr := st.FinishRun(runID, status, counts); ferr != nil && err == nil {
			err = ferr
		}
		return Result{RunID: runID, Status: status, RunCounts: counts}, err
	}

	switch {
	case writeErr != nil:
		return finish(store.RunPartial, fmt.Errorf("write index: %w", writeErr))
	case ctx.Err() != nil:
		return finish(store.RunCancelled, ctx.Err())
	case walkErr != nil:
		return finish(store.RunPartial, walkErr)
	case walkIncomplete.Load():
		logf("some directories could not be read; skipping removal of missing files")
		return finish(store.RunPartial, nil)
	}

	// Removal phase: the walk was complete, so files not seen are really gone.
	unseen, err := st.UnseenCount(ctx, runID, opt.Roots)
	if err != nil {
		return finish(store.RunPartial, err)
	}
	if total := countUnder(known, opt.Roots); !opt.AllowMassRemove && total > 0 && unseen*2 > total {
		return finish(store.RunAborted, fmt.Errorf("%w (would remove %d of %d)", ErrMassRemove, unseen, total))
	}
	removed, err := st.DeleteUnseen(ctx, runID, opt.Roots)
	counts.Removed = removed
	if err != nil {
		return finish(store.RunPartial, err)
	}
	return finish(store.RunOK, nil)
}

func walk(ctx context.Context, opt Options, known map[string]store.Fingerprint, c *counters,
	incomplete *atomic.Bool, jobs chan<- *store.Track, events chan<- event, logf func(string, ...any)) error {

	for _, root := range opt.Roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				// Unreadable entry: keep going, but remember that the walk
				// is incomplete so nothing gets deleted afterwards.
				logf("skip %s: %v", path, err)
				incomplete.Store(true)
				c.errors.Add(1)
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if !opt.Extensions[ext] {
				return nil
			}
			var fi fs.FileInfo
			if d.Type()&fs.ModeSymlink != 0 {
				fi, err = os.Stat(path) // follow file symlinks
			} else {
				fi, err = d.Info()
			}
			if err != nil || !fi.Mode().IsRegular() {
				if err != nil {
					logf("skip %s: %v", path, err)
					incomplete.Store(true)
					c.errors.Add(1)
				}
				return nil
			}
			c.seen.Add(1)

			size, mtime := fi.Size(), fi.ModTime().UnixNano()
			// A file whose tags failed to parse is not retried until it
			// changes, to avoid re-reading broken files on every scan.
			if fp, ok := known[path]; ok && fp.Size == size && fp.MtimeNs == mtime {
				c.unchanged.Add(1)
				select {
				case events <- event{touch: path}:
				case <-ctx.Done():
					return ctx.Err()
				}
				return nil
			}
			t := &store.Track{Path: path, Size: size, MtimeNs: mtime, Format: strings.TrimPrefix(ext, ".")}
			select {
			case jobs <- t:
			case <-ctx.Done():
				return ctx.Err()
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func writeLoop(ctx context.Context, st *store.Store, runID int64, events <-chan event) error {
	var tracks []store.Track
	var touched []string
	var firstErr error
	flush := func() {
		if firstErr == nil {
			// context.Background: a cancelled scan still saves what it has.
			if err := st.UpsertTracks(context.Background(), runID, tracks); err != nil {
				firstErr = err
			} else if err := st.MarkSeen(context.Background(), runID, touched); err != nil {
				firstErr = err
			}
		}
		tracks, touched = tracks[:0], touched[:0]
	}
	tick := time.NewTicker(flushInterval)
	defer tick.Stop()
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				flush()
				return firstErr
			}
			if ev.track != nil {
				tracks = append(tracks, *ev.track)
			} else {
				touched = append(touched, ev.touch)
			}
			if len(tracks)+len(touched) >= batchSize {
				flush()
			}
		case <-tick.C:
			if len(tracks)+len(touched) > 0 {
				flush()
			}
		}
	}
}

func countUnder(known map[string]store.Fingerprint, roots []string) int {
	n := 0
	for p := range known {
		for _, r := range roots {
			if strings.HasPrefix(p, strings.TrimRight(r, "/")+"/") {
				n++
				break
			}
		}
	}
	return n
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
