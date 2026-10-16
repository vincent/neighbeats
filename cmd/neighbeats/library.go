package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/vincent/neighbeats/internal/config"
	"github.com/vincent/neighbeats/internal/embed"
	"github.com/vincent/neighbeats/internal/route"
	"github.com/vincent/neighbeats/internal/store"
	"github.com/vincent/neighbeats/internal/vecindex"
)

// queryError marks an error in what was asked (an unknown mode, a theme
// without lyric vectors), as opposed to a failure while answering. `serve`
// answers it with 400.
type queryError struct{ error }

func (e queryError) Unwrap() error { return e.error }

// errSameTrack is returned when a route would start and end on one track.
var errSameTrack = queryError{errors.New("start and destination are the same track")}

// library is what routes and neighbour lists read: the vectors in memory and
// the per-track lookups. A CLI command loads it once; `serve` keeps it and
// reloads it when the index changes.
type library struct {
	ix        *vecindex.Index
	artists   map[int64]string // artist key per track, see store.ArtistKeys
	songs     map[int64]string // same-song key per track, see store.DedupeKeys
	durations map[int64]float64
	lyrics    *lyricBase // nil without lyric vectors or when not asked for
}

// loadLibrary reads every embedding of the current model and the lookups.
// withLyrics also loads the lyric vectors, which only routes use.
func loadLibrary(ctx context.Context, st *store.Store, raw, withLyrics bool) (*library, error) {
	ix, err := loadIndex(ctx, st, raw)
	if err != nil {
		return nil, err
	}
	lib := &library{ix: ix}
	if lib.artists, err = st.ArtistKeys(ctx); err != nil {
		return nil, err
	}
	if lib.songs, err = st.DedupeKeys(ctx); err != nil {
		return nil, err
	}
	if lib.durations, err = st.Durations(ctx, embed.ModelID); err != nil {
		return nil, err
	}
	if withLyrics {
		if lib.lyrics, err = loadLyricBase(ctx, st, ix); err != nil {
			return nil, err
		}
	}
	return lib, nil
}

// longTracks returns a filter for tracks longer than maxMin minutes (nil when
// the limit is off) and how many embedded tracks it removes. Unknown
// durations are kept.
func (lib *library) longTracks(maxMin float64) (func(int64) bool, int) {
	if maxMin <= 0 {
		return nil, 0
	}
	limit := maxMin * 60
	n := 0
	for _, d := range lib.durations {
		if d > limit {
			n++
		}
	}
	return func(id int64) bool { return lib.durations[id] > limit }, n
}

// pathQuery is one route request, whether it comes from flags or from `serve`.
type pathQuery struct {
	from, to   int64
	length     int           // exact number of tracks; 0 = cheapest route
	opt        route.Options // search knobs; the hooks are filled by planPath
	maxMinutes float64
	avoid      []string // artist name fragments never passed through
	user, mode string   // taste, see applyTaste
	lyric      lyricOptions
}

// planPath runs the route search. Notes on what shaped it (taste, lyrics,
// tracks left out) go to stderr.
func planPath(ctx context.Context, st *store.Store, cfg *config.Config, lib *library, q pathQuery, stderr io.Writer) (route.Route, *lyricSignals, error) {
	group := func(id int64) string { return lib.artists[id] }
	opt := q.opt
	opt.Group = group
	opt.Key = func(id int64) string { return lib.songs[id] }
	opt.Forbidden = map[int64]bool{}
	for _, name := range q.avoid {
		if name = strings.TrimSpace(name); name == "" {
			continue
		}
		ids, err := st.TrackIDsByArtist(ctx, name)
		if err != nil {
			return route.Route{}, nil, err
		}
		if len(ids) == 0 {
			fmt.Fprintf(stderr, "warning: no artist matches %q\n", name)
		}
		for _, id := range ids {
			opt.Forbidden[id] = true
		}
	}
	if err := applyTaste(ctx, st, cfg, q.user, q.mode, &opt, stderr); err != nil {
		return route.Route{}, nil, err
	}
	lyr, err := lib.lyrics.signals(cfg, q.lyric, stderr)
	if err != nil {
		return route.Route{}, nil, err
	}
	lyr.apply(&opt)

	exclude, nLong := lib.longTracks(q.maxMinutes)
	if nLong > 0 {
		fmt.Fprintf(stderr, "left out %d tracks longer than %g min (-max-minutes)\n", nLong, q.maxMinutes)
	}
	g := cappedGraph{ix: lib.ix, filter: vecindex.Filter{MaxPerArtist: 2, Group: group, Exclude: exclude}}
	var r route.Route
	if q.length > 0 {
		r, err = route.Spread(g, q.from, q.to, q.length, opt)
	} else {
		r, err = route.Find(g, q.from, q.to, opt)
	}
	if errors.Is(err, route.ErrSame) {
		err = errSameTrack
	}
	return r, lyr, err
}

// similarQuery is one neighbour-list request.
type similarQuery struct {
	seed       int64
	n          int
	perArtist  int  // at most N tracks per artist (0 = no cap)
	adjacent   bool // allow consecutive results by the same artist
	maxMinutes float64
}

// findSimilar returns the seed's nearest tracks and how many long tracks
// were left out.
func findSimilar(lib *library, q similarQuery) ([]vecindex.Neighbour, int, error) {
	exclude, nLong := lib.longTracks(q.maxMinutes)
	filter := vecindex.Filter{Exclude: exclude}
	if q.perArtist > 0 || !q.adjacent {
		filter.MaxPerArtist, filter.NoAdjacent = q.perArtist, !q.adjacent
		filter.Group = func(id int64) string { return lib.artists[id] }
	}
	hits, err := lib.ix.Search(q.seed, q.n, filter)
	return hits, nLong, err
}
