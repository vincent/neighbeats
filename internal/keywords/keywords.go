// Package keywords finds the words two lyrics have in common that say
// something: frequent in both, rare in the library as a whole (TF-IDF).
package keywords

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

const (
	minRunes = 3
	maxRunes = 20 // longer runs are scripts without spaces, not words
	// maxDocShare drops words found in more than this share of the lyrics
	// ("love", "baby" and every function word).
	maxDocShare = 0.04
	// minDocs keeps the filter meaningful on a tiny library: with fewer
	// lyrics every word looks common.
	minDocs = 50
	// minCount is how often a word must occur in each lyric to count; a word
	// repeated in a chorus is about the song, a one-off is not.
	minCount = 2
)

// Corpus holds how many lyrics each word occurs in.
type Corpus struct {
	docs int
	df   map[string]int
}

// NewCorpus returns an empty corpus; Add every lyric once.
func NewCorpus() *Corpus { return &Corpus{df: map[string]int{}} }

// Add counts the words of one lyric.
func (c *Corpus) Add(text string) {
	c.docs++
	for w := range Terms(text) {
		c.df[w]++
	}
}

// Docs is the number of lyrics added.
func (c *Corpus) Docs() int { return c.docs }

// Terms returns the lower-cased words of a text with how often each occurs.
func Terms(text string) map[string]int {
	out := map[string]int{}
	for _, w := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && r != '\''
	}) {
		w = strings.Trim(w, "'")
		if n := len([]rune(w)); n >= minRunes && n <= maxRunes {
			out[w]++
		}
	}
	return out
}

// Shared returns up to n words both texts use, the most telling first. It is
// empty when the corpus is too small to tell common words from telling ones.
func (c *Corpus) Shared(a, b string, n int) []string {
	if c.docs < minDocs {
		return nil
	}
	ta, tb := Terms(a), Terms(b)
	type scored struct {
		word  string
		score float64
	}
	var hits []scored
	for w, ca := range ta {
		cb := tb[w]
		df := c.df[w]
		if ca < minCount || cb < minCount || df == 0 || float64(df) > maxDocShare*float64(c.docs) {
			continue
		}
		idf := math.Log(float64(c.docs) / float64(df))
		hits = append(hits, scored{w, idf * math.Sqrt(float64(min(ca, cb)))})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].word < hits[j].word
	})
	var out []string
	for _, h := range hits[:min(n, len(hits))] {
		out = append(out, h.word)
	}
	return out
}
