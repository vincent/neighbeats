# Lyrics track (L1-L3)

Lyrics live in sibling `.lrc` files (same name as the audio file, `.lrc`
extension). Measured on the user's library: **9,178 tracks with lyrics, 15,947
without, 36.5% coverage**, mostly English.

Because coverage is partial, lyrics are always an **optional signal**: a track
without lyrics is scored on audio alone, never penalised for lacking them.

Consequence for blending: if lyric coverage were independent of everything else,
only about 13% of track pairs (0.365 squared) would have lyrics on both sides.
So a lyric term can only nudge a minority of hops, and any gain should be
measured on those hops specifically.

## L1: ingest and search (done)

- [x] Parse LRC: timestamps, `[ar:]`/`[ti:]`/`[offset:]` metadata, repeated-chorus
      lines (`[00:30][01:10]text`), enhanced word marks, BOM, Latin-1 fallback,
      plain-text files without timestamps.
- [x] `lyrics` table (one row per track, plain text, synced flag, source file
      fingerprint) and an FTS5 index kept in sync by triggers; rows disappear
      with their track.
- [x] `scan` syncs lyrics after a complete scan (`--no-lyrics` to skip). Unchanged
      `.lrc` files are not re-read. A missing `.lrc` removes the lyrics; any other
      stat error keeps them.
- [x] `neighbeats lyrics search [-n N] [-phrase] words...`.
- [x] `stats` shows lyric coverage.

Not stored yet: the per-line timestamps. Add them when something needs them.

## L2: language and cheap features

- [ ] Detect language per track (English dominates; the point is to spot the
      exceptions). Pick a small Go library or a stopword heuristic and measure it
      on a hand-checked sample; record accuracy here.
- [ ] Soft language-jump penalty in the path cost (M3), off by default.
- [ ] Keyword overlap between neighbouring tracks (TF-IDF over the lyrics
      corpus) to give hops a reason like "both about: sea, night".
- [ ] Resolve path endpoints from a lyric line: `path --from-lyric "..." ...`.

## L3: lyric embeddings (after M3)

Only start once `path` works on audio alone, so there is a baseline to compare.

- [ ] Choose a text-embedding model that runs through the same ONNX Runtime setup
      as M1. Only ~9,200 lyrics need embedding, so even a slow run is short; check
      the model's memory use on the 6 GB GPU. English dominates, so an English-only
      model may do, with other languages left unembedded.
- [ ] Embed the lyric text (cap length; decide how to treat very long lyrics).
- [ ] Blend into the edge score only when both tracks have lyrics:
      `s = (1 - w) * audio + w * lyric`, with `w` small to start (~0.2) and tuned by
      listening. Otherwise audio only.
- [ ] Steering by theme: embed a free-text phrase and bias a path toward it.
- [ ] Listening test: does the blend improve 10 hand-picked pairs, or only add noise?

## Known limits

- Lyric embeddings capture topic and language more than mood or sound.
- Instrumentals have no lyrics and can never get a lyric score.
- Timestamps could help with transitions (where the vocals start and stop), but
  that is speculative and not planned.
