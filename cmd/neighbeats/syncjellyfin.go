package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/vincent/neighbeats/internal/config"
	"github.com/vincent/neighbeats/internal/jellyfin"
	"github.com/vincent/neighbeats/internal/taste"
)

// jellyfinClient builds the client from the config and the key in the environment.
func jellyfinClient(cfg *config.Config) (*jellyfin.Client, error) {
	if cfg.Jellyfin.URL == "" {
		return nil, errors.New("set jellyfin.url in the config file")
	}
	key := strings.TrimSpace(os.Getenv(config.KeyEnvVar))
	if key == "" {
		return nil, fmt.Errorf("set %s to a Jellyfin API key (it is never read from the config file)", config.KeyEnvVar)
	}
	return jellyfin.New(cfg.Jellyfin.URL, key), nil
}

func cmdSyncJellyfin(ctx context.Context, cfgPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("sync-jellyfin", flag.ContinueOnError)
	fs.SetOutput(stderr)
	userName := fs.String("user", "", "Jellyfin user name (default: jellyfin.user in the config)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, st, err := open(cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()
	client, err := jellyfinClient(cfg)
	if err != nil {
		return err
	}
	name := *userName
	if name == "" {
		name = cfg.Jellyfin.User
	}
	if name == "" {
		return errors.New("which user? pass -user NAME or set jellyfin.user in the config")
	}
	user, err := client.FindUser(ctx, name)
	if err != nil {
		return err
	}

	rep, err := taste.Sync(ctx, st, client, user, cfg.PathMapper())
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "user %s: %d played or favourite items in Jellyfin\n", user.Name, rep.Items)
	fmt.Fprintf(stdout, "  matched by path %d, by recording MBID %d, unmatched %d\n", rep.ByPath, rep.ByMBID, rep.Unmatched)
	for _, p := range rep.UnmatchedSamples {
		fmt.Fprintf(stdout, "    e.g. %s\n", p)
	}
	if rep.Unmatched > 0 && rep.ByPath == 0 {
		fmt.Fprintln(stdout, "  nothing matched by path: check path_map (remote must be the path Jellyfin reports)")
	}
	fmt.Fprintf(stdout, "  taste stored for %d tracks (%d favourites)\n", rep.Tracks, rep.Favourites)
	switch {
	case rep.PluginMissing:
		fmt.Fprintln(stdout, "  Playback Reporting plugin not found: skips are unknown, only plays and favourites are used")
	case rep.EventsErr != nil:
		fmt.Fprintf(stderr, "warning: play events not synced: %v\n", rep.EventsErr)
	default:
		fmt.Fprintf(stdout, "  %d new play events\n", rep.NewEvents)
	}
	return nil
}
