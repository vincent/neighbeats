// Package jellyfin is a small read-only client for the parts of the Jellyfin
// API (and the Playback Reporting plugin) that taste needs. Field names were
// checked against a live Jellyfin 12.2 server.
package jellyfin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client talks to one server. The API key is sent in the Authorization
// header, never in the URL, so it does not end up in logs.
type Client struct {
	BaseURL string
	Key     string
	HTTP    *http.Client
}

// New builds a client with a sensible timeout.
func New(baseURL, key string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Key: key,
		HTTP: &http.Client{Timeout: 2 * time.Minute}}
}

// User is a Jellyfin account.
type User struct {
	ID   string `json:"Id"`
	Name string `json:"Name"`
}

// Item is an audio item with the fields sync needs.
type Item struct {
	ID         string            `json:"Id"`
	Name       string            `json:"Name"`
	Path       string            `json:"Path"`
	RunTime    int64             `json:"RunTimeTicks"` // 100 ns ticks
	ProviderID map[string]string `json:"ProviderIds"`
	UserData   struct {
		PlayCount    int    `json:"PlayCount"`
		IsFavorite   bool   `json:"IsFavorite"`
		LastPlayed   string `json:"LastPlayedDate"`
		Played       bool   `json:"Played"`
		PlaybackTick int64  `json:"PlaybackPositionTicks"`
	} `json:"UserData"`
}

// Seconds is the item's length in seconds (0 if unknown).
func (i Item) Seconds() float64 { return float64(i.RunTime) / 1e7 }

// LastPlayed parses UserData.LastPlayedDate; the zero time means never.
func (i Item) LastPlayed() time.Time {
	t, err := time.Parse(time.RFC3339Nano, i.UserData.LastPlayed)
	if err != nil {
		return time.Time{}
	}
	return t
}

// RecordingMBID is the MusicBrainz recording id, if the server knows it.
func (i Item) RecordingMBID() string { return i.ProviderID["MusicBrainzRecording"] }

// PlayEvent is one row of the Playback Reporting plugin.
type PlayEvent struct {
	At       time.Time
	ItemID   string
	Seconds  float64 // how long it was actually played
	ItemName string
}

func (c *Client) do(ctx context.Context, method, path string, q url.Values, body any, out any) error {
	u := c.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", fmt.Sprintf(`MediaBrowser Token="%s"`, c.Key))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, redact(err, c.Key))
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(msg)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func redact(err error, key string) error {
	if key == "" {
		return err
	}
	return fmt.Errorf("%s", strings.ReplaceAll(err.Error(), key, "***"))
}

// Users lists the accounts.
func (c *Client) Users(ctx context.Context) ([]User, error) {
	var out []User
	err := c.do(ctx, http.MethodGet, "/Users", nil, nil, &out)
	return out, err
}

// FindUser returns the account with this name (case-insensitive) or id.
func (c *Client) FindUser(ctx context.Context, nameOrID string) (User, error) {
	us, err := c.Users(ctx)
	if err != nil {
		return User{}, err
	}
	var names []string
	for _, u := range us {
		if strings.EqualFold(u.Name, nameOrID) || u.ID == nameOrID {
			return u, nil
		}
		names = append(names, u.Name)
	}
	return User{}, fmt.Errorf("no Jellyfin user %q (have: %s)", nameOrID, strings.Join(names, ", "))
}

const pageSize = 1000

type itemsPage struct {
	Items []Item `json:"Items"`
	Total int    `json:"TotalRecordCount"`
}

// UserItems returns every audio item of the user matching the filter
// ("IsPlayed" or "IsFavorite"), with paths, provider ids and play data.
func (c *Client) UserItems(ctx context.Context, userID, filter string) ([]Item, error) {
	var all []Item
	for start := 0; ; start += pageSize {
		q := url.Values{
			"userId": {userID}, "IncludeItemTypes": {"Audio"}, "Recursive": {"true"},
			"Fields": {"Path,ProviderIds"}, "Filters": {filter}, "SortBy": {"SortName"},
			"StartIndex": {strconv.Itoa(start)}, "Limit": {strconv.Itoa(pageSize)},
		}
		var p itemsPage
		if err := c.do(ctx, http.MethodGet, "/Items", q, nil, &p); err != nil {
			return nil, err
		}
		all = append(all, p.Items...)
		if len(p.Items) == 0 || len(all) >= p.Total {
			return all, nil
		}
	}
}

// ItemsByID fetches specific items (used for tracks that were skipped and so
// never became "played").
func (c *Client) ItemsByID(ctx context.Context, userID string, ids []string) ([]Item, error) {
	var all []Item
	const chunk = 100
	for i := 0; i < len(ids); i += chunk {
		q := url.Values{
			"userId": {userID}, "Ids": {strings.Join(ids[i:min(i+chunk, len(ids))], ",")},
			"Fields": {"Path,ProviderIds"},
		}
		var p itemsPage
		if err := c.do(ctx, http.MethodGet, "/Items", q, nil, &p); err != nil {
			return nil, err
		}
		all = append(all, p.Items...)
	}
	return all, nil
}

type queryReq struct {
	CustomQueryString string `json:"CustomQueryString"`
	ReplaceUserID     bool   `json:"ReplaceUserId"`
}

type queryResp struct {
	Results [][]string `json:"results"`
	Message string     `json:"message"`
}

// ErrNoPlugin hints that the Playback Reporting plugin is missing.
var ErrNoPlugin = errors.New("the Playback Reporting plugin is not available")

// PlayEvents returns the user's audio play events strictly after `since`,
// oldest first. The plugin stores times in UTC with 7 fractional digits.
func (c *Client) PlayEvents(ctx context.Context, userID string, since time.Time) ([]PlayEvent, error) {
	if !isHex(userID) {
		return nil, fmt.Errorf("unexpected user id %q", userID)
	}
	sinceStr := ""
	if !since.IsZero() {
		sinceStr = since.UTC().Format("2006-01-02 15:04:05.0000000")
	}
	sql := fmt.Sprintf(`SELECT DateCreated, ItemId, PlayDuration, ItemName FROM PlaybackActivity `+
		`WHERE UserId = '%s' AND ItemType = 'Audio' AND DateCreated > '%s' ORDER BY DateCreated`, userID, sinceStr)
	var r queryResp
	err := c.do(ctx, http.MethodPost, "/user_usage_stats/submit_custom_query", nil, queryReq{CustomQueryString: sql}, &r)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return nil, ErrNoPlugin
		}
		return nil, err
	}
	if r.Message != "" {
		return nil, fmt.Errorf("playback reporting query: %s", firstLine(r.Message))
	}
	var out []PlayEvent
	for _, row := range r.Results {
		if len(row) < 4 {
			continue
		}
		at, err := time.Parse("2006-01-02 15:04:05.9999999", row[0])
		if err != nil {
			return nil, fmt.Errorf("playback reporting time %q: %w", row[0], err)
		}
		secs, _ := strconv.ParseFloat(row[2], 64)
		out = append(out, PlayEvent{At: at.UTC(), ItemID: row[1], Seconds: secs, ItemName: row[3]})
	}
	return out, nil
}

func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n<"); i > 0 {
		return s[:i]
	}
	return s
}
