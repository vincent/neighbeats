package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/vincentlark/neighbeats/internal/embed"
	"github.com/vincentlark/neighbeats/internal/m3u"
	"github.com/vincentlark/neighbeats/internal/pathmap"
	"github.com/vincentlark/neighbeats/internal/store"
	"github.com/vincentlark/neighbeats/internal/vecindex"
)

// defaultK is the exp(-k*distance) falloff used to turn cosine similarity
// into the 0..1 score shown by `similar` (see docs/roadmap/M2-neighbour-index.md).
const defaultK = 2.3

// defaultMaxMinutes leaves out tracks that are probably mistagged audiobooks,
// DJ mixes or radio shows.
const defaultMaxMinutes = 15

// longTracks returns a filter for tracks longer than maxMin minutes (nil when
// the limit is off) and how many embedded tracks it removes. Unknown
// durations are kept.
func longTracks(ctx context.Context, st *store.Store, maxMin float64) (func(int64) bool, int, error) {
	if maxMin <= 0 {
		return nil, 0, nil
	}
	durations, err := st.Durations(ctx, embed.ModelID)
	if err != nil {
		return nil, 0, err
	}
	limit := maxMin * 60
	n := 0
	for _, d := range durations {
		if d > limit {
			n++
		}
	}
	return func(id int64) bool { return durations[id] > limit }, n, nil
}

// loadIndex reads every embedding of the current model into memory.
func loadIndex(ctx context.Context, st *store.Store, raw bool) (*vecindex.Index, error) {
	embs, err := st.Embeddings(ctx, embed.ModelID)
	if err != nil {
		return nil, err
	}
	if len(embs) == 0 {
		return nil, errors.New("no embeddings yet; run `neighbeats analyze`")
	}
	ids := make([]int64, len(embs))
	vecs := make([][]float32, len(embs))
	for i, e := range embs {
		ids[i], vecs[i] = e.TrackID, e.Vector
	}
	return vecindex.Build(ids, vecs, vecindex.Options{Center: !raw})
}

func cmdSimilar(ctx context.Context, cfgPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("similar", flag.ContinueOnError)
	fs.SetOutput(stderr)
	n := fs.Int("n", 20, "number of neighbours")
	perArtist := fs.Int("max-per-artist", 2, "keep at most N tracks per artist (0 = no cap)")
	maxMin := fs.Float64("max-minutes", defaultMaxMinutes, "leave out tracks longer than N minutes (0 = no limit)")
	adjacent := fs.Bool("allow-adjacent", false, "allow consecutive results by the same artist")
	raw := fs.Bool("raw", false, "plain cosine; do not centre the vectors on the library mean")
	id := fs.Int64("id", 0, "seed track id (from an earlier listing)")
	k := fs.Float64("k", defaultK, "falloff of the score: exp(-k * (1 - cosine))")
	out := fs.String("out", "", "also write the seed and its neighbours as an M3U8 playlist")
	words, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if *id == 0 && len(words) == 0 {
		return errors.New("usage: neighbeats similar [-n N] [-max-per-artist N] [-raw] [-id N | path | recording-mbid | words...]")
	}
	cfg, st, err := open(cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()

	seed := *id
	if seed == 0 {
		if seed, err = resolveTrack(ctx, st, strings.Join(words, " "), stdout, stderr); err != nil {
			return err
		}
	}

	t0 := time.Now()
	ix, err := loadIndex(ctx, st, *raw)
	if err != nil {
		return err
	}
	load := time.Since(t0)

	exclude, nLong, err := longTracks(ctx, st, *maxMin)
	if err != nil {
		return err
	}
	filter := vecindex.Filter{Exclude: exclude}
	if *perArtist > 0 || !*adjacent {
		keys, err := st.ArtistKeys(ctx)
		if err != nil {
			return err
		}
		filter = vecindex.Filter{MaxPerArtist: *perArtist, NoAdjacent: !*adjacent, Group: func(id int64) string { return keys[id] }, Exclude: exclude}
	}
	t0 = time.Now()
	hits, err := ix.Search(seed, *n, filter)
	if err != nil {
		return err
	}
	search := time.Since(t0)

	ids := []int64{seed}
	for _, h := range hits {
		ids = append(ids, h.ID)
	}
	rows, err := st.TracksByIDs(ctx, ids)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "like: %s\n", label(rows[seed]))
	for i, h := range hits {
		fmt.Fprintf(stdout, "%3d  %.2f  cos %5.2f  %s  (id %d)\n", i+1, vecindex.Score(h.Cosine, *k), h.Cosine, label(rows[h.ID]), h.ID)
	}
	if *out != "" {
		if err := exportPlaylist(ctx, st, cfg.PathMapper(), *out, rows, ids, stderr); err != nil {
			return err
		}
	}
	if nLong > 0 {
		fmt.Fprintf(stderr, "left out %d tracks longer than %g min (-max-minutes)\n", nLong, *maxMin)
	}
	fmt.Fprintf(stderr, "searched %d tracks in %s (index load %s)\n", ix.Len(), search.Round(100*time.Microsecond), load.Round(time.Millisecond))
	return nil
}

func label(r store.TrackRow) string {
	switch {
	case r.Artist != "" && r.Title != "":
		return r.Artist + " - " + r.Title
	case r.Title != "":
		return r.Title
	}
	return r.Path
}

// parseInterspersed parses flags that may appear anywhere among the words
// (`similar vitalic mort -n 5`); the standard flag package stops at the first
// non-flag. Use "--" to pass a word that starts with a dash.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var words []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return words, nil
		}
		if len(args) >= len(rest)+1 && args[len(args)-len(rest)-1] == "--" {
			return append(words, rest...), nil
		}
		words = append(words, rest[0])
		args = rest[1:]
	}
}

// exportPlaylist writes the tracks, in the order of ids, as an M3U8 with the
// paths as the media server sees them (path_map).
func exportPlaylist(ctx context.Context, st *store.Store, pm *pathmap.Mapper, out string,
	rows map[int64]store.TrackRow, ids []int64, stderr io.Writer) error {
	durations, err := st.Durations(ctx, embed.ModelID)
	if err != nil {
		return err
	}
	var entries []m3u.Entry
	unmapped := 0
	for _, id := range ids {
		r := rows[id]
		p, ok := pm.ToRemote(r.Path)
		if !ok {
			unmapped++
		}
		entries = append(entries, m3u.Entry{Path: p, Artist: r.Artist, Title: r.Title, Duration: durations[id]})
	}
	if unmapped > 0 {
		fmt.Fprintf(stderr, "warning: %d paths matched no path_map entry and are written as stored\n", unmapped)
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	if err := m3u.Write(f, entries); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	fmt.Fprintf(stderr, "wrote %d tracks to %s\n", len(entries), out)
	return nil
}

// resolveTrack finds the one embedded track a path, recording MBID or words
// refer to, or a track id. Several matches are listed with their ids and reported as an error.
func resolveTrack(ctx context.Context, st *store.Store, query string, stdout, stderr io.Writer) (int64, error) {
	// An id as printed by earlier listings: "#3476", "id:3476" or just 3476.
	if n, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(query), "id:"), "#"), 10, 64); err == nil {
		if _, ok, err := st.EmbeddedTrack(ctx, embed.ModelID, n); err != nil {
			return 0, err
		} else if ok {
			return n, nil
		}
	}
	rows, err := st.FindTracks(ctx, embed.ModelID, query, 15)
	if err != nil {
		return 0, err
	}
	switch len(rows) {
	case 0:
		return 0, fmt.Errorf("no embedded track matches %q (is it analysed? try `neighbeats stats`)", query)
	case 1:
		return rows[0].ID, nil
	}
	fmt.Fprintf(stderr, "%d tracks match %q; pick one by id:\n", len(rows), query)
	for _, r := range rows {
		fmt.Fprintf(stdout, "  id %-6d %s\n", r.ID, label(r))
	}
	return 0, errors.New("ambiguous track")
}
