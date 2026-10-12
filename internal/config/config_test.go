package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadDefaults(t *testing.T) {
	c, err := Load(write(t, "music_roots: [/music/]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.MusicRoots[0] != "/music" {
		t.Errorf("root not cleaned: %q", c.MusicRoots[0])
	}
	if c.ScanWorkers != 8 || c.DBPath == "" || len(c.Extensions) == 0 {
		t.Errorf("defaults missing: %+v", c)
	}
	if c.Provider != "auto" || c.ClipSeconds != 30 || c.AnalyzeWorkers < 1 || c.ModelPath == "" {
		t.Errorf("analyze defaults missing: %+v", c)
	}
	if !c.ExtensionSet()[".mp3"] {
		t.Error("mp3 not in default extensions")
	}
}

func TestExtensionNormalised(t *testing.T) {
	c, err := Load(write(t, "music_roots: [/m]\nextensions: [MP3, .Flac]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(c.Extensions, ","); got != ".mp3,.flac" {
		t.Errorf("extensions = %s", got)
	}
}

func TestLoadErrors(t *testing.T) {
	cases := map[string]string{
		"no roots":      "db_path: x\n",
		"relative root": "music_roots: [music]\n",
		"unknown key":   "music_roots: [/m]\nmusic_root: [/x]\n",
		"bad workers":   "music_roots: [/m]\nscan_workers: -2\n",
		"bad path map":  "music_roots: [/m]\npath_map:\n  - {local: m, remote: /x}\n",
		"bad provider":  "music_roots: [/m]\nprovider: tpu\n",
		"bad clip":      "music_roots: [/m]\nclip_seconds: 1\n",
		"empty file":    "",
	}
	for name, body := range cases {
		if _, err := Load(write(t, body)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Error("missing file: expected error")
	}
}

func TestPathMapper(t *testing.T) {
	c, err := Load(write(t, "music_roots: [/music]\npath_map:\n  - {local: /music, remote: /data/music}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := c.PathMapper().ToRemote("/music/a.mp3"); !ok || got != "/data/music/a.mp3" {
		t.Errorf("got %q,%v", got, ok)
	}
}

func TestResolveSystemFallback(t *testing.T) {
	t.Setenv(EnvVar, "")
	t.Chdir(t.TempDir())
	sys := filepath.Join(t.TempDir(), "config.yaml")
	if resolve("", sys) != "config.yaml" {
		t.Error("no system file: want ./config.yaml")
	}
	if err := os.WriteFile(sys, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if resolve("", sys) != sys {
		t.Error("system file should be used when ./config.yaml is missing")
	}
	if err := os.WriteFile("config.yaml", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if resolve("", sys) != "config.yaml" {
		t.Error("./config.yaml wins")
	}
}

func TestResolve(t *testing.T) {
	t.Setenv(EnvVar, "")
	t.Chdir(t.TempDir())
	if resolve("", "/nonexistent/config.yaml") != "config.yaml" {
		t.Error("default")
	}
	t.Setenv(EnvVar, "/env.yaml")
	if Resolve("") != "/env.yaml" || Resolve("/flag.yaml") != "/flag.yaml" {
		t.Error("precedence")
	}
}
