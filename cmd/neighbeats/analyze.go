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

	"github.com/vincentlark/neighbeats/internal/analyze"
	"github.com/vincentlark/neighbeats/internal/audio"
	"github.com/vincentlark/neighbeats/internal/embed"
)

func cmdAnalyze(ctx context.Context, cfgPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	fs.SetOutput(stderr)
	retry := fs.Bool("retry-errors", false, "retry tracks that failed on the same file version")
	limit := fs.Int("limit", 0, "embed at most N tracks (0 = all)")
	provider := fs.String("provider", "", "override the config: auto, cuda or cpu")
	workers := fs.Int("workers", 0, "override analyze_workers")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, st, err := open(cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if *provider != "" {
		cfg.Provider = *provider
	}
	if *workers > 0 {
		cfg.AnalyzeWorkers = *workers
	}
	if _, err := os.Stat(cfg.ModelPath); err != nil {
		return fmt.Errorf("model: %w (see model_path in the config)", err)
	}
	dec, err := audio.NewDecoder()
	if err != nil {
		return err
	}
	logf := func(f string, a ...any) { fmt.Fprintf(stderr, f+"\n", a...) }
	emb, err := embed.NewORT(embed.ORTOptions{
		ModelPath: cfg.ModelPath, LibraryPath: cfg.ORTLibrary,
		Provider: embed.Provider(cfg.Provider), Logf: logf,
	})
	if err != nil {
		return err
	}
	defer emb.Close()
	fmt.Fprintf(stdout, "model %s on %s, %d workers\n", embed.ModelID, emb.Provider(), cfg.AnalyzeWorkers)

	// The CUDA provider can replace Go's signal handlers; register ours again
	// so Ctrl-C still stops the run cleanly after the session exists.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	res, err := analyze.Run(ctx, st, analyze.Options{
		Decoder: dec, Embedder: emb, Workers: cfg.AnalyzeWorkers, ClipSeconds: cfg.ClipSeconds,
		RetryErrors: *retry, Limit: *limit, Logf: logf,
	})
	fmt.Fprintf(stdout, "analyze: %d pending, %d embedded, %d failed in %s\n",
		res.Pending, res.Done, res.Failed, res.Wall.Round(time.Millisecond))
	if res.Done > 0 {
		per := func(d time.Duration) time.Duration {
			return (d / time.Duration(res.Done)).Round(100 * time.Microsecond)
		}
		fmt.Fprintf(stdout, "per track (summed over workers): decode %s, mel %s, inference %s, db write %s\n",
			per(res.Decode), per(res.Mel), per(res.Infer), per(res.Write))
		fmt.Fprintf(stdout, "throughput: %.2f tracks/s\n", float64(res.Done)/res.Wall.Seconds())
	}
	if embedded, failed, serr := st.EmbeddingStats(context.WithoutCancel(ctx), embed.ModelID); serr == nil {
		fmt.Fprintf(stdout, "library: %d embedded, %d failed\n", embedded, failed)
	}
	return err
}
