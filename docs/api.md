# Plugin API (`neighbeats serve`)

`neighbeats serve` keeps the index in memory and answers playlist requests from
media-server plugins (Jellyfin first). It speaks HTTP + JSON over a **Unix socket**,
not a TCP port: no network exposure, and whoever can open the socket file may ask.

- Socket: `serve.socket` in the config (default `/run/neighbeats/neighbeats.sock`),
  or `-socket PATH`. Mode `0660` by default (`-socket-mode`), so the media server
  must share the socket's group (see [operations](operations.md#plugin-api-serve)).
- One request is answered at a time. A route over 25k tracks takes well under a second.
- When `update` (or `scan`, `analyze`, `embed-lyrics`) changes the index, the next
  request reloads it first. No restart needed.
- Answers are JSON. Errors are `{"error": "..."}` with 400 (bad request), 404
  (track unknown or not analysed) or 500.

Try it:

```sh
curl --unix-socket /run/neighbeats/neighbeats.sock http://neighbeats/v1/health
```

## Naming tracks

A track is named by a `trackRef`:

```json
{"item_id": "5f3c...", "path": "/media/music/Artist/Album/01 - Song.mp3"}
```

Give either field or both. `item_id` (the Jellyfin item id) is tried first; it is
known once `sync-jellyfin` has run. `path` is the file as the media server sees it,
translated back through `path_map`; it works without any sync. Sending both is the
robust choice.

Tracks in answers:

```json
{"item_id": "5f3c...", "path": "/media/music/...", "artist": "…", "title": "…",
 "cosine": 0.62, "reason": "similarity 0.71"}
```

`item_id` is `null` when neighbeats does not know the track's item (it was added
to the library after the last `sync-jellyfin`). `path` is always there, so the
plugin can look the item up itself. `cosine` and `reason` describe the hop from the
previous track (routes) or the likeness to the seed (similar).

neighbeats never writes to Jellyfin: the plugin builds the playlist from the ids.

## `GET /v1/health`

```json
{"version": "1.2.0", "tracks": 25125, "lyric_vectors": 9170, "loaded_at": "2026-10-16T17:18:21+02:00"}
```

## `POST /v1/path`

A playlist travelling from one track to another (the `path` command).

```json
{
  "from": {"item_id": "..."},
  "to":   {"path": "/media/music/..."},
  "length": 15,
  "mode": "comfort",
  "user": "vincent",
  "theme": "sea, night",
  "avoid_artists": ["nickelback"],
  "max_minutes": 15
}
```

| Field | Default | |
|-------|---------|--|
| `from`, `to` | required | `trackRef` |
| `length` | 0 | exact number of tracks (1-200); 0 = cheapest route, any length |
| `mode` | comfort | taste: `comfort`, `explore`, `rediscover`, `none` |
| `user` | `jellyfin.user` | Jellyfin user name whose synced history biases the route |
| `theme` | none | steer toward lyrics matching the phrase (needs lyric vectors) |
| `avoid_artists` | none | never pass through artists whose name contains one of these |
| `max_minutes` | 15 | leave out longer tracks (0 = no limit) |

Answer (200):

```json
{"found": true, "tracks": [ ... ], "cost": 4.21,
 "notes": ["taste: comfort mode for vincent (812 tracks with history, 3 recently skipped avoided)"]}
```

When no route exists the answer is **422** with `found: false`, `error`, `why`
(where the search broke) and the tracks as far as it got:

```json
{"found": false, "tracks": [ ... ], "error": "no route found", "why": "..."}
```

## `POST /v1/similar`

Tracks that sound like one (the `similar` command). The seed comes first.

```json
{"item": {"item_id": "..."}, "count": 20, "max_per_artist": 2, "max_minutes": 15}
```

Answer: `{"tracks": [seed, ...neighbours], "notes": [...]}`.

## From .NET (a Jellyfin plugin)

```csharp
var handler = new SocketsHttpHandler {
    ConnectCallback = async (_, ct) => {
        var s = new Socket(AddressFamily.Unix, SocketType.Stream, ProtocolType.Unspecified);
        await s.ConnectAsync(new UnixDomainSocketEndPoint("/run/neighbeats/neighbeats.sock"), ct);
        return new NetworkStream(s, ownsSocket: true);
    }
};
var http = new HttpClient(handler) { BaseAddress = new Uri("http://neighbeats") };
var resp = await http.PostAsJsonAsync("/v1/path", new { from = new { item_id = a }, to = new { item_id = b }, length = 15 });
```
