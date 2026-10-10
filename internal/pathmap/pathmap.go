// Package pathmap translates file paths between the view this tool has of the
// library (e.g. /music inside Docker) and the view of another system such as
// Jellyfin (e.g. /data/music).
package pathmap

import (
	"fmt"
	"path"
	"strings"
)

// Mapping pairs a local prefix with the equivalent remote prefix.
type Mapping struct {
	Local  string `yaml:"local"`
	Remote string `yaml:"remote"`
}

// Mapper converts paths using longest-prefix matching on path boundaries.
type Mapper struct {
	maps []Mapping
}

// New validates and normalises the mappings. An empty list is valid and
// yields an identity mapper.
func New(maps []Mapping) (*Mapper, error) {
	out := make([]Mapping, 0, len(maps))
	for i, m := range maps {
		if !strings.HasPrefix(m.Local, "/") || !strings.HasPrefix(m.Remote, "/") {
			return nil, fmt.Errorf("path_map[%d]: local and remote must be absolute (got %q -> %q)", i, m.Local, m.Remote)
		}
		out = append(out, Mapping{Local: path.Clean(m.Local), Remote: path.Clean(m.Remote)})
	}
	return &Mapper{maps: out}, nil
}

// ToRemote rewrites a local path for the remote system. ok is false when no
// mapping matches and the path is returned unchanged.
func (m *Mapper) ToRemote(local string) (string, bool) {
	return translate(m.maps, local, func(x Mapping) (string, string) { return x.Local, x.Remote })
}

// ToLocal rewrites a remote path for this tool. ok is false when no mapping
// matches and the path is returned unchanged.
func (m *Mapper) ToLocal(remote string) (string, bool) {
	return translate(m.maps, remote, func(x Mapping) (string, string) { return x.Remote, x.Local })
}

func translate(maps []Mapping, p string, ends func(Mapping) (from, to string)) (string, bool) {
	best, bestLen := -1, -1
	for i, mp := range maps {
		from, _ := ends(mp)
		if hasPathPrefix(p, from) && len(from) > bestLen {
			best, bestLen = i, len(from)
		}
	}
	if best < 0 {
		return p, false
	}
	from, to := ends(maps[best])
	// Trimming the trailing slash makes "/" behave like any other prefix.
	rest := strings.TrimPrefix(p, strings.TrimSuffix(from, "/"))
	out := strings.TrimSuffix(to, "/") + rest
	if out == "" {
		out = "/"
	}
	return out, true
}

// hasPathPrefix reports whether p equals prefix or lives below it, so that
// /music does not match /music2.
func hasPathPrefix(p, prefix string) bool {
	if prefix == "/" {
		return strings.HasPrefix(p, "/")
	}
	return p == prefix || strings.HasPrefix(p, prefix+"/")
}
