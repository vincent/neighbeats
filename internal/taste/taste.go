package taste

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/vincentlark/neighbeats/internal/store"
)

// Mode picks how listening history bends route costs.
type Mode string

const (
	None       Mode = "none"       // ignore history
	Comfort    Mode = "comfort"    // favourites and well-loved tracks, nothing recent
	Explore    Mode = "explore"    // tracks you rarely or never played
	Rediscover Mode = "rediscover" // tracks you loved but have not heard for months
)

// ParseMode validates a mode name.
func ParseMode(s string) (Mode, error) {
	switch m := Mode(strings.ToLower(s)); m {
	case None, Comfort, Explore, Rediscover:
		return m, nil
	}
	return "", fmt.Errorf("mode must be comfort, explore, rediscover or none, got %q", s)
}

const (
	// A play shorter than this (or half the track, if that is less) is a skip.
	skipSeconds = 30.0
	// A skip keeps a track out of routes for this long, unless it is a favourite.
	skipMemory = 30 * 24 * time.Hour
	day        = 24 * time.Hour
)

// Profile is one user's history, ready to score tracks.
type Profile struct {
	ByTrack map[int64]store.Taste
	// LastSkip is the unix time of the latest skip of each track.
	LastSkip map[int64]int64
}

// Load reads the user's snapshot and derives skips from the play events.
// durations (seconds per track) lets short tracks count a partial play as a
// full one; a missing duration means the 30 s rule alone.
func Load(ctx context.Context, st *store.Store, user string, durations map[int64]float64) (*Profile, error) {
	taste, err := st.LoadTaste(ctx, user)
	if err != nil {
		return nil, err
	}
	events, err := st.PlayEvents(ctx, user, 0)
	if err != nil {
		return nil, err
	}
	p := &Profile{ByTrack: taste, LastSkip: map[int64]int64{}}
	for _, e := range events {
		limit := skipSeconds
		if d := durations[e.TrackID]; d > 0 {
			limit = math.Min(limit, d/2)
		}
		if e.Seconds < limit {
			if at := e.AtMs / 1000; at > p.LastSkip[e.TrackID] {
				p.LastSkip[e.TrackID] = at
			}
		}
	}
	return p, nil
}

// Skipped returns the tracks skipped recently and not marked as favourites:
// the routes keep them out as intermediates.
func (p *Profile) Skipped(now time.Time) map[int64]bool {
	out := map[int64]bool{}
	for id, at := range p.LastSkip {
		if now.Sub(time.Unix(at, 0)) <= skipMemory && !p.ByTrack[id].Favourite {
			out[id] = true
		}
	}
	return out
}

// Bias returns the cost change (negative = preferred) of entering a track,
// with a short reason when the change is big enough to mention.
func (p *Profile) Bias(mode Mode, now time.Time) func(id int64) (float64, string) {
	if mode == None || mode == "" {
		return nil
	}
	return func(id int64) (float64, string) {
		t, played := p.ByTrack[id]
		var parts []part
		add := func(d float64, why string) {
			if d != 0 {
				parts = append(parts, part{d, why})
			}
		}
		age := math.Inf(1)
		if played && t.LastPlayed > 0 {
			age = now.Sub(time.Unix(t.LastPlayed, 0)).Hours() / 24
		}
		// Anything heard in the last days is penalised in every mode.
		switch {
		case age < 3:
			add(0.5, playedAgo(age))
		case age < 14:
			add(0.2, playedAgo(age))
		}
		loved := math.Min(math.Log1p(float64(t.Plays)), 3)
		switch mode {
		case Comfort:
			if t.Favourite {
				add(-0.30, "favourite")
			}
			add(-0.06*loved, fmt.Sprintf("played %d times", t.Plays))
		case Explore:
			if !played || t.Plays == 0 {
				add(-0.25, "never played")
			} else {
				add(0.05*loved, fmt.Sprintf("played %d times", t.Plays))
			}
			if t.Favourite {
				add(-0.05, "favourite")
			}
		case Rediscover:
			if t.Favourite {
				add(-0.15, "favourite")
			}
			if played && t.Plays > 0 && age >= 30 {
				// Ramp up from 30 days, full bonus from 120 days.
				add(-0.35*math.Min(1, (age-30)/90), fmt.Sprintf("not played for %s", ago(age)))
			}
		}
		return sum(parts)
	}
}

type part struct {
	delta float64
	why   string
}

// sum adds the parts and names the one that moved the cost most, if it moved
// it by at least 0.1.
func sum(parts []part) (float64, string) {
	var total float64
	var top part
	for _, p := range parts {
		total += p.delta
		if math.Abs(p.delta) > math.Abs(top.delta) {
			top = p
		}
	}
	if math.Abs(top.delta) < 0.1 {
		return total, ""
	}
	return total, top.why
}

func ago(days float64) string {
	switch {
	case days < 60:
		return fmt.Sprintf("%.0f days", days)
	case days < 730:
		return fmt.Sprintf("%.0f months", days/30)
	}
	return fmt.Sprintf("%.0f years", days/365)
}

func playedAgo(days float64) string {
	if days < 1 {
		return "played today"
	}
	return "played " + ago(days) + " ago"
}
