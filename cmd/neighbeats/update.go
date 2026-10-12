package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vincent/neighbeats/internal/config"
)

const backupPrefix, backupSuffix = "neighbeats-", ".sqlite"

// cmdBackup copies the database to a timestamped file and prunes old copies.
func cmdBackup(ctx context.Context, cfgPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dir := fs.String("dir", "", "backup folder (default: backups next to the database)")
	keep := fs.Int("keep", 7, "copies to keep (older ones are deleted)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keep < 1 {
		return errors.New("-keep must be at least 1")
	}
	cfg, st, err := open(cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if *dir == "" {
		*dir = filepath.Join(filepath.Dir(cfg.DBPath), "backups")
	}
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		return err
	}
	dest := filepath.Join(*dir, backupPrefix+time.Now().Format("20060102-150405.000")+backupSuffix)
	if err := st.Backup(ctx, dest); err != nil {
		return err
	}
	fi, _ := os.Stat(dest)
	fmt.Fprintf(stdout, "backup: %s (%.1f MB)\n", dest, float64(fi.Size())/1e6)
	removed, err := pruneBackups(*dir, *keep)
	if removed > 0 {
		fmt.Fprintf(stdout, "backup: removed %d old copies, keeping %d\n", removed, *keep)
	}
	return err
}

// pruneBackups deletes all but the newest keep backups in dir (names sort by time).
func pruneBackups(dir string, keep int) (int, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	var names []string
	for _, e := range ents {
		n := e.Name()
		if !e.IsDir() && strings.HasPrefix(n, backupPrefix) && strings.HasSuffix(n, backupSuffix) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	removed := 0
	for len(names)-removed > keep {
		if err := os.Remove(filepath.Join(dir, names[removed])); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// cmdUpdate is the one command to schedule: backup, scan, analyze, embed the
// new lyrics and pull Jellyfin plays. Every step is incremental, so a run with
// nothing new takes seconds. Optional steps are skipped when not set up.
func cmdUpdate(ctx context.Context, cfgPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(stderr)
	noBackup := fs.Bool("no-backup", false, "do not back up the database first")
	keep := fs.Int("keep", 7, "backups to keep")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(config.Resolve(cfgPath))
	if err != nil {
		return err
	}
	type step struct {
		name string
		skip string // why the step is skipped ("" = run it)
		run  func() error
	}
	_, lyrErr := os.Stat(filepath.Join(cfg.LyricModelDir, "model.onnx"))
	jfSkip := ""
	if cfg.Jellyfin.URL == "" || os.Getenv(config.KeyEnvVar) == "" || cfg.Jellyfin.User == "" {
		jfSkip = "jellyfin.url, jellyfin.user or " + config.KeyEnvVar + " not set"
	}
	lyrSkip := ""
	if lyrErr != nil {
		lyrSkip = "no lyric model in " + cfg.LyricModelDir
	}
	steps := []step{
		{"backup", map[bool]string{true: "-no-backup"}[*noBackup], func() error {
			return cmdBackup(ctx, cfgPath, []string{"-keep", fmt.Sprint(*keep)}, stdout, stderr)
		}},
		{"scan", "", func() error { return cmdScan(ctx, cfgPath, nil, stdout, stderr) }},
		{"analyze", "", func() error { return cmdAnalyze(ctx, cfgPath, nil, stdout, stderr) }},
		{"embed-lyrics", lyrSkip, func() error { return cmdEmbedLyrics(ctx, cfgPath, nil, stdout, stderr) }},
		{"sync-jellyfin", jfSkip, func() error { return cmdSyncJellyfin(ctx, cfgPath, nil, stdout, stderr) }},
	}
	start := time.Now()
	for _, s := range steps {
		if s.skip != "" {
			fmt.Fprintf(stdout, "== %s: skipped (%s)\n", s.name, s.skip)
			continue
		}
		fmt.Fprintf(stdout, "== %s\n", s.name)
		if err := s.run(); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	fmt.Fprintf(stdout, "update done in %s\n", time.Since(start).Round(time.Second))
	return nil
}
