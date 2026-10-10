package scan

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/vincentlark/neighbeats/internal/store"
)

var exts = map[string]bool{".mp3": true, ".flac": true}

func setup(t *testing.T) (root string, st *store.Store, opt Options) {
	t.Helper()
	root = t.TempDir()
	st, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return root, st, Options{Roots: []string{root}, Extensions: exts, Workers: 3}
}

// put writes a fake audio file. The content is not valid audio, so the scan
// records a tag error but still indexes the file.
func put(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIncrementalScan(t *testing.T) {
	root, st, opt := setup(t)
	ctx := context.Background()
	put(t, filepath.Join(root, "a.mp3"), "aaa")
	put(t, filepath.Join(root, "d/b.flac"), "bbb")
	put(t, filepath.Join(root, "d/notes.txt"), "ignored")

	res, err := Run(ctx, st, opt)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != store.RunOK || res.Seen != 2 || res.Added != 2 || res.Errors != 2 {
		t.Fatalf("first scan = %+v", res)
	}

	res, err = Run(ctx, st, opt)
	if err != nil || res.Unchanged != 2 || res.Added != 0 || res.Changed != 0 {
		t.Fatalf("second scan = %+v, %v", res, err)
	}

	// Change one file (different size), add one, delete one.
	put(t, filepath.Join(root, "a.mp3"), "aaaa-longer")
	put(t, filepath.Join(root, "c.mp3"), "ccc")
	os.Remove(filepath.Join(root, "d/b.flac"))

	res, err = Run(ctx, st, opt)
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed != 1 || res.Added != 1 || res.Removed != 1 || res.Unchanged != 0 {
		t.Fatalf("third scan = %+v", res)
	}
	if s, _ := st.Stats(ctx); s.Tracks != 2 {
		t.Errorf("tracks = %d, want 2", s.Tracks)
	}
}

func TestMissingRootDeletesNothing(t *testing.T) {
	root, st, opt := setup(t)
	ctx := context.Background()
	put(t, filepath.Join(root, "a.mp3"), "a")
	if _, err := Run(ctx, st, opt); err != nil {
		t.Fatal(err)
	}

	os.RemoveAll(root) // e.g. the share is unmounted
	if _, err := Run(ctx, st, opt); err == nil {
		t.Fatal("expected an error for a missing root")
	}
	if s, _ := st.Stats(ctx); s.Tracks != 1 {
		t.Errorf("tracks = %d, index must be untouched", s.Tracks)
	}
}

func TestMassRemoveGuard(t *testing.T) {
	root, st, opt := setup(t)
	ctx := context.Background()
	for _, n := range []string{"a", "b", "c", "d"} {
		put(t, filepath.Join(root, n+".mp3"), n)
	}
	if _, err := Run(ctx, st, opt); err != nil {
		t.Fatal(err)
	}
	// Root still exists but is now empty, as with an empty mount point.
	for _, n := range []string{"a", "b", "c"} {
		os.Remove(filepath.Join(root, n+".mp3"))
	}

	res, err := Run(ctx, st, opt)
	if !errors.Is(err, ErrMassRemove) || res.Status != store.RunAborted {
		t.Fatalf("got %+v, %v; want aborted with ErrMassRemove", res, err)
	}
	if s, _ := st.Stats(ctx); s.Tracks != 4 {
		t.Errorf("tracks = %d, nothing should be removed", s.Tracks)
	}

	opt.AllowMassRemove = true
	res, err = Run(ctx, st, opt)
	if err != nil || res.Removed != 3 {
		t.Fatalf("with override: %+v, %v", res, err)
	}
}

func TestCancelledScanKeepsIndex(t *testing.T) {
	root, st, opt := setup(t)
	put(t, filepath.Join(root, "a.mp3"), "a")
	if _, err := Run(context.Background(), st, opt); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Cancelled before it starts: fails fast and leaves the index alone.
	if _, err := Run(ctx, st, opt); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if s, _ := st.Stats(context.Background()); s.Tracks != 1 {
		t.Errorf("tracks = %d", s.Tracks)
	}
}

func TestUnreadableDirectorySkipsRemoval(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	root, st, opt := setup(t)
	ctx := context.Background()
	put(t, filepath.Join(root, "locked/a.mp3"), "a")
	put(t, filepath.Join(root, "b.mp3"), "b")
	if _, err := Run(ctx, st, opt); err != nil {
		t.Fatal(err)
	}

	locked := filepath.Join(root, "locked")
	os.Chmod(locked, 0)
	t.Cleanup(func() { os.Chmod(locked, 0o755) })

	res, err := Run(ctx, st, opt)
	if err != nil || res.Status != store.RunPartial || res.Removed != 0 {
		t.Fatalf("got %+v, %v", res, err)
	}
	if s, _ := st.Stats(ctx); s.Tracks != 2 {
		t.Errorf("tracks = %d, a.mp3 must not be dropped", s.Tracks)
	}
}

func TestSymlinkedFileIsIndexed(t *testing.T) {
	root, st, opt := setup(t)
	target := filepath.Join(t.TempDir(), "real.mp3")
	put(t, target, "x")
	if err := os.Symlink(target, filepath.Join(root, "link.mp3")); err != nil {
		t.Skip("symlinks unsupported")
	}
	res, err := Run(context.Background(), st, opt)
	if err != nil || res.Seen != 1 {
		t.Fatalf("got %+v, %v", res, err)
	}
}
