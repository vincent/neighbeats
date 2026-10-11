package m3u

import (
	"strings"
	"testing"
)

func TestWrite(t *testing.T) {
	var b strings.Builder
	err := Write(&b, []Entry{
		{Path: "/data/music/a.flac", Artist: "A", Title: "One", Duration: 200.4},
		{Path: "/data/music/b.mp3", Title: "Two\nlines"},
		{Path: "/data/music/c.mp3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "#EXTM3U\n" +
		"#EXTINF:200,A - One\n/data/music/a.flac\n" +
		"#EXTINF:-1,Two lines\n/data/music/b.mp3\n" +
		"#EXTINF:-1,/data/music/c.mp3\n/data/music/c.mp3\n"
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}
}
