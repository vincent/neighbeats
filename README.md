# neighbeats

Playlists that travel from one song to another, built from your own library.

Give it two songs and it finds a path of tracks that drifts from the first
to the second, one small step at a time. It is a CLI with no UI, meant to run
on a NAS next to a Jellyfin server and to write plain M3U8 files.

Library size this is built for: about 25,000 tracks.

## Quick start (Docker)

Everything runs in the container; you mount the config, the model, the data
folder and your music.

```sh
cp config.example.yaml config.yaml        # container paths; set path_map.remote
mkdir -p data models                      # put discogs-effnet-bsdynamic-1.onnx in ./models
export MUSIC_DIR=/path/to/your/music      # or put it in .env

docker compose run --rm -it neighbeats onboard         # first run: asks about paths, models and GPU, then sets them up
docker compose run --rm neighbeats scan                # incremental; safe to re-run
docker compose run --rm neighbeats update              # the one to schedule: backup, scan, analyze, embed-lyrics, sync-jellyfin (see docs/operations.md)
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

## Developing natively

Needs Go, a C compiler (the ONNX binding uses cgo), `ffmpeg` and `libonnxruntime.so`
on the loader path. Copy the same template and change `db_path`, `music_roots`,
`path_map.local` and `model_path` to host paths (`config.yaml` is git-ignored):

```sh
cp config.example.yaml config.yaml
make build && ./neighbeats scan
```

## Development

```sh
make test    # go test -race ./...
make vet
```

Tests that need `ffmpeg` skip themselves when it is not installed.

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

## Known limits (M0)

- MusicBrainz IDs in **M4A** files are read from iTunes-style freeform atoms
  (what Picard writes). This is covered by a unit test on the parsing logic
  but not yet by a real Picard-tagged file. MP3 (ID3v2) and FLAC are covered by
  generated files.
- No duration is stored yet; it arrives with audio analysis in M1.

## Acknowledgments

The route search and cost model draw on ideas from [Melodex](https://github.com/Cliff-Lee/melodex) and [AudioMuse](https://github.com/NeptuneHub/AudioMuse-AI) ; no code is shared.

neighbeats downloads or bundles:
- the Discogs-EffNet audio embedding model from the Music Technology Group, Universitat Pompeu Fabra (CC BY-NC-SA); 
- multilingual-e5-small (MIT); 
- ONNX Runtime (MIT); 
- whatlanggo for language detection. 

This project was largely written with AI assistance (Claude Code).