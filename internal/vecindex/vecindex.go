// Package vecindex does exact nearest-neighbour search over track embeddings.
//
// The whole library lives in one contiguous []float32 of unit vectors, so
// cosine similarity is a dot product and a query is a linear scan. At 25k
// tracks that is milliseconds (see the benchmark), so there is no approximate
// index and nothing to persist or rebuild. Callers depend on Neighbourer so
// an approximate index can replace this one later.
package vecindex

import (
	"container/heap"
	"errors"
	"fmt"
	"math"
	"sort"
)

// Neighbour is a search result. Cosine is the cosine similarity of the
// (optionally centred) vectors, in [-1, 1].
type Neighbour struct {
	ID     int64
	Cosine float32
}

// Neighbourer is what path search needs from an index.
type Neighbourer interface {
	// Neighbours returns up to k tracks most similar to id, best first,
	// excluding id itself.
	Neighbours(id int64, k int) ([]Neighbour, error)
}

// ErrUnknown is returned for an id that is not in the index.
var ErrUnknown = errors.New("track is not in the index")

// Options configures Build.
type Options struct {
	// Center subtracts the mean vector of the whole set before normalising.
	// Embeddings share a large common component that inflates every cosine;
	// removing it spreads the scores out.
	Center bool
}

// Index is immutable after Build and safe for concurrent use.
type Index struct {
	ids  []int64
	pos  map[int64]int
	dim  int
	vecs []float32 // len(ids)*dim, unit length (or all zero)
}

// Build copies and normalises the vectors. ids[i] belongs to vectors[i].
func Build(ids []int64, vectors [][]float32, opt Options) (*Index, error) {
	if len(ids) != len(vectors) {
		return nil, fmt.Errorf("%d ids for %d vectors", len(ids), len(vectors))
	}
	if len(ids) == 0 {
		return nil, errors.New("no vectors to index")
	}
	dim := len(vectors[0])
	ix := &Index{ids: append([]int64(nil), ids...), pos: make(map[int64]int, len(ids)), dim: dim,
		vecs: make([]float32, len(ids)*dim)}
	for i, v := range vectors {
		if len(v) != dim {
			return nil, fmt.Errorf("vector %d has %d values, want %d", i, len(v), dim)
		}
		if _, dup := ix.pos[ids[i]]; dup {
			return nil, fmt.Errorf("duplicate id %d", ids[i])
		}
		ix.pos[ids[i]] = i
		copy(ix.vecs[i*dim:], v)
	}
	if opt.Center {
		mean := make([]float64, dim)
		for i := range ids {
			for j, x := range ix.vecs[i*dim : (i+1)*dim] {
				mean[j] += float64(x)
			}
		}
		for j := range mean {
			mean[j] /= float64(len(ids))
		}
		for i := range ids {
			row := ix.vecs[i*dim : (i+1)*dim]
			for j := range row {
				row[j] -= float32(mean[j])
			}
		}
	}
	for i := range ids {
		row := ix.vecs[i*dim : (i+1)*dim]
		var n float64
		for _, x := range row {
			n += float64(x) * float64(x)
		}
		if n == 0 {
			continue // stays zero: similar to nothing
		}
		inv := float32(1 / math.Sqrt(n))
		for j := range row {
			row[j] *= inv
		}
	}
	return ix, nil
}

// Len is the number of indexed tracks.
func (ix *Index) Len() int { return len(ix.ids) }

// Dim is the vector length.
func (ix *Index) Dim() int { return ix.dim }

// Has reports whether id is indexed.
func (ix *Index) Has(id int64) bool { _, ok := ix.pos[id]; return ok }

// Similarity is the cosine similarity of two indexed tracks.
func (ix *Index) Similarity(a, b int64) (float32, error) {
	pa, ok := ix.pos[a]
	if !ok {
		return 0, fmt.Errorf("track %d: %w", a, ErrUnknown)
	}
	pb, ok := ix.pos[b]
	if !ok {
		return 0, fmt.Errorf("track %d: %w", b, ErrUnknown)
	}
	return ix.dot(ix.row(pa), pb), nil
}

func (ix *Index) row(p int) []float32 { return ix.vecs[p*ix.dim : (p+1)*ix.dim] }

func (ix *Index) dot(q []float32, p int) float32 {
	r := ix.vecs[p*ix.dim : (p+1)*ix.dim]
	var s0, s1, s2, s3 float32
	i := 0
	for ; i+4 <= len(q); i += 4 {
		s0 += q[i] * r[i]
		s1 += q[i+1] * r[i+1]
		s2 += q[i+2] * r[i+2]
		s3 += q[i+3] * r[i+3]
	}
	for ; i < len(q); i++ {
		s0 += q[i] * r[i]
	}
	return s0 + s1 + s2 + s3
}

// Neighbours implements Neighbourer.
func (ix *Index) Neighbours(id int64, k int) ([]Neighbour, error) {
	return ix.Search(id, k, Filter{})
}

// Filter narrows a search.
type Filter struct {
	// MaxPerArtist keeps at most this many results per group (0 = no cap).
	// Group maps a track id to its group, usually the artist; an empty group
	// is never capped.
	MaxPerArtist int
	// NoAdjacent keeps two consecutive results from sharing a group, taking
	// the best-ranked track of a different group instead.
	NoAdjacent bool
	Group      func(id int64) string
}

// Search returns up to k neighbours of id under the filter.
func (ix *Index) Search(id int64, k int, f Filter) ([]Neighbour, error) {
	p, ok := ix.pos[id]
	if !ok {
		return nil, fmt.Errorf("track %d: %w", id, ErrUnknown)
	}
	if k <= 0 {
		return nil, nil
	}
	return ix.search(ix.row(p), p, k, f), nil
}

// SearchVec returns up to k tracks closest to an arbitrary query vector of
// the index's dimension (it is normalised first), best first.
func (ix *Index) SearchVec(q []float32, k int, f Filter) ([]Neighbour, error) {
	if len(q) != ix.dim {
		return nil, fmt.Errorf("query has %d values, want %d", len(q), ix.dim)
	}
	if k <= 0 {
		return nil, nil
	}
	return ix.search(unit(q), -1, k, f), nil
}

// Blend returns the unit vector a fraction t of the way from track a to
// track b (t=0 is a, t=1 is b) along the arc between them.
func (ix *Index) Blend(a, b int64, t float64) ([]float32, error) {
	pa, ok := ix.pos[a]
	if !ok {
		return nil, fmt.Errorf("track %d: %w", a, ErrUnknown)
	}
	pb, ok := ix.pos[b]
	if !ok {
		return nil, fmt.Errorf("track %d: %w", b, ErrUnknown)
	}
	ra, rb := ix.row(pa), ix.row(pb)
	// Spherical interpolation, so equal steps in t are equal angles.
	wa, wb := 1-t, t
	if d := float64(dotf(ra, rb)); d < 0.9999 && d > -0.9999 {
		omega := math.Acos(d)
		wa, wb = math.Sin((1-t)*omega)/math.Sin(omega), math.Sin(t*omega)/math.Sin(omega)
	}
	out := make([]float32, ix.dim)
	for i := range out {
		out[i] = float32(wa*float64(ra[i]) + wb*float64(rb[i]))
	}
	return unit(out), nil
}

func unit(v []float32) []float32 {
	var n float64
	for _, x := range v {
		n += float64(x) * float64(x)
	}
	out := append([]float32(nil), v...)
	if n == 0 {
		return out
	}
	inv := float32(1 / math.Sqrt(n))
	for i := range out {
		out[i] *= inv
	}
	return out
}

// search scans every track except position skip (-1 for none).
func (ix *Index) search(q []float32, p, k int, f Filter) []Neighbour {
	if (f.MaxPerArtist > 0 || f.NoAdjacent) && f.Group != nil {
		return ix.searchCapped(p, q, k, f)
	}
	h := make(minHeap, 0, k+1)
	for i := range ix.ids {
		if i == p {
			continue
		}
		s := ix.dot(q, i)
		if len(h) < k {
			heap.Push(&h, Neighbour{ix.ids[i], s})
		} else if s > h[0].Cosine {
			h[0] = Neighbour{ix.ids[i], s}
			heap.Fix(&h, 0)
		}
	}
	out := []Neighbour(h)
	sort.Slice(out, func(a, b int) bool { return better(out[a], out[b]) })
	return out
}

// searchCapped scores everything, then walks down the ranking taking at most
// MaxPerArtist per group. Exact, and still only a few milliseconds.
func (ix *Index) searchCapped(p int, q []float32, k int, f Filter) []Neighbour {
	all := make([]Neighbour, 0, len(ix.ids)-1)
	for i := range ix.ids {
		if i != p {
			all = append(all, Neighbour{ix.ids[i], ix.dot(q, i)})
		}
	}
	sort.Slice(all, func(a, b int) bool { return better(all[a], all[b]) })
	count := map[string]int{}
	used := make([]bool, len(all))
	out := make([]Neighbour, 0, k)
	last := ""
	for len(out) < k {
		picked := false
		for i, n := range all {
			if used[i] {
				continue
			}
			g := f.Group(n.ID)
			if g != "" {
				if f.MaxPerArtist > 0 && count[g] >= f.MaxPerArtist {
					used[i] = true // can never be taken again
					continue
				}
				if f.NoAdjacent && g == last {
					continue
				}
				count[g]++
			}
			used[i] = true
			out = append(out, n)
			last = g
			picked = true
			break
		}
		if !picked {
			break
		}
	}
	return out
}

// better orders by cosine, then id, so results are deterministic.
func better(a, b Neighbour) bool {
	if a.Cosine != b.Cosine {
		return a.Cosine > b.Cosine
	}
	return a.ID < b.ID
}

type minHeap []Neighbour

func (h minHeap) Len() int           { return len(h) }
func (h minHeap) Less(i, j int) bool { return better(h[j], h[i]) } // worst at the root
func (h minHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x any)        { *h = append(*h, x.(Neighbour)) }
func (h *minHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

// Score turns a cosine similarity into a 0..1 closeness with the exp(-k*d)
// shape of the reference route engine, where d = 1 - cosine is the cosine
// distance. k controls how fast scores fall off (see DefaultK).
func Score(cosine float32, k float64) float64 {
	return math.Exp(-k * (1 - float64(cosine)))
}

func dotf(a, b []float32) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}
