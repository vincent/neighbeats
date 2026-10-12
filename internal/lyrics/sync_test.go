package lyrics

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vincent/neighbeats/internal/store"
	"github.com/vincent/neighbeats/internal/tags"
)

func TestSyncLifecycle(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	music := filepath.Join(dir, "music")
	os.MkdirAll(music, 0o755)
	var tracks []store.Track
	for _, n := range []string{"a", "b", "c"} {
		tracks = append(tracks, store.Track{Path: filepath.Join(music, n+".mp3"), Size: 1, MtimeNs: 1, Format: "mp3",
			Info: tags.Info{Title: "Title " + n, Artist: "Artist " + n}})
	}
	run, _ := st.StartRun(ctx, nil)
	if err := st.UpsertTracks(ctx, run, tracks); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(music, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.lrc", "[00:01.00]walking by the ocean\n[00:05.00]under a silver moon\n")
	write("b.lrc", "[ar:nobody]\n") // no text: counted as empty, not stored

	res, err := Sync(ctx, st)
	if err != nil {
		t.Fatal(err)
	}
	if res.Tracks != 3 || res.Added != 1 || res.Empty != 1 || res.Errors != 0 {
		t.Fatalf("first sync = %+v", res)
	}
	if res.Detected != 1 {
		t.Errorf("languages detected = %d, want 1", res.Detected)
	}
	if langs, _ := st.LyricLangs(ctx); len(langs) != 1 {
		t.Errorf("langs = %v", langs) // too short to tell, but recorded as undetermined
	}

	hits, err := st.SearchLyrics(ctx, "silver ocean", false, 10)
	if err != nil || len(hits) != 1 || hits[0].Title != "Title a" {
		t.Fatalf("search = %+v, %v", hits, err)
	}
	if hits, _ := st.SearchLyrics(ctx, "ocean moon", true, 10); len(hits) != 0 {
		t.Errorf("phrase 'ocean moon' should not match across lines: %+v", hits)
	}
	if hits, _ := st.SearchLyrics(ctx, `"`+"; DROP", false, 10); hits != nil {
		t.Logf("odd input handled: %v", hits)
	}

	// Unchanged on the second pass.
	if res, _ = Sync(ctx, st); res.Unchanged != 1 || res.Added != 0 {
		t.Fatalf("second sync = %+v", res)
	}

	// Edit: the search index must follow.
	write("a.lrc", "[00:01.00]city lights at midnight\n")
	later := time.Now().Add(2 * time.Second)
	os.Chtimes(filepath.Join(music, "a.lrc"), later, later)
	if res, _ = Sync(ctx, st); res.Updated != 1 || res.Detected != 1 {
		t.Fatalf("edit sync = %+v", res)
	}
	if hits, _ := st.SearchLyrics(ctx, "ocean", false, 10); len(hits) != 0 {
		t.Errorf("old text still searchable: %+v", hits)
	}
	if hits, _ := st.SearchLyrics(ctx, "midnight", false, 10); len(hits) != 1 {
		t.Errorf("new text not searchable: %+v", hits)
	}

	// Delete the file: lyrics go away.
	os.Remove(filepath.Join(music, "a.lrc"))
	if res, _ = Sync(ctx, st); res.Removed != 1 {
		t.Fatalf("delete sync = %+v", res)
	}
	if hits, _ := st.SearchLyrics(ctx, "midnight", false, 10); len(hits) != 0 {
		t.Errorf("deleted lyrics still searchable: %+v", hits)
	}
	if ls, _ := st.LyricsStats(ctx); ls.WithLyrics != 0 {
		t.Errorf("stats = %+v", ls)
	}
}

func TestCascadeOnTrackRemoval(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "db.sqlite"))
	defer st.Close()
	p := filepath.Join(dir, "a.mp3")
	run1, _ := st.StartRun(ctx, nil)
	st.UpsertTracks(ctx, run1, []store.Track{{Path: p, Size: 1, MtimeNs: 1, Format: "mp3"}})
	os.WriteFile(filepath.Join(dir, "a.lrc"), []byte("[00:01.00]lonely road\n"), 0o644)
	if _, err := Sync(ctx, st); err != nil {
		t.Fatal(err)
	}

	run2, _ := st.StartRun(ctx, nil)
	if n, err := st.DeleteUnseen(ctx, run2, []string{dir}); err != nil || n != 1 {
		t.Fatalf("delete = %d, %v", n, err)
	}
	if ls, _ := st.LyricsStats(ctx); ls.WithLyrics != 0 {
		t.Errorf("lyrics survived track removal: %+v", ls)
	}
	if hits, _ := st.SearchLyrics(ctx, "lonely", false, 5); len(hits) != 0 {
		t.Errorf("search index not cleaned: %+v", hits)
	}
}
