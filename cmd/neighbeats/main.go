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

	"github.com/vincentlark/neighbeats/internal/config"
	"github.com/vincentlark/neighbeats/internal/lyrics"
	"github.com/vincentlark/neighbeats/internal/scan"
	"github.com/vincentlark/neighbeats/internal/store"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `neighbeats - playlists that travel between songs

Usage:
  neighbeats [--config FILE] <command> [flags]

Commands:
  scan      index the music folders and sibling .lrc lyrics (incremental)
  lyrics    search lyrics: neighbeats lyrics search [-n N] [-phrase] words...
  analyze   embed tracks with the music model (resumable): [-retry-errors] [-limit N] [-provider P]
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
	case "analyze":
		err = cmdAnalyze(ctx, *cfgPath, cmdArgs, stdout, stderr)
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
	res, err := scan.Run(ctx, st, scan.Options{
		Roots:           cfg.MusicRoots,
		Extensions:      cfg.ExtensionSet(),
		Workers:         cfg.ScanWorkers,
		AllowMassRemove: *massRemove,
		Logf:            func(f string, a ...any) { fmt.Fprintf(stderr, f+"\n", a...) },
	})
	fmt.Fprintf(stdout, "scan %s in %s: %d files seen, %d new, %d changed, %d unchanged, %d removed, %d tag errors\n",
		res.Status, time.Since(start).Round(time.Millisecond),
		res.Seen, res.Added, res.Changed, res.Unchanged, res.Removed, res.Errors)
	// Lyrics are only synced after a complete scan: after a partial or
	// aborted one the track list may be stale.
	if err != nil || *noLyrics || res.Status != store.RunOK {
		return err
	}
	lr, err := lyrics.Sync(ctx, st)
	fmt.Fprintf(stdout, "lyrics: %d added, %d updated, %d unchanged, %d removed, %d empty, %d errors\n",
		lr.Added, lr.Updated, lr.Unchanged, lr.Removed, lr.Empty, lr.Errors)
	return err
}

func cmdLyrics(ctx context.Context, cfgPath string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] != "search" {
		return errors.New("usage: neighbeats lyrics search [-n N] [-phrase] words...")
	}
	fs := flag.NewFlagSet("lyrics search", flag.ContinueOnError)
	fs.SetOutput(stderr)
	n := fs.Int("n", 10, "maximum results")
	phrase := fs.Bool("phrase", false, "match the words as an exact phrase")
	if err := fs.Parse(args[1:]); err != nil {
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
	if l, err := st.LyricsStats(ctx); err != nil {
		return err
	} else {
		fmt.Fprintf(stdout, "with lyrics:          %d  %s (%d synced)\n", l.WithLyrics, pct(l.WithLyrics, s.Tracks), l.Synced)
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
