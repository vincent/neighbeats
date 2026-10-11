package route

import (
	"errors"
	"fmt"

	"github.com/vincentlark/neighbeats/internal/vecindex"
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
// space and, for each, takes the nearest track that satisfies the
// constraints. Unlike Find it never loiters near the destination:
// the journey takes the whole length. Constraints (forbidden tracks, copies,
// artist spread) are the same as for Find.
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
	cur := &node{id: a}
	route := Route{Found: true, Steps: []Step{{ID: a}}}
	pool := opt.Neighbours * 5
	for i := 1; i < n-1; i++ {
		q, err := g.Blend(a, b, float64(i)/float64(n-1))
		if err != nil {
			return Route{}, err
		}
		near, err := g.SearchVec(q, pool, vecindex.Filter{})
		if err != nil {
			return Route{}, err
		}
		var best vecindex.Neighbour
		bestVal, bestSim := -1e9, float32(0)
		for _, c := range near {
			if c.ID == a || c.ID == b || !allowed(cur, c.ID, b, &opt) {
				continue
			}
			sim, err := g.Similarity(cur.id, c.ID)
			if err != nil {
				return Route{}, err
			}
			if v := float64(c.Cosine); v > bestVal {
				best, bestVal, bestSim = c, v, sim
			}
		}
		if bestVal == -1e9 {
			route.Found = false
			route.Why = fmt.Sprintf("no usable track near waypoint %d of %d (constraints too tight?)", i, n-2)
			return route, nil
		}
		cur = &node{id: best.ID, parent: cur, hops: cur.hops + 1}
		route.Steps = append(route.Steps, Step{ID: best.ID, Cosine: bestSim,
			Score:  vecindex.Score(bestSim, opt.K),
			Reason: fmt.Sprintf("waypoint %d of %d", i, n-2)})
	}
	sim, err := g.Similarity(cur.id, b)
	if err != nil {
		return Route{}, err
	}
	route.Steps = append(route.Steps, Step{ID: b, Cosine: sim, Score: vecindex.Score(sim, opt.K), Reason: "destination"})
	return route, nil
}
