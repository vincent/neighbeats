package vecindex

import (
	"errors"
	"math"
	"math/rand"
	"testing"
)

// Four tracks on the unit circle (2-D), angle in degrees.
func circle(angles ...float64) ([]int64, [][]float32) {
	ids := make([]int64, len(angles))
	vs := make([][]float32, len(angles))
	for i, a := range angles {
		ids[i] = int64(10 + i)
		r := a * math.Pi / 180
		vs[i] = []float32{float32(math.Cos(r)) * 3, float32(math.Sin(r)) * 3} // unnormalised on purpose
	}
	return ids, vs
}

func TestNeighboursOrderAndSelfExclusion(t *testing.T) {
	ids, vs := circle(0, 10, 50, 170)
	ix, err := Build(ids, vs, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ix.Neighbours(10, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != 11 || got[1].ID != 12 || got[2].ID != 13 {
		t.Fatalf("order = %+v", got)
	}
	if math.Abs(float64(got[0].Cosine)-math.Cos(10*math.Pi/180)) > 1e-5 {
		t.Errorf("cosine = %v", got[0].Cosine)
	}
	if got, _ := ix.Neighbours(10, 99); len(got) != 3 {
		t.Errorf("k larger than the set returned %d", len(got))
	}
}

func TestUnknownIDAndBuildErrors(t *testing.T) {
	ids, vs := circle(0, 10)
	ix, _ := Build(ids, vs, Options{})
	if _, err := ix.Neighbours(999, 1); !errors.Is(err, ErrUnknown) {
		t.Errorf("err = %v", err)
	}
	if _, err := Build([]int64{1, 1}, [][]float32{{1, 0}, {0, 1}}, Options{}); err == nil {
		t.Error("duplicate ids accepted")
	}
	if _, err := Build([]int64{1, 2}, [][]float32{{1, 0}, {0}}, Options{}); err == nil {
		t.Error("ragged vectors accepted")
	}
	if _, err := Build(nil, nil, Options{}); err == nil {
		t.Error("empty set accepted")
	}
}

func TestCenteringRemovesCommonComponent(t *testing.T) {
	// All vectors share a big +x offset; only the y component tells them apart.
	ids := []int64{1, 2, 3}
	vs := [][]float32{{10, 1}, {10, -1}, {10, 0.9}}
	raw, _ := Build(ids, vs, Options{})
	cen, _ := Build(ids, vs, Options{Center: true})
	sr, _ := raw.Similarity(1, 2)
	sc, _ := cen.Similarity(1, 2)
	if sr < 0.97 {
		t.Fatalf("raw similarity of opposite tracks = %v, expected near 1", sr)
	}
	if sc > 0 {
		t.Errorf("centred similarity of opposite tracks = %v, want negative", sc)
	}
	if n, _ := cen.Neighbours(1, 1); n[0].ID != 3 {
		t.Errorf("centred nearest = %+v, want id 3", n)
	}
}

func TestMaxPerArtist(t *testing.T) {
	ids, vs := circle(0, 5, 10, 15, 60)
	ix, _ := Build(ids, vs, Options{})
	artist := map[int64]string{11: "a", 12: "a", 13: "a", 14: "b"} // 10 is the seed, 14 is alone
	f := Filter{MaxPerArtist: 2, Group: func(id int64) string { return artist[id] }}
	got, err := ix.Search(10, 4, f)
	if err != nil {
		t.Fatal(err)
	}
	var ids2 []int64
	for _, n := range got {
		ids2 = append(ids2, n.ID)
	}
	want := []int64{11, 12, 14} // 13 dropped: third "a"
	if len(ids2) != len(want) {
		t.Fatalf("got %v want %v", ids2, want)
	}
	for i := range want {
		if ids2[i] != want[i] {
			t.Fatalf("got %v want %v", ids2, want)
		}
	}
	// An empty group is never capped.
	f.Group = func(int64) string { return "" }
	if got, _ := ix.Search(10, 4, f); len(got) != 4 {
		t.Errorf("uncapped group returned %d", len(got))
	}
}

func TestNoAdjacent(t *testing.T) {
	ids, vs := circle(0, 5, 10, 15, 60)
	ix, _ := Build(ids, vs, Options{})
	artist := map[int64]string{11: "a", 12: "a", 13: "b", 14: "a"}
	f := Filter{NoAdjacent: true, Group: func(id int64) string { return artist[id] }}
	got, err := ix.Search(10, 4, f)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(got); i++ {
		if artist[got[i].ID] == artist[got[i-1].ID] {
			t.Fatalf("adjacent same artist in %v", got)
		}
	}
	if len(got) < 3 {
		t.Fatalf("got %v", got)
	}
}

func TestExclude(t *testing.T) {
	ids, vs := circle(0, 5, 10, 15, 60)
	ix, _ := Build(ids, vs, Options{})
	skip := func(id int64) bool { return id == 11 || id == 13 }
	artist := map[int64]string{}
	for name, f := range map[string]Filter{
		"plain":  {Exclude: skip},
		"capped": {MaxPerArtist: 2, Group: func(id int64) string { return artist[id] }, Exclude: skip},
	} {
		got, err := ix.Search(10, 4, f)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 {
			t.Fatalf("%s: no results", name)
		}
		for _, n := range got {
			if skip(n.ID) {
				t.Errorf("%s: excluded track %d returned", name, n.ID)
			}
		}
	}
}

func TestProject(t *testing.T) {
	raw := [][]float32{{3, 1}, {1, 3}, {2, 2}}
	ix, err := Build([]int64{1, 2, 3}, raw, Options{Center: true})
	if err != nil {
		t.Fatal(err)
	}
	// An indexed vector projects onto its own row.
	q, err := ix.Project(raw[0])
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := ix.CosineTo(q, 1); !ok || c < 0.9999 {
		t.Errorf("own row: %v %v", c, ok)
	}
	if c, _ := ix.CosineTo(q, 2); c > -0.9999 {
		t.Errorf("opposite row: %v", c) // centred: (3,1) and (1,3) point in opposite directions
	}
	if _, ok := ix.CosineTo(q, 99); ok {
		t.Error("unknown id should not be ok")
	}
	if _, err := ix.Project([]float32{1}); err == nil {
		t.Error("wrong dimension should fail")
	}
}

func TestScore(t *testing.T) {
	if Score(1, 2.3) != 1 {
		t.Error("identical tracks must score 1")
	}
	if !(Score(0.8, 2.3) > Score(0.4, 2.3) && Score(0.4, 2.3) > Score(0, 2.3)) {
		t.Error("score must decrease with cosine")
	}
}

func randomIndex(n, dim int) (*Index, error) {
	r := rand.New(rand.NewSource(1))
	ids := make([]int64, n)
	vs := make([][]float32, n)
	for i := range vs {
		ids[i] = int64(i + 1)
		v := make([]float32, dim)
		for j := range v {
			v[j] = r.Float32()
		}
		vs[i] = v
	}
	return Build(ids, vs, Options{Center: true})
}

// Library-sized query: the roadmap asks for this number on the NAS CPU.
//
//	go test -bench Neighbours25k ./internal/vecindex
func BenchmarkNeighbours25k(b *testing.B) {
	ix, err := randomIndex(25000, 1280)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := range b.N {
		if _, err := ix.Neighbours(int64(i%25000+1), 20); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCapped25k(b *testing.B) {
	ix, err := randomIndex(25000, 1280)
	if err != nil {
		b.Fatal(err)
	}
	f := Filter{MaxPerArtist: 2, Group: func(id int64) string { return string(rune('a' + id%50)) }}
	b.ResetTimer()
	for i := range b.N {
		if _, err := ix.Search(int64(i%25000+1), 20, f); err != nil {
			b.Fatal(err)
		}
	}
}
