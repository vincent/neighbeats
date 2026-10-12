package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vincent/neighbeats/internal/config"
)

func modelServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, "f", testTime, strings.NewReader("data of "+r.URL.Path))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func setup(t *testing.T, srv *httptest.Server, answers string) (cfgPath, models string, run func(args ...string) (string, error)) {
	t.Helper()
	dir := t.TempDir()
	models = filepath.Join(dir, "models")
	cfgPath = filepath.Join(dir, "config.yaml")
	cfg := fmt.Sprintf("db_path: %s/db.sqlite\nmusic_roots:\n  - /music\npath_map:\n  - local: /music\n    remote: /data/music # note\nmodel_path: %s/eff.onnx\nlyric_model_dir: %s/e5\nprovider: auto\n", dir, models, models)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	oldIn, oldE, oldB := onboardIn, effnetURL, e5Base
	t.Cleanup(func() { onboardIn, effnetURL, e5Base = oldIn, oldE, oldB })
	onboardIn = strings.NewReader(answers)
	effnetURL, e5Base = srv.URL+"/eff.onnx", srv.URL
	return cfgPath, models, func(args ...string) (string, error) {
		var out bytes.Buffer
		err := cmdOnboard(context.Background(), cfgPath, args, &out, &out)
		return out.String(), err
	}
}

func TestOnboardAnswers(t *testing.T) {
	// remote: no, new path; effnet: yes; lyric model: no; GPU: no
	cfgPath, models, run := setup(t, modelServer(t), "n\n/mnt/media\ny\nn\nn\n")
	out, err := run()
	if err != nil {
		t.Fatal(err, out)
	}
	got, _ := os.ReadFile(cfgPath)
	for _, want := range []string{"remote: /mnt/media", "provider: cpu"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("config lacks %q:\n%s", want, got)
		}
	}
	if _, err := os.Stat(cfgPath + ".bak"); err != nil {
		t.Error("no backup of the old config")
	}
	if b, err := os.ReadFile(filepath.Join(models, "eff.onnx")); err != nil || string(b) != "data of /eff.onnx" {
		t.Errorf("effnet: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(models, "e5", "model.onnx")); err == nil {
		t.Error("lyric model downloaded although declined")
	}
	if _, err := os.Stat(filepath.Join(models, "eff.onnx.part")); err == nil {
		t.Error(".part left behind")
	}
	if _, err := config.Load(cfgPath); err != nil {
		t.Error(err)
	}
}

func TestOnboardYesKeepsExisting(t *testing.T) {
	cfgPath, models, run := setup(t, modelServer(t), "")
	if err := os.MkdirAll(models, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(models, "eff.onnx"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := run("-yes", "-remote", "/x/music", "-provider", "auto")
	if err != nil {
		t.Fatal(err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(models, "eff.onnx")); string(b) != "mine" {
		t.Error("existing model overwritten")
	}
	for _, f := range []string{"e5/model.onnx", "e5/tokenizer.json"} {
		if _, err := os.Stat(filepath.Join(models, f)); err != nil {
			t.Error(f, err)
		}
	}
	if b, _ := os.ReadFile(cfgPath); !strings.Contains(string(b), "remote: /x/music") {
		t.Error(string(b))
	}
}

func TestOnboardCreatesConfig(t *testing.T) {
	_, _, run := setup(t, modelServer(t), "")
	dir := t.TempDir()
	cfg := filepath.Join(dir, "new.yaml")
	var out bytes.Buffer
	onboardIn = strings.NewReader("")
	if err := cmdOnboard(context.Background(), cfg, []string{"-yes", "-no-models", "-provider", "cpu"}, &out, &out); err != nil {
		t.Fatal(err, out.String())
	}
	if _, err := config.Load(cfg); err != nil {
		t.Fatal(err)
	}
	_ = run
}

func TestOnboardDownloadFailureReported(t *testing.T) {
	_, _, run := setup(t, modelServer(t), "")
	effnetURL = effnetURL[:strings.LastIndex(effnetURL, "/")] + "/missing"
	out, err := run("-yes")
	if err == nil || !strings.Contains(out, "download failed") {
		t.Fatalf("err=%v\n%s", err, out)
	}
}

func TestOnboardNoAnswer(t *testing.T) {
	_, _, run := setup(t, modelServer(t), "")
	if _, err := run(); err == nil {
		t.Fatal("expected an error when input is empty")
	}
}

func TestDownloadResume(t *testing.T) {
	srv := modelServer(t)
	dest := filepath.Join(t.TempDir(), "f.bin")
	if err := os.WriteFile(dest+".part", []byte("data "), 0o644); err != nil {
		t.Fatal(err)
	}
	sum, err := download(context.Background(), srv.URL+"/x", dest, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dest); string(b) != "data of /x" || sum == "" {
		t.Fatalf("%q %s", b, sum)
	}
}

var testTime = time.Unix(0, 0)
