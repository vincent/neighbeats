package main

import (
	"bytes"
	"context"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vincent/neighbeats/internal/embed"
	"github.com/vincent/neighbeats/internal/route"
	"github.com/vincent/neighbeats/internal/store"
	"github.com/vincent/neighbeats/internal/tags"
	"github.com/vincent/neighbeats/internal/vecindex"
)

// ring builds an index of n unit vectors spread over a quarter circle.
func ring(t *testing.T, ids []int64, angles []float64) *vecindex.Index {
	t.Helper()
	vs := make([][]float32, len(ids))
	for i, a := range angles {
		vs[i] = []float32{float32(math.Cos(a)), float32(math.Sin(a))}
	}
	ix, err := vecindex.Build(ids, vs, vecindex.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

func TestCalibrateAndApply(t *testing.T) {
	var ids []int64
	var audio, lyric []float64
	for i := range 60 {
		ids = append(ids, int64(i))
		audio = append(audio, float64(i)*0.02)
		lyric = append(lyric, float64(i)*0.02) // lyrics agree with the audio
	}
	ax, lx := ring(t, ids, audio), ring(t, ids, lyric)
	mu, err := calibrate(ax, lx)
	if err != nil || mu < 0.9 {
		t.Fatalf("mu = %v, %v (neighbours agree, so the typical cosine is high)", mu, err)
	}

	s := &lyricSignals{lx: lx, mu: mu, opt: lyricOptions{weight: 0.2, langPenalty: 0.5}, langs: map[int64]string{1: "eng", 2: "fra"}}
	var o route.Options
	s.apply(&o)
	if o.Pair == nil || o.Lang == nil || o.Theme != nil {
		t.Fatalf("hooks: pair %v lang %v theme %v", o.Pair != nil, o.Lang != nil, o.Theme != nil)
	}
	// A pair far apart in lyrics loses; a pair as close as typical is unchanged.
	if got := o.Pair(0, 59, 0.5); got >= 0.5 {
		t.Errorf("distant lyrics should lower the cosine, got %v", got)
	}
	if got := o.Pair(0, 1, 0.5); math.Abs(float64(got)-0.5) > 0.01 {
		t.Errorf("typical lyrics should leave it, got %v", got)
	}
	// A track without a lyric vector is never penalised.
	if got := o.Pair(0, 999, 0.5); got != 0.5 {
		t.Errorf("no lyrics: %v", got)
	}
	if o.Lang(1) != "eng" || o.Lang(7) != "" {
		t.Errorf("lang: %q %q", o.Lang(1), o.Lang(7))
	}

	// A nil set of signals changes nothing.
	var none *lyricSignals
	var o2 route.Options
	none.apply(&o2)
	if o2.Pair != nil || o2.Lang != nil || o2.Theme != nil || none.notes(1, 2, nil) != "" {
		t.Error("nil signals must be inert")
	}

	// Zero weight leaves the audio alone.
	off := &lyricSignals{lx: lx, opt: lyricOptions{}}
	var o3 route.Options
	off.apply(&o3)
	if o3.Pair != nil || o3.Lang != nil {
		t.Error("zero weights must not install hooks")
	}
}

func TestThemeHook(t *testing.T) {
	lx := ring(t, []int64{1, 2, 3}, []float64{0, 0.8, 1.5})
	theme, err := lx.Project([]float32{1, 0})
	if err != nil {
		t.Fatal(err)
	}
	s := &lyricSignals{lx: lx, theme: theme, opt: lyricOptions{themeWeight: 3}}
	var o route.Options
	s.apply(&o)
	if o.Theme == nil || o.ThemeWeight != 3 {
		t.Fatal("theme hook missing")
	}
	if a, b := o.Theme(1), o.Theme(3); !(a > b) || b < 0 || o.Theme(99) != 0 {
		t.Errorf("theme: %v %v %v", a, b, o.Theme(99))
	}
}

func TestResolveLyric(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	run, _ := st.StartRun(ctx, []string{"/m"})
	var ts []store.Track
	for _, n := range []string{"a", "b", "c"} {
		ts = append(ts, store.Track{Path: "/m/" + n + ".mp3", Size: 1, MtimeNs: 1, Format: "mp3", Info: tags.Info{Title: n, Artist: "x"}})
	}
	if err := st.UpsertTracks(ctx, run, ts); err != nil {
		t.Fatal(err)
	}
	refs, _ := st.TrackRefs(ctx)
	lines := []string{"walking by the ocean tonight", "walking by the ocean alone", "city lights at midnight"}
	for i, r := range refs { // the third has lyrics but no audio embedding
		if err := st.UpsertLyrics(ctx, []store.Lyrics{{TrackID: r.ID, Path: r.Path + ".lrc", Size: 1, MtimeNs: 1, Text: lines[i], LineCount: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range refs[:2] {
		if err := st.SaveEmbedding(ctx, store.Embedding{TrackID: r.ID, Model: embed.ModelID, Vector: []float32{1}, Size: 1, MtimeNs: 1}); err != nil {
			t.Fatal(err)
		}
	}
	var out, errw bytes.Buffer
	if id, err := resolveLyric(ctx, st, "ocean alone", &out, &errw); err != nil || id != refs[1].ID {
		t.Errorf("unique line: %d %v", id, err)
	}
	if _, err := resolveLyric(ctx, st, "walking by the ocean", &out, &errw); err == nil || !strings.Contains(out.String(), "x - b") {
		t.Errorf("ambiguous line should be listed: %v %q", err, out.String())
	}
	if _, err := resolveLyric(ctx, st, "city lights", &out, &errw); err == nil {
		t.Error("a track without an audio embedding cannot be a route end")
	}
	if _, err := resolveLyric(ctx, st, "no such words anywhere", &out, &errw); err == nil {
		t.Error("no match should fail")
	}
}
