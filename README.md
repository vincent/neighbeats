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

## Quick start

```sh
cp config.example.yaml config.yaml   # edit music_roots
make build
./neighbeats scan                    # incremental; safe to re-run
./neighbeats stats                   # formats, MusicBrainz coverage, lyrics coverage
./neighbeats lyrics search silver sea
./neighbeats lyrics search -phrase "silver sea"
```

`--config FILE` or `$NEIGHBEATS_CONFIG` selects another config file.

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
