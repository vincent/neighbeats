package embed

import (
	"math"
	"math/cmplx"
)

// Preprocessing constants of discogs-effnet-bsdynamic-1. They reproduce
// Essentia's TensorflowInputMusiCNN front end (checked against the reference
// implementation, see mel_test.go). Do not change them without changing the
// model.
const (
	SampleRate  = 16000
	frameSize   = 512
	hopSize     = 256
	numBins     = frameSize/2 + 1 // 257
	NumBands    = 96
	PatchFrames = 128 // frames per model input
	PatchHop    = 62  // frames between patch starts
	minHz       = 0.0
	maxHz       = 8000.0
	logScale    = 10000.0 // log10(logScale*x + 1)
)

// MinSamples is the shortest clip that yields one patch.
const MinSamples = frameSize + (PatchFrames-1)*hopSize

// Mel computes log-mel patches from mono 16 kHz audio. It is safe for
// concurrent use: all state is read-only after construction, and per-call
// scratch space lives on the stack of the caller's goroutine.
type Mel struct {
	window  [frameSize]float64
	twiddle [frameSize / 2]complex128
	filters [NumBands][]weight // sparse triangles over the 257 bins
}

type weight struct {
	bin int
	w   float64
}

// NewMel builds the window, FFT twiddles and mel filter bank.
func NewMel() *Mel {
	m := &Mel{}
	// Essentia's Hann window is symmetric: divisor N-1.
	for i := range m.window {
		m.window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(frameSize-1))
	}
	for k := range m.twiddle {
		m.twiddle[k] = cmplx.Rect(1, -2*math.Pi*float64(k)/float64(frameSize))
	}
	m.buildFilters()
	return m
}

// Slaney mel scale (as in Essentia's "slaneyMel").
func hzToMel(f float64) float64 {
	const minLogHz, minLogMel = 1000.0, 15.0
	logStep := math.Log(6.4) / 27
	if f >= minLogHz {
		return minLogMel + math.Log(f/minLogHz)/logStep
	}
	return f / (200.0 / 3)
}

func melToHz(m float64) float64 {
	const minLogHz, minLogMel = 1000.0, 15.0
	logStep := math.Log(6.4) / 27
	if m >= minLogMel {
		return minLogHz * math.Exp(logStep*(m-minLogMel))
	}
	return m * 200.0 / 3
}

// buildFilters makes NumBands triangles linear in frequency ("linear"
// weighting), each scaled to unit area in Hz ("unit_tri" in Essentia, whose
// name is misleading: the peak is 2/(r-l), not 1).
func (m *Mel) buildFilters() {
	lo, hi := hzToMel(minHz), hzToMel(maxHz)
	edges := make([]float64, NumBands+2)
	for i := range edges {
		edges[i] = melToHz(lo + (hi-lo)*float64(i)/float64(NumBands+1))
	}
	binHz := float64(SampleRate) / 2 / float64(numBins-1)
	for b := range NumBands {
		l, c, r := edges[b], edges[b+1], edges[b+2]
		area := 2 / (r - l)
		for bin := range numBins {
			f := float64(bin) * binHz
			var w float64
			switch {
			case f > l && f <= c:
				w = (f - l) / (c - l)
			case f > c && f < r:
				w = (r - f) / (r - c)
			}
			if w > 0 {
				m.filters[b] = append(m.filters[b], weight{bin, w * area})
			}
		}
	}
}

// Frames returns the number of mel frames for n samples (full frames only).
func Frames(n int) int {
	if n < frameSize {
		return 0
	}
	return (n-frameSize)/hopSize + 1
}

// Patches returns how many model inputs Frames(frames) yields.
func Patches(frames int) int {
	if frames < PatchFrames {
		return 0
	}
	return (frames-PatchFrames)/PatchHop + 1
}

// LogMel returns Frames(len(samples)) rows of NumBands values, row-major.
func (m *Mel) LogMel(samples []float32) []float32 {
	nf := Frames(len(samples))
	out := make([]float32, nf*NumBands)
	var buf [frameSize]complex128
	var power [numBins]float64
	for f := range nf {
		frame := samples[f*hopSize : f*hopSize+frameSize]
		for i, s := range frame {
			buf[i] = complex(float64(s)*m.window[i], 0)
		}
		m.fft(&buf)
		for k := range numBins {
			re, im := real(buf[k]), imag(buf[k])
			power[k] = re*re + im*im
		}
		row := out[f*NumBands : (f+1)*NumBands]
		for b := range row {
			var acc float64
			for _, w := range m.filters[b] {
				acc += w.w * power[w.bin]
			}
			row[b] = float32(math.Log10(logScale*acc + 1))
		}
	}
	return out
}

// MakePatches slices a LogMel result into model inputs: Patches(frames)
// consecutive blocks of PatchFrames*NumBands values.
func MakePatches(logMel []float32) (data []float32, n int) {
	n = Patches(len(logMel) / NumBands)
	data = make([]float32, 0, n*PatchFrames*NumBands)
	for p := range n {
		start := p * PatchHop * NumBands
		data = append(data, logMel[start:start+PatchFrames*NumBands]...)
	}
	return data, n
}

// fft is an in-place iterative radix-2 FFT of length frameSize.
func (m *Mel) fft(a *[frameSize]complex128) {
	// bit reversal
	for i, j := 1, 0; i < frameSize; i++ {
		bit := frameSize >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	for size := 2; size <= frameSize; size <<= 1 {
		half, step := size/2, frameSize/size
		for start := 0; start < frameSize; start += size {
			for k := 0; k < half; k++ {
				t := m.twiddle[k*step] * a[start+k+half]
				a[start+k+half] = a[start+k] - t
				a[start+k] += t
			}
		}
	}
}
