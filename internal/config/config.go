// Package config loads and validates the YAML configuration file.
package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/vincent/neighbeats/internal/pathmap"
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

	// Jellyfin is the server whose listening history biases routes. The API
	// key is read from $NEIGHBEATS_JELLYFIN_KEY, never from this file.
	Jellyfin Jellyfin `yaml:"jellyfin"`

	// Serve is where `neighbeats serve` listens for media-server plugins.
	Serve Serve `yaml:"serve"`

	// ModelPath is the Discogs-EffNet ONNX file used by `analyze`.
	ModelPath string `yaml:"model_path"`
	// LyricModelDir holds model.onnx and tokenizer.json of multilingual-e5-small,
	// used by `embed-lyrics` and `path -theme`.
	LyricModelDir string `yaml:"lyric_model_dir"`
	// ORTLibrary is the libonnxruntime shared library; empty lets the system
	// loader find it.
	ORTLibrary string `yaml:"ort_library"`
	// Provider is the ONNX execution provider: auto, cuda or cpu.
	Provider string `yaml:"provider"`
	// AnalyzeWorkers is the number of concurrent decode+mel workers.
	AnalyzeWorkers int `yaml:"analyze_workers"`
	// ClipSeconds is the length of the clip embedded from each track.
	ClipSeconds float64 `yaml:"clip_seconds"`
}

// KeyEnvVar names the environment variable holding the Jellyfin API key.
const KeyEnvVar = "NEIGHBEATS_JELLYFIN_KEY"

// Jellyfin locates the media server.
type Jellyfin struct {
	// URL is the server's base address, e.g. https://jellyfin.example.org.
	URL string `yaml:"url"`
	// User is the default account (name) for sync-jellyfin and path; the
	// commands' -user flag overrides it.
	User string `yaml:"user"`
}

// DefaultSocket is where `serve` listens unless the config says otherwise.
const DefaultSocket = "/run/neighbeats/neighbeats.sock"

// Serve configures the local API used by media-server plugins.
type Serve struct {
	// Socket is the Unix socket path. Whoever can write to it can ask for
	// playlists, so share its folder only with the media server.
	Socket string `yaml:"socket"`
}

// SystemConfig is where the Debian package puts the config.
const SystemConfig = "/etc/neighbeats/config.yaml"

// Resolve picks the config file path: explicit flag, then $NEIGHBEATS_CONFIG,
// then ./config.yaml, then the system file of the Debian package when only
// that one exists.
func Resolve(flagValue string) string {
	return resolve(flagValue, SystemConfig)
}

func resolve(flagValue, system string) string {
	if flagValue != "" {
		return flagValue
	}
	if v := os.Getenv(EnvVar); v != "" {
		return v
	}
	if _, err := os.Stat("config.yaml"); err != nil {
		if _, err := os.Stat(system); err == nil {
			return system
		}
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
	if c.ModelPath == "" {
		c.ModelPath = "models/discogs-effnet-bsdynamic-1.onnx"
	}
	if c.LyricModelDir == "" {
		c.LyricModelDir = "models/multilingual-e5-small"
	}
	switch c.Provider {
	case "":
		c.Provider = "auto"
	case "auto", "cuda", "cpu":
	default:
		return fmt.Errorf("provider must be auto, cuda or cpu, got %q", c.Provider)
	}
	if c.AnalyzeWorkers == 0 {
		c.AnalyzeWorkers = max(1, runtime.NumCPU()-1)
	}
	if c.AnalyzeWorkers < 1 || c.AnalyzeWorkers > 256 {
		return fmt.Errorf("analyze_workers must be between 1 and 256, got %d", c.AnalyzeWorkers)
	}
	if c.ClipSeconds == 0 {
		c.ClipSeconds = 30
	}
	if c.ClipSeconds < 5 || c.ClipSeconds > 300 {
		return fmt.Errorf("clip_seconds must be between 5 and 300, got %g", c.ClipSeconds)
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
	if c.Serve.Socket == "" {
		c.Serve.Socket = DefaultSocket
	}
	c.Jellyfin.URL = strings.TrimRight(strings.TrimSpace(c.Jellyfin.URL), "/")
	if u := c.Jellyfin.URL; u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return fmt.Errorf("jellyfin.url must start with http:// or https://, got %q", u)
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
