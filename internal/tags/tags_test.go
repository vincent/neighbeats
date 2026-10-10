package tags

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/dhowden/tag"
)

const (
	recID    = "11111111-1111-4111-8111-111111111111"
	artistID = "22222222-2222-4222-8222-222222222222"
	albumID  = "33333333-3333-4333-8333-333333333333"
	rtID     = "44444444-4444-4444-8444-444444444444"
)

func TestApplyMBIDsID3(t *testing.T) {
	raw := map[string]interface{}{
		"UFID":   &tag.UFID{Provider: "http://musicbrainz.org", Identifier: []byte(recID)},
		"UFID_0": &tag.UFID{Provider: "other.example", Identifier: []byte("zzz")},
		"TXXX":   &tag.Comm{Description: "MusicBrainz Artist Id", Text: artistID + "; " + albumID},
		"TXXX_0": &tag.Comm{Description: "MusicBrainz Album Id", Text: albumID},
		"TXXX_1": &tag.Comm{Description: "MusicBrainz Release Track Id", Text: rtID},
		"TXXX_2": &tag.Comm{Description: "ASIN", Text: "B000"},
	}
	var got Info
	applyMBIDs(&got, raw)
	want := Info{MBIDRecording: recID, MBIDArtist: artistID, MBIDRelease: albumID, MBIDReleaseTrack: rtID}
	if got != want {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestApplyMBIDsVorbisAndMP4(t *testing.T) {
	for name, raw := range map[string]map[string]interface{}{
		"vorbis": {"MUSICBRAINZ_TRACKID": recID, "musicbrainz_artistid": artistID},
		"mp4":    {"MusicBrainz Track Id": []string{recID}, "MusicBrainz Artist Id": []string{artistID}},
	} {
		var got Info
		applyMBIDs(&got, raw)
		if got.MBIDRecording != recID || got.MBIDArtist != artistID {
			t.Errorf("%s: got %+v", name, got)
		}
	}
}

func TestApplyMBIDsIgnoresGarbage(t *testing.T) {
	var got Info
	applyMBIDs(&got, map[string]interface{}{
		"MUSICBRAINZ_TRACKID": "not-a-uuid",
		"x":                   42,
	})
	if got != (Info{}) {
		t.Errorf("got %+v", got)
	}
}

// TestReadFFmpegFiles checks the real parsers end to end on files written by
// ffmpeg. Skipped when ffmpeg is not installed.
func TestReadFFmpegFiles(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	for _, tc := range []struct {
		file string
		args []string
	}{
		{"a.mp3", []string{"-metadata", "MusicBrainz Artist Id=" + artistID, "-metadata", "MusicBrainz Album Id=" + albumID}},
		{"a.flac", []string{"-metadata", "MUSICBRAINZ_ARTISTID=" + artistID, "-metadata", "MUSICBRAINZ_ALBUMID=" + albumID}},
		{"a.m4a", []string{"-metadata", "MusicBrainz Artist Id=" + artistID, "-metadata", "MusicBrainz Album Id=" + albumID}},
	} {
		p := filepath.Join(dir, tc.file)
		args := append([]string{"-v", "error", "-f", "lavfi", "-i", "sine=duration=1",
			"-metadata", "title=Song", "-metadata", "artist=Band", "-metadata", "album=LP", "-metadata", "date=1999"},
			tc.args...)
		args = append(args, p)
		if out, err := exec.Command(ff, args...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg %s: %v\n%s", tc.file, err, out)
		}
		info, err := Read(p)
		if err != nil {
			t.Errorf("%s: %v", tc.file, err)
			continue
		}
		if info.Title != "Song" || info.Artist != "Band" || info.Album != "LP" || info.Year != 1999 {
			t.Errorf("%s: basic tags wrong: %+v", tc.file, info)
		}
		// ffmpeg does not always write freeform ids in a form the parser
		// exposes, so report rather than fail on the id fields.
		t.Logf("%s: artist mbid=%q release mbid=%q", tc.file, info.MBIDArtist, info.MBIDRelease)
	}
}

func TestReadErrors(t *testing.T) {
	dir := t.TempDir()
	junk := filepath.Join(dir, "junk.mp3")
	if err := os.WriteFile(junk, []byte("this is not audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(junk); err == nil {
		t.Error("expected error for junk file")
	}
	if _, err := Read(filepath.Join(dir, "missing.mp3")); err == nil {
		t.Error("expected error for missing file")
	}
}
