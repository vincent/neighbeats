package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vincentlark/neighbeats/internal/config"
	"github.com/vincentlark/neighbeats/internal/embed"
	"github.com/vincentlark/neighbeats/internal/lyrics"
	"github.com/vincentlark/neighbeats/internal/textembed"
)

// openTextModel loads the lyric text model named by the config.
func openTextModel(cfg *config.Config, provider string, logf func(string, ...any)) (*textembed.Model, error) {
	if _, err := os.Stat(cfg.LyricModelDir + "/model.onnx"); err != nil {
		return nil, fmt.Errorf("lyric model: %w (see lyric_model_dir in the config and the README)", err)
	}
	if provider == "" {
		provider = cfg.Provider
	}
	return textembed.Open(textembed.Options{
		Dir: cfg.LyricModelDir, LibraryPath: cfg.ORTLibrary, Provider: embed.Provider(provider), Logf: logf,
	})
}

func cmdEmbedLyrics(ctx context.Context, cfgPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("embed-lyrics", flag.ContinueOnError)
	fs.SetOutput(stderr)
	limit := fs.Int("limit", 0, "embed at most N lyrics (0 = all)")
	provider := fs.String("provider", "", "override the config: auto, cuda or cpu")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, st, err := open(cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if _, err := lyrics.DetectLanguages(ctx, st); err != nil {
		return err
	}
	logf := func(f string, a ...any) { fmt.Fprintf(stderr, f+"\n", a...) }
	m, err := openTextModel(cfg, *provider, logf)
	if err != nil {
		return err
	}
	defer m.Close()
	fmt.Fprintf(stdout, "model %s on %s\n", textembed.ModelID, m.Provider())

	// The CUDA provider can replace Go's signal handlers; register ours again.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	t0 := time.Now()
	res, err := lyrics.EmbedAll(ctx, st, m, lyrics.EmbedOptions{Limit: *limit, Logf: logf})
	fmt.Fprintf(stdout, "embed-lyrics: %d pending, %d embedded (%d cut at %d tokens) in %s\n",
		res.Pending, res.Done, res.Truncated, textembed.MaxTokens, time.Since(t0).Round(time.Millisecond))
	if n, cerr := st.LyricEmbeddingCount(context.WithoutCancel(ctx), textembed.ModelID); cerr == nil {
		fmt.Fprintf(stdout, "library: %d lyrics embedded\n", n)
	}
	return err
}
