# M0: Library index

**Status: done.** Scope was a repo that can index the library and report on it.

## Delivered

- [x] Go module, CLI skeleton (`scan`, `stats`, `version`, `--config`).
- [x] YAML config with defaults and strict validation (unknown keys rejected).
- [x] Path mapping between this tool's view and Jellyfin's, longest prefix wins,
      boundary-aware (`/music` never matches `/music2`). Not used until M3/M4.
- [x] SQLite store (pure Go driver, no cgo), WAL mode, versioned migrations.
- [x] Tag reader: title, artist, album, album artist, genre, year, track, disc,
      plus MusicBrainz recording, release track, artist, album artist, release,
      release group and work IDs.
- [x] Incremental scan: size+mtime fingerprint, parallel tag readers, one batched
      writer, progress line every 5 s.
- [x] Safe removal: only after a complete walk; refuses to drop more than half
      of the tracks under the roots without `--allow-mass-remove`; unreadable
      folders and Ctrl-C leave the index alone.
- [x] `stats`: tracks per format, MBID coverage, distinct artists/releases,
      missing tags, unreadable files, last scan.
- [x] Unit tests for each package, run with `-race`.

## Not done / follow-ups

- [ ] Real Picard-tagged M4A sample as a fixture (MP3 and FLAC are covered by
      generated files; M4A parsing is only unit-tested on a synthetic tag map).
- [ ] Opus tag reading is untested.
- [ ] Lyrics were added on top of M0; see [L-lyrics.md](L-lyrics.md).
- [ ] A scan cancelled part-way (not before it starts) is not covered by a test.
- [ ] Dockerfile and compose file with the NVIDIA runtime: moved to M1, where the
      GPU is first needed.
- [ ] Run `scan` and `stats` on the real library (25,125 tracks) and record the
      numbers (format split, MBID coverage, scan time) in this file. MBID coverage
      decides how much the factual-link layer in [later](later.md) can lean on
      MusicBrainz.

## Schema (v1)

`tracks` (one row per file, unique `path`, size, mtime_ns, format, descriptive
tags, seven MBID columns, `tag_error`, `seen_run`) and `scan_runs` (one row per
scan with status and counters). Partial indexes on recording and artist MBIDs.
Later milestones add tables through new migrations; never edit an applied one.
