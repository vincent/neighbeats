// Package lang guesses the language of a lyric from its text.
package lang

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/abadojack/whatlanggo"
)

// Undetermined is stored when the text is too short or the detector is not
// sure; callers treat it as "unknown", never as a language of its own.
const Undetermined = "und"

const (
	// minLetters is the least text worth guessing from.
	minLetters = 40
	// maxRunes caps the cost on very long texts; the start of a lyric is as
	// representative as the rest.
	maxRunes = 4000
)

// Detect returns an ISO 639-3 code ("eng", "fra", ...) and the detector's
// confidence (0..1). Short or ambiguous text gives Undetermined.
func Detect(text string) (code string, confidence float64) {
	if utf8.RuneCountInString(text) > maxRunes {
		text = string([]rune(text)[:maxRunes])
	}
	letters := 0
	for _, r := range text {
		if unicode.IsLetter(r) {
			letters++
		}
	}
	if letters < minLetters {
		return Undetermined, 0
	}
	info := whatlanggo.Detect(text)
	if !info.IsReliable() {
		return Undetermined, info.Confidence
	}
	return strings.ToLower(info.Lang.Iso6393()), info.Confidence
}

// Name returns the English name of a code from Detect, or the code itself.
func Name(code string) string {
	if code == Undetermined || code == "" {
		return "undetermined"
	}
	for l := range whatlanggo.Langs {
		if strings.EqualFold(l.Iso6393(), code) {
			return l.String()
		}
	}
	return code
}

// Known reports whether the code names a language.
func Known(code string) bool { return code != "" && code != Undetermined }
