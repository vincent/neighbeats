// Package m3u writes extended M3U8 playlists.
package m3u

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"strings"
)

// Entry is one playlist item.
type Entry struct {
	Path     string  // as the player will see it
	Artist   string  // optional
	Title    string  // optional
	Duration float64 // seconds; unknown or negative is written as -1
}

// Write emits "#EXTM3U" then an #EXTINF line and the path per entry (UTF-8,
// the usual meaning of the .m3u8 extension).
func Write(w io.Writer, entries []Entry) error {
	bw := bufio.NewWriter(w)
	fmt.Fprintln(bw, "#EXTM3U")
	for _, e := range entries {
		secs := -1
		if e.Duration > 0 {
			secs = int(math.Round(e.Duration))
		}
		fmt.Fprintf(bw, "#EXTINF:%d,%s\n%s\n", secs, display(e), oneLine(e.Path))
	}
	return bw.Flush()
}

func display(e Entry) string {
	switch {
	case e.Artist != "" && e.Title != "":
		return oneLine(e.Artist + " - " + e.Title)
	case e.Title != "":
		return oneLine(e.Title)
	}
	return oneLine(e.Path)
}

// oneLine keeps a stray newline in a tag from breaking the format.
func oneLine(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}
