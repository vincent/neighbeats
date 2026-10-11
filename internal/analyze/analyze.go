// Package analyze embeds the indexed tracks: parallel decode and mel
// extraction feed a single inference stage, and a writer stores the results.
// The run is resumable: tracks that already have an up-to-date embedding for
// the model are skipped.
package analyze

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vincentlark/neighbeats/internal/audio"
	"github.com/vincentlark/neighbeats/internal/embed"
	"github.com/vincentlark/neighbeats/internal/store"
)

const maxErrLen = 200

// Decoder yields a clip of a track. *audio.Decoder implements it.
type Decoder interface {
	MiddleClip(ctx context.Context, path string, clipSeconds float64, rate int) (audio.Clip, error)
}

// Options configures a run.
type Options struct {
	Decoder     Decoder
	Embedder    embed.Embedder
	Workers     int     // concurrent decode+mel workers
	ClipSeconds float64 // length of the clip taken from the middle of each track
	RetryErrors bool    // retry tracks that failed on the same file version
	Limit       int     // stop after this many tracks (0 = all)
	Logf        func(format string, args ...any)
}

// Timings is the cumulative time spent per stage across all workers, so the
// per-track average is value/Done.
type Timings struct {
	Decode, Mel, Infer, Write time.Duration
}

// Result summarises a run.
type Result struct {
	Pending int // tracks that needed work at the start
	Done    int
	Failed  int
	Wall    time.Duration
	Timings
}

type prepared struct {
	track   store.PendingTrack
	clip    audio.Clip
	patches []float32
	n       int
}

// Run embeds every pending track.
func Run(ctx context.Context, st *store.Store, opt Options) (Result, error) {
	logf := opt.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if opt.Workers < 1 {
		opt.Workers = 1
	}
	pending, err := st.PendingEmbeddings(ctx, embed.ModelID, opt.RetryErrors)
	if err != nil {
		return Result{}, err
	}
	if opt.Limit > 0 && len(pending) > opt.Limit {
		pending = pending[:opt.Limit]
	}
	res := Result{Pending: len(pending)}
	if len(pending) == 0 {
		return res, nil
	}
	start := time.Now()

	var decodeNs, melNs, inferNs, writeNs atomic.Int64
	var done, failed atomic.Int64
	var firstErr atomic.Pointer[error]
	setErr := func(e error) { firstErr.CompareAndSwap(nil, &e) }

	// A fatal error (database, inference) cancels the whole run; a per-track
	// decode failure is recorded and the run continues.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan store.PendingTrack)
	ready := make(chan prepared, opt.Workers)
	embedded := make(chan store.Embedding, opt.Workers)

	fail := func(t store.PendingTrack, err error) {
		failed.Add(1)
		msg := err.Error()
		if len(msg) > maxErrLen {
			msg = msg[:maxErrLen]
		}
		logf("skip %s: %s", t.Path, msg)
		if werr := st.SaveEmbeddingError(ctx, t, embed.ModelID, msg); werr != nil {
			setErr(werr)
			cancel()
		}
	}

	mel := embed.NewMel()
	var workers sync.WaitGroup
	for range opt.Workers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for t := range jobs {
				t0 := time.Now()
				clip, err := opt.Decoder.MiddleClip(ctx, t.Path, opt.ClipSeconds, embed.SampleRate)
				decodeNs.Add(int64(time.Since(t0)))
				if err != nil {
					if ctx.Err() == nil {
						fail(t, err)
					}
					continue
				}
				if len(clip.Samples) < embed.MinSamples {
					fail(t, fmt.Errorf("clip too short: %d samples, need %d", len(clip.Samples), embed.MinSamples))
					continue
				}
				t0 = time.Now()
				patches, n := embed.MakePatches(mel.LogMel(clip.Samples))
				melNs.Add(int64(time.Since(t0)))
				select {
				case ready <- prepared{track: t, clip: clip, patches: patches, n: n}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// Single inference stage: with a GPU one stream of batches is what we
	// want; on the CPU ONNX Runtime parallelises inside the call.
	var infer sync.WaitGroup
	infer.Add(1)
	go func() {
		defer infer.Done()
		defer close(embedded)
		for p := range ready {
			if ctx.Err() != nil {
				continue // drain
			}
			t0 := time.Now()
			out, err := opt.Embedder.Embed(p.patches, p.n)
			inferNs.Add(int64(time.Since(t0)))
			if err != nil {
				setErr(fmt.Errorf("inference on %s: %w", p.track.Path, err))
				cancel()
				continue
			}
			vec, err := embed.Pool(out, p.n)
			if err != nil {
				setErr(err)
				cancel()
				continue
			}
			// Always hand over finished work: the writer drains until close.
			embedded <- store.Embedding{
				TrackID: p.track.ID, Model: embed.ModelID, Vector: vec, Patches: p.n,
				ClipStart: p.clip.Start, ClipLen: float64(len(p.clip.Samples)) / embed.SampleRate,
				Duration: p.clip.Duration, Size: p.track.Size, MtimeNs: p.track.MtimeNs,
			}
		}
	}()

	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		for e := range embedded {
			t0 := time.Now()
			// A fresh context: finish storing what was already computed
			// when the run is interrupted.
			err := st.SaveEmbedding(context.WithoutCancel(ctx), e)
			writeNs.Add(int64(time.Since(t0)))
			if err != nil {
				setErr(err)
				cancel()
				continue
			}
			if n := done.Add(1); n%100 == 0 {
				logf("%d/%d embedded (%.1f tracks/s)", n, len(pending), float64(n)/time.Since(start).Seconds())
			}
		}
	}()

feed:
	for _, t := range pending {
		select {
		case jobs <- t:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	workers.Wait()
	close(ready)
	infer.Wait()
	writer.Wait()

	res.Done, res.Failed, res.Wall = int(done.Load()), int(failed.Load()), time.Since(start)
	res.Timings = Timings{
		Decode: time.Duration(decodeNs.Load()), Mel: time.Duration(melNs.Load()),
		Infer: time.Duration(inferNs.Load()), Write: time.Duration(writeNs.Load()),
	}
	if e := firstErr.Load(); e != nil {
		return res, *e
	}
	return res, ctx.Err()
}
