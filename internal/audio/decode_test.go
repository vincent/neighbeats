package audio

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func newDecoder(t *testing.T) *Decoder {
	t.Helper()
	d, err := NewDecoder()
	if err != nil {
		t.Skip("ffmpeg/ffprobe not installed")
	}
	return d
}

func makeWAV(t *testing.T, d *Decoder, seconds string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tone.wav")
	out, err := exec.Command(d.FFmpeg, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=22050",
		"-t", seconds, p).CombinedOutput()
	if err != nil {
		t.Fatalf("make wav: %v: %s", err, out)
	}
	return p
}

func TestMiddleClipTakesTheMiddle(t *testing.T) {
	d := newDecoder(t)
	clip, err := d.MiddleClip(context.Background(), makeWAV(t, d, "40"), 10, 16000, 100)
	if err != nil {
		t.Fatal(err)
	}
	if clip.Start < 14.9 || clip.Start > 15.1 || clip.Duration < 39.9 || clip.Duration > 40.1 {
		t.Errorf("start %.2f duration %.2f, want 15 and 40", clip.Start, clip.Duration)
	}
	if n := len(clip.Samples); n < 159000 || n > 161000 {
		t.Errorf("%d samples, want about 160000", n)
	}
}

func TestShortTrackIsDecodedWhole(t *testing.T) {
	d := newDecoder(t)
	clip, err := d.MiddleClip(context.Background(), makeWAV(t, d, "4"), 30, 16000, 100)
	if err != nil || clip.Start != 0 || len(clip.Samples) < 63000 {
		t.Fatalf("start %v samples %d err %v", clip.Start, len(clip.Samples), err)
	}
}

func TestUnreadableFileErrorSaysWhy(t *testing.T) {
	d := newDecoder(t)
	p := filepath.Join(t.TempDir(), "junk.mp3")
	if err := os.WriteFile(p, []byte("this is not audio at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := d.MiddleClip(context.Background(), p, 10, 16000, 100)
	if err == nil || !strings.HasPrefix(err.Error(), "ffprobe: exit status") || strings.HasSuffix(err.Error(), ": ") {
		t.Fatalf("error should carry ffprobe's reason, got %v", err)
	}
	t.Log(err)
}
