package keywords

import (
	"fmt"
	"slices"
	"testing"
)

func corpus() *Corpus {
	c := NewCorpus()
	for i := range 200 {
		// "love" and "you" are in every lyric, "ocean" in a handful.
		text := fmt.Sprintf("love you love you filler%d", i)
		if i%40 == 0 {
			text += " ocean ocean harbour harbour"
		}
		c.Add(text)
	}
	return c
}

func TestSharedPicksTellingWords(t *testing.T) {
	c := corpus()
	got := c.Shared("love love you ocean ocean harbour harbour ship ship", "I love you, ocean ocean, harbour harbour, tower tower", 3)
	if want := []string{"harbour", "ocean"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSharedNeedsRepetitionAndRarity(t *testing.T) {
	c := corpus()
	// "ocean" appears once in each: not about it. "love" is everywhere.
	if got := c.Shared("ocean love love", "ocean love love", 3); len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

func TestSmallCorpusSaysNothing(t *testing.T) {
	c := NewCorpus()
	c.Add("ocean ocean")
	if got := c.Shared("ocean ocean", "ocean ocean", 3); got != nil {
		t.Errorf("got %v", got)
	}
}

func TestTerms(t *testing.T) {
	got := Terms("Don't stop, DON'T stop! 'tis a ok 夜に駆ける")
	if got["don't"] != 2 || got["stop"] != 2 || got["tis"] != 1 || got["ok"] != 0 {
		t.Errorf("terms: %v", got)
	}
}
