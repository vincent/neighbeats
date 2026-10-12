package config

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	providerLine = regexp.MustCompile(`^provider:.*$`)
	remoteLine   = regexp.MustCompile(`^(\s+(?:-\s+)?remote:).*$`)
	topKey       = regexp.MustCompile(`^[A-Za-z_]`)
)

// SetProvider sets the top-level provider key in YAML text, keeping every
// comment and every other line. A missing key is appended.
func SetProvider(text, value string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		if providerLine.MatchString(l) {
			lines[i] = "provider: " + value
			return strings.Join(lines, "\n")
		}
	}
	return strings.TrimRight(text, "\n") + "\nprovider: " + value + "\n"
}

// SetFirstRemote sets remote of the first path_map entry. ok is false when
// the file has no such line, in which case the text is unchanged.
func SetFirstRemote(text, value string) (out string, ok bool) {
	lines := strings.Split(text, "\n")
	in := false
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "path_map:"):
			in = true
		case in && topKey.MatchString(l):
			in = false
		case in && remoteLine.MatchString(l):
			lines[i] = remoteLine.ReplaceAllString(l, "${1} "+value)
			return strings.Join(lines, "\n"), true
		}
	}
	return text, false
}

// ValidRemote checks a path_map.remote answer.
func ValidRemote(p string) error {
	if !strings.HasPrefix(p, "/") && !regexp.MustCompile(`^[A-Za-z]:[\\/]`).MatchString(p) {
		return fmt.Errorf("%q is not an absolute path", p)
	}
	if strings.ContainsAny(p, "\n\"'#") {
		return fmt.Errorf("%q has characters that are not allowed here", p)
	}
	return nil
}
