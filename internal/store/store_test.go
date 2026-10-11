package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/vincentlark/neighbeats/internal/tags"
)

func open(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "sub", "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func track(path string) Track {
	return Track{Path: path, Size: 10, MtimeNs: 1, Format: "mp3",
		Info: tags.Info{Title: "t", Artist: "a", MBIDRecording: "r-" + path}}
}

func TestMigrateIsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "db.sqlite")
	for i := 0; i < 2; i++ {
		st, err := Open(p)
		if err != nil {
			t.Fatalf("open #%d: %v", i, err)
		}
		st.Close()
	}
}

func TestUpsertAndFingerprints(t *testing.T) {
	st, ctx := open(t), context.Background()
	run, _ := st.StartRun(ctx, []string{"/m"})
	if err := st.UpsertTracks(ctx, run, []Track{track("/m/a.mp3"), track("/m/b.mp3")}); err != nil {
		t.Fatal(err)
	}
	// Update one in place: still two rows.
	u := track("/m/a.mp3")
	u.Size = 99
	if err := st.UpsertTracks(ctx, run, []Track{u}); err != nil {
		t.Fatal(err)
	}
	fp, err := st.Fingerprints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(fp) != 2 || fp["/m/a.mp3"].Size != 99 {
		t.Errorf("fingerprints = %+v", fp)
	}
}

func TestUnseenRemovalIsScopedToRoots(t *testing.T) {
	st, ctx := open(t), context.Background()
	r1, _ := st.StartRun(ctx, []string{"/m", "/other"})
	st.UpsertTracks(ctx, r1, []Track{track("/m/a.mp3"), track("/m/b.mp3"), track("/m_x/c.mp3"), track("/other/d.mp3")})

	r2, _ := st.StartRun(ctx, []string{"/m"})
	st.MarkSeen(ctx, r2, []string{"/m/a.mp3"})

	n, err := st.UnseenCount(ctx, r2, []string{"/m"})
	if err != nil || n != 1 { // only /m/b.mp3; /m_x and /other are out of scope
		t.Fatalf("unseen = %d, %v", n, err)
	}
	removed, err := st.DeleteUnseen(ctx, r2, []string{"/m"})
	if err != nil || removed != 1 {
		t.Fatalf("removed = %d, %v", removed, err)
	}
	s, _ := st.Stats(ctx)
	if s.Tracks != 3 {
		t.Errorf("tracks left = %d, want 3", s.Tracks)
	}
}

func TestLikeWildcardsInRootAreEscaped(t *testing.T) {
	st, ctx := open(t), context.Background()
	r1, _ := st.StartRun(ctx, nil)
	st.UpsertTracks(ctx, r1, []Track{track("/a_b/x.mp3"), track("/aXb/y.mp3")})
	r2, _ := st.StartRun(ctx, nil)
	n, _ := st.UnseenCount(ctx, r2, []string{"/a_b"})
	if n != 1 {
		t.Errorf("unseen under /a_b = %d, want 1 (underscore must not be a wildcard)", n)
	}
}

func TestStatsAndRuns(t *testing.T) {
	st, ctx := open(t), context.Background()
	run, _ := st.StartRun(ctx, []string{"/m"})
	a, b := track("/m/a.mp3"), track("/m/b.flac")
	b.Format = "flac"
	b.MBIDRecording = ""
	b.TagError = "bad"
	st.UpsertTracks(ctx, run, []Track{a, b})
	if err := st.FinishRun(run, RunOK, RunCounts{Seen: 2, Added: 2}); err != nil {
		t.Fatal(err)
	}

	s, err := st.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if s.Tracks != 2 || s.WithRecording != 1 || s.TagErrors != 1 || len(s.Formats) != 2 {
		t.Errorf("stats = %+v", s)
	}
	last, err := st.LastRun(ctx)
	if err != nil || last == nil || last.Status != RunOK || last.Added != 2 {
		t.Errorf("last run = %+v, %v", last, err)
	}
}

func TestLastRunEmpty(t *testing.T) {
	if r, err := open(t).LastRun(context.Background()); r != nil || err != nil {
		t.Errorf("got %v, %v", r, err)
	}
}

func TestFindTracksAndTracksByIDs(t *testing.T) {
	st, ctx := open(t), context.Background()
	run, _ := st.StartRun(ctx, []string{"/m"})
	a, b, c := track("/m/a.mp3"), track("/m/b.mp3"), track("/m/c.mp3")
	a.Artist, a.Title, a.Album = "Yann Tiersen", "La Valse d'Amélie", "Amélie"
	b.Artist, b.Title, b.Album = "Yann Tiersen", "Comptine", "Amélie"
	c.Artist, c.Title = "Other", "100% Pure_Love" // LIKE wildcards in the data
	if err := st.UpsertTracks(ctx, run, []Track{a, b, c}); err != nil {
		t.Fatal(err)
	}
	ids := map[string]int64{}
	rows, err := st.TracksByIDs(ctx, []int64{1, 2, 3})
	if err != nil || len(rows) != 3 {
		t.Fatalf("TracksByIDs = %d rows, %v", len(rows), err)
	}
	for _, r := range rows {
		ids[r.Path] = r.ID
	}
	// Only embedded tracks are findable.
	for _, p := range []string{"/m/a.mp3", "/m/b.mp3", "/m/c.mp3"} {
		if err := st.SaveEmbedding(ctx, Embedding{TrackID: ids[p], Model: "m", Vector: []float32{1}, Size: 10, MtimeNs: 1}); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		q    string
		want int
	}{
		{"tiersen", 2}, {"TIERSEN comptine", 1}, {"/m/a.mp3", 1}, {"r-/m/b.mp3", 1},
		{"100% pure_", 1}, {"%", 1}, {"nothing here", 0}, {"  ", 0},
	}
	for _, tc := range cases {
		got, err := st.FindTracks(ctx, "m", tc.q, 10)
		if err != nil || len(got) != tc.want {
			t.Errorf("FindTracks(%q) = %d rows (%v), want %d", tc.q, len(got), err, tc.want)
		}
	}
	if got, _ := st.FindTracks(ctx, "other-model", "tiersen", 10); len(got) != 0 {
		t.Error("found a track without an embedding for the model")
	}
	if got, _ := st.FindTracks(ctx, "m", "tiersen", 1); len(got) != 1 {
		t.Error("limit not applied")
	}
	if rows[ids["/m/a.mp3"]].ArtistKey != "yann tiersen" {
		t.Errorf("artist key = %q", rows[ids["/m/a.mp3"]].ArtistKey)
	}
}
