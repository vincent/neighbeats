package textembed

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"
)

// tinyTokenizer writes a small Unigram tokenizer.json (no charsmap).
func tinyTokenizer(t *testing.T) *Tokenizer {
	t.Helper()
	vocab := [][]any{
		{"<s>", 0}, {"<pad>", 0}, {"</s>", 0}, {"<unk>", 0},
		{"▁", -3.0}, {"▁a", -2.0}, {"▁ab", -1.5}, {"b", -2.5}, {"c", -2.5}, {"▁c", -2.2}, {"bc", -1.0}, {"ab", -4.0},
	}
	doc := map[string]any{
		"added_tokens": []map[string]any{{"content": "<s>", "special": true}, {"content": "</s>", "special": true}},
		"model":        map[string]any{"type": "Unigram", "unk_id": 3, "vocab": vocab},
	}
	data, _ := json.Marshal(doc)
	p := filepath.Join(t.TempDir(), "tokenizer.json")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	tk, err := LoadTokenizer(p)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func TestEncodeTiny(t *testing.T) {
	tk := tinyTokenizer(t)
	for _, c := range []struct {
		text string
		want []int64
	}{
		{"ab", []int64{0, 6, 2}},         // best single piece beats ▁a + b
		{"abc", []int64{0, 5, 10, 2}},    // ▁a + bc (-3.0) beats ▁ab + c (-4.0)
		{"a  c", []int64{0, 5, 9, 2}},    // runs of spaces count once
		{"c ", []int64{0, 9, 4, 2}},      // a trailing space is a token of its own
		{" c", []int64{0, 9, 2}},         // a leading space is not doubled
		{"", []int64{0, 2}},              // nothing to encode
		{"z", []int64{0, 4, 3, 2}},       // unknown character
		{"zz", []int64{0, 4, 3, 2}},      // consecutive unknowns fuse
		{"a <s> c", []int64{0, 5, 9, 2}}, // special strings in text are dropped
	} {
		if got := tk.Encode(c.text, 0); !slices.Equal(got, c.want) {
			t.Errorf("Encode(%q) = %v, want %v", c.text, got, c.want)
		}
	}
	if got := tk.Encode("a c a c a c", 4); len(got) != 4 || got[0] != BOS || got[3] != EOS {
		t.Errorf("truncated to 4: %v", got)
	}
}

func TestSplitWords(t *testing.T) {
	if got := strings.Join(splitWords("▁a▁bc▁"), "|"); got != "▁a|▁bc|▁" {
		t.Errorf("got %q", got)
	}
}

// TestParityWithReference compares Encode with ids dumped by the reference
// Hugging Face tokenizer on ~18,000 strings (multilingual lyric lines,
// whitespace and Unicode edge cases, one string per BMP character). Needs the
// real model files, so it only runs when they are given:
//
//	NEIGHBEATS_TEST_LYRICMODEL=/dir/with/tokenizer.json NEIGHBEATS_TEST_TOKREF=ref.json go test ./internal/textembed
//
// Two known differences are skipped: special-token strings typed in the text
// (Encode drops them) and decomposed Hangul jamo (the reference normalises
// them one character at a time).
func TestParityWithReference(t *testing.T) {
	dir, ref := os.Getenv("NEIGHBEATS_TEST_LYRICMODEL"), os.Getenv("NEIGHBEATS_TEST_TOKREF")
	if dir == "" || ref == "" {
		t.Skip("NEIGHBEATS_TEST_LYRICMODEL / NEIGHBEATS_TEST_TOKREF not set")
	}
	tk, err := LoadTokenizer(filepath.Join(dir, "tokenizer.json"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(ref)
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Text string  `json:"text"`
		IDs  []int64 `json:"ids"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	bad, skipped := 0, 0
	for _, c := range cases {
		if strings.Contains(c.Text, "<s>") || strings.ContainsFunc(c.Text, func(r rune) bool { return unicode.Is(unicode.Hangul, r) && r >= 0x1100 && r < 0x1200 }) {
			skipped++
			continue
		}
		if got := tk.Encode(c.Text, 0); !slices.Equal(got, c.IDs) {
			bad++
			if bad <= 10 {
				t.Errorf("%q\n got  %v\n want %v", c.Text, got, c.IDs)
			}
		}
	}
	t.Logf("%d cases, %d skipped, %d differ", len(cases), skipped, bad)
}
