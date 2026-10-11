// Package embed turns audio into music embeddings: a Go log-mel front end
// (mel.go) feeding Essentia's Discogs-EffNet model through ONNX Runtime.
package embed

import "fmt"

const (
	// ModelID names the model and the preprocessing in mel.go. It is stored
	// with every embedding; bump it when either changes.
	ModelID = "discogs-effnet-bsdynamic-1"
	// Dim is the length of one embedding vector.
	Dim = 1280
)

// Embedder maps mel patches to embeddings. Implementations must be safe for
// concurrent use.
type Embedder interface {
	// Embed runs n patches (n*PatchFrames*NumBands values, row-major) and
	// returns n*Dim values.
	Embed(patches []float32, n int) ([]float32, error)
	// Provider names the execution provider actually in use ("cuda", "cpu",
	// "fake").
	Provider() string
	Close() error
}

// Pool averages per-patch embeddings into one track vector.
func Pool(perPatch []float32, n int) ([]float32, error) {
	if n <= 0 || len(perPatch) != n*Dim {
		return nil, fmt.Errorf("pool: %d values for %d patches, want %d", len(perPatch), n, n*Dim)
	}
	out := make([]float32, Dim)
	for p := range n {
		for i, v := range perPatch[p*Dim : (p+1)*Dim] {
			out[i] += v
		}
	}
	inv := 1 / float32(n)
	for i := range out {
		out[i] *= inv
	}
	return out, nil
}

// Fake is a deterministic Embedder for tests: each patch embeds to a vector
// derived from its mean value, so identical audio gives identical vectors.
type Fake struct{}

func (Fake) Embed(patches []float32, n int) ([]float32, error) {
	per := PatchFrames * NumBands
	if len(patches) != n*per {
		return nil, fmt.Errorf("fake: %d values for %d patches", len(patches), n)
	}
	out := make([]float32, n*Dim)
	for p := range n {
		var mean float32
		for _, v := range patches[p*per : (p+1)*per] {
			mean += v
		}
		mean /= float32(per)
		for i := range Dim {
			out[p*Dim+i] = mean * float32(i%7+1)
		}
	}
	return out, nil
}

func (Fake) Provider() string { return "fake" }
func (Fake) Close() error     { return nil }
