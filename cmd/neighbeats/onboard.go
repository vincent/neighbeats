package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/vincent/neighbeats"
	"github.com/vincent/neighbeats/internal/config"
)

// onboardIn is where answers are read from (replaced in tests).
var onboardIn io.Reader = os.Stdin

// modelFile is one download offered by onboard.
type modelFile struct {
	url, dest string
}

// Official sources. Replaced in tests.
var (
	effnetURL = "https://essentia.upf.edu/models/feature-extractors/discogs-effnet/discogs-effnet-bsdynamic-1.onnx"
	e5Base    = "https://huggingface.co/intfloat/multilingual-e5-small/resolve/main"
)

type prompter struct {
	in  *bufio.Reader
	out io.Writer
	yes bool
}

// ask returns the answer, or def for an empty answer (and always def with -yes).
func (p *prompter) ask(q, def string) (string, error) {
	if p.yes {
		return def, nil
	}
	if def != "" {
		fmt.Fprintf(p.out, "%s [%s] ", q, def)
	} else {
		fmt.Fprintf(p.out, "%s ", q)
	}
	line, err := p.in.ReadString('\n')
	if err != nil && (err != io.EOF || line == "") {
		return "", errors.New("no answer (input closed)")
	}
	if a := strings.TrimSpace(line); a != "" {
		return a, nil
	}
	return def, nil
}

func (p *prompter) confirm(q string, defYes bool) (bool, error) {
	def := "y/N"
	if defYes {
		def = "Y/n"
	}
	a, err := p.ask(q+" ("+def+")", "")
	if err != nil {
		return false, err
	}
	if a == "" {
		return defYes, nil
	}
	return strings.HasPrefix(strings.ToLower(a), "y"), nil
}

func cmdOnboard(ctx context.Context, cfgPath string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("onboard", flag.ContinueOnError)
	fs.SetOutput(stderr)
	yes := fs.Bool("yes", false, "accept the defaults without asking (download the models)")
	remote := fs.String("remote", "", "path_map.remote: the music folder as the player sees it")
	provider := fs.String("provider", "", "auto (use the GPU if there is one) or cpu")
	noModels := fs.Bool("no-models", false, "do not download models")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *provider != "" && *provider != "auto" && *provider != "cpu" {
		return fmt.Errorf("-provider must be auto or cpu, got %q", *provider)
	}
	if f, ok := onboardIn.(*os.File); ok && !*yes && !isTTY(f) {
		return errors.New("onboard asks questions: run it with a terminal (docker compose run --rm -it neighbeats onboard) or pass -yes")
	}
	p := &prompter{in: bufio.NewReader(onboardIn), out: stdout, yes: *yes}

	path := config.Resolve(cfgPath)
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		return fmt.Errorf("%s is a folder: Docker creates one when the file is missing on the host; remove it and run `cp config.example.yaml config.yaml` first", path)
	}
	text, err := os.ReadFile(path)
	created := false
	switch {
	case err == nil:
	case errors.Is(err, os.ErrNotExist):
		text, created = neighbeats.ConfigExample, true
		fmt.Fprintf(stdout, "No config at %s: starting from the template.\n", path)
	default:
		return err
	}
	cur, err := parseConfig(text)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	s := string(text)

	// 1. path_map.remote
	fmt.Fprintln(stdout, "\n1. Music paths")
	if len(cur.PathMap) == 0 {
		fmt.Fprintln(stdout, "   The config has no path_map; add one by hand if your player uses other paths.")
	} else {
		m := cur.PathMap[0]
		fmt.Fprintf(stdout, "   neighbeats reads files at %s. path_map.remote is the same folder as your\n"+
			"   player (Jellyfin, ...) sees it; playlists and play history are matched through it.\n", m.Local)
		ans := *remote
		if ans == "" {
			ok, err := p.confirm(fmt.Sprintf("   Does your player see %s as %s?", m.Local, m.Remote), true)
			if err != nil {
				return err
			}
			ans = m.Remote
			for !ok {
				if ans, err = p.ask("   Path your player sees for that folder:", ""); err != nil {
					return err
				}
				if verr := config.ValidRemote(ans); verr != nil {
					fmt.Fprintln(stdout, "   ", verr)
					continue
				}
				break
			}
		} else if err := config.ValidRemote(ans); err != nil {
			return err
		}
		if ans != m.Remote {
			var done bool
			if s, done = config.SetFirstRemote(s, ans); !done {
				fmt.Fprintf(stdout, "   Could not edit the file; set path_map remote to %s by hand.\n", ans)
			}
		}
	}

	// 2. models
	fmt.Fprintln(stdout, "\n2. Models")
	var failed error
	if *noModels {
		fmt.Fprintln(stdout, "   skipped (-no-models)")
	} else {
		music := []modelFile{{effnetURL, cur.ModelPath}}
		lyric := []modelFile{{e5Base + "/onnx/model.onnx", filepath.Join(cur.LyricModelDir, "model.onnx")},
			{e5Base + "/tokenizer.json", filepath.Join(cur.LyricModelDir, "tokenizer.json")}}
		for _, set := range []struct {
			name, note string
			files      []modelFile
			defYes     bool
		}{
			{"music model (Discogs-EffNet, needed)", "CC BY-NC-SA 4.0: personal, non-commercial use", music, true},
			{"lyric model (multilingual-e5-small, optional)", "MIT licence; about 470 MB", lyric, true},
		} {
			if err := offer(ctx, p, stdout, set.name, set.note, set.files, set.defYes); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				fmt.Fprintln(stdout, "   download failed:", err)
				failed = err
			}
		}
	}

	// 3. provider
	fmt.Fprintln(stdout, "\n3. GPU")
	if _, err := os.Stat("/dev/nvidiactl"); err == nil {
		fmt.Fprintln(stdout, "   An NVIDIA device is visible here.")
	} else {
		fmt.Fprintln(stdout, "   No NVIDIA device is visible in this container. The GPU is enabled in compose.yaml:")
		fmt.Fprintln(stdout, "   uncomment the `deploy:` block (resources > reservations > devices, driver nvidia)")
		fmt.Fprintln(stdout, "   and it needs the NVIDIA container toolkit on the host. Until then \"auto\" runs on the CPU.")
	}
	prov := *provider
	if prov == "" {
		use, err := p.confirm("   Use the GPU when it works (otherwise CPU only)?", true)
		if err != nil {
			return err
		}
		prov = "cpu"
		if use {
			prov = "auto"
		}
	}
	s = config.SetProvider(s, prov)

	// write the config
	if s != string(text) || created {
		if !created {
			backupConfig(stdout, path, text, cur.DBPath)
		}
		if err := writeInPlace(path, []byte(s)); err != nil {
			return fmt.Errorf("write %s: %w (is the file mounted read-only?)", path, err)
		}
		fmt.Fprintf(stdout, "\nWrote %s (provider %s).\n", path, prov)
	} else {
		fmt.Fprintln(stdout, "\nConfig unchanged.")
	}
	if _, err := config.Load(path); err != nil {
		return fmt.Errorf("the new config does not load: %w", err)
	}
	fmt.Fprintln(stdout, "\nNext: neighbeats update   (or scan, then analyze, then embed-lyrics)")
	return failed
}

// parseConfig loads config text through the normal loader, so the checks are the same.
func parseConfig(text []byte) (*config.Config, error) {
	f, err := os.CreateTemp("", "neighbeats-onboard-*.yaml")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(text); err != nil {
		f.Close()
		return nil, err
	}
	f.Close()
	return config.Load(f.Name())
}

// offer asks about one set of downloads and fetches the ones that are missing.
func offer(ctx context.Context, p *prompter, out io.Writer, name, note string, files []modelFile, defYes bool) error {
	var todo []modelFile
	for _, f := range files {
		if _, err := os.Stat(f.dest); err == nil {
			fmt.Fprintf(out, "   %s: already there\n", f.dest)
			continue
		}
		todo = append(todo, f)
	}
	if len(todo) == 0 {
		return nil
	}
	fmt.Fprintf(out, "   %s, %s:\n", name, note)
	for _, f := range todo {
		size := "size unknown"
		if n := remoteSize(ctx, f.url); n > 0 {
			size = fmt.Sprintf("%.0f MB", float64(n)/1e6)
		}
		fmt.Fprintf(out, "     %s -> %s (%s)\n", f.url, f.dest, size)
	}
	ok, err := p.confirm("   Download it?", defYes)
	if err != nil || !ok {
		return err
	}
	lv := newLive(out)
	defer lv.Done()
	for _, f := range todo {
		sum, err := download(ctx, f.url, f.dest, lv.Status)
		if err != nil {
			return err
		}
		lv.Logf("   %s ok, sha256 %s", f.dest, sum)
	}
	return nil
}

// backupConfig keeps the old file next to the new one, or next to the
// database when that folder is read-only (a single-file mount in Docker).
func backupConfig(out io.Writer, path string, text []byte, dbPath string) {
	for _, dst := range []string{path + ".bak", filepath.Join(filepath.Dir(dbPath), filepath.Base(path)+".bak")} {
		if os.WriteFile(dst, text, 0o644) == nil {
			fmt.Fprintf(out, "\nOld config saved as %s.\n", dst)
			return
		}
	}
	fmt.Fprintln(out, "\nCould not save a copy of the old config; only the path_map remote and provider lines change.")
}

// writeInPlace overwrites the file's content without replacing the file, which
// a Docker single-file mount does not allow.
func writeInPlace(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
