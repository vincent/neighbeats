// Package tags reads the metadata neighbeats cares about from audio files:
// basic descriptive tags and the MusicBrainz identifiers written by Picard
// and beets.
package tags

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/dhowden/tag"
)

// Info is the metadata extracted from one file.
type Info struct {
	Title       string
	Artist      string
	Album       string
	AlbumArtist string
	Genre       string
	Year        int
	TrackNo     int
	DiscNo      int

	MBIDRecording    string // the recording (what a "song" is in MusicBrainz)
	MBIDReleaseTrack string // the track on one specific release
	MBIDArtist       string
	MBIDAlbumArtist  string
	MBIDRelease      string
	MBIDReleaseGroup string
	MBIDWork         string
}

var uuidRe = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// Read opens path and extracts its metadata. A panic inside the parser on a
// corrupt file is converted to an error.
func Read(path string) (info Info, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("tag parser panic: %v", r)
		}
	}()
	f, err := os.Open(path)
	if err != nil {
		return Info{}, err
	}
	defer f.Close()

	m, err := tag.ReadFrom(f)
	if err != nil {
		return Info{}, err
	}
	info = Info{
		Title:       strings.TrimSpace(m.Title()),
		Artist:      strings.TrimSpace(m.Artist()),
		Album:       strings.TrimSpace(m.Album()),
		AlbumArtist: strings.TrimSpace(m.AlbumArtist()),
		Genre:       strings.TrimSpace(m.Genre()),
		Year:        m.Year(),
	}
	info.TrackNo, _ = m.Track()
	info.DiscNo, _ = m.Disc()
	applyMBIDs(&info, m.Raw())
	return info, nil
}

// applyMBIDs fills the MBID fields from a format's raw tag map. The raw map
// differs by container:
//   - ID3v2 (MP3): the recording id is a UFID frame owned by musicbrainz.org;
//     everything else is a TXXX frame, keyed "TXXX", "TXXX_0", ... with the
//     real name in the description.
//   - MP4 (M4A/AAC): iTunes freeform atoms, keyed by atom name.
//   - Vorbis (FLAC/Ogg/Opus): keys such as MUSICBRAINZ_TRACKID.
//
// Names are compared after lowercasing and dropping punctuation, so
// "MusicBrainz Artist Id" and "MUSICBRAINZ_ARTISTID" are the same key.
func applyMBIDs(info *Info, raw map[string]interface{}) {
	var recordingFromTrackID string
	for key, v := range raw {
		name := key
		var text string
		switch val := v.(type) {
		case *tag.UFID:
			if strings.Contains(strings.ToLower(val.Provider), "musicbrainz.org") {
				info.MBIDRecording = firstUUID(string(val.Identifier))
			}
			continue
		case *tag.Comm:
			if strings.HasPrefix(key, "TXXX") {
				name = val.Description
			}
			text = val.Text
		case string:
			text = val
		case []string:
			if len(val) > 0 {
				text = val[0]
			}
		default:
			continue
		}
		id := firstUUID(text)
		if id == "" {
			continue
		}
		switch normalise(name) {
		case "musicbrainzalbumid":
			info.MBIDRelease = id
		case "musicbrainzartistid":
			info.MBIDArtist = id
		case "musicbrainzalbumartistid":
			info.MBIDAlbumArtist = id
		case "musicbrainzreleasegroupid":
			info.MBIDReleaseGroup = id
		case "musicbrainzreleasetrackid":
			info.MBIDReleaseTrack = id
		case "musicbrainzworkid":
			info.MBIDWork = id
		case "musicbrainztrackid":
			// In MP4 and Vorbis (Picard convention) this is the recording id.
			recordingFromTrackID = id
		}
	}
	if info.MBIDRecording == "" {
		info.MBIDRecording = recordingFromTrackID
	}
}

// firstUUID returns the first UUID in s, lowercased. Multi-artist tags hold
// several ids separated by ";" or "/", and only the first is kept.
func firstUUID(s string) string {
	return strings.ToLower(uuidRe.FindString(s))
}

func normalise(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
