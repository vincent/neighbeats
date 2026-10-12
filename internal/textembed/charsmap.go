package textembed

import (
	"encoding/binary"
	"errors"
	"strings"
	"unicode/utf8"
)

// charsmap is SentencePiece's precompiled normalisation table (the "nmt_nfkc"
// rules the XLM-R tokenizer ships): a Darts double-array trie that maps a
// character sequence to the offset of its replacement in a string pool.
type charsmap struct {
	units []uint32
	pool  []byte
}

func parseCharsmap(blob []byte) (*charsmap, error) {
	if len(blob) < 4 {
		return nil, errors.New("charsmap too short")
	}
	n := int(binary.LittleEndian.Uint32(blob))
	if n%4 != 0 || 4+n > len(blob) {
		return nil, errors.New("charsmap: bad trie size")
	}
	c := &charsmap{units: make([]uint32, n/4), pool: blob[4+n:]}
	for i := range c.units {
		c.units[i] = binary.LittleEndian.Uint32(blob[4+4*i:])
	}
	return c, nil
}

// match returns the replacement for the longest rule that is a prefix of s,
// and how many bytes of s that rule consumes.
func (c *charsmap) match(s string) (repl string, used int, ok bool) {
	at := func(i uint32) uint32 {
		if int(i) < len(c.units) {
			return c.units[i]
		}
		return 0
	}
	offset := func(u uint32) uint32 { return (u >> 10) << ((u & (1 << 9)) >> 6) }
	pos := uint32(0)
	pos ^= offset(at(pos))
	for i := 0; i < len(s); i++ {
		pos ^= uint32(s[i])
		u := at(pos)
		if u&(1<<31|0xFF) != uint32(s[i]) {
			break
		}
		pos ^= offset(u)
		if (u>>8)&1 == 1 {
			v := at(pos) & (1<<31 - 1)
			if int(v) < len(c.pool) {
				end := v
				for int(end) < len(c.pool) && c.pool[end] != 0 {
					end++
				}
				repl, used, ok = string(c.pool[v:end]), i+1, true
			}
		}
	}
	return
}

// normalize applies the rules from left to right; text without a rule is
// copied as is.
func (c *charsmap) normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if repl, used, ok := c.match(s[i:]); ok {
			b.WriteString(repl)
			i += used
			continue
		}
		_, w := utf8.DecodeRuneInString(s[i:])
		b.WriteString(s[i : i+w])
		i += w
	}
	return b.String()
}
