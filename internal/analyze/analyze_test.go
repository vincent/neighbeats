package analyze

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vincentlark/neighbeats/internal/audio"
	"github.com/vincentlark/neighbeats/internal/embed"
	"github.com/vincentlark/neighbeats/internal/store"
	"github.com/vincentlark/neighbeats/internal/tags"
)

// fakeDecoder returns a sine whose pitch depends on the file name, a too-short
// clip for "short", and an error for "broken".
type fakeDecoder struct{ calls atomic.Int64 }

func (d *fakeDecoder) MiddleClip(_ context.Context, path string, _ float64, rate int) (audio.Clip, error) {
	d.calls.Add(1)
	switch {
	case strings.Contains(path, "broken"):
		return audio.Clip{}, errors.New("decode failed")
	case strings.Contains(path, "short"):
		return audio.Clip{Samples: make([]float32, 100), Duration: 1}, nil
	}
	freq := 200 + 100*float64(len(path))
	s := make([]float32, 5*rate)
	for i := range s {
		s[i] = float32(0.5 * math.Sin(2*math.Pi*freq*float64(i)/float64(rate)))
	}
	return audio.Clip{Samples: s, Start: 1, Duration: 7}, nil
}

func setup(t *testing.T, paths ...string) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	var tr []store.Track
	for _, p := range paths {
		tr = append(tr, store.Track{Path: p, Size: 1, MtimeNs: 1, Format: "mp3", Info: tags.Info{Title: p}})
	}
	run, _ := st.StartRun(context.Background(), []string{"/m"})
	if err := st.UpsertTracks(context.Background(), run, tr); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestRunEmbedsResumesAndRecordsFailures(t *testing.T) {
	ctx := context.Background()
	st := setup(t, "/m/a.mp3", "/m/bb.mp3", "/m/broken.mp3", "/m/short.mp3")
	dec := &fakeDecoder{}
	opt := Options{Decoder: dec, Embedder: embed.Fake{}, Workers: 3, ClipSeconds: 30}

	res, err := Run(ctx, st, opt)
	if err != nil {
		t.Fatal(err)
	}
	if res.Pending != 4 || res.Done != 2 || res.Failed != 2 {
		t.Fatalf("first run = %+v", res)
	}
	embs, err := st.Embeddings(ctx, embed.ModelID)
	if err != nil || len(embs) != 2 {
		t.Fatalf("embeddings = %d, %v", len(embs), err)
	}
	e := embs[0]
	if len(e.Vector) != embed.Dim || e.Patches != 3 || e.ClipStart != 1 || e.Duration != 7 || e.ClipLen != 5 {
		t.Errorf("stored embedding = patches %d dim %d start %v dur %v len %v", e.Patches, len(e.Vector), e.ClipStart, e.Duration, e.ClipLen)
	}

	// Second run: nothing to do, failures are not retried.
	dec.calls.Store(0)
	res, err = Run(ctx, st, opt)
	if err != nil || res.Pending != 0 || dec.calls.Load() != 0 {
		t.Fatalf("second run = %+v, calls %d, err %v", res, dec.calls.Load(), err)
	}
	// ... unless asked to.
	opt.RetryErrors = true
	if res, _ = Run(ctx, st, opt); res.Pending != 2 || res.Failed != 2 {
		t.Fatalf("retry run = %+v", res)
	}
	if n, f, _ := st.EmbeddingStats(ctx, embed.ModelID); n != 2 || f != 2 {
		t.Errorf("stats = %d embedded, %d failed", n, f)
	}
}

func TestChangedFileIsReembedded(t *testing.T) {
	ctx := context.Background()
	st := setup(t, "/m/a.mp3")
	opt := Options{Decoder: &fakeDecoder{}, Embedder: embed.Fake{}, Workers: 1, ClipSeconds: 30}
	if _, err := Run(ctx, st, opt); err != nil {
		t.Fatal(err)
	}
	run, _ := st.StartRun(ctx, []string{"/m"})
	if err := st.UpsertTracks(ctx, run, []store.Track{{Path: "/m/a.mp3", Size: 2, MtimeNs: 5, Format: "mp3"}}); err != nil {
		t.Fatal(err)
	}
	res, err := Run(ctx, st, opt)
	if err != nil || res.Done != 1 {
		t.Fatalf("re-run after change = %+v, %v", res, err)
	}
}

func TestInterruptedRunKeepsFinishedWork(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	st := setup(t, "/m/a.mp3", "/m/b.mp3", "/m/c.mp3")
	var n atomic.Int64
	opt := Options{Decoder: &fakeDecoder{}, Embedder: cancelAfter{embed.Fake{}, &n, 1, cancel}, Workers: 1, ClipSeconds: 30}
	if _, err := Run(ctx, st, opt); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	done, _, _ := st.EmbeddingStats(context.Background(), embed.ModelID)
	if done < 1 || done > 2 {
		t.Fatalf("kept %d embeddings, want 1 or 2", done)
	}
}

type cancelAfter struct {
	embed.Embedder
	n     *atomic.Int64
	limit int
	stop  context.CancelFunc
}

func (c cancelAfter) Embed(p []float32, n int) ([]float32, error) {
	if c.n.Add(1) >= int64(c.limit) {
		c.stop()
	}
	return c.Embedder.Embed(p, n)
}
