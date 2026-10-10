package lyrics

import (
	"strings"
	"testing"
)

func TestParseSynced(t *testing.T) {
	in := "\xef\xbb\xbf[ar:Band]\r\n[ti:Song]\r\n[offset:0]\r\n" +
		"[00:20.50]Second line\r\n[00:10.00]First line\r\n" +
		"[00:05.00]\r\n" + // instrumental gap
		"[00:30.00][01:10.00]Chorus <00:30.10>words\r\n"
	p := ParseLRC([]byte(in))
	want := "First line\nSecond line\nChorus words\nChorus words"
	if p.Text != want || !p.Synced || p.LineCount != 4 {
		t.Errorf("got %+v\nwant text %q", p, want)
	}
}

func TestParseOffset(t *testing.T) {
	// A positive offset makes lyrics earlier, so line B (10.2 s) at -300 ms
	// moves ahead of line A (10.0 s).
	p := ParseLRC([]byte("[offset:300]\n[00:10.00]A\n[00:10.20]B\n[00:10.35]C\n"))
	if p.Text != "A\nB\nC" {
		t.Errorf("text = %q", p.Text)
	}
}

func TestParsePlainText(t *testing.T) {
	p := ParseLRC([]byte("[ar:X]\nhello there\n\n[Chorus]\nla la\n"))
	if p.Synced || p.Text != "hello there\n[Chorus]\nla la" || p.LineCount != 3 {
		t.Errorf("got %+v", p)
	}
}

func TestParseLatin1Fallback(t *testing.T) {
	p := ParseLRC([]byte("[00:01.00]caf\xe9\n"))
	if p.Text != "café" {
		t.Errorf("text = %q", p.Text)
	}
}

func TestParseEmpty(t *testing.T) {
	for _, in := range []string{"", "[ar:A]\n[ti:B]\n", "[00:01.00]\n[00:02.00]  \n"} {
		if p := ParseLRC([]byte(in)); p.LineCount != 0 || strings.TrimSpace(p.Text) != "" {
			t.Errorf("%q -> %+v", in, p)
		}
	}
}

func TestParseStamp(t *testing.T) {
	for in, want := range map[string]int64{"00:01": 1000, "01:02.5": 62500, "01:02.50": 62500, "01:02,125": 62125, "10:00.000": 600000} {
		if got, ok := parseStamp(in); !ok || got != want {
			t.Errorf("%q = %d,%v want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"ar:Band", "1", ":10", "00:xx", "00:100"} {
		if _, ok := parseStamp(in); ok {
			t.Errorf("%q parsed", in)
		}
	}
}

func TestSidecarPath(t *testing.T) {
	for in, want := range map[string]string{
		"/m/a.mp3":        "/m/a.lrc",
		"/m/a.b.flac":     "/m/a.b.lrc",
		"/m.d/noext":      "/m.d/noext.lrc",
		"/m/Song (1).m4a": "/m/Song (1).lrc",
	} {
		if got := SidecarPath(in); got != want {
			t.Errorf("%q = %q want %q", in, got, want)
		}
	}
}
