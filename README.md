# neighbeats

Playlists that travel from one song to another, built from your own library.

Give it two songs and it finds a path of tracks that drifts from the first
to the second, one small step at a time. It is a CLI with no UI, meant to run
on a NAS next to a Jellyfin server and to write plain M3U8 files.

Library size this is built for: about 25,000 tracks.

The design follows an existing reference project (a desktop app with a route
engine, cost model and live replanning). Its ideas are reused; no code is shared.

## Status

**M0 done: library indexing**, plus **lyrics ingest and search (L1)**. Songs are
indexed with their tags, MusicBrainz IDs and sibling `.lrc` lyrics; there are no
embeddings, paths or playlists yet.
See [ROADMAP.md](ROADMAP.md).

## Quick start (Docker)

Everything runs in the container; you mount the config, the model, the data
folder and your music.

```sh
cp config.example.yaml config.yaml        # container paths; set path_map.remote
mkdir -p data models                      # put discogs-effnet-bsdynamic-1.onnx in ./models
export MUSIC_DIR=/path/to/your/music      # or put it in .env

docker compose run --rm neighbeats scan                # incremental; safe to re-run
docker compose run --rm neighbeats stats               # formats, MusicBrainz and lyrics coverage
docker compose run --rm neighbeats analyze             # embeddings (GPU if available, else CPU)
docker compose run --rm neighbeats similar "yann tiersen valse"   # what sounds like it (after analyze)
docker compose run --rm neighbeats similar -id 123 -out /data/mix.m3u8   # same, as a playlist (Jellyfin paths via path_map)
docker compose run --rm neighbeats path -length 15 -out /data/trip.m3u8 "song a" "song b"   # playlist travelling from one song to another
docker compose run --rm neighbeats sync-jellyfin        # plays, favourites, skips (needs NEIGHBEATS_JELLYFIN_KEY in .env)
docker compose run --rm neighbeats path -mode rediscover -length 15 "song a" "song b"
docker compose run --rm neighbeats lyrics search silver sea
docker compose run --rm neighbeats lyrics langs -samples 5    # detected lyric languages, to check by eye
docker compose run --rm neighbeats embed-lyrics         # lyric vectors (needs the lyric model, below)
docker compose run --rm neighbeats path -theme "sea, night" -length 15 "song a" "song b"
docker compose run --rm neighbeats path -from-lyric "walking by the ocean" -to-id 123 -length 15
```

### Lyric model (optional)

Lyrics only add a small nudge to a route, and nothing needs them. To use them, put the
multilingual-e5-small text model next to the music model (MIT licence, 470 MB):

```sh
mkdir -p models/multilingual-e5-small && cd models/multilingual-e5-small
B=https://huggingface.co/intfloat/multilingual-e5-small/resolve/main/onnx
curl -LO $B/model.onnx && curl -LO $B/tokenizer.json
```

Then `scan` (it also detects each lyric's language), `embed-lyrics` (minutes, resumable) and
`path` uses the lyrics by itself: `-lyric-weight` (0.2), `-lang-penalty` (0.5), `-theme`,
`-theme-weight`. Set a weight to 0 to switch that part off.

`compose.yaml` documents the mounts and the GPU reservation. `--config FILE` or
`$NEIGHBEATS_CONFIG` selects another config file (compose sets the latter).

## Developing natively

Needs Go, a C compiler (the ONNX binding uses cgo), `ffmpeg` and `libonnxruntime.so`
on the loader path. Copy the same template and change `db_path`, `music_roots`,
`path_map.local` and `model_path` to host paths (`config.yaml` is git-ignored):

```sh
cp config.example.yaml config.yaml
make build && ./neighbeats scan
```

## What `scan` does

- Walks `music_roots`, reads tags (title, artist, album, genre, year, track and
  disc) and MusicBrainz IDs (recording, release track, artist, album artist,
  release, release group, work) from MP3, M4A/AAC, FLAC and Ogg Vorbis (Opus is untested).
- Skips files whose size and mtime are unchanged, so a re-scan of a large
  library is fast.
- Removes tracks whose files are gone, but only when the whole walk succeeded.
  An unmounted share or an unreadable folder never empties the index, and a
  scan that would remove more than half of the tracks is aborted unless you
  pass `--allow-mass-remove`.
- After a complete scan it reads the sibling `.lrc` of each track (same name,
  `.lrc` extension), stores the plain lyric text and indexes it for search.
  Pass `--no-lyrics` to skip this.
- Files with unreadable tags are still indexed, with the error recorded, and
  are not retried until they change.

## Layout

```
cmd/neighbeats     CLI entry point
internal/config    YAML config, defaults, validation
internal/pathmap   local <-> Jellyfin path translation
internal/tags      tag and MusicBrainz ID extraction
internal/lyrics    LRC parsing and sibling-file sync
internal/store     SQLite schema, migrations, queries
internal/scan      incremental library walk
docs/roadmap       one file per milestone
```

## Development

```sh
make test    # go test -race ./...
make vet
```

Tests that need `ffmpeg` skip themselves when it is not installed.

## Known limits (M0)

- MusicBrainz IDs in **M4A** files are read from iTunes-style freeform atoms
  (what Picard writes). This is covered by a unit test on the parsing logic
  but not yet by a real Picard-tagged file. MP3 (ID3v2) and FLAC are covered by
  generated files.
- No duration is stored yet; it arrives with audio analysis in M1.
