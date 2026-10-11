package embed

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

// goldenSignal must match the generator used to produce testdata/mel_golden.json
// (Essentia: Windowing hann, Spectrum, MelBands slaneyMel/unit_tri/power,
// log10(10000x+1)).
func goldenSignal() []float32 {
	x := make([]float32, 4096)
	for i := range x {
		n := float64(i)
		v := 0.5*math.Sin(2*math.Pi*440*n/16000) +
			0.25*math.Sin(2*math.Pi*3000*n/16000+1) +
			0.1*math.Sin(2*math.Pi*150*n/16000*(1+n/8192))
		x[i] = float32(v)
	}
	return x
}

func TestLogMelMatchesEssentia(t *testing.T) {
	raw, err := os.ReadFile("testdata/mel_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		Frames int       `json:"frames"`
		Mel    []float32 `json:"mel"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	got := NewMel().LogMel(goldenSignal())
	if len(got) != g.Frames*NumBands || len(g.Mel) != len(got) {
		t.Fatalf("size: got %d values, want %d (%d frames)", len(got), len(g.Mel), g.Frames)
	}
	var maxDiff float64
	for i := range got {
		if d := math.Abs(float64(got[i] - g.Mel[i])); d > maxDiff {
			maxDiff = d
		}
	}
	if maxDiff > 1e-3 {
		t.Fatalf("max abs difference to Essentia = %g", maxDiff)
	}
}

func TestPatchCounts(t *testing.T) {
	// 30 s at 16 kHz: 1874 frames, 29 patches (matches the reference run).
	if f := Frames(480000); f != 1874 {
		t.Fatalf("Frames(480000) = %d, want 1874", f)
	}
	if p := Patches(1874); p != 29 {
		t.Fatalf("Patches(1874) = %d, want 29", p)
	}
	if Patches(Frames(MinSamples)) != 1 || Patches(Frames(MinSamples-1)) != 0 {
		t.Fatal("MinSamples is not the one-patch boundary")
	}
}
