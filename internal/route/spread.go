package route

import (
	"errors"
	"fmt"
	"sort"

	"github.com/vincent/neighbeats/internal/vecindex"
)

// VectorGraph is a Graph that can also look up tracks near a point in the
// embedding space.
type VectorGraph interface {
	Graph
	Blend(a, b int64, t float64) ([]float32, error)
	SearchVec(q []float32, k int, f vecindex.Filter) ([]vecindex.Neighbour, error)
}

// Spread builds a route of exactly n tracks (n >= 2) from a to b. It places
// n-2 evenly spaced waypoints on the arc between the two tracks in embedding
// space and picks one track near each. Unlike Find it never loiters near the
// destination: the journey takes the whole length.
//
// Picking the nearest track to every waypoint on its own can leave two
// consecutive picks that sound nothing alike. With Options.Smooth > 0 a beam
// search keeps the Beam best partial routes and scores each candidate by its
// distance to the waypoint plus Smooth times its distance to the previous
// track (and, at the end, to the destination). Smooth 0 is the nearest-track
// pick. Constraints (forbidden tracks, copies, artist spread) hold along
// every kept route.
func Spread(g VectorGraph, a, b int64, n int, opt Options) (Route, error) {
	opt = opt.withDefaults()
	if a == b {
		return Route{}, ErrSame
	}
	if n < 2 {
		return Route{}, errors.New("a route needs at least 2 tracks")
	}
	if _, err := g.Similarity(a, b); err != nil {
		return Route{}, err
	}
	beamWidth := opt.Beam
	if opt.Smooth <= 0 || beamWidth < 1 {
		beamWidth = 1
	}
	const biasWeight = 0.12 // cosine given up per unit of taste cost
	pool := opt.Neighbours * 8

	type state struct {
		n    *node
		cost float64
	}
	beam := []state{{n: &node{id: a}}}
	for i := 1; i < n-1; i++ {
		q, err := g.Blend(a, b, float64(i)/float64(n-1))
		if err != nil {
			return Route{}, err
		}
		near, err := g.SearchVec(q, pool, vecindex.Filter{})
		if err != nil {
			return Route{}, err
		}
		var next []state
		for _, s := range beam {
			for _, c := range near {
				if c.ID == a || c.ID == b || !allowed(s.n, c.ID, b, &opt) {
					continue
				}
				cost := s.cost + (1 - float64(c.Cosine))
				sim, err := g.Similarity(s.n.id, c.ID)
				if err != nil {
					return Route{}, err
				}
				cost += opt.Smooth * (1 - float64(opt.pair(s.n.id, c.ID, sim)))
				if i == n-2 && opt.Smooth > 0 {
					end, err := g.Similarity(c.ID, b)
					if err != nil {
						return Route{}, err
					}
					cost += opt.Smooth * (1 - float64(opt.pair(c.ID, b, end)))
				}
				if i == n-2 { // the last hop is to the destination
					last, _ := opt.hopExtra(c.ID, b)
					cost += biasWeight * last
				}
				var note string
				if opt.Bias != nil {
					var d float64
					d, note = opt.Bias(c.ID)
					cost += biasWeight * d
				}
				extra, enote := opt.hopExtra(s.n.id, c.ID)
				cost += biasWeight * extra
				note = joinNotes(note, enote)
				next = append(next, state{&node{id: c.ID, parent: s.n, hops: i, cos: sim,
					score: vecindex.Score(sim, opt.K), note: note}, cost})
			}
		}
		if len(next) == 0 {
			r := spreadRoute(g, beam[0].n, b, n, opt)
			r.Found = false
			r.Why = fmt.Sprintf("no usable track near waypoint %d of %d (constraints too tight?)", i, n-2)
			return r, nil
		}
		sort.SliceStable(next, func(x, y int) bool { return next[x].cost < next[y].cost })
		beam = beam[:0]
		seen := map[int64]bool{}
		for _, s := range next {
			// Different routes ending on the same track would crowd the beam.
			if seen[s.n.id] {
				continue
			}
			seen[s.n.id] = true
			beam = append(beam, s)
			if len(beam) == beamWidth {
				break
			}
		}
	}
	return spreadRoute(g, beam[0].n, b, n, opt), nil
}

// spreadRoute turns the chain of picks into a Route, appending the
// destination when the chain is complete.
func spreadRoute(g Graph, end *node, b int64, n int, opt Options) Route {
	var rev []*node
	for p := end; p != nil; p = p.parent {
		rev = append(rev, p)
	}
	r := Route{Found: true}
	for i := len(rev) - 1; i >= 0; i-- {
		nd := rev[i]
		st := Step{ID: nd.id, Cosine: nd.cos, Score: nd.score}
		if nd.parent != nil {
			st.Reason = waypointReason(nd.hops, n-2, nd.note)
		}
		r.Steps = append(r.Steps, st)
	}
	if end.hops == n-2 {
		last := Step{ID: b, Reason: "destination"}
		if sim, err := g.Similarity(end.id, b); err == nil {
			last.Cosine, last.Score = sim, vecindex.Score(sim, opt.K)
		}
		r.Steps = append(r.Steps, last)
	}
	return r
}
func waypointReason(i, n int, note string) string {
	r := fmt.Sprintf("waypoint %d of %d", i, n)
	if note != "" {
		r += "; " + note
	}
	return r
}
