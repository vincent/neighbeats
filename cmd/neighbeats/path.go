package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/vincentlark/neighbeats/internal/route"
	"github.com/vincentlark/neighbeats/internal/vecindex"
)

// cappedGraph hands the route search neighbour lists with at most two tracks
// per artist, so one prolific artist cannot fill every candidate list.
type cappedGraph struct {
	ix     *vecindex.Index
	filter vecindex.Filter
}

func (g cappedGraph) Neighbours(id int64, k int) ([]vecindex.Neighbour, error) {
	return g.ix.Search(id, k, g.filter)
}

func (g cappedGraph) Blend(a, b int64, t float64) ([]float32, error) { return g.ix.Blend(a, b, t) }

func (g cappedGraph) SearchVec(q []float32, k int, f vecindex.Filter) ([]vecindex.Neighbour, error) {
	return g.ix.SearchVec(q, k, f)
}

func (g cappedGraph) Similarity(a, b int64) (float32, error) { return g.ix.Similarity(a, b) }

func cmdPath(ctx context.Context, cfgPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("path", flag.ContinueOnError)
	fs.SetOutput(stderr)
	def := route.Defaults()
	fromID := fs.Int64("from-id", 0, "start track id (instead of the first argument)")
	toID := fs.Int64("to-id", 0, "destination track id (instead of the second argument)")
	length := fs.Int("length", 0, "exact number of tracks, spread evenly along the way (0 = cheapest route, any length)")
	hops := fs.Int("max-hops", def.MaxHops, "longest route, in hops")
	nbrs := fs.Int("neighbours", def.Neighbours, "candidates considered per step")
	perArtist := fs.Int("max-per-artist", def.MaxPerArtist, "at most N tracks per artist on the route (0 = no cap)")
	avoid := fs.String("avoid-artist", "", "never pass through artists whose name contains one of these (comma separated)")
	hop := fs.Float64("hop-penalty", def.Hop, "extra cost per hop: higher gives shorter routes")
	expand := fs.Int("max-expand", def.MaxExpand, "search effort: tracks expanded before giving up")
	explain := fs.Bool("explain", false, "show cosine and score of every hop")
	raw := fs.Bool("raw", false, "plain cosine; do not centre the vectors on the library mean")
	out := fs.String("out", "", "write the route as an M3U8 playlist")
	words, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	const usageMsg = "usage: neighbeats path [flags] <from> <to>   (each a path, recording MBID or words; or -from-id / -to-id)"
	need := 2
	if *fromID != 0 {
		need--
	}
	if *toID != 0 {
		need--
	}
	if len(words) != need {
		return errors.New(usageMsg)
	}
	cfg, st, err := open(cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()

	a, b := *fromID, *toID
	if a == 0 {
		if a, err = resolveTrack(ctx, st, words[0], stdout, stderr); err != nil {
			return err
		}
		words = words[1:]
	}
	if b == 0 {
		if b, err = resolveTrack(ctx, st, words[0], stdout, stderr); err != nil {
			return err
		}
	}

	t0 := time.Now()
	ix, err := loadIndex(ctx, st, *raw)
	if err != nil {
		return err
	}
	artists, err := st.ArtistKeys(ctx)
	if err != nil {
		return err
	}
	songs, err := st.DedupeKeys(ctx)
	if err != nil {
		return err
	}
	group := func(id int64) string { return artists[id] }
	opt := route.Options{
		Neighbours: *nbrs, MaxHops: *hops, MaxExpand: *expand, Hop: *hop,
		Base: def.Base, Scale: def.Scale, K: def.K, Heuristic: def.Heuristic,
		MaxPerArtist: *perArtist, Group: group,
		Key:       func(id int64) string { return songs[id] },
		Forbidden: map[int64]bool{},
	}
	for _, name := range strings.Split(*avoid, ",") {
		if name = strings.TrimSpace(name); name == "" {
			continue
		}
		ids, err := st.TrackIDsByArtist(ctx, name)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			fmt.Fprintf(stderr, "warning: no artist matches %q\n", name)
		}
		for _, id := range ids {
			opt.Forbidden[id] = true
		}
	}
	load := time.Since(t0)

	t0 = time.Now()
	g := cappedGraph{ix: ix, filter: vecindex.Filter{MaxPerArtist: 2, Group: group}}
	var r route.Route
	if *length > 0 {
		r, err = route.Spread(g, a, b, *length, opt)
	} else {
		r, err = route.Find(g, a, b, opt)
	}
	if err != nil {
		if errors.Is(err, route.ErrSame) {
			return errors.New("start and destination are the same track")
		}
		return err
	}
	took := time.Since(t0)

	ids := make([]int64, len(r.Steps))
	for i, s := range r.Steps {
		ids[i] = s.ID
	}
	rows, err := st.TracksByIDs(ctx, ids)
	if err != nil {
		return err
	}
	if r.Found {
		fmt.Fprintf(stdout, "route: %d tracks", len(r.Steps))
		if r.Cost > 0 {
			fmt.Fprintf(stdout, ", cost %.2f", r.Cost)
		}
		fmt.Fprintln(stdout)
	} else {
		fmt.Fprintf(stdout, "NO ROUTE: %s\nwhat was found so far:\n", r.Why)
	}
	for i, s := range r.Steps {
		fmt.Fprintf(stdout, "%3d  %s", i+1, label(rows[s.ID]))
		if *explain && i > 0 {
			fmt.Fprintf(stdout, "   (cos %.2f, score %.2f)", s.Cosine, s.Score)
		}
		fmt.Fprintln(stdout)
	}
	if *out != "" && r.Found {
		if err := exportPlaylist(ctx, st, cfg.PathMapper(), *out, rows, ids, stderr); err != nil {
			return err
		}
	}
	fmt.Fprintf(stderr, "searched in %s, %d tracks expanded (index load %s)\n", took.Round(time.Millisecond), r.Expanded, load.Round(time.Millisecond))
	if !r.Found {
		return errors.New("no route found")
	}
	return nil
}
