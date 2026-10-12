package taste

import (
	"context"
	"testing"
	"time"

	"github.com/vincent/neighbeats/internal/jellyfin"
	"github.com/vincent/neighbeats/internal/pathmap"
	"github.com/vincent/neighbeats/internal/store"
)

var now = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

func daysAgo(d int) int64 { return now.Add(-time.Duration(d) * day).Unix() }

func profile() *Profile {
	return &Profile{
		ByTrack: map[int64]store.Taste{
			1: {TrackID: 1, Plays: 20, Favourite: true, LastPlayed: daysAgo(200)},
			2: {TrackID: 2, Plays: 5, LastPlayed: daysAgo(1)},
			3: {TrackID: 3, Plays: 1, LastPlayed: daysAgo(10)},
		},
		LastSkip: map[int64]int64{4: daysAgo(2), 1: daysAgo(2), 5: daysAgo(90)},
	}
}

func TestBiasModes(t *testing.T) {
	p := profile()
	comfort := p.Bias(Comfort, now)
	explore := p.Bias(Explore, now)
	redisc := p.Bias(Rediscover, now)

	if d, why := comfort(1); d >= 0 || why != "favourite" {
		t.Errorf("comfort favourite: %v %q", d, why)
	}
	if d, why := comfort(2); d < 0.3 || why != "played 1 days ago" {
		t.Errorf("comfort recent: %v %q", d, why)
	}
	if d, why := explore(99); d >= 0 || why != "never played" {
		t.Errorf("explore unplayed: %v %q", d, why)
	}
	if dc, _ := comfort(99); dc != 0 {
		t.Errorf("comfort must ignore unplayed: %v", dc)
	}
	if d, why := redisc(1); d > -0.4 || why == "" {
		t.Errorf("rediscover long-unheard favourite: %v %q", d, why)
	}
	if d, _ := redisc(3); d <= 0 {
		t.Errorf("recent play must cost in rediscover: %v", d)
	}
	if p.Bias(None, now) != nil {
		t.Error("none mode must disable bias")
	}
}

func TestSkipped(t *testing.T) {
	got := profile().Skipped(now)
	if !got[4] {
		t.Error("recent skip should be forbidden")
	}
	if got[1] {
		t.Error("favourites are never forbidden for a skip")
	}
	if got[5] {
		t.Error("an old skip is forgiven")
	}
}

func TestParseMode(t *testing.T) {
	if m, err := ParseMode("Explore"); err != nil || m != Explore {
		t.Errorf("%v %v", m, err)
	}
	if _, err := ParseMode("loud"); err == nil {
		t.Error("bad mode accepted")
	}
}

type fakeAPI struct {
	played, favs []jellyfin.Item
	byID         map[string]jellyfin.Item
	events       []jellyfin.PlayEvent
	noPlugin     bool
}

func (f fakeAPI) UserItems(_ context.Context, _, filter string) ([]jellyfin.Item, error) {
	if filter == "IsFavorite" {
		return f.favs, nil
	}
	return f.played, nil
}
func (f fakeAPI) ItemsByID(_ context.Context, _ string, ids []string) ([]jellyfin.Item, error) {
	var out []jellyfin.Item
	for _, id := range ids {
		if it, ok := f.byID[id]; ok {
			out = append(out, it)
		}
	}
	return out, nil
}
func (f fakeAPI) PlayEvents(context.Context, string, time.Time) ([]jellyfin.PlayEvent, error) {
	if f.noPlugin {
		return nil, jellyfin.ErrNoPlugin
	}
	return f.events, nil
}

func item(id, path, mbid string, plays int, fav bool) jellyfin.Item {
	it := jellyfin.Item{ID: id, Path: path, ProviderID: map[string]string{}}
	if mbid != "" {
		it.ProviderID["MusicBrainzRecording"] = mbid
	}
	it.UserData.PlayCount, it.UserData.IsFavorite = plays, fav
	it.UserData.LastPlayed = "2026-10-01T10:00:00Z"
	return it
}

func TestSync(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(t.TempDir() + "/db.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	run, _ := st.StartRun(ctx, []string{"/music"})
	if err := st.UpsertTracks(ctx, run, []store.Track{
		{Path: "/music/a.mp3", Size: 1, MtimeNs: 1, Format: "mp3"},
		{Path: "/music/b.mp3", Size: 1, MtimeNs: 1, Format: "mp3"},
	}); err != nil {
		t.Fatal(err)
	}
	pm, _ := pathmap.New([]pathmap.Mapping{{Local: "/music", Remote: "/media/Music"}})
	api := fakeAPI{
		played: []jellyfin.Item{
			item("i1", "/media/Music/a.mp3", "", 3, false),
			item("i2", "/elsewhere/zzz.mp3", "", 1, false), // unmatched
		},
		favs: []jellyfin.Item{item("i1", "/media/Music/a.mp3", "", 3, true)}, // same item, deduped
		byID: map[string]jellyfin.Item{"i3": item("i3", "/media/Music/b.mp3", "", 0, false)},
		events: []jellyfin.PlayEvent{
			{At: now, ItemID: "i3", Seconds: 8},
			{At: now.Add(time.Minute), ItemID: "gone", Seconds: 100},
		},
	}
	user := jellyfin.User{ID: "u1", Name: "V"}
	for range 2 { // idempotent
		rep, err := Sync(ctx, st, api, user, pm)
		if err != nil {
			t.Fatal(err)
		}
		if rep.Items != 2 || rep.ByPath != 1 || rep.Unmatched != 1 || rep.Tracks != 1 || rep.Favourites != 1 {
			t.Errorf("%+v", rep)
		}
	}
	taste, _ := st.LoadTaste(ctx, Key("V"))
	if len(taste) != 1 {
		t.Fatalf("%v", taste)
	}
	ev, _ := st.PlayEvents(ctx, Key("V"), 0)
	if len(ev) != 1 || ev[0].Seconds != 8 {
		t.Errorf("only the matched event is kept: %v", ev)
	}
	p, err := Load(ctx, st, Key("V"), map[int64]float64{})
	if err != nil || len(p.LastSkip) != 1 {
		t.Errorf("skip not derived: %+v %v", p, err)
	}

	api.noPlugin = true
	if rep, err := Sync(ctx, st, api, user, pm); err != nil || !rep.PluginMissing {
		t.Errorf("missing plugin must not fail the sync: %+v %v", rep, err)
	}
}
