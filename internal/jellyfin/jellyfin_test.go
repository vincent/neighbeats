package jellyfin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func server(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != `MediaBrowser Token="secret"` {
			http.Error(w, "unauthorized", 401)
			return
		}
		if strings.Contains(r.URL.RawQuery, "secret") {
			t.Error("key leaked into the URL")
		}
		switch r.URL.Path {
		case "/Users":
			io := `[{"Id":"aa11","Name":"Vincent"},{"Id":"bb22","Name":"Ari"}]`
			w.Write([]byte(io))
		case "/Items":
			start := r.URL.Query().Get("StartIndex")
			items := `[{"Id":"i1","Path":"/media/Music/a.mp3","RunTimeTicks":2000000000,
				"ProviderIds":{"MusicBrainzRecording":"rec1"},
				"UserData":{"PlayCount":3,"IsFavorite":true,"LastPlayedDate":"2025-07-13T18:05:47.0127891Z","Played":true}}]`
			if start != "0" {
				items = "[]"
			}
			w.Write([]byte(`{"Items":` + items + `,"TotalRecordCount":1}`))
		case "/user_usage_stats/submit_custom_query":
			var req queryReq
			json.NewDecoder(r.Body).Decode(&req)
			if !strings.Contains(req.CustomQueryString, "'aa11'") {
				t.Errorf("query: %s", req.CustomQueryString)
			}
			w.Write([]byte(`{"colums":[],"results":[["2026-10-11 17:30:18.0750866","i1","101","A - B"]],"message":""}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestClient(t *testing.T) {
	srv := server(t)
	defer srv.Close()
	c := New(srv.URL+"/", "secret")
	ctx := context.Background()

	u, err := c.FindUser(ctx, "vincent")
	if err != nil || u.ID != "aa11" {
		t.Fatalf("%+v %v", u, err)
	}
	if _, err := c.FindUser(ctx, "nobody"); err == nil || !strings.Contains(err.Error(), "Ari") {
		t.Errorf("unknown user error should list users: %v", err)
	}

	items, err := c.UserItems(ctx, u.ID, "IsPlayed")
	if err != nil || len(items) != 1 {
		t.Fatalf("%v %v", items, err)
	}
	it := items[0]
	if it.UserData.PlayCount != 3 || !it.UserData.IsFavorite || it.RecordingMBID() != "rec1" || it.Seconds() != 200 {
		t.Errorf("%+v", it)
	}
	if it.LastPlayed().Year() != 2025 {
		t.Errorf("last played %v", it.LastPlayed())
	}

	ev, err := c.PlayEvents(ctx, u.ID, time.Time{})
	if err != nil || len(ev) != 1 || ev[0].Seconds != 101 || ev[0].ItemID != "i1" || ev[0].At.Hour() != 17 {
		t.Fatalf("%+v %v", ev, err)
	}
}

func TestBadKey(t *testing.T) {
	srv := server(t)
	defer srv.Close()
	_, err := New(srv.URL, "wrong").Users(context.Background())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("err = %v", err)
	}
}

func TestRejectsOddUserID(t *testing.T) {
	if _, err := New("http://x", "k").PlayEvents(context.Background(), "a'; DROP", time.Time{}); err == nil {
		t.Error("user id must be hex")
	}
}
