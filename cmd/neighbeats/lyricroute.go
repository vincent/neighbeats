package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/vincent/neighbeats/internal/config"
	"github.com/vincent/neighbeats/internal/embed"
	"github.com/vincent/neighbeats/internal/keywords"
	"github.com/vincent/neighbeats/internal/lang"
	"github.com/vincent/neighbeats/internal/route"
	"github.com/vincent/neighbeats/internal/store"
	"github.com/vincent/neighbeats/internal/textembed"
	"github.com/vincent/neighbeats/internal/vecindex"
)

// Defaults of the lyric side of a route. The weights are first guesses, to be
// tuned by listening (see docs/roadmap/L-lyrics.md).
const (
	defaultLyricWeight = 0.2 // share of the lyric similarity in a hop's cosine
	defaultLangPenalty = 0.5 // cost of a hop between two different languages (units of the taste bias)
	defaultThemeWeight = 3.0 // cost taken off per unit of cosine between the theme and a track's lyrics
	calibrationTracks  = 40  // lyric tracks sampled to learn what a good audio neighbour scores in lyrics
)

type lyricOptions struct {
	weight, langPenalty, themeWeight float64
	theme                            string
}

// lyricSignals is everything the route needs from lyrics.
type lyricSignals struct {
	lx    *vecindex.Index
	langs map[int64]string
	mu    float64 // mean lyric cosine between audio neighbours
	theme []float32
	opt   lyricOptions
}

// loadLyricSignals reads the lyric vectors and languages. It returns nil when
// there are no lyric vectors (routes then use audio alone) unless a theme was
// asked for, which needs them.
func loadLyricSignals(ctx context.Context, st *store.Store, cfg *config.Config, ax *vecindex.Index,
	opt lyricOptions, stderr io.Writer) (*lyricSignals, error) {
	embs, err := st.LyricEmbeddings(ctx, textembed.ModelID)
	if err != nil {
		return nil, err
	}
	if len(embs) == 0 {
		if opt.theme != "" {
			return nil, errors.New("-theme needs lyric vectors: run `neighbeats embed-lyrics` first")
		}
		return nil, nil
	}
	ids := make([]int64, len(embs))
	vecs := make([][]float32, len(embs))
	for i, e := range embs {
		ids[i], vecs[i] = e.TrackID, e.Vector
	}
	lx, err := vecindex.Build(ids, vecs, vecindex.Options{Center: true})
	if err != nil {
		return nil, err
	}
	langs, err := st.LyricLangs(ctx)
	if err != nil {
		return nil, err
	}
	for id, l := range langs {
		if !lang.Known(l) {
			delete(langs, id)
		}
	}
	s := &lyricSignals{lx: lx, langs: langs, opt: opt}
	if opt.weight > 0 {
		if s.mu, err = calibrate(ax, lx); err != nil {
			return nil, err
		}
	}
	if opt.theme != "" {
		m, err := openTextModel(cfg, string(embed.ProviderCPU), nil) // one phrase: the CPU is plenty
		if err != nil {
			return nil, err
		}
		defer m.Close()
		v, err := m.Embed(textembed.QueryPrefix, []string{opt.theme})
		if err != nil {
			return nil, err
		}
		if s.theme, err = lx.Project(v[0]); err != nil {
			return nil, err
		}
	}
	fmt.Fprintf(stderr, "lyrics: %d tracks with vectors, %d with a known language", lx.Len(), len(langs))
	if opt.weight > 0 {
		fmt.Fprintf(stderr, ", blend %.2f (typical neighbour %.2f)", opt.weight, s.mu)
	}
	if opt.theme != "" {
		fmt.Fprintf(stderr, ", theme %q", opt.theme)
	}
	fmt.Fprintln(stderr)
	return s, nil
}

// calibrate returns the mean lyric cosine between a track and its audio
// neighbours, over a spread sample of tracks. The lyric term of a hop is its
// distance from this typical value, so pairs whose lyrics agree better than
// usual gain and pairs that agree worse lose, and tracks without lyrics (no
// term at all) are neither favoured nor penalised.
func calibrate(ax, lx *vecindex.Index) (float64, error) {
	ids := lx.IDs()
	step := max(1, len(ids)/calibrationTracks)
	var sum float64
	n := 0
	for i := 0; i < len(ids); i += step {
		if !ax.Has(ids[i]) {
			continue
		}
		ns, err := ax.Neighbours(ids[i], 10)
		if err != nil {
			return 0, err
		}
		for _, nb := range ns {
			if c, err := lx.Similarity(ids[i], nb.ID); err == nil {
				sum += float64(c)
				n++
			}
		}
	}
	if n < 20 {
		return 0, nil // too few pairs with lyrics on both sides to say
	}
	return sum / float64(n), nil
}

// apply puts the lyric hooks into the route options.
func (s *lyricSignals) apply(o *route.Options) {
	if s == nil {
		return
	}
	if s.opt.weight > 0 {
		w, mu := float32(s.opt.weight), float32(s.mu)
		o.Pair = func(a, b int64, cos float32) float32 {
			if l, err := s.lx.Similarity(a, b); err == nil {
				return cos + w*(l-mu)
			}
			return cos
		}
	}
	if s.opt.langPenalty > 0 && len(s.langs) > 0 {
		o.Lang = func(id int64) string { return s.langs[id] }
		o.LangPenalty = s.opt.langPenalty
	}
	if s.theme != nil && s.opt.themeWeight > 0 {
		o.Theme = func(id int64) float64 {
			c, _ := s.lx.CosineTo(s.theme, id)
			return max(0, float64(c))
		}
		o.ThemeWeight = s.opt.themeWeight
	}
}

// notes describes, for -explain, what the lyrics say about the hop from a to b.
func (s *lyricSignals) notes(a, b int64, kw func(a, b int64) []string) string {
	if s == nil {
		return ""
	}
	var parts []string
	if l, err := s.lx.Similarity(a, b); err == nil {
		parts = append(parts, fmt.Sprintf("lyrics %.2f", l))
		if kw != nil {
			if words := kw(a, b); len(words) > 0 {
				parts = append(parts, "both about: "+strings.Join(words, ", "))
			}
		}
	}
	return strings.Join(parts, "; ")
}

// sharedWords returns a function finding the telling words two tracks' lyrics
// share, with the document frequencies taken over the whole library.
func sharedWords(ctx context.Context, st *store.Store) (func(a, b int64) []string, error) {
	c := keywords.NewCorpus()
	if err := st.EachLyric(ctx, func(_ int64, text string) { c.Add(text) }); err != nil {
		return nil, err
	}
	cache := map[int64]string{}
	return func(a, b int64) []string {
		var missing []int64
		for _, id := range []int64{a, b} {
			if _, ok := cache[id]; !ok {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			texts, err := st.LyricTexts(ctx, missing)
			if err != nil {
				return nil
			}
			for _, id := range missing {
				cache[id] = texts[id]
			}
		}
		return c.Shared(cache[a], cache[b], 3)
	}, nil
}

// resolveLyric finds the one embedded track whose lyrics contain the line (as
// a phrase, else all its words). Several matches are listed with their ids.
func resolveLyric(ctx context.Context, st *store.Store, line string, stdout, stderr io.Writer) (int64, error) {
	var found []store.LyricsHit
	for _, phrase := range []bool{true, false} {
		hits, err := st.SearchLyrics(ctx, line, phrase, 50)
		if err != nil {
			return 0, err
		}
		for _, h := range hits {
			if _, ok, err := st.EmbeddedTrack(ctx, embed.ModelID, h.TrackID); err != nil {
				return 0, err
			} else if ok {
				found = append(found, h)
			}
		}
		if len(found) > 0 {
			break
		}
	}
	switch len(found) {
	case 0:
		return 0, fmt.Errorf("no analysed track has lyrics matching %q (try `neighbeats lyrics search`)", line)
	case 1:
		return found[0].TrackID, nil
	}
	fmt.Fprintf(stderr, "%d tracks have lyrics matching %q; pick one by id:\n", len(found), line)
	for _, h := range found[:min(len(found), 15)] {
		fmt.Fprintf(stdout, "  id %-6d %s - %s\n      %s\n", h.TrackID, h.Artist, h.Title, strings.ReplaceAll(h.Snippet, "\n", " / "))
	}
	return 0, errors.New("ambiguous lyric")
}
