package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/vincent/neighbeats/internal/tags"
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

func TestTasteRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	if _, err := st.db.ExecContext(ctx, `INSERT INTO tracks (id, path, size, mtime_ns, format, seen_run, updated_at, mbid_recording)
		VALUES (1,'/a.mp3',1,1,'mp3',1,1,'rec'),(2,'/b.mp3',1,1,'mp3',1,1,'rec'),(3,'/c.mp3',1,1,'mp3',1,1,'solo')`); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceTaste(ctx, "u", []Taste{{TrackID: 1, Plays: 3, Favourite: true, LastPlayed: 100}}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceTaste(ctx, "u", []Taste{{TrackID: 3, Plays: 1}}); err != nil {
		t.Fatal(err)
	}
	got, _ := st.LoadTaste(ctx, "u")
	if len(got) != 1 || got[3].Plays != 1 {
		t.Errorf("replace should drop old rows: %v", got)
	}
	if other, _ := st.LoadTaste(ctx, "v"); len(other) != 0 {
		t.Errorf("users must be separate: %v", other)
	}
	rec, _ := st.RecordingIndex(ctx)
	if len(rec) != 1 || rec["solo"] != 3 {
		t.Errorf("ambiguous MBIDs must be left out: %v", rec)
	}

	n, err := st.AddPlayEvents(ctx, "u", []PlayEvent{{AtMs: 5, ItemID: "x", TrackID: 1, Seconds: 10}, {AtMs: 6, ItemID: "y", Seconds: 99}})
	if err != nil || n != 2 {
		t.Fatalf("%d %v", n, err)
	}
	if n, _ := st.AddPlayEvents(ctx, "u", []PlayEvent{{AtMs: 5, ItemID: "x", TrackID: 1, Seconds: 10}}); n != 0 {
		t.Errorf("events must be idempotent, added %d", n)
	}
	if ms, _ := st.LastPlayEventMs(ctx, "u"); ms != 6 {
		t.Errorf("last = %d", ms)
	}
	if ev, _ := st.PlayEvents(ctx, "u", 0); len(ev) != 1 || ev[0].TrackID != 1 {
		t.Errorf("only matched events: %v", ev)
	}
	if err := st.SaveItemTracks(ctx, map[string]int64{"i1": 1, "i2": 0}); err != nil {
		t.Fatal(err)
	}
	if m, _ := st.ItemTracks(ctx); m["i1"] != 1 || len(m) != 2 {
		t.Errorf("%v", m)
	}
}

func TestSongKey(t *testing.T) {
	a := SongKey("Dire Straits", "Fade to Black", "m1")
	for _, title := range []string{"Fade To Black", "Fade to Black (2013 Mix)", "fade to black [Live]"} {
		if SongKey("Dire Straits", title, "m2") != a {
			t.Errorf("%q should match", title)
		}
	}
	if SongKey("Dire Straits", "Romeo and Juliet", "") == a {
		t.Error("different songs must differ")
	}
	if SongKey("X", "", "mbid") != "mbid" || SongKey("", "", "") != "" {
		t.Error("fallbacks")
	}
}

func TestLyricFeatures(t *testing.T) {
	st, ctx := open(t), context.Background()
	run, _ := st.StartRun(ctx, []string{"/m"})
	if err := st.UpsertTracks(ctx, run, []Track{track("/m/a.mp3"), track("/m/b.mp3")}); err != nil {
		t.Fatal(err)
	}
	refs, _ := st.TrackRefs(ctx)
	a, b := refs[0].ID, refs[1].ID
	if err := st.UpsertLyrics(ctx, []Lyrics{
		{TrackID: a, Path: "/m/a.lrc", Size: 5, MtimeNs: 1, Text: "alpha", LineCount: 1},
		{TrackID: b, Path: "/m/b.lrc", Size: 6, MtimeNs: 1, Text: "beta", LineCount: 1},
	}); err != nil {
		t.Fatal(err)
	}

	// Languages: only lyrics without a guess are listed, and new text drops the guess.
	todo, _ := st.LyricsWithoutLang(ctx)
	if len(todo) != 2 {
		t.Fatalf("without lang: %v", todo)
	}
	if err := st.SetLyricLangs(ctx, []LyricLang{{a, "eng", 1}, {b, "und", 0.3}}); err != nil {
		t.Fatal(err)
	}
	if todo, _ = st.LyricsWithoutLang(ctx); len(todo) != 0 {
		t.Fatalf("still without lang: %v", todo)
	}
	if langs, _ := st.LyricLangs(ctx); langs[a] != "eng" || langs[b] != "und" {
		t.Fatalf("langs: %v", langs)
	}
	if cs, _ := st.LangCounts(ctx); len(cs) != 2 {
		t.Fatalf("counts: %v", cs)
	}
	if err := st.UpsertLyrics(ctx, []Lyrics{{TrackID: a, Path: "/m/a.lrc", Size: 7, MtimeNs: 2, Text: "alpha two", LineCount: 1}}); err != nil {
		t.Fatal(err)
	}
	if todo, _ = st.LyricsWithoutLang(ctx); len(todo) != 1 || todo[0].TrackID != a {
		t.Fatalf("after change: %v", todo)
	}

	// Embeddings: pending until stored, pending again when the file changes.
	const model = "m"
	p, _ := st.PendingLyricEmbeddings(ctx, model)
	if len(p) != 2 {
		t.Fatalf("pending: %v", p)
	}
	if err := st.SaveLyricEmbeddings(ctx, model, []LyricEmbedding{
		{TrackID: a, Vector: []float32{1, 0}, Tokens: 3, Size: 7, MtimeNs: 2},
		{TrackID: b, Vector: []float32{0, 1}, Tokens: 4, Size: 6, MtimeNs: 1},
	}); err != nil {
		t.Fatal(err)
	}
	if p, _ = st.PendingLyricEmbeddings(ctx, model); len(p) != 0 {
		t.Fatalf("pending after save: %v", p)
	}
	es, _ := st.LyricEmbeddings(ctx, model)
	if len(es) != 2 || es[0].Vector[0] != 1 || es[1].Vector[1] != 1 {
		t.Fatalf("embeddings: %+v", es)
	}
	if err := st.UpsertLyrics(ctx, []Lyrics{{TrackID: b, Path: "/m/b.lrc", Size: 9, MtimeNs: 3, Text: "beta two", LineCount: 1}}); err != nil {
		t.Fatal(err)
	}
	if p, _ = st.PendingLyricEmbeddings(ctx, model); len(p) != 1 || p[0].TrackID != b {
		t.Fatalf("pending after change: %v", p)
	}
	// Removing lyrics removes what was derived from them.
	if err := st.DeleteLyrics(ctx, []int64{a}); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.LyricEmbeddingCount(ctx, model); n != 1 {
		t.Fatalf("embeddings left: %d", n)
	}
	if err := st.SetLyricLangs(ctx, []LyricLang{{b, "fra", 0.9}}); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteLyrics(ctx, []int64{b}); err != nil {
		t.Fatal(err)
	}
	if langs, _ := st.LyricLangs(ctx); len(langs) != 0 {
		t.Fatalf("langs left: %v", langs)
	}
}

func TestLangSamples(t *testing.T) {
	st, ctx := open(t), context.Background()
	run, _ := st.StartRun(ctx, []string{"/m"})
	var ts []Track
	for _, n := range []string{"a", "b", "c", "d", "e", "f"} {
		ts = append(ts, track("/m/"+n+".mp3"))
	}
	if err := st.UpsertTracks(ctx, run, ts); err != nil {
		t.Fatal(err)
	}
	refs, _ := st.TrackRefs(ctx)
	var ls []Lyrics
	var gs []LyricLang
	for _, r := range refs {
		ls = append(ls, Lyrics{TrackID: r.ID, Path: r.Path + ".lrc", Size: 1, MtimeNs: 1, Text: "text", LineCount: 1})
		gs = append(gs, LyricLang{r.ID, "eng", 1})
	}
	if err := st.UpsertLyrics(ctx, ls); err != nil {
		t.Fatal(err)
	}
	if err := st.SetLyricLangs(ctx, gs); err != nil {
		t.Fatal(err)
	}
	got, err := st.LangSamples(ctx, "eng", 3)
	if err != nil || len(got) == 0 || len(got) > 3 {
		t.Fatalf("samples: %v %v", got, err)
	}
}

func TestBackup(t *testing.T) {
	st := open(t)
	dest := filepath.Join(t.TempDir(), "b.sqlite")
	if err := st.Backup(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	b, err := Open(dest)
	if err != nil {
		t.Fatal(err)
	}
	b.Close()
	if err := st.Backup(context.Background(), dest); err == nil {
		t.Fatal("overwrote an existing backup")
	}
}

func TestItemLookups(t *testing.T) {
	st, ctx := open(t), context.Background()
	run, _ := st.StartRun(ctx, []string{"/m"})
	if err := st.UpsertTracks(ctx, run, []Track{track("/m/a.mp3"), track("/m/b.mp3")}); err != nil {
		t.Fatal(err)
	}
	a, ok, err := st.TrackByPath(ctx, "/m/a.mp3")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, ok, _ := st.TrackByPath(ctx, "/m/none.mp3"); ok {
		t.Error("unknown path resolved")
	}
	// Two items for the same file: the smallest id wins, stably.
	if err := st.SaveItemTracks(ctx, map[string]int64{"zz": a, "aa": a, "gone": 0}); err != nil {
		t.Fatal(err)
	}
	if id, ok, _ := st.ItemTrack(ctx, "zz"); !ok || id != a {
		t.Errorf("item zz: %d %v", id, ok)
	}
	if _, ok, _ := st.ItemTrack(ctx, "gone"); ok {
		t.Error("an item cached as unmatched must not resolve")
	}
	got, err := st.TrackItems(ctx, []int64{a, a + 1})
	if err != nil || len(got) != 1 || got[a] != "aa" {
		t.Errorf("%v %v", got, err)
	}
	s1, _ := st.IndexStamp(ctx, "m", "l")
	if err := st.SaveEmbedding(ctx, Embedding{TrackID: a, Model: "m", Vector: []float32{1}, Size: 1, MtimeNs: 1}); err != nil {
		t.Fatal(err)
	}
	if s2, _ := st.IndexStamp(ctx, "m", "l"); s2 == s1 {
		t.Errorf("stamp unchanged after a new embedding: %s", s2)
	}
}
