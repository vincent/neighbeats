// Package neighbeats only carries files that ship inside the binary.
package neighbeats

import _ "embed"

// ConfigExample is config.example.yaml, the template `onboard` writes.
//
//go:embed config.example.yaml
var ConfigExample []byte
