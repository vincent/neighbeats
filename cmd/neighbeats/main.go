// Command neighbeats builds playlists that travel between songs.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/vincent/neighbeats/internal/config"
	"github.com/vincent/neighbeats/internal/embed"
	"github.com/vincent/neighbeats/internal/lang"
	"github.com/vincent/neighbeats/internal/lyrics"
	"github.com/vincent/neighbeats/internal/scan"
	"github.com/vincent/neighbeats/internal/store"
	"github.com/vincent/neighbeats/internal/textembed"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `neighbeats - playlists that travel between songs

Usage:
  neighbeats [--config FILE] <command> [flags]

Commands:
  scan      index the music folders and sibling .lrc lyrics (incremental)
  lyrics    search lyrics: neighbeats lyrics search [-n N] [-phrase] words...; or list their languages: neighbeats lyrics langs [-samples N]
  embed-lyrics  embed lyrics with the text model (resumable): [-limit N] [-provider P]
  analyze   embed tracks with the music model (resumable): [-retry-errors] [-limit N] [-provider P]
  similar   tracks that sound like one: neighbeats similar [-n N] [-max-per-artist N] [-allow-adjacent] [-raw] <path|mbid|words...>
  path      playlist travelling from one track to another: neighbeats path [-length N | -max-hops N] [-mode M] [-user U] [-explain] [-out f.m3u8] <from> <to>
  sync-jellyfin  pull plays, favourites and skips of a Jellyfin user: neighbeats sync-jellyfin [-user NAME]
  onboard   first-run setup, asks before changing anything: [-yes] [-remote P] [-provider auto|cpu] [-no-models]
  update    the one to schedule: backup, scan, analyze, embed-lyrics, sync-jellyfin (each step incremental; optional ones skipped if not set up): [-no-backup] [-keep N]
  backup    safe copy of the database to data/backups, keeping the newest N: [-dir D] [-keep N]
  serve     answer playlist requests from media-server plugins on a Unix socket (see docs/api.md): [-socket PATH] [-socket-mode 0660]
  stats     show what the index contains
  version   print the version

Config file lookup: --config, then $NEIGHBEATS_CONFIG, then ./config.yaml.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	global := flag.NewFlagSet("neighbeats", flag.ContinueOnError)
	global.SetOutput(stderr)
	global.Usage = func() { fmt.Fprint(stderr, usage) }
	cfgPath := global.String("config", "", "path to config file")
	if err := global.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	rest := global.Args()
	if len(rest) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, cmdArgs := rest[0], rest[1:]

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd {
	case "version":
		fmt.Fprintln(stdout, "neighbeats", version)
	case "scan":
		err = cmdScan(ctx, *cfgPath, cmdArgs, stdout, stderr)
	case "lyrics":
		err = cmdLyrics(ctx, *cfgPath, cmdArgs, stdout, stderr)
	case "embed-lyrics":
		err = cmdEmbedLyrics(ctx, *cfgPath, cmdArgs, stdout, stderr)
	case "analyze":
		err = cmdAnalyze(ctx, *cfgPath, cmdArgs, stdout, stderr)
	case "similar":
		err = cmdSimilar(ctx, *cfgPath, cmdArgs, stdout, stderr)
	case "path":
		err = cmdPath(ctx, *cfgPath, cmdArgs, stdout, stderr)
	case "sync-jellyfin":
		err = cmdSyncJellyfin(ctx, *cfgPath, cmdArgs, stdout, stderr)
	case "onboard":
		err = cmdOnboard(ctx, *cfgPath, cmdArgs, stdout, stderr)
	case "update":
		err = cmdUpdate(ctx, *cfgPath, cmdArgs, stdout, stderr)
	case "backup":
		err = cmdBackup(ctx, *cfgPath, cmdArgs, stdout, stderr)
	case "serve":
		err = cmdServe(ctx, *cfgPath, cmdArgs, stdout, stderr)
	case "stats":
		err = cmdStats(ctx, *cfgPath, stdout)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return 0
}

func open(cfgPath string) (*config.Config, *store.Store, error) {
	cfg, err := config.Load(config.Resolve(cfgPath))
	if err != nil {
		return nil, nil, err
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return nil, nil, err
	}
	return cfg, st, nil
}

func cmdScan(ctx context.Context, cfgPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	massRemove := fs.Bool("allow-mass-remove", false, "allow removing more than half of the indexed tracks")
	noLyrics := fs.Bool("no-lyrics", false, "do not read sibling .lrc files")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, st, err := open(cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()

	start := time.Now()
	lv := newLive(stderr)
	res, err := scan.Run(ctx, st, scan.Options{
		Roots:           cfg.MusicRoots,
		Extensions:      cfg.ExtensionSet(),
		Workers:         cfg.ScanWorkers,
		AllowMassRemove: *massRemove,
		Logf:            lv.Logf,
		Progress:        lv.Status,
	})
	lv.Done()
	fmt.Fprintf(stdout, "scan %s in %s: %d files seen, %d new, %d changed, %d unchanged, %d removed, %d tag errors\n",
		res.Status, time.Since(start).Round(time.Millisecond),
		res.Seen, res.Added, res.Changed, res.Unchanged, res.Removed, res.Errors)
	// Lyrics are only synced after a complete scan: after a partial or
	// aborted one the track list may be stale.
	if err != nil || *noLyrics || res.Status != store.RunOK {
		return err
	}
	lr, err := lyrics.Sync(ctx, st)
	fmt.Fprintf(stdout, "lyrics: %d added, %d updated, %d unchanged, %d removed, %d empty, %d errors; %d languages detected\n",
		lr.Added, lr.Updated, lr.Unchanged, lr.Removed, lr.Empty, lr.Errors, lr.Detected)
	return err
}

func cmdLyrics(ctx context.Context, cfgPath string, args []string, stdout, stderr io.Writer) error {
	const usageMsg = "usage: neighbeats lyrics search [-n N] [-phrase] words...\n       neighbeats lyrics langs [-samples N]"
	if len(args) == 0 {
		return errors.New(usageMsg)
	}
	switch args[0] {
	case "search":
		return cmdLyricsSearch(ctx, cfgPath, args[1:], stdout, stderr)
	case "langs":
		return cmdLyricsLangs(ctx, cfgPath, args[1:], stdout, stderr)
	}
	return errors.New(usageMsg)
}

func cmdLyricsSearch(ctx context.Context, cfgPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("lyrics search", flag.ContinueOnError)
	fs.SetOutput(stderr)
	n := fs.Int("n", 10, "maximum results")
	phrase := fs.Bool("phrase", false, "match the words as an exact phrase")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("give at least one word to search for")
	}
	_, st, err := open(cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()
	hits, err := st.SearchLyrics(ctx, strings.Join(fs.Args(), " "), *phrase, *n)
	if err != nil {
		return err
	}
	if len(hits) == 0 {
		fmt.Fprintln(stdout, "no matches")
		return nil
	}
	for _, h := range hits {
		fmt.Fprintf(stdout, "%s - %s\n    %s\n", h.Artist, h.Title, strings.ReplaceAll(h.Snippet, "\n", " / "))
	}
	return nil
}

// cmdLyricsLangs shows the detected languages, optionally with a few tracks
// of each to check the guesses by eye.
func cmdLyricsLangs(ctx context.Context, cfgPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("lyrics langs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	samples := fs.Int("samples", 0, "also show N example tracks for each of the six most common languages")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_, st, err := open(cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if _, err := lyrics.DetectLanguages(ctx, st); err != nil {
		return err
	}
	counts, err := st.LangCounts(ctx)
	if err != nil {
		return err
	}
	if len(counts) == 0 {
		fmt.Fprintln(stdout, "no languages yet; run `neighbeats scan` (it detects them for every lyric)")
		return nil
	}
	total := 0
	for _, c := range counts {
		total += c.Count
	}
	for _, c := range counts {
		fmt.Fprintf(stdout, "%-4s %-16s %6d  %s\n", c.Lang, lang.Name(c.Lang), c.Count, pct(c.Count, total))
	}
	shown := 0
	for _, c := range counts {
		if *samples <= 0 || shown == 6 {
			break
		}
		if c.Lang == lang.Undetermined {
			continue
		}
		shown++
		rows, err := st.LangSamples(ctx, c.Lang, *samples)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "\n%s:\n", lang.Name(c.Lang))
		for _, r := range rows {
			fmt.Fprintf(stdout, "  %s - %s (conf %.2f)\n      %s\n", r.Artist, r.Title, r.Confidence, strings.ReplaceAll(r.Excerpt, "\n", " / "))
		}
	}
	return nil
}

func cmdStats(ctx context.Context, cfgPath string, stdout io.Writer) error {
	_, st, err := open(cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()

	s, err := st.Stats(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "tracks:               %d\n", s.Tracks)
	if s.Tracks == 0 {
		fmt.Fprintln(stdout, "index is empty; run `neighbeats scan`")
		return nil
	}
	fmt.Fprintln(stdout, "formats:")
	for _, f := range s.Formats {
		fmt.Fprintf(stdout, "  %-8s %8d  %s\n", f.Format, f.Count, pct(f.Count, s.Tracks))
	}
	fmt.Fprintf(stdout, "recording MBID:       %d  %s\n", s.WithRecording, pct(s.WithRecording, s.Tracks))
	fmt.Fprintf(stdout, "artist MBID:          %d  %s\n", s.WithArtist, pct(s.WithArtist, s.Tracks))
	fmt.Fprintf(stdout, "release MBID:         %d  %s\n", s.WithRelease, pct(s.WithRelease, s.Tracks))
	fmt.Fprintf(stdout, "distinct artists:     %d (by MBID)\n", s.Artists)
	fmt.Fprintf(stdout, "distinct releases:    %d (by MBID)\n", s.Albums)
	fmt.Fprintf(stdout, "missing title:        %d\n", s.Tracks-s.WithTitle)
	fmt.Fprintf(stdout, "missing artist:       %d\n", s.Tracks-s.WithArtistName)
	fmt.Fprintf(stdout, "unreadable tags:      %d\n", s.TagErrors)
	if e, f, err := st.EmbeddingStats(ctx, embed.ModelID); err != nil {
		return err
	} else {
		fmt.Fprintf(stdout, "embedded:             %d  %s (%d failed)\n", e, pct(e, s.Tracks), f)
	}
	if l, err := st.LyricsStats(ctx); err != nil {
		return err
	} else {
		fmt.Fprintf(stdout, "with lyrics:          %d  %s (%d synced)\n", l.WithLyrics, pct(l.WithLyrics, s.Tracks), l.Synced)
		if n, err := st.LyricEmbeddingCount(ctx, textembed.ModelID); err != nil {
			return err
		} else if l.WithLyrics > 0 {
			fmt.Fprintf(stdout, "lyrics embedded:      %d  %s\n", n, pct(n, l.WithLyrics))
		}
	}

	if r, err := st.LastRun(ctx); err != nil {
		return err
	} else if r != nil {
		fmt.Fprintf(stdout, "last scan:            %s (%s)\n", r.StartedAt.Format(time.DateTime), r.Status)
	}
	return nil
}

func pct(n, total int) string {
	if total == 0 {
		return ""
	}
	return fmt.Sprintf("%.1f%%", 100*float64(n)/float64(total))
}
