// Package config loads and validates the YAML configuration file.
package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/vincentlark/neighbeats/internal/pathmap"
)

// EnvVar names the environment variable that selects the config file.
const EnvVar = "NEIGHBEATS_CONFIG"

var defaultExtensions = []string{".mp3", ".m4a", ".aac", ".flac", ".ogg", ".opus", ".wav"}

// Config is the on-disk configuration.
type Config struct {
	// DBPath is the SQLite database file.
	DBPath string `yaml:"db_path"`
	// MusicRoots are the folders to scan, as seen by this process.
	MusicRoots []string `yaml:"music_roots"`
	// Extensions are the audio file extensions to index (with leading dot).
	Extensions []string `yaml:"extensions"`
	// ScanWorkers is the number of concurrent tag readers.
	ScanWorkers int `yaml:"scan_workers"`
	// PathMap translates local paths to Jellyfin's view of the same files.
	PathMap []pathmap.Mapping `yaml:"path_map"`
}

// Resolve picks the config file path: explicit flag, then $NEIGHBEATS_CONFIG,
// then ./config.yaml.
func Resolve(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if v := os.Getenv(EnvVar); v != "" {
		return v
	}
	return "config.yaml"
}

// Load reads, defaults and validates the file at path. Unknown keys are an
// error so typos do not pass silently.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

	var c Config
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := c.applyDefaults(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

func (c *Config) applyDefaults() error {
	if c.DBPath == "" {
		c.DBPath = "data/neighbeats.sqlite"
	}
	if c.ScanWorkers == 0 {
		c.ScanWorkers = 8
	}
	if c.ScanWorkers < 1 || c.ScanWorkers > 256 {
		return fmt.Errorf("scan_workers must be between 1 and 256, got %d", c.ScanWorkers)
	}
	if len(c.Extensions) == 0 {
		c.Extensions = append([]string(nil), defaultExtensions...)
	}
	for i, e := range c.Extensions {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" || e == "." {
			return fmt.Errorf("extensions[%d] is empty", i)
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		c.Extensions[i] = e
	}
	if len(c.MusicRoots) == 0 {
		return errors.New("music_roots must list at least one folder")
	}
	for i, r := range c.MusicRoots {
		if !filepath.IsAbs(r) {
			return fmt.Errorf("music_roots[%d] must be absolute, got %q", i, r)
		}
		c.MusicRoots[i] = filepath.Clean(r)
	}
	if _, err := pathmap.New(c.PathMap); err != nil {
		return err
	}
	return nil
}

// ExtensionSet returns Extensions as a lookup set.
func (c *Config) ExtensionSet() map[string]bool {
	s := make(map[string]bool, len(c.Extensions))
	for _, e := range c.Extensions {
		s[e] = true
	}
	return s
}

// PathMapper builds the mapper for PathMap. Load already validated it.
func (c *Config) PathMapper() *pathmap.Mapper {
	m, _ := pathmap.New(c.PathMap)
	return m
}
