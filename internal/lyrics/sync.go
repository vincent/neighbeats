package lyrics

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/vincentlark/neighbeats/internal/lang"
	"github.com/vincentlark/neighbeats/internal/store"
)

// maxLRCSize skips files that are clearly not lyrics.
const maxLRCSize = 1 << 20

// Result counts what a sync did.
type Result struct {
	Tracks    int // tracks examined
	Added     int
	Updated   int
	Unchanged int
	Removed   int // lyrics dropped because the .lrc is gone
	Empty     int // .lrc files with no lyric text
	Errors    int
	Detected  int // languages guessed for lyrics that had none
}

// SidecarPath returns the .lrc path next to an audio file.
func SidecarPath(audio string) string {
	if i := strings.LastIndexByte(audio, '.'); i > strings.LastIndexByte(audio, '/') && i >= 0 {
		return audio[:i] + ".lrc"
	}
	return audio + ".lrc"
}

// Sync reads the sibling .lrc of every indexed track and updates the lyrics
// table. Unchanged files (same size and mtime) are not re-read. A missing .lrc
// removes the stored lyrics, but any other stat error keeps them, so a
// flaky share does not wipe data.
func Sync(ctx context.Context, st *store.Store) (Result, error) {
	var res Result
	refs, err := st.TrackRefs(ctx)
	if err != nil {
		return res, err
	}
	known, err := st.LyricsFingerprints(ctx)
	if err != nil {
		return res, err
	}

	var upserts []store.Lyrics
	var removes []int64
	flush := func() error {
		if err := st.UpsertLyrics(ctx, upserts); err != nil {
			return err
		}
		if err := st.DeleteLyrics(ctx, removes); err != nil {
			return err
		}
		upserts, removes = upserts[:0], removes[:0]
		return nil
	}

	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		res.Tracks++
		lrc := SidecarPath(ref.Path)
		fi, err := os.Stat(lrc)
		prev, had := known[ref.ID]
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if had {
				removes = append(removes, ref.ID)
				res.Removed++
			}
		case err != nil || !fi.Mode().IsRegular() || fi.Size() > maxLRCSize:
			res.Errors++
		case had && prev.Path == lrc && prev.Size == fi.Size() && prev.MtimeNs == fi.ModTime().UnixNano():
			res.Unchanged++
		default:
			data, err := os.ReadFile(lrc)
			if err != nil {
				res.Errors++
				break
			}
			p := ParseLRC(data)
			if p.LineCount == 0 {
				res.Empty++
				if had {
					removes = append(removes, ref.ID)
					res.Removed++
				}
				break
			}
			upserts = append(upserts, store.Lyrics{
				TrackID: ref.ID, Path: lrc, Size: fi.Size(), MtimeNs: fi.ModTime().UnixNano(),
				Text: p.Text, Synced: p.Synced, LineCount: p.LineCount,
			})
			if had {
				res.Updated++
			} else {
				res.Added++
			}
		}
		if len(upserts)+len(removes) >= 500 {
			if err := flush(); err != nil {
				return res, err
			}
		}
	}
	if err := flush(); err != nil {
		return res, err
	}
	res.Detected, err = DetectLanguages(ctx, st)
	return res, err
}

// DetectLanguages guesses the language of every lyric that has none yet.
func DetectLanguages(ctx context.Context, st *store.Store) (int, error) {
	todo, err := st.LyricsWithoutLang(ctx)
	if err != nil {
		return 0, err
	}
	const chunk = 500
	for start := 0; start < len(todo); start += chunk {
		if err := ctx.Err(); err != nil {
			return start, err
		}
		part := todo[start:min(start+chunk, len(todo))]
		langs := make([]store.LyricLang, len(part))
		for i, l := range part {
			code, conf := lang.Detect(l.Text)
			langs[i] = store.LyricLang{TrackID: l.TrackID, Lang: code, Confidence: conf}
		}
		if err := st.SetLyricLangs(ctx, langs); err != nil {
			return start, err
		}
	}
	return len(todo), nil
}
