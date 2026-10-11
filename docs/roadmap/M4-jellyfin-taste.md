# M4: Jellyfin sync and taste bias

Goal: make paths prefer what the listener likes and avoid what they just heard
or skipped.

Jellyfin is 12.2, with the Playback Reporting plugin installed (2026-10-11).

## Tasks

- [x] Read the live API (Swagger on the server) before writing the client; do not
      rely on remembered field names.
- [x] Client: API key from an environment variable, never in the config file.
      Per user: items with play count, favourite flag, last played date.
- [x] Match Jellyfin items to indexed tracks: first by file path through `pathmap`
      `ToLocal`, then by MusicBrainz IDs as a fallback. Report unmatched counts.
- [x] `taste` table: per track plays, favourite, last played, source, synced_at.
- [x] `sync-jellyfin` command, idempotent, incremental where the API allows.
- [x] Decide on skips: install Playback Reporting and check whether it exposes
      play duration per event; a play far shorter than the track is a skip
      (the reference project uses < 30 s). Until then taste uses plays and favourites only.
- [x] Cost terms (from the reference project's session and taste scoring, simplified):
      favourites get a bonus; recently played tracks and over-used artists a
      penalty; a `--mode comfort|explore|rediscover` flag picks the weights.
- [x] Rediscovery: a mild bonus for tracks last played long ago (the reference peaks
      around 120 days).
- [x] Each hop's reason includes the taste term that mattered.

## Exit criteria

- Same A/B pair gives visibly different paths per mode, and the reasons say why.
- Unmatched Jellyfin items are below an agreed threshold, or understood.

## Built so far (2026-10-11)

Checked against the live server (12.2.0), not memory:

- Auth: header `Authorization: MediaBrowser Token="<key>"` (never in the URL).
  `GET /Users` lists accounts; `GET /Items?userId=&IncludeItemTypes=Audio&Recursive=true&Fields=Path,ProviderIds&Filters=IsPlayed|IsFavorite&StartIndex=&Limit=`
  returns items with `UserData` (`PlayCount`, `IsFavorite`, `LastPlayedDate`) and `Path`,
  `ProviderIds.MusicBrainzRecording`. Tags are mostly present only for some items.
  Paging by 1000: 10.7k played items in ~1.4 s per page.
- Playback Reporting: `POST /user_usage_stats/submit_custom_query` with
  `{"CustomQueryString": "SELECT ... FROM PlaybackActivity ...", "ReplaceUserId": false}`
  returns `results` rows; columns `DateCreated` (UTC), `UserId`, `ItemId`, `ItemType`,
  `ItemName`, `PlaybackMethod`, `ClientName`, `DeviceName`, `PlayDuration` (seconds
  actually played). So a skip can be told apart. Only history since the plugin was
  installed exists; it fills as you listen.
- `config.yaml`: `jellyfin.url`, `jellyfin.user`; key in `$NEIGHBEATS_JELLYFIN_KEY`
  (compose passes it from `.env`). `path_map.remote` must be what Jellyfin reports
  (here `/media/Music`).
- `neighbeats sync-jellyfin [-user NAME]`: items matched by path (`path_map`
  `ToLocal`), then by a recording MBID that is unique in the index; prints matched /
  unmatched counts with samples. Taste is replaced as a whole in one transaction;
  play events are appended once (incremental by time); item -> track matches are
  cached, including "no match". A missing plugin only disables skips.
  History is stored per lowercased user name, so users stay separate.
- `path -mode comfort|explore|rediscover|none [-user NAME]`. Default is comfort as
  soon as history exists for `jellyfin.user`. Cost adjustments per entered track
  (negative = preferred): all modes +0.5 if played < 3 days ago, +0.2 < 14 days;
  comfort: favourite -0.30, -0.06*ln(1+plays) (max -0.18); explore: never played
  -0.25, +0.05*ln(1+plays); rediscover: favourite -0.15, up to -0.35 for tracks
  not heard for 30..120+ days (full from 120). A play shorter than 30 s (or half
  the track if shorter) is a skip; a track skipped in the last 30 days is kept out of
  the route (favourites excepted). `-explain` shows the taste term that mattered.
  `-length` mode weighs taste by giving up 0.12 cosine per cost unit among the 80 tracks nearest each waypoint (first try at 0.05 let "played today" tracks through).
- Tuning is still to do on the full library: the weights above are first guesses.

## First real sync (2026-10-11, NAS)

10,742 played or favourite items for `vincent`: 10,734 matched by path (99.93%), 0 by
MBID, 8 unmatched. The 8 are `.wma` and `.mpc` files, formats the scanner does not
index (`extensions` default), so they are understood and left out. 10,734 tracks with
taste, 260 favourites. (A first run with `path_map.remote: /music` matched nothing by
path; it must equal the path Jellyfin reports, `/media/Music`.)

## First listening check (2026-10-11, 100-track `-length` route, four modes)

- Modes differ as designed: explore fills with `never played` tracks, rediscover shows
  `not played for N months`, comfort picks favourites.
- Found: the same song twice ("Fade to Black" / "Fade To Black", different recording
  MBIDs). The duplicate key is now artist + normalised title (case, punctuation and
  bracketed suffixes ignored), MBID only when there is no title.
- Found: recent plays barely moved `-length` routes; taste weight raised (see above).
