// Package audio decodes a clip of a track to mono float32 PCM through ffmpeg.
package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
)

// Clip is decoded audio and where it came from.
type Clip struct {
	Samples  []float32 // mono, at the requested sample rate
	Start    float64   // seconds into the track
	Duration float64   // full track length in seconds
}

// Decoder spawns ffprobe and ffmpeg.
type Decoder struct {
	FFmpeg, FFprobe string
}

// NewDecoder looks both tools up on PATH.
func NewDecoder() (*Decoder, error) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		return nil, errors.New("ffmpeg not found on PATH")
	}
	fp, err := exec.LookPath("ffprobe")
	if err != nil {
		return nil, errors.New("ffprobe not found on PATH")
	}
	return &Decoder{FFmpeg: ff, FFprobe: fp}, nil
}

// Duration returns the track length in seconds.
func (d *Decoder) Duration(ctx context.Context, path string) (float64, error) {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, d.FFprobe, "-v", "error",
		"-show_entries", "format=duration", "-of", "default=nw=1:nk=1", path)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe: %w: %s", err, lastLine(stderr.String()))
	}
	sec, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || sec <= 0 || math.IsInf(sec, 0) || math.IsNaN(sec) {
		return 0, fmt.Errorf("ffprobe: no usable duration (%q)", strings.TrimSpace(string(out)))
	}
	return sec, nil
}

// MiddleClip decodes up to clipSeconds from the middle of the track at the
// given sample rate. Tracks shorter than the clip are decoded whole. Some
// files (VBR MP3 without a header) report a duration longer than their audio,
// so a middle clip that comes back too short is retried from the start.
func (d *Decoder) MiddleClip(ctx context.Context, path string, clipSeconds float64, rate, minSamples int) (Clip, error) {
	dur, err := d.Duration(ctx, path)
	if err != nil {
		return Clip{}, err
	}
	start := 0.0
	if dur > clipSeconds {
		start = (dur - clipSeconds) / 2
	}
	samples, err := d.decode(ctx, path, start, clipSeconds, rate)
	if err == nil && len(samples) < minSamples && start > 0 {
		start = 0
		samples, err = d.decode(ctx, path, start, clipSeconds, rate)
	}
	if err != nil {
		return Clip{}, err
	}
	return Clip{Samples: samples, Start: start, Duration: dur}, nil
}

func (d *Decoder) decode(ctx context.Context, path string, start, length float64, rate int) ([]float32, error) {
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, d.FFmpeg, "-v", "error", "-nostdin",
		"-ss", strconv.FormatFloat(start, 'f', 3, 64),
		"-t", strconv.FormatFloat(length, 'f', 3, 64),
		"-i", path, "-vn", "-ac", "1", "-ar", strconv.Itoa(rate), "-f", "f32le", "pipe:1")
	cmd.Stderr = &stderr
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg: %w: %s", err, lastLine(stderr.String()))
	}
	samples := make([]float32, len(raw)/4)
	for i := range samples {
		samples[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
	}
	return samples, nil
}

// lastLine keeps error messages short: the last non-empty line of tool output.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
