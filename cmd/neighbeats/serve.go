package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vincent/neighbeats/internal/config"
	"github.com/vincent/neighbeats/internal/embed"
	"github.com/vincent/neighbeats/internal/route"
	"github.com/vincent/neighbeats/internal/store"
	"github.com/vincent/neighbeats/internal/textembed"
)

// Limits on what one request may ask for, so a plugin bug cannot tie the
// server up for minutes.
const (
	maxRouteTracks   = 200
	maxSimilarTracks = 500
	maxRequestBytes  = 64 << 10
)

// cmdServe answers playlist requests from media-server plugins over a Unix
// socket (HTTP + JSON, see docs/api.md). It keeps the index in memory and
// reloads it when `update` has changed the database.
func cmdServe(ctx context.Context, cfgPath string, args []string, stdout, stderr io.Writer) error {
	fl := flag.NewFlagSet("serve", flag.ContinueOnError)
	fl.SetOutput(stderr)
	socket := fl.String("socket", "", "Unix socket to listen on (default: serve.socket in the config, else "+config.DefaultSocket+")")
	modeStr := fl.String("socket-mode", "0660", "permissions of the socket file, octal")
	if err := fl.Parse(args); err != nil {
		return err
	}
	mode, err := strconv.ParseUint(*modeStr, 8, 32)
	if err != nil {
		return fmt.Errorf("-socket-mode %q: not an octal mode", *modeStr)
	}
	cfg, st, err := open(cfgPath)
	if err != nil {
		return err
	}
	defer st.Close()
	if *socket == "" {
		*socket = cfg.Serve.Socket
	}

	srv := &server{cfg: cfg, st: st, log: newLive(stderr)}
	t0 := time.Now()
	if _, err := srv.library(ctx); err != nil {
		return err
	}
	srv.log.Logf("index loaded: %d tracks in %s", srv.lib.ix.Len(), time.Since(t0).Round(time.Millisecond))

	ln, err := listenUnix(*socket, fs.FileMode(mode))
	if err != nil {
		return err
	}
	hs := &http.Server{Handler: srv.routes(), ReadHeaderTimeout: 10 * time.Second}
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := hs.Shutdown(sctx); err != nil {
			srv.log.Logf("shutdown: %v", err)
		}
	}()
	srv.log.Logf("listening on %s", *socket)
	if err := hs.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	<-done
	srv.log.Logf("stopped")
	return nil
}

// listenUnix opens the socket, replacing a stale one left by a crash but
// refusing to steal it from a server that still answers.
func listenUnix(path string, mode fs.FileMode) (net.Listener, error) {
	// The kernel's limit (108 bytes with the final NUL) fails with a bare
	// "invalid argument".
	if len(path) > 107 {
		return nil, fmt.Errorf("socket path %s is %d bytes, Unix sockets allow 107: choose a shorter serve.socket", path, len(path))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("socket folder: %w", err)
	}
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&fs.ModeSocket == 0 {
			return nil, fmt.Errorf("%s exists and is not a socket", path)
		}
		if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
			c.Close()
			return nil, fmt.Errorf("another server is already listening on %s", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale socket: %w", err)
		}
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, mode); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

// server holds the loaded library. One request is served at a time: a route
// takes well under a second on a 25k-track library, and the route search and
// the reload are then free of locking concerns.
type server struct {
	cfg *config.Config
	st  *store.Store
	log *live

	mu    sync.Mutex
	lib   *library
	stamp string
	since time.Time
}

// library returns the in-memory index, reloading it first when the database
// changed since it was built. A failed reload keeps serving the old one.
// The caller holds s.mu (or is the only goroutine, at start).
func (s *server) library(ctx context.Context) (*library, error) {
	stamp, err := s.st.IndexStamp(ctx, embed.ModelID, textembed.ModelID)
	if err != nil {
		return nil, err
	}
	if s.lib != nil && stamp == s.stamp {
		return s.lib, nil
	}
	lib, err := loadLibrary(ctx, s.st, false, true)
	if err != nil {
		if s.lib != nil {
			s.log.Logf("reload failed, keeping the previous index: %v", err)
			return s.lib, nil
		}
		return nil, err
	}
	if s.lib != nil {
		s.log.Logf("index reloaded: %d tracks", lib.ix.Len())
	}
	s.lib, s.stamp, s.since = lib, stamp, time.Now()
	return lib, nil
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.handle(s.health))
	mux.HandleFunc("POST /v1/path", s.handle(s.path))
	mux.HandleFunc("POST /v1/similar", s.handle(s.similar))
	return mux
}

// apiError is an error with the HTTP status it should be answered with.
type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func badRequest(f string, a ...any) error {
	return &apiError{http.StatusBadRequest, fmt.Sprintf(f, a...)}
}

func notFound(f string, a ...any) error {
	return &apiError{http.StatusNotFound, fmt.Sprintf(f, a...)}
}

// handle runs one endpoint under the lock and writes its answer as JSON. An
// endpoint may return both a body and an error (a route that was not found
// still shows how far it got).
func (s *server) handle(fn func(r *http.Request, lib *library) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t0 := time.Now()
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
		s.mu.Lock()
		var body any
		lib, err := s.library(r.Context())
		if err == nil {
			body, err = fn(r, lib)
		}
		s.mu.Unlock()

		status := http.StatusOK
		if err != nil {
			status = http.StatusInternalServerError
			var ae *apiError
			if errors.As(err, &ae) {
				status = ae.status
			}
			if body == nil {
				body = errorOut{Error: err.Error()}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if err := json.NewEncoder(w).Encode(body); err != nil {
			s.log.Logf("%s %s: write answer: %v", r.Method, r.URL.Path, err)
		}
		s.log.Logf("%s %s %d %s", r.Method, r.URL.Path, status, time.Since(t0).Round(time.Millisecond))
	}
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return badRequest("request body: %v", err)
	}
	return nil
}

type errorOut struct {
	Error string `json:"error"`
}

type healthOut struct {
	Version      string    `json:"version"`
	Tracks       int       `json:"tracks"`        // analysed tracks in the index
	LyricVectors int       `json:"lyric_vectors"` // 0 = routes use audio only
	LoadedAt     time.Time `json:"loaded_at"`
}

func (s *server) health(_ *http.Request, lib *library) (any, error) {
	out := healthOut{Version: version, Tracks: lib.ix.Len(), LoadedAt: s.since}
	if lib.lyrics != nil {
		out.LyricVectors = lib.lyrics.lx.Len()
	}
	return out, nil
}

// trackRef names a track the way a media-server plugin knows it: by its
// Jellyfin item id, by its path as the server sees the file, or both (the id
// is tried first, the path is the fallback that needs no sync).
type trackRef struct {
	ItemID string `json:"item_id,omitempty"`
	Path   string `json:"path,omitempty"`
}

// trackOut is one track of an answer. ItemID is null when neighbeats does not
// know the track's Jellyfin item (run sync-jellyfin); Path is always there.
type trackOut struct {
	ItemID *string `json:"item_id"`
	Path   string  `json:"path"`
	Artist string  `json:"artist"`
	Title  string  `json:"title"`
	Cosine float32 `json:"cosine,omitempty"` // similarity to the previous track (route) or to the seed (similar)
	Reason string  `json:"reason,omitempty"`
}

type pathIn struct {
	From         trackRef `json:"from"`
	To           trackRef `json:"to"`
	Length       int      `json:"length"` // exact number of tracks; 0 = cheapest route
	Mode         string   `json:"mode"`   // comfort, explore, rediscover, none
	User         string   `json:"user"`   // Jellyfin user name, for taste
	Theme        string   `json:"theme"`
	AvoidArtists []string `json:"avoid_artists"`
	MaxMinutes   *float64 `json:"max_minutes"`
}

type pathOut struct {
	Found  bool       `json:"found"`
	Tracks []trackOut `json:"tracks"` // without a route: as far as the search got
	Cost   float64    `json:"cost,omitempty"`
	Error  string     `json:"error,omitempty"`
	Why    string     `json:"why,omitempty"`
	Notes  []string   `json:"notes,omitempty"` // what shaped the route (taste, lyrics, left-out tracks)
}

func (s *server) path(r *http.Request, lib *library) (any, error) {
	var in pathIn
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if in.Length < 0 || in.Length > maxRouteTracks {
		return nil, badRequest("length must be between 0 and %d", maxRouteTracks)
	}
	ctx := r.Context()
	a, err := s.resolve(ctx, lib, in.From, "from")
	if err != nil {
		return nil, err
	}
	b, err := s.resolve(ctx, lib, in.To, "to")
	if err != nil {
		return nil, err
	}
	maxMin := float64(defaultMaxMinutes)
	if in.MaxMinutes != nil {
		maxMin = *in.MaxMinutes
	}
	var notes bytes.Buffer
	rt, _, err := planPath(ctx, s.st, s.cfg, lib, pathQuery{
		from: a, to: b, length: in.Length, opt: route.Defaults(),
		maxMinutes: maxMin, avoid: in.AvoidArtists, user: in.User, mode: in.Mode,
		lyric: lyricOptions{weight: defaultLyricWeight, langPenalty: defaultLangPenalty, themeWeight: defaultThemeWeight, theme: in.Theme},
	}, &notes)
	if err != nil {
		if errors.As(err, new(queryError)) {
			return nil, badRequest("%v", err)
		}
		return nil, err
	}
	ids := make([]int64, len(rt.Steps))
	for i, st := range rt.Steps {
		ids[i] = st.ID
	}
	tracks, err := s.tracks(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i, st := range rt.Steps {
		if i > 0 {
			tracks[i].Cosine, tracks[i].Reason = st.Cosine, st.Reason
		}
	}
	out := pathOut{Found: rt.Found, Tracks: tracks, Cost: rt.Cost, Notes: lines(notes.String())}
	if !rt.Found {
		out.Error, out.Why = "no route found", rt.Why
		return out, &apiError{http.StatusUnprocessableEntity, out.Error}
	}
	return out, nil
}

type similarIn struct {
	Item         trackRef `json:"item"`
	Count        int      `json:"count"`          // neighbours, default 20
	MaxPerArtist *int     `json:"max_per_artist"` // default 2, 0 = no cap
	MaxMinutes   *float64 `json:"max_minutes"`
}

type similarOut struct {
	Tracks []trackOut `json:"tracks"` // the seed first, then its neighbours
	Notes  []string   `json:"notes,omitempty"`
}

func (s *server) similar(r *http.Request, lib *library) (any, error) {
	in := similarIn{Count: 20}
	if err := decode(r, &in); err != nil {
		return nil, err
	}
	if in.Count < 1 || in.Count > maxSimilarTracks {
		return nil, badRequest("count must be between 1 and %d", maxSimilarTracks)
	}
	ctx := r.Context()
	seed, err := s.resolve(ctx, lib, in.Item, "item")
	if err != nil {
		return nil, err
	}
	q := similarQuery{seed: seed, n: in.Count, perArtist: 2, maxMinutes: defaultMaxMinutes}
	if in.MaxPerArtist != nil {
		q.perArtist = *in.MaxPerArtist
	}
	if in.MaxMinutes != nil {
		q.maxMinutes = *in.MaxMinutes
	}
	hits, nLong, err := findSimilar(lib, q)
	if err != nil {
		return nil, err
	}
	ids := []int64{seed}
	for _, h := range hits {
		ids = append(ids, h.ID)
	}
	tracks, err := s.tracks(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i, h := range hits {
		tracks[i+1].Cosine = h.Cosine
	}
	out := similarOut{Tracks: tracks}
	if nLong > 0 {
		out.Notes = []string{fmt.Sprintf("left out %d tracks longer than %g min", nLong, q.maxMinutes)}
	}
	return out, nil
}

// resolve finds the analysed track a plugin refers to.
func (s *server) resolve(ctx context.Context, lib *library, ref trackRef, field string) (int64, error) {
	if ref.ItemID == "" && ref.Path == "" {
		return 0, badRequest("%s: give item_id or path", field)
	}
	var id int64
	var ok bool
	var err error
	if ref.ItemID != "" {
		if id, ok, err = s.st.ItemTrack(ctx, ref.ItemID); err != nil {
			return 0, err
		}
	}
	if !ok && ref.Path != "" {
		local, _ := s.cfg.PathMapper().ToLocal(ref.Path)
		if id, ok, err = s.st.TrackByPath(ctx, local); err != nil {
			return 0, err
		}
	}
	switch {
	case !ok && ref.Path == "":
		return 0, notFound("%s: item %s is not known; send its path too, or run sync-jellyfin", field, ref.ItemID)
	case !ok:
		return 0, notFound("%s: no indexed track at %s (check path_map, or run scan)", field, ref.Path)
	case !lib.ix.Has(id):
		return 0, notFound("%s: the track is indexed but not analysed yet (run analyze)", field)
	}
	return id, nil
}

// tracks describes the tracks in order, with their Jellyfin item ids and
// paths as the media server sees them.
func (s *server) tracks(ctx context.Context, ids []int64) ([]trackOut, error) {
	rows, err := s.st.TracksByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	items, err := s.st.TrackItems(ctx, ids)
	if err != nil {
		return nil, err
	}
	pm := s.cfg.PathMapper()
	out := make([]trackOut, len(ids))
	for i, id := range ids {
		r := rows[id]
		p, _ := pm.ToRemote(r.Path)
		out[i] = trackOut{Path: p, Artist: r.Artist, Title: r.Title}
		if it, ok := items[id]; ok {
			out[i].ItemID = &it
		}
	}
	return out, nil
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
