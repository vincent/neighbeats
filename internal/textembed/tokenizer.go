// Package textembed turns text into embeddings with a multilingual sentence
// model (multilingual-e5-small) through ONNX Runtime. tokenizer.go is a
// pure-Go reader for the model's Hugging Face tokenizer.json (XLM-R
// SentencePiece Unigram), checked against the reference tokenizer in
// tokenizer_test.go.
package textembed

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Special token ids of the XLM-R vocabulary.
const (
	BOS = 0
	PAD = 1
	EOS = 2
	unk = 3
)

// Tokenizer splits text into vocabulary ids.
type Tokenizer struct {
	cm       *charsmap
	pieces   map[string]int32
	scores   []float64
	maxRunes int
	unkScore float64
	specials []string
}

type tokenizerJSON struct {
	Normalizer struct {
		Normalizers []struct {
			Type     string `json:"type"`
			Charsmap string `json:"precompiled_charsmap"`
		} `json:"normalizers"`
	} `json:"normalizer"`
	AddedTokens []struct {
		Content string `json:"content"`
		Special bool   `json:"special"`
	} `json:"added_tokens"`
	Model struct {
		Type  string              `json:"type"`
		UnkID int                 `json:"unk_id"`
		Vocab [][]json.RawMessage `json:"vocab"`
	} `json:"model"`
}

// LoadTokenizer reads a Hugging Face tokenizer.json of an XLM-R model.
func LoadTokenizer(path string) (*Tokenizer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var j tokenizerJSON
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if j.Model.Type != "Unigram" || j.Model.UnkID != unk || len(j.Model.Vocab) < 4 {
		return nil, fmt.Errorf("%s: not an XLM-R Unigram tokenizer", path)
	}
	t := &Tokenizer{pieces: make(map[string]int32, len(j.Model.Vocab)), scores: make([]float64, len(j.Model.Vocab))}
	for _, n := range j.Normalizer.Normalizers {
		if n.Type != "Precompiled" {
			continue
		}
		blob, err := base64.StdEncoding.DecodeString(n.Charsmap)
		if err != nil {
			return nil, fmt.Errorf("%s: charsmap: %w", path, err)
		}
		if t.cm, err = parseCharsmap(blob); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	minScore := math.Inf(1)
	for i, e := range j.Model.Vocab {
		if len(e) != 2 {
			return nil, fmt.Errorf("%s: bad vocab entry %d", path, i)
		}
		var piece string
		var score float64
		if err := json.Unmarshal(e[0], &piece); err != nil {
			return nil, fmt.Errorf("%s: vocab %d: %w", path, i, err)
		}
		if err := json.Unmarshal(e[1], &score); err != nil {
			return nil, fmt.Errorf("%s: vocab %d: %w", path, i, err)
		}
		t.scores[i] = score
		if i > unk { // ids 0-3 are specials, never produced from text
			t.pieces[piece] = int32(i)
			t.maxRunes = max(t.maxRunes, utf8.RuneCountInString(piece))
			minScore = min(minScore, score)
		}
	}
	t.unkScore = minScore - 10 // SentencePiece's kUnkPenalty
	for _, a := range j.AddedTokens {
		if a.Special {
			t.specials = append(t.specials, a.Content)
		}
	}
	return t, nil
}

var manySpaces = regexp.MustCompile(` {2,}`)

// Encode returns <s> ids </s>, truncated to maxLen ids (0 = no limit).
func (t *Tokenizer) Encode(text string, maxLen int) []int64 {
	for _, sp := range t.specials {
		text = strings.ReplaceAll(text, sp, " ")
	}
	if t.cm != nil {
		text = t.cm.normalize(text)
	}
	// Runs of spaces collapse to one (the model's Replace rule). Metaspace
	// then puts a "▁" before every word, including a lone one for a trailing
	// space, and before the text if it does not start with a space.
	text = manySpaces.ReplaceAllString(text, " ")
	ids := []int64{BOS}
	if text != "" {
		text = strings.ReplaceAll(text, " ", "▁")
		if !strings.HasPrefix(text, "▁") {
			text = "▁" + text
		}
		for _, w := range splitWords(text) {
			ids = t.segment(w, ids)
		}
	}
	if maxLen > 0 && len(ids) > maxLen-1 {
		ids = ids[:maxLen-1]
	}
	return append(ids, EOS)
}

// segment appends the best-scoring segmentation (Viterbi) of one word.
func (t *Tokenizer) segment(word string, out []int64) []int64 {
	// starts[i] is the byte offset of rune i; the last entry is len(word).
	starts := make([]int, 0, len(word)+1)
	for i := range word {
		starts = append(starts, i)
	}
	n := len(starts)
	starts = append(starts, len(word))

	best := make([]float64, n+1)
	from := make([]int, n+1)
	id := make([]int32, n+1)
	for i := 1; i <= n; i++ {
		best[i] = math.Inf(-1)
	}
	for i := 0; i < n; i++ {
		if math.IsInf(best[i], -1) {
			continue
		}
		hit := false
		for l := 1; l <= t.maxRunes && i+l <= n; l++ {
			p, ok := t.pieces[word[starts[i]:starts[i+l]]]
			if !ok {
				continue
			}
			hit = true
			if s := best[i] + t.scores[p]; s > best[i+l] {
				best[i+l], from[i+l], id[i+l] = s, i, p
			}
		}
		if !hit || math.IsInf(best[i+1], -1) {
			// No piece covers this character: it becomes <unk>.
			if s := best[i] + t.unkScore; s > best[i+1] {
				best[i+1], from[i+1], id[i+1] = s, i, unk
			}
		}
	}
	var rev []int32
	for i := n; i > 0; i = from[i] {
		rev = append(rev, id[i])
	}
	for i := len(rev) - 1; i >= 0; i-- {
		if rev[i] == unk && len(out) > 0 && out[len(out)-1] == unk {
			continue // consecutive unknown characters fuse into one
		}
		out = append(out, int64(rev[i]))
	}
	return out
}

// splitWords cuts "▁a▁b▁" into "▁a", "▁b", "▁".
func splitWords(s string) []string {
	var out []string
	start := 0
	for i, r := range s {
		if r == '▁' && i > start {
			out = append(out, s[start:i])
			start = i
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
