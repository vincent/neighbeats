package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/vincentlark/neighbeats/internal/config"
	"github.com/vincentlark/neighbeats/internal/embed"
	"github.com/vincentlark/neighbeats/internal/route"
	"github.com/vincentlark/neighbeats/internal/store"
	"github.com/vincentlark/neighbeats/internal/taste"
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
	f.Exclude = g.filter.Exclude
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
	smooth := fs.Float64("smooth", def.Smooth, "with -length: weight of similarity to the previous track against closeness to the ideal waypoint (0 = nearest track to each waypoint)")
	beam := fs.Int("beam", def.Beam, "with -length and -smooth: partial routes kept")
	hops := fs.Int("max-hops", def.MaxHops, "longest route, in hops")
	nbrs := fs.Int("neighbours", def.Neighbours, "candidates considered per step")
	perArtist := fs.Int("max-per-artist", def.MaxPerArtist, "at most N tracks per artist on the route (0 = no cap)")
	maxMin := fs.Float64("max-minutes", defaultMaxMinutes, "leave out tracks longer than N minutes (0 = no limit)")
	avoid := fs.String("avoid-artist", "", "never pass through artists whose name contains one of these (comma separated)")
	hop := fs.Float64("hop-penalty", def.Hop, "extra cost per hop: higher gives shorter routes")
	expand := fs.Int("max-expand", def.MaxExpand, "search effort: tracks expanded before giving up")
	explain := fs.Bool("explain", false, "show cosine and score of every hop")
	raw := fs.Bool("raw", false, "plain cosine; do not centre the vectors on the library mean")
	modeName := fs.String("mode", "", "taste: comfort, explore, rediscover or none (default comfort when history is synced)")
	userName := fs.String("user", "", "whose history to use (default: jellyfin.user in the config)")
	fromLyric := fs.String("from-lyric", "", "start at the track whose lyrics contain this line (instead of the first argument)")
	toLyric := fs.String("to-lyric", "", "destination: the track whose lyrics contain this line (instead of the second argument)")
	lyricWeight := fs.Float64("lyric-weight", defaultLyricWeight, "share of lyric similarity in a hop, for tracks that both have lyric vectors (0 = audio only)")
	langPenalty := fs.Float64("lang-penalty", defaultLangPenalty, "cost of a hop between two different lyric languages (0 = off)")
	theme := fs.String("theme", "", "steer the route toward tracks whose lyrics match this phrase, e.g. \"sea, night\"")
	themeWeight := fs.Float64("theme-weight", defaultThemeWeight, "with -theme: how strongly a matching lyric pulls")
	out := fs.String("out", "", "write the route as an M3U8 playlist")
	words, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	const usageMsg = "usage: neighbeats path [flags] <from> <to>   (each a path, recording MBID or words; or -from-id / -to-id / -from-lyric / -to-lyric)"
	need := 2
	if *fromID != 0 || *fromLyric != "" {
		need--
	}
	if *toID != 0 || *toLyric != "" {
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
	if a == 0 && *fromLyric != "" {
		if a, err = resolveLyric(ctx, st, *fromLyric, stdout, stderr); err != nil {
			return err
		}
	}
	if b == 0 && *toLyric != "" {
		if b, err = resolveLyric(ctx, st, *toLyric, stdout, stderr); err != nil {
			return err
		}
	}
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
		MaxPerArtist: *perArtist, Group: group, Smooth: *smooth, Beam: *beam,
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

	if err := applyTaste(ctx, st, cfg, *userName, *modeName, &opt, stderr); err != nil {
		return err
	}
	lyr, err := loadLyricSignals(ctx, st, cfg, ix, lyricOptions{
		weight: *lyricWeight, langPenalty: *langPenalty, themeWeight: *themeWeight, theme: *theme,
	}, stderr)
	if err != nil {
		return err
	}
	lyr.apply(&opt)
	var shared func(a, b int64) []string
	if lyr != nil && *explain {
		if shared, err = sharedWords(ctx, st); err != nil {
			return err
		}
	}

	exclude, nLong, err := longTracks(ctx, st, *maxMin)
	if err != nil {
		return err
	}
	if nLong > 0 {
		fmt.Fprintf(stderr, "left out %d tracks longer than %g min (-max-minutes)\n", nLong, *maxMin)
	}

	t0 = time.Now()
	g := cappedGraph{ix: ix, filter: vecindex.Filter{MaxPerArtist: 2, Group: group, Exclude: exclude}}
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
			fmt.Fprintf(stdout, "   (cos %.2f, score %.2f", s.Cosine, s.Score)
			if _, note, ok := strings.Cut(s.Reason, "; "); ok {
				fmt.Fprintf(stdout, "; %s", note)
			}
			if ln := lyr.notes(r.Steps[i-1].ID, s.ID, shared); ln != "" {
				fmt.Fprintf(stdout, "; %s", ln)
			}
			fmt.Fprint(stdout, ")")
		}
		fmt.Fprintln(stdout)
	}
	if *out != "" && r.Found {
		if err := exportPlaylist(ctx, st, cfg.PathMapper(), *out, rows, ids, stderr); err != nil {
			return err
		}
	}
	if len(r.Steps) > 1 {
		var sum float64
		low := 0
		minCos := float32(1)
		for _, s := range r.Steps[1:] {
			sum += float64(s.Cosine)
			minCos = min(minCos, s.Cosine)
			if s.Cosine < 0.3 {
				low++
			}
		}
		fmt.Fprintf(stderr, "hops: mean cosine %.2f, weakest %.2f, %d of %d under 0.30\n", sum/float64(len(r.Steps)-1), minCos, low, len(r.Steps)-1)
	}
	fmt.Fprintf(stderr, "searched in %s, %d tracks expanded (index load %s)\n", took.Round(time.Millisecond), r.Expanded, load.Round(time.Millisecond))
	if !r.Found {
		return errors.New("no route found")
	}
	return nil
}

// applyTaste loads the user's synced history and puts the mode's cost
// adjustments and skipped tracks into the route options. Without synced
// history the default mode is skipped silently; asking for a mode explicitly
// without history is an error.
func applyTaste(ctx context.Context, st *store.Store, cfg *config.Config, userName, modeName string, opt *route.Options, stderr io.Writer) error {
	explicit := modeName != ""
	if modeName == "" {
		modeName = string(taste.Comfort)
	}
	mode, err := taste.ParseMode(modeName)
	if err != nil {
		return err
	}
	if mode == taste.None {
		return nil
	}
	if userName == "" {
		userName = cfg.Jellyfin.User
	}
	if userName == "" {
		if explicit {
			return errors.New("which user's history? pass -user NAME or set jellyfin.user in the config")
		}
		return nil
	}
	durations, err := st.Durations(ctx, embed.ModelID)
	if err != nil {
		return err
	}
	prof, err := taste.Load(ctx, st, taste.Key(userName), durations)
	if err != nil {
		return err
	}
	if len(prof.ByTrack) == 0 {
		if explicit {
			return fmt.Errorf("no history stored for %q: run `neighbeats sync-jellyfin` first", userName)
		}
		return nil
	}
	now := time.Now()
	opt.Bias = prof.Bias(mode, now)
	skipped := prof.Skipped(now)
	for id := range skipped {
		opt.Forbidden[id] = true
	}
	fmt.Fprintf(stderr, "taste: %s mode for %s (%d tracks with history, %d recently skipped avoided)\n", mode, userName, len(prof.ByTrack), len(skipped))
	return nil
}
