package textembed

import (
	"encoding/json"
	"math"
	"os"
	"testing"

	"github.com/vincentlark/neighbeats/internal/embed"
)

func TestPrepare(t *testing.T) {
	got := Prepare("Hello there\n\n  hello THERE \nsecond line\nHello there\nthird")
	if want := "Hello there\nsecond line\nthird"; got != want {
		t.Errorf("Prepare = %q, want %q", got, want)
	}
}

// Needs the real model and runtime:
//
//	NEIGHBEATS_TEST_LYRICMODEL=/dir NEIGHBEATS_TEST_ORT=/path/libonnxruntime.so go test ./internal/textembed
func TestModelCPU(t *testing.T) {
	dir := os.Getenv("NEIGHBEATS_TEST_LYRICMODEL")
	if dir == "" {
		t.Skip("NEIGHBEATS_TEST_LYRICMODEL not set")
	}
	m, err := Open(Options{Dir: dir, LibraryPath: os.Getenv("NEIGHBEATS_TEST_ORT"), Provider: embed.ProviderCPU})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	texts := []string{
		"The sea at night, waves on the shore, the moon over the water",
		"Ocean waves roll in under moonlight on the beach",
		"Je t'aime encore, mon amour, reviens-moi",
		"Hard drive failure on the production database server",
	}
	vs, err := m.Embed(PassagePrefix, texts)
	if err != nil {
		t.Fatal(err)
	}
	dot := func(a, b []float32) float64 {
		var s float64
		for i := range a {
			s += float64(a[i]) * float64(b[i])
		}
		return s
	}
	for i, v := range vs {
		if len(v) != Dim || math.Abs(dot(v, v)-1) > 1e-4 {
			t.Fatalf("text %d: len %d norm² %g", i, len(v), dot(v, v))
		}
	}
	if sea, love, db := dot(vs[0], vs[1]), dot(vs[0], vs[2]), dot(vs[0], vs[3]); !(sea > love && sea > db) {
		t.Errorf("sea/sea %.3f should beat sea/love %.3f and sea/database %.3f", sea, love, db)
	}
	// Batching must not change the result: a text alone equals the same text in a batch.
	alone, err := m.Embed(PassagePrefix, texts[2:3])
	if err != nil {
		t.Fatal(err)
	}
	if d := dot(alone[0], vs[2]); d < 0.9999 {
		t.Errorf("batched vs alone cosine %.5f", d)
	}
}

// TestModelMatchesReference compares vectors with the reference pipeline
// (Hugging Face tokenizer + onnxruntime in Python); NEIGHBEATS_TEST_E5REF is
// its JSON dump {"texts": [...], "vectors": [...]} of "passage: "-prefixed texts.
func TestModelMatchesReference(t *testing.T) {
	dir, ref := os.Getenv("NEIGHBEATS_TEST_LYRICMODEL"), os.Getenv("NEIGHBEATS_TEST_E5REF")
	if dir == "" || ref == "" {
		t.Skip("NEIGHBEATS_TEST_LYRICMODEL / NEIGHBEATS_TEST_E5REF not set")
	}
	data, err := os.ReadFile(ref)
	if err != nil {
		t.Fatal(err)
	}
	var want struct {
		Texts   []string    `json:"texts"`
		Vectors [][]float32 `json:"vectors"`
	}
	if err := json.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	m, err := Open(Options{Dir: dir, LibraryPath: os.Getenv("NEIGHBEATS_TEST_ORT"), Provider: embed.ProviderCPU})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	got, err := m.Embed(PassagePrefix, want.Texts)
	if err != nil {
		t.Fatal(err)
	}
	for i := range got {
		var d float64
		for k := range got[i] {
			d += float64(got[i][k]) * float64(want.Vectors[i][k])
		}
		t.Logf("text %d: cosine with reference %.6f", i, d)
		if d < 0.999 {
			t.Errorf("text %d: cosine %.6f", i, d)
		}
	}
}
