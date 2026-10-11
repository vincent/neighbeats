// Package route finds a playlist that travels from one track to another by
// hopping between similar tracks.
//
// The search is A* over a graph that is never built: expanding a track asks
// the index for its nearest neighbours. Edge cost follows the reference route
// engine: base + scale*(1-score) + a constant per hop, so a hop is never free
// and long chains of mediocre hops lose to shorter ones. The heuristic (how
// far a track still is from the destination) is deliberately NOT admissible:
// it trades guaranteed optimality for expanding hundreds of tracks instead of
// thousands, each expansion costing one scan of the library.
package route

import (
	"container/heap"
	"errors"
	"fmt"
	"math"

	"github.com/vincentlark/neighbeats/internal/vecindex"
)

// Graph is what the search needs from the index.
type Graph interface {
	Neighbours(id int64, k int) ([]vecindex.Neighbour, error)
	Similarity(a, b int64) (float32, error)
}

// Options tunes the search. Zero values take the defaults of Defaults.
type Options struct {
	Neighbours int     // candidates asked from the index per expansion
	MaxHops    int     // longest route, in hops (tracks - 1)
	MaxExpand  int     // expansions before giving up
	Base       float64 // edge cost: Base + Scale*(1-score) + Hop
	Scale      float64
	Hop        float64
	K          float64 // score falloff, see vecindex.Score
	Heuristic  float64 // cost estimate per unit of cosine distance to the target

	// MaxPerArtist limits tracks per group on a route (0 = no limit) and
	// consecutive tracks never share a group. Group may be nil.
	MaxPerArtist int
	Group        func(id int64) string
	// Key identifies copies of one song; a route never holds two. May be nil.
	Key func(id int64) string
	// Bias adjusts the cost of entering a track (negative = preferred) and may
	// give a short reason. May be nil.
	Bias func(id int64) (float64, string)
	// Forbidden tracks may not be intermediates. The endpoints are always allowed.
	Forbidden map[int64]bool
}

// Defaults returns the tuned defaults.
func Defaults() Options {
	return Options{Neighbours: 10, MaxHops: 12, MaxExpand: 400,
		Base: 0.68, Scale: 1.18, Hop: 0.17, K: 2.3, Heuristic: 5, MaxPerArtist: 2}
}

func (o Options) withDefaults() Options {
	d := Defaults()
	if o.Neighbours <= 0 {
		o.Neighbours = d.Neighbours
	}
	if o.MaxHops <= 0 {
		o.MaxHops = d.MaxHops
	}
	if o.MaxExpand <= 0 {
		o.MaxExpand = d.MaxExpand
	}
	if o.K <= 0 {
		o.K = d.K
	}
	if o.Base == 0 && o.Scale == 0 && o.Hop == 0 {
		o.Base, o.Scale, o.Hop = d.Base, d.Scale, d.Hop
	}
	if o.Heuristic <= 0 {
		o.Heuristic = d.Heuristic
	}
	return o
}

// Step is one track on a route. Cosine and Score describe the hop that
// reached it (zero for the first track).
type Step struct {
	ID     int64
	Cosine float32
	Score  float64
	Reason string
}

// Route is the outcome of Find.
type Route struct {
	Found bool
	Steps []Step // on success from A to B; on failure to the closest track reached
	Cost  float64
	// Expanded is how many tracks were expanded; Why explains a failure.
	Expanded int
	Why      string
}

type node struct {
	id     int64
	parent *node
	g      float64
	hops   int
	cos    float32
	score  float64
	closed bool
	note   string // why the taste bias applied, if it did
	f      float64
	index  int // heap position
	inHeap bool
}

type pq []*node

func (q pq) Len() int           { return len(q) }
func (q pq) Less(i, j int) bool { return q[i].f < q[j].f }
func (q pq) Swap(i, j int)      { q[i], q[j] = q[j], q[i]; q[i].index = i; q[j].index = j }
func (q *pq) Push(x any)        { n := x.(*node); n.index, n.inHeap = len(*q), true; *q = append(*q, n) }
func (q *pq) Pop() any {
	old := *q
	n := old[len(old)-1]
	n.inHeap = false
	*q = old[:len(old)-1]
	return n
}

// ErrSame is returned when both endpoints are the same track.
var ErrSame = errors.New("start and destination are the same track")

// Find searches for a route from a to b.
func Find(g Graph, a, b int64, opt Options) (Route, error) {
	opt = opt.withDefaults()
	if a == b {
		return Route{}, ErrSame
	}
	if _, err := g.Similarity(a, b); err != nil {
		return Route{}, err // unknown endpoint
	}

	// Tracks that sit in the destination's neighbourhood may hop straight
	// to it even when it is not in their own top list (similarity is
	// symmetric, but top-k lists are not).
	entry := map[int64]float32{}
	ns, err := g.Neighbours(b, opt.Neighbours*3)
	if err != nil {
		return Route{}, err
	}
	for _, n := range ns {
		entry[n.ID] = n.Cosine
	}

	h := func(id int64) (float64, error) {
		c, err := g.Similarity(id, b)
		return opt.Heuristic * (1 - float64(c)), err
	}
	h0, err := h(a)
	if err != nil {
		return Route{}, err
	}
	nodes := map[int64]*node{a: {id: a, f: h0}}
	open := &pq{}
	heap.Push(open, nodes[a])

	closest, closestH := nodes[a], h0
	expanded := 0
	for open.Len() > 0 {
		cur := heap.Pop(open).(*node)
		if cur.closed {
			continue
		}
		cur.closed = true
		if cur.id == b {
			return build(cur, expanded), nil
		}
		if ch, _ := h(cur.id); ch < closestH {
			closest, closestH = cur, ch
		}
		if cur.hops >= opt.MaxHops {
			continue
		}
		if expanded >= opt.MaxExpand {
			break
		}
		expanded++

		cands, err := g.Neighbours(cur.id, opt.Neighbours)
		if err != nil {
			return Route{}, err
		}
		if c, ok := entry[cur.id]; ok {
			cands = append(cands, vecindex.Neighbour{ID: b, Cosine: c})
		}
		for _, c := range cands {
			if !allowed(cur, c.ID, b, &opt) {
				continue
			}
			score := vecindex.Score(c.Cosine, opt.K)
			cost := opt.Base + opt.Scale*(1-score) + opt.Hop
			var note string
			if opt.Bias != nil {
				var d float64
				d, note = opt.Bias(c.ID)
				cost += d
			}
			cost = math.Max(opt.Hop+0.05, cost) // a hop is never free
			gNew := cur.g + cost
			nd := nodes[c.ID]
			if nd != nil && (nd.closed || gNew >= nd.g) {
				continue
			}
			hn, err := h(c.ID)
			if err != nil {
				return Route{}, err
			}
			if nd == nil {
				nd = &node{id: c.ID}
				nodes[c.ID] = nd
			}
			nd.parent, nd.g, nd.hops, nd.cos, nd.score, nd.note = cur, gNew, cur.hops+1, c.Cosine, score, note
			nd.f = gNew + hn
			if nd.inHeap {
				heap.Fix(open, nd.index)
			} else {
				heap.Push(open, nd)
			}
		}
	}

	r := build(closest, expanded)
	r.Found = false
	r.Why = fmt.Sprintf("no route within %d hops after expanding %d tracks", opt.MaxHops, expanded)
	if sim, err := g.Similarity(closest.id, b); err == nil {
		r.Why += fmt.Sprintf("; the closest track reached has cosine %.2f to the destination", sim)
	}
	return r, nil
}

// allowed applies the per-route constraints along cur's chain.
func allowed(cur *node, id, b int64, o *Options) bool {
	if id == b {
		return !sameSong(cur, id, o)
	}
	if o.Forbidden[id] || sameSong(cur, id, o) {
		return false
	}
	if o.Group != nil {
		g := o.Group(id)
		if g != "" {
			if o.Group(cur.id) == g {
				return false
			}
			if o.MaxPerArtist > 0 {
				n := 0
				for p := cur; p != nil; p = p.parent {
					if o.Group(p.id) == g {
						n++
					}
				}
				if n >= o.MaxPerArtist {
					return false
				}
			}
		}
	}
	return true
}

// sameSong reports whether id, or a copy of it, is already on the chain.
func sameSong(cur *node, id int64, o *Options) bool {
	var key string
	if o.Key != nil {
		key = o.Key(id)
	}
	for p := cur; p != nil; p = p.parent {
		if p.id == id || (key != "" && o.Key(p.id) == key) {
			return true
		}
	}
	return false
}

func build(end *node, expanded int) Route {
	var rev []*node
	for p := end; p != nil; p = p.parent {
		rev = append(rev, p)
	}
	r := Route{Found: true, Cost: end.g, Expanded: expanded}
	for i := len(rev) - 1; i >= 0; i-- {
		n := rev[i]
		s := Step{ID: n.id, Cosine: n.cos, Score: n.score}
		if n.parent != nil {
			s.Reason = fmt.Sprintf("similarity %.2f", n.score)
			if n.note != "" {
				s.Reason += "; " + n.note
			}
		}
		r.Steps = append(r.Steps, s)
	}
	return r
}
