// Package lyrics reads sibling .lrc files and keeps their text in the index.
package lyrics

import (
	"sort"
	"strings"
	"unicode/utf8"
)

// Parsed is the plain-text content of one LRC file.
type Parsed struct {
	Text      string // lyric lines in singing order, one per line, no timestamps
	Synced    bool   // true when the file carried line timestamps
	LineCount int
}

type timedLine struct {
	ms   int64
	text string
	seq  int
}

// ParseLRC extracts the lyric text from LRC content. Metadata tags such as
// [ar:], [ti:] and [offset:] are dropped, as are word-level <mm:ss.xx> marks.
// A line with several leading timestamps (a repeated chorus) is emitted once
// per timestamp, and lines are ordered by time so the text reads in song order.
// Content that is not valid UTF-8 is decoded as Latin-1 rather than rejected.
func ParseLRC(data []byte) Parsed {
	s := string(data)
	if !utf8.ValidString(s) {
		rs := make([]rune, len(data))
		for i, b := range data {
			rs[i] = rune(b)
		}
		s = string(rs)
	}
	s = strings.TrimPrefix(s, "\ufeff")

	var timed []timedLine
	var plain []string
	var offset int64
	seq := 0

	for _, raw := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(strings.ReplaceAll(raw, "\r", ""))
		if line == "" {
			continue
		}
		var stamps []int64
		rest := line
		for strings.HasPrefix(rest, "[") {
			end := strings.IndexByte(rest, ']')
			if end < 0 {
				break
			}
			tag := rest[1:end]
			if ms, ok := parseStamp(tag); ok {
				stamps = append(stamps, ms)
			} else if k, v, ok := strings.Cut(tag, ":"); ok && isMetaKey(k) {
				if strings.EqualFold(strings.TrimSpace(k), "offset") {
					offset = parseOffset(v)
				}
			} else {
				// Not a tag at all (e.g. "[Chorus]" written by hand): keep it as text.
				break
			}
			rest = strings.TrimSpace(rest[end+1:])
		}
		text := stripWordMarks(rest)
		if len(stamps) == 0 {
			// Metadata-only lines leave nothing; hand-written lines are kept.
			if text != "" && !(len(line) > len(rest)) {
				plain = append(plain, text)
			}
			continue
		}
		if text == "" {
			continue // timestamp with no words: an instrumental gap
		}
		for _, ms := range stamps {
			timed = append(timed, timedLine{ms: ms, text: text, seq: seq})
			seq++
		}
	}

	if len(timed) == 0 {
		return Parsed{Text: strings.Join(plain, "\n"), LineCount: len(plain)}
	}
	for i := range timed {
		timed[i].ms += offset
	}
	sort.SliceStable(timed, func(i, j int) bool {
		if timed[i].ms != timed[j].ms {
			return timed[i].ms < timed[j].ms
		}
		return timed[i].seq < timed[j].seq
	})
	out := make([]string, len(timed))
	for i, t := range timed {
		out[i] = t.text
	}
	return Parsed{Text: strings.Join(out, "\n"), Synced: true, LineCount: len(out)}
}

// parseStamp parses "mm:ss", "mm:ss.xx" or "mm:ss.xxx" into milliseconds.
func parseStamp(tag string) (int64, bool) {
	m, rest, ok := strings.Cut(tag, ":")
	if !ok || m == "" || !allDigits(m) || len(m) > 4 {
		return 0, false
	}
	sec, frac, _ := strings.Cut(strings.ReplaceAll(rest, ",", "."), ".")
	if sec == "" || !allDigits(sec) || len(sec) > 2 || (frac != "" && !allDigits(frac)) {
		return 0, false
	}
	ms := atoi(m)*60000 + atoi(sec)*1000
	if frac != "" {
		f := (frac + "000")[:3]
		ms += atoi(f)
	}
	return ms, true
}

func parseOffset(v string) int64 {
	v = strings.TrimSpace(v)
	neg := strings.HasPrefix(v, "-")
	v = strings.TrimLeft(v, "+-")
	if v == "" || !allDigits(v) {
		return 0
	}
	// The LRC offset tag is in milliseconds and positive values make lyrics
	// appear earlier, so it is subtracted from the stamps.
	if neg {
		return atoi(v)
	}
	return -atoi(v)
}

func isMetaKey(k string) bool {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case "ar", "ti", "al", "au", "by", "length", "offset", "re", "ve", "tool", "id", "la", "lang":
		return true
	}
	return false
}

// stripWordMarks removes enhanced-LRC word timestamps like <00:12.30>.
func stripWordMarks(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '<' {
			if end := strings.IndexByte(s[i:], '>'); end > 0 {
				if _, ok := parseStamp(s[i+1 : i+end]); ok {
					i += end + 1
					continue
				}
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func atoi(s string) int64 {
	var n int64
	for _, r := range s {
		n = n*10 + int64(r-'0')
	}
	return n
}
