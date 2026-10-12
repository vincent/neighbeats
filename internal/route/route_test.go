package route

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/vincentlark/neighbeats/internal/vecindex"
)

// fake is a hand-made graph: adj lists neighbours (best first) and sim gives
// the cosine of any pair (symmetric, default 0).
type fake struct {
	adj map[int64][]vecindex.Neighbour
	sim map[[2]int64]float32
}

func (f fake) Neighbours(id int64, k int) ([]vecindex.Neighbour, error) {
	n := f.adj[id]
	if len(n) > k {
		n = n[:k]
	}
	return n, nil
}

func (f fake) Similarity(a, b int64) (float32, error) {
	if a == b {
		return 1, nil
	}
	if s, ok := f.sim[[2]int64{a, b}]; ok {
		return s, nil
	}
	return f.sim[[2]int64{b, a}], nil
}

func nb(id int64, c float32) vecindex.Neighbour { return vecindex.Neighbour{ID: id, Cosine: c} }

// chain 1 - 2 - 3 - 4 plus a dead-end 5 close to 1.
func chain() fake {
	return fake{
		adj: map[int64][]vecindex.Neighbour{
			1: {nb(5, 0.9), nb(2, 0.7)},
			2: {nb(1, 0.7), nb(3, 0.7)},
			3: {nb(2, 0.7), nb(4, 0.7)},
			4: {nb(3, 0.7)},
			5: {nb(1, 0.9)},
		},
		sim: map[[2]int64]float32{{1, 4}: 0.1, {2, 4}: 0.3, {3, 4}: 0.7, {5, 4}: 0.05},
	}
}

func ids(r Route) []int64 {
	var out []int64
	for _, s := range r.Steps {
		out = append(out, s.ID)
	}
	return out
}

func eq(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestHopsThroughIntermediates(t *testing.T) {
	r, err := Find(chain(), 1, 4, Options{})
	if err != nil || !r.Found || !eq(ids(r), []int64{1, 2, 3, 4}) {
		t.Fatalf("got %+v, %v", r, err)
	}
	if r.Steps[0].Reason != "" || r.Steps[1].Reason == "" {
		t.Errorf("reasons: %+v", r.Steps)
	}
}

func TestForbiddenIntermediate(t *testing.T) {
	r, _ := Find(chain(), 1, 4, Options{Forbidden: map[int64]bool{3: true}})
	if r.Found {
		t.Fatalf("route through forbidden track: %v", ids(r))
	}
	if r.Why == "" || ids(r)[len(ids(r))-1] != 2 {
		t.Errorf("closest should be 2 with a reason: %+v", r)
	}
}

func TestForbiddenDestinationStillAllowed(t *testing.T) {
	r, _ := Find(chain(), 1, 4, Options{Forbidden: map[int64]bool{4: true, 1: true}})
	if !r.Found {
		t.Fatal("endpoints must stay allowed")
	}
}

func TestDestinationReachedFromItsNeighbourhood(t *testing.T) {
	// 9 lists nothing, but 4 lists 9 as a neighbour, so 9 may hop to 4.
	f := chain()
	f.adj[1] = []vecindex.Neighbour{nb(9, 0.8)}
	f.adj[9] = nil
	f.adj[4] = append(f.adj[4], nb(9, 0.8))
	f.sim[[2]int64{9, 4}] = 0.8
	r, _ := Find(f, 1, 4, Options{})
	if !eq(ids(r), []int64{1, 9, 4}) {
		t.Fatalf("got %v", ids(r))
	}
}

func TestMaxHops(t *testing.T) {
	if r, _ := Find(chain(), 1, 4, Options{MaxHops: 2}); r.Found {
		t.Fatal("needs 3 hops")
	}
}

func TestSameAndMissing(t *testing.T) {
	if _, err := Find(chain(), 1, 1, Options{}); !errors.Is(err, ErrSame) {
		t.Errorf("same: %v", err)
	}
	f := errGraph{chain()}
	if _, err := Find(f, 1, 99, Options{}); err == nil {
		t.Error("missing endpoint accepted")
	}
}

type errGraph struct{ fake }

func (e errGraph) Similarity(a, b int64) (float32, error) {
	if a == 99 || b == 99 {
		return 0, vecindex.ErrUnknown
	}
	return e.fake.Similarity(a, b)
}

func TestArtistSpread(t *testing.T) {
	artist := map[int64]string{2: "x", 3: "x"}
	g := func(id int64) string { return artist[id] }
	// 2 and 3 are consecutive tracks of one artist: refused, so no route.
	if r, _ := Find(chain(), 1, 4, Options{Group: g, MaxPerArtist: 2}); r.Found {
		t.Fatalf("consecutive same artist: %v", ids(r))
	}
}

func TestDedupeCopies(t *testing.T) {
	f := chain()
	f.adj[1] = []vecindex.Neighbour{nb(2, 0.7)}
	// 3 is a second copy of the song 2: it cannot follow 2.
	key := func(id int64) string {
		if id == 2 || id == 3 {
			return "song"
		}
		return ""
	}
	if r, _ := Find(f, 1, 4, Options{Key: key}); r.Found {
		t.Fatalf("two copies on one route: %v", ids(r))
	}
}

// line builds a real index of points on a quarter circle, so ids 0..n-1
// run from angle 0 to 90 degrees.
func line(t *testing.T, n int) *vecindex.Index {
	t.Helper()
	ids := make([]int64, n)
	vs := make([][]float32, n)
	for i := range n {
		ang := float64(i) / float64(n-1) * math.Pi / 2
		ids[i] = int64(i)
		vs[i] = []float32{float32(math.Cos(ang)), float32(math.Sin(ang))}
	}
	ix, err := vecindex.Build(ids, vs, vecindex.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

func TestSpreadEvenly(t *testing.T) {
	ix := line(t, 91) // one track per degree
	r, err := Spread(ix, 0, 90, 4, Options{})
	if err != nil || !r.Found {
		t.Fatalf("%+v %v", r, err)
	}
	got := ids(r)
	want := []int64{0, 30, 60, 90}
	if !eq(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestSpreadConstraints(t *testing.T) {
	ix := line(t, 91)
	r, _ := Spread(ix, 0, 90, 4, Options{Forbidden: map[int64]bool{30: true, 29: true, 31: true}})
	for _, s := range r.Steps {
		if s.ID == 30 || s.ID == 29 || s.ID == 31 {
			t.Fatalf("forbidden track used: %v", ids(r))
		}
	}
	if len(r.Steps) != 4 {
		t.Errorf("want 4 tracks, got %v", ids(r))
	}
	if _, err := Spread(ix, 0, 0, 4, Options{}); !errors.Is(err, ErrSame) {
		t.Errorf("same: %v", err)
	}
}

func TestBiasChangesTheRoute(t *testing.T) {
	// Two ways from 1 to 4: through 2 (plain) or through 6 (liked by taste).
	f := chain()
	f.adj[1] = []vecindex.Neighbour{nb(2, 0.7), nb(6, 0.6)}
	f.adj[2] = []vecindex.Neighbour{nb(4, 0.7)}
	f.adj[6] = []vecindex.Neighbour{nb(4, 0.7)}
	f.adj[4] = []vecindex.Neighbour{nb(2, 0.7), nb(6, 0.7)}
	f.sim[[2]int64{6, 4}] = 0.7
	f.sim[[2]int64{2, 4}] = 0.7
	plain, _ := Find(f, 1, 4, Options{})
	if !eq(ids(plain), []int64{1, 2, 4}) {
		t.Fatalf("plain %v", ids(plain))
	}
	bias := func(id int64) (float64, string) {
		switch id {
		case 6:
			return -0.5, "favourite"
		case 2:
			return 0.5, "played today"
		}
		return 0, ""
	}
	liked, _ := Find(f, 1, 4, Options{Bias: bias})
	if !eq(ids(liked), []int64{1, 6, 4}) {
		t.Fatalf("liked %v", ids(liked))
	}
	if r := liked.Steps[1].Reason; !strings.HasSuffix(r, "; favourite") {
		t.Errorf("reason should name the taste term: %q", r)
	}
}

// twoWays: from 1 to 4 through 2 (slightly better audio) or through 6.
func twoWays() fake {
	f := chain()
	f.adj[1] = []vecindex.Neighbour{nb(2, 0.72), nb(6, 0.70)}
	f.adj[2] = []vecindex.Neighbour{nb(4, 0.7)}
	f.adj[6] = []vecindex.Neighbour{nb(4, 0.7)}
	f.adj[4] = []vecindex.Neighbour{nb(2, 0.7), nb(6, 0.7)}
	f.sim[[2]int64{6, 4}] = 0.7
	f.sim[[2]int64{2, 4}] = 0.7
	return f
}

func TestLanguageJumpPenalty(t *testing.T) {
	langs := map[int64]string{1: "eng", 2: "fra", 6: "eng", 4: "eng"}
	lang := func(id int64) string { return langs[id] }
	if r, _ := Find(twoWays(), 1, 4, Options{}); !eq(ids(r), []int64{1, 2, 4}) {
		t.Fatalf("without penalty %v", ids(r))
	}
	r, _ := Find(twoWays(), 1, 4, Options{Lang: lang, LangPenalty: 0.5})
	if !eq(ids(r), []int64{1, 6, 4}) {
		t.Fatalf("with penalty %v", ids(r))
	}
	// Unknown languages are never a jump.
	langs[6] = ""
	langs[2] = ""
	if r, _ = Find(twoWays(), 1, 4, Options{Lang: lang, LangPenalty: 0.5}); !eq(ids(r), []int64{1, 2, 4}) {
		t.Fatalf("unknown languages %v", ids(r))
	}
	// The reason names the jump when it is taken anyway.
	langs[2] = "fra"
	r, _ = Find(twoWays(), 1, 4, Options{Lang: lang, LangPenalty: 0.01})
	if !strings.Contains(r.Steps[1].Reason, "language eng to fra") {
		t.Errorf("reason %q", r.Steps[1].Reason)
	}
}

func TestThemeBonus(t *testing.T) {
	theme := func(id int64) float64 {
		if id == 6 {
			return 0.4
		}
		return 0
	}
	r, _ := Find(twoWays(), 1, 4, Options{Theme: theme, ThemeWeight: 1})
	if !eq(ids(r), []int64{1, 6, 4}) {
		t.Fatalf("with theme %v", ids(r))
	}
	if !strings.Contains(r.Steps[1].Reason, "theme 0.40") {
		t.Errorf("reason %q", r.Steps[1].Reason)
	}
	if r, _ = Find(twoWays(), 1, 4, Options{Theme: theme}); !eq(ids(r), []int64{1, 2, 4}) {
		t.Fatalf("zero weight %v", ids(r)) // no weight, no effect
	}
}

func TestPairNudgesTheChoice(t *testing.T) {
	// Lyrics say 1 and 6 go together; the audio alone prefers 2.
	pair := func(a, b int64, c float32) float32 {
		if a == 1 && b == 6 {
			return c + 0.2
		}
		return c
	}
	r, _ := Find(twoWays(), 1, 4, Options{Pair: pair})
	if !eq(ids(r), []int64{1, 6, 4}) {
		t.Fatalf("with pair %v", ids(r))
	}
	if r.Steps[1].Cosine != 0.70 {
		t.Errorf("Step.Cosine should stay the audio cosine, got %v", r.Steps[1].Cosine)
	}
}

func TestSpreadLyricHooks(t *testing.T) {
	ix := line(t, 91) // one track per degree; the middle waypoint is track 45
	if r, _ := Spread(ix, 0, 90, 3, Options{}); r.Steps[1].ID != 45 {
		t.Fatalf("plain pick %v", ids(r))
	}
	// A theme match a few degrees away beats the exact waypoint.
	themed, _ := Spread(ix, 0, 90, 3, Options{ThemeWeight: 3, Theme: func(id int64) float64 {
		if id == 50 {
			return 0.5
		}
		return 0
	}})
	if themed.Steps[1].ID != 50 || !strings.Contains(themed.Steps[1].Reason, "theme 0.50") {
		t.Errorf("themed pick %v %q", ids(themed), themed.Steps[1].Reason)
	}
	// Leaving the language of the start costs: the exact waypoint is French.
	lang := func(id int64) string {
		if id == 45 {
			return "fra"
		}
		return "eng"
	}
	away, _ := Spread(ix, 0, 90, 3, Options{Lang: lang, LangPenalty: 0.5})
	if away.Steps[1].ID == 45 {
		t.Errorf("language penalty ignored: %v", ids(away))
	}
	// The nudge from lyrics changes which neighbour of the previous track is liked.
	pair := func(a, b int64, c float32) float32 {
		if b == 46 {
			return c + 0.3
		}
		return c
	}
	nudged, _ := Spread(ix, 0, 90, 3, Options{Pair: pair, Smooth: 5})
	if nudged.Steps[1].ID == 45 {
		t.Errorf("pair nudge ignored: %v", ids(nudged))
	}
}

func TestSpreadSmoothKeepsTheJourney(t *testing.T) {
	ix := line(t, 91)
	r, err := Spread(ix, 0, 90, 7, Options{Smooth: 0.5, Beam: 8})
	if err != nil || !r.Found || len(r.Steps) != 7 {
		t.Fatalf("%+v %v", r, err)
	}
	// Waypoints are 15 degrees apart; smoothing may shift a pick a little
	// but never lets the route stall or skip ahead.
	prev := int64(-1)
	for i, s := range r.Steps {
		want := int64(15 * i)
		if d := s.ID - want; d < -4 || d > 4 {
			t.Errorf("step %d at %d degrees, waypoint at %d", i, s.ID, want)
		}
		if s.ID <= prev {
			t.Errorf("route goes backwards: %v", ids(r))
		}
		prev = s.ID
	}
}
