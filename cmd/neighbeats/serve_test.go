package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vincent/neighbeats/internal/config"
	"github.com/vincent/neighbeats/internal/embed"
	"github.com/vincent/neighbeats/internal/pathmap"
	"github.com/vincent/neighbeats/internal/store"
	"github.com/vincent/neighbeats/internal/tags"
)

// testServer starts `serve` on a socket over a library of n tracks spread on
// a quarter circle, each by its own artist. Tracks 0..n-2 have Jellyfin item
// ids "i0".."i{n-2}"; the last one has none.
func testServer(t *testing.T, n int) (*http.Client, *store.Store, []store.TrackRef) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	run, _ := st.StartRun(ctx, []string{"/m"})
	var ts []store.Track
	for i := range n {
		ts = append(ts, store.Track{Path: fmt.Sprintf("/m/t%d.mp3", i), Size: 1, MtimeNs: 1, Format: "mp3",
			Info: tags.Info{Title: fmt.Sprintf("t%d", i), Artist: fmt.Sprintf("a%d", i)}})
	}
	if err := st.UpsertTracks(ctx, run, ts); err != nil {
		t.Fatal(err)
	}
	refs, _ := st.TrackRefs(ctx)
	items := map[string]int64{}
	for i, r := range refs {
		a := float64(i) / float64(n-1) * math.Pi / 2
		if err := st.SaveEmbedding(ctx, store.Embedding{TrackID: r.ID, Model: embed.ModelID,
			Vector: []float32{float32(math.Cos(a)), float32(math.Sin(a))}, Duration: 200, Size: 1, MtimeNs: 1}); err != nil {
			t.Fatal(err)
		}
		if i < n-1 {
			items[fmt.Sprintf("i%d", i)] = r.ID
		}
	}
	if err := st.SaveItemTracks(ctx, items); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{PathMap: []pathmap.Mapping{{Local: "/m", Remote: "/media"}}}
	srv := &server{cfg: cfg, st: st, log: newLive(io.Discard)}
	if _, err := srv.library(ctx); err != nil {
		t.Fatal(err)
	}
	// Socket paths are limited to about 100 bytes; t.TempDir can be longer.
	dir, err := os.MkdirTemp("", "nb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	ln, err := listenUnix(sock, 0o660)
	if err != nil {
		t.Fatal(err)
	}
	hs := &http.Server{Handler: srv.routes()}
	go hs.Serve(ln)
	t.Cleanup(func() { hs.Close() })

	if _, err := listenUnix(sock, 0o660); err == nil || !strings.Contains(err.Error(), "already listening") {
		t.Errorf("a live socket must not be taken over: %v", err)
	}
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}
	return client, st, refs
}

func call(t *testing.T, c *http.Client, method, path, body string, out any) int {
	t.Helper()
	req, _ := http.NewRequest(method, "http://neighbeats"+path, strings.NewReader(body))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("%s %s: decode: %v", method, path, err)
	}
	return resp.StatusCode
}

func TestServePath(t *testing.T) {
	c, _, _ := testServer(t, 8)

	var out pathOut
	code := call(t, c, "POST", "/v1/path", `{"from":{"item_id":"i0"},"to":{"path":"/media/t7.mp3"},"length":4}`, &out)
	if code != 200 || !out.Found || len(out.Tracks) != 4 {
		t.Fatalf("%d %+v", code, out)
	}
	first, last := out.Tracks[0], out.Tracks[3]
	if first.ItemID == nil || *first.ItemID != "i0" || first.Path != "/media/t0.mp3" || first.Artist != "a0" {
		t.Errorf("first: %+v", first)
	}
	if last.ItemID != nil || last.Path != "/media/t7.mp3" {
		t.Errorf("a track without a known item must still come with its path: %+v", last)
	}
	if out.Tracks[1].Reason == "" || out.Tracks[1].Cosine == 0 {
		t.Errorf("hops carry a reason: %+v", out.Tracks[1])
	}

	var e errorOut
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"from":{"item_id":"i0"},"to":{"item_id":"i0"}}`, 400},        // same track
		{`{"from":{"item_id":"i0"},"to":{}}`, 400},                      // no reference
		{`{"from":{"item_id":"i0"},"to":{"item_id":"nope"}}`, 404},      // unknown item, no path
		{`{"from":{"item_id":"i0"},"to":{"path":"/elsewhere/x"}}`, 404}, // unmapped path
		{`{"from":{"item_id":"i0"},"to":{"item_id":"i1"},"bogus":1}`, 400},
		{`{"from":{"item_id":"i0"},"to":{"item_id":"i1"},"mode":"loud"}`, 400},
		{`{"from":{"item_id":"i0"},"to":{"item_id":"i1"},"length":1000}`, 400},
		{`{"from":{"item_id":"i0"},"to":{"item_id":"i1"},"theme":"sea"}`, 400}, // no lyric vectors
	} {
		e = errorOut{}
		if code := call(t, c, "POST", "/v1/path", tc.body, &e); code != tc.want || e.Error == "" {
			t.Errorf("%s: %d %q, want %d", tc.body, code, e.Error, tc.want)
		}
	}
}

func TestServeSimilarAndReload(t *testing.T) {
	c, st, refs := testServer(t, 6)

	var out similarOut
	if code := call(t, c, "POST", "/v1/similar", `{"item":{"item_id":"i2"},"count":3}`, &out); code != 200 || len(out.Tracks) != 4 {
		t.Fatalf("%d %+v", code, out)
	}
	if *out.Tracks[0].ItemID != "i2" || out.Tracks[1].Cosine <= 0 {
		t.Errorf("seed first, then neighbours: %+v", out.Tracks)
	}

	var h healthOut
	if code := call(t, c, "GET", "/v1/health", "", &h); code != 200 || h.Tracks != 6 {
		t.Fatalf("%d %+v", code, h)
	}
	// A new analysed track, as `update` would add, shows up without a restart.
	ctx := context.Background()
	run, _ := st.StartRun(ctx, []string{"/m"})
	if err := st.UpsertTracks(ctx, run, []store.Track{{Path: "/m/new.mp3", Size: 1, MtimeNs: 1, Format: "mp3"}}); err != nil {
		t.Fatal(err)
	}
	all, _ := st.TrackRefs(ctx)
	if err := st.SaveEmbedding(ctx, store.Embedding{TrackID: all[len(all)-1].ID, Model: embed.ModelID, Vector: []float32{0.5, 0.5}, Size: 1, MtimeNs: 1}); err != nil {
		t.Fatal(err)
	}
	if code := call(t, c, "GET", "/v1/health", "", &h); code != 200 || h.Tracks != len(refs)+1 {
		t.Errorf("after update: %d %+v", code, h)
	}
}

func TestListenUnixReplacesStaleSocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "nb")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false) // as after a crash
	ln.Close()
	ln, err = listenUnix(sock, 0o600)
	if err != nil {
		t.Fatalf("stale socket: %v", err)
	}
	defer ln.Close()
	if fi, _ := os.Stat(sock); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	if err := os.WriteFile(filepath.Join(dir, "plain"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := listenUnix(filepath.Join(dir, "plain"), 0o600); err == nil {
		t.Error("a regular file must not be removed")
	}
	if _, err := listenUnix(filepath.Join(dir, strings.Repeat("x", 120)), 0o600); err == nil || !strings.Contains(err.Error(), "107") {
		t.Errorf("a too long path must say why: %v", err)
	}
}
