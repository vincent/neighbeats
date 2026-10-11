# Roadmap

Goal: given two songs, write an M3U8 playlist that walks from one to the other
through a library of about 25,000 tracks (mostly MP3/AAC, mostly MusicBrainz-tagged),
biased by what Jellyfin knows about the listener's taste.

Library size: **25,125 tracks** (confirmed). That is small enough to keep everything
in memory and search exactly, so there is no approximate-search or sharding work.

Constraints: Go, CLI only, Docker on a NAS (Debian, i7-3770K without AVX2,
GTX 1060 6 GB), no Python at runtime if it can be avoided. The build needs cgo
(ONNX Runtime binding only); it happens inside the Docker build stage, see
[M1](docs/roadmap/M1-embeddings.md).

| # | Milestone | Status | Detail |
|---|-----------|--------|--------|
| M0 | Library index: scan, tags, MBIDs, path mapping | **done** | [M0](docs/roadmap/M0-library-index.md) |
| M1 | Audio embeddings for the whole library + throughput measurement | **in progress** (pipeline built; NAS/GPU run pending) | [M1](docs/roadmap/M1-embeddings.md) |
| M2 | In-memory exact neighbour search + `similar` debug command | **in progress** (search + `similar` built; listening test pending) | [M2](docs/roadmap/M2-neighbour-index.md) |
| M3 | `path A B` + M3U8 export | **in progress** (built; listening test pending) | [M3](docs/roadmap/M3-path-and-export.md) |
| M4 | Jellyfin sync + taste bias | **in progress** (sync + modes built; tuning pending) | [M4](docs/roadmap/M4-jellyfin-taste.md) |
| M5 | Tuning, upkeep, packaging | planned | [M5](docs/roadmap/M5-full-library.md) |

Lyrics (sibling `.lrc` files, 36.5% of tracks): [L1-L3](docs/roadmap/L-lyrics.md).
L1 (ingest + full-text search) is **done**; L2 (language, keywords) can happen any time;
L3 (embeddings and blending) waits for M3.

Later (not scheduled): [docs/roadmap/later.md](docs/roadmap/later.md).

## Success test

1. **Quality (after M3):** on the real library, 10 paths between song pairs
   chosen by hand sound coherent when listened to.
2. **Throughput (after M1):** the whole library embedded on the NAS, with a
   measured seconds-per-track figure and the bottleneck identified (decode on
   CPU vs GPU inference vs disk). At 25k tracks this is one run, not a campaign.

## Principles carried over from the reference project

- Every hop of a path carries a human-readable reason.
- Honest failure: if no path exists, say so and say where it broke.
- Skips and avoided tracks are forbidden in later plans; the destination never is.
- Nothing is deleted from the index on partial information.

## Open risks

| Risk | Where | How it gets closed |
|------|-------|--------------------|
| ~~Mel-spectrogram in Go does not match the model's reference preprocessing~~ | M1 | Closed: cosine 1.0000 against the reference on identical samples. Compare against the reference on a handful of tracks (dev-time script only) |
| ONNX Runtime GPU build does not run on a GTX 1060 / the NAS driver | M1 | First task of M1, before any pipeline work |
| ONNX Runtime or Go binaries assume AVX2, which the i7-3770K lacks | M1 | Same smoke test on the NAS CPU |
| Decoding 25k MP3/AAC clips on 4 cores is the slow part | M1 | Measure on a 500-track slice first, then run the lot; tune clip length and worker count |
| M4A MusicBrainz IDs unverified on real Picard files | M0 follow-up | Add a real sample file as a test fixture |
| Jellyfin 12.2 API shapes assumed from memory | M4 | Read the live API before writing the client |
