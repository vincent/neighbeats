package embed

import (
	"math"
	"os"
	"testing"
)

// Needs a real runtime and model, so it only runs when both are given:
//
//	NEIGHBEATS_TEST_MODEL=/path/discogs-effnet-bsdynamic-1.onnx \
//	NEIGHBEATS_TEST_ORT=/path/libonnxruntime.so go test ./internal/embed
func TestORTCPU(t *testing.T) {
	model := os.Getenv("NEIGHBEATS_TEST_MODEL")
	if model == "" {
		t.Skip("NEIGHBEATS_TEST_MODEL not set")
	}
	e, err := NewORT(ORTOptions{ModelPath: model, LibraryPath: os.Getenv("NEIGHBEATS_TEST_ORT"), Provider: ProviderCPU})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if e.Provider() != "cpu" {
		t.Fatalf("provider = %s", e.Provider())
	}
	const n = 3
	in := make([]float32, n*PatchFrames*NumBands)
	for i := range in {
		in[i] = float32(math.Abs(math.Sin(float64(i)*0.01))) * 3
	}
	out, err := e.Embed(in, n)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != n*Dim {
		t.Fatalf("got %d values, want %d", len(out), n*Dim)
	}
	var norm float64
	for _, v := range out[:Dim] {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			t.Fatal("non-finite embedding")
		}
		norm += float64(v) * float64(v)
	}
	if norm == 0 {
		t.Fatal("all-zero embedding")
	}
	again, _ := e.Embed(in, n)
	for i := range out {
		if out[i] != again[i] {
			t.Fatal("inference is not deterministic")
		}
	}
}
