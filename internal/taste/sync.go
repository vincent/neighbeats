// Package taste turns Jellyfin listening history into a stored snapshot and
// into route cost adjustments.
package taste

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vincent/neighbeats/internal/jellyfin"
	"github.com/vincent/neighbeats/internal/pathmap"
	"github.com/vincent/neighbeats/internal/store"
)

// API is the part of the Jellyfin client that Sync uses.
type API interface {
	UserItems(ctx context.Context, userID, filter string) ([]jellyfin.Item, error)
	ItemsByID(ctx context.Context, userID string, ids []string) ([]jellyfin.Item, error)
	PlayEvents(ctx context.Context, userID string, since time.Time) ([]jellyfin.PlayEvent, error)
}

// Report says what a sync did.
type Report struct {
	Items            int // played or favourite items Jellyfin returned
	ByPath           int
	ByMBID           int
	Unmatched        int
	UnmatchedSamples []string
	Tracks           int // distinct indexed tracks with taste
	Favourites       int
	NewEvents        int
	PluginMissing    bool
	EventsErr        error
}

// Key is how a user is named in the database: the lowercased account name, so
// `path` can find the history without asking Jellyfin for an id.
func Key(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// Sync makes the stored taste of a user match Jellyfin. It is idempotent:
// taste is replaced as a whole, events are added once. A failure to read
// play events (e.g. the plugin is missing) does not undo the taste sync.
func Sync(ctx context.Context, st *store.Store, api API, user jellyfin.User, pm *pathmap.Mapper) (Report, error) {
	var rep Report
	played, err := api.UserItems(ctx, user.ID, "IsPlayed")
	if err != nil {
		return rep, err
	}
	favs, err := api.UserItems(ctx, user.ID, "IsFavorite")
	if err != nil {
		return rep, err
	}
	byID := map[string]jellyfin.Item{}
	for _, it := range append(played, favs...) {
		byID[it.ID] = it
	}
	rep.Items = len(byID)

	m, err := newMatcher(ctx, st, pm)
	if err != nil {
		return rep, err
	}
	cache := map[string]int64{}
	rows := map[int64]*store.Taste{}
	for _, it := range byID {
		id, how := m.match(it)
		cache[it.ID] = id
		switch how {
		case "path":
			rep.ByPath++
		case "mbid":
			rep.ByMBID++
		default:
			rep.Unmatched++
			if len(rep.UnmatchedSamples) < 5 {
				rep.UnmatchedSamples = append(rep.UnmatchedSamples, it.Path)
			}
			continue
		}
		t := rows[id]
		if t == nil {
			t = &store.Taste{TrackID: id}
			rows[id] = t
		}
		t.Plays += it.UserData.PlayCount
		t.Favourite = t.Favourite || it.UserData.IsFavorite
		if lp := it.LastPlayed(); !lp.IsZero() && lp.Unix() > t.LastPlayed {
			t.LastPlayed = lp.Unix()
		}
	}
	out := make([]store.Taste, 0, len(rows))
	for _, t := range rows {
		out = append(out, *t)
		if t.Favourite {
			rep.Favourites++
		}
	}
	rep.Tracks = len(out)
	if err := st.ReplaceTaste(ctx, Key(user.Name), out); err != nil {
		return rep, err
	}
	if err := st.SaveItemTracks(ctx, cache); err != nil {
		return rep, err
	}

	n, err := syncEvents(ctx, st, api, user, m)
	rep.NewEvents = n
	if errors.Is(err, jellyfin.ErrNoPlugin) {
		rep.PluginMissing = true
	} else if err != nil {
		rep.EventsErr = err
	}
	return rep, nil
}

func syncEvents(ctx context.Context, st *store.Store, api API, user jellyfin.User, m *matcher) (int, error) {
	last, err := st.LastPlayEventMs(ctx, Key(user.Name))
	if err != nil {
		return 0, err
	}
	var since time.Time
	if last > 0 {
		since = time.UnixMilli(last)
	}
	events, err := api.PlayEvents(ctx, user.ID, since)
	if err != nil {
		return 0, err
	}
	known, err := st.ItemTracks(ctx)
	if err != nil {
		return 0, err
	}
	var missing []string
	seen := map[string]bool{}
	for _, e := range events {
		if _, ok := known[e.ItemID]; !ok && !seen[e.ItemID] {
			seen[e.ItemID] = true
			missing = append(missing, e.ItemID)
		}
	}
	if len(missing) > 0 {
		// Skipped tracks never became "played", so ask for them by id.
		items, err := api.ItemsByID(ctx, user.ID, missing)
		if err != nil {
			return 0, err
		}
		fresh := map[string]int64{}
		for _, it := range items {
			id, _ := m.match(it)
			fresh[it.ID] = id
			known[it.ID] = id
		}
		for _, id := range missing {
			if _, ok := known[id]; !ok {
				fresh[id], known[id] = 0, 0 // deleted from Jellyfin
			}
		}
		if err := st.SaveItemTracks(ctx, fresh); err != nil {
			return 0, err
		}
	}
	rows := make([]store.PlayEvent, 0, len(events))
	for _, e := range events {
		rows = append(rows, store.PlayEvent{AtMs: e.At.UnixMilli(), ItemID: e.ItemID, TrackID: known[e.ItemID], Seconds: e.Seconds})
	}
	n, err := st.AddPlayEvents(ctx, Key(user.Name), rows)
	if err != nil {
		return 0, fmt.Errorf("store play events: %w", err)
	}
	return n, nil
}

// matcher finds the indexed track of a Jellyfin item: by file path through
// path_map first, then by a recording MBID that is unique in the index.
type matcher struct {
	pm    *pathmap.Mapper
	paths map[string]int64
	recs  map[string]int64
}

func newMatcher(ctx context.Context, st *store.Store, pm *pathmap.Mapper) (*matcher, error) {
	paths, err := st.PathIndex(ctx)
	if err != nil {
		return nil, err
	}
	recs, err := st.RecordingIndex(ctx)
	if err != nil {
		return nil, err
	}
	return &matcher{pm: pm, paths: paths, recs: recs}, nil
}

func (m *matcher) match(it jellyfin.Item) (id int64, how string) {
	if it.Path != "" {
		local, _ := m.pm.ToLocal(it.Path)
		if id, ok := m.paths[local]; ok {
			return id, "path"
		}
	}
	if mb := it.RecordingMBID(); mb != "" {
		if id, ok := m.recs[mb]; ok {
			return id, "mbid"
		}
	}
	return 0, ""
}
