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

## L2: language and cheap features (built 2026-10-11)

- [x] Language per track (`internal/lang`, the `whatlanggo` trigram detector).
      Done by `scan` after the lyric sync (and by `embed-lyrics` / `lyrics langs`
      for any lyric that has none), stored in `lyric_lang` as an ISO 639-3 code
      or `und`. Accuracy: on 21 lyric-like passages written for the test (11
      languages) it answered right 16 times and `und` 5 times (Spanish, Italian,
      Russian: low confidence), **never wrongly**. The rule is deliberately
      conservative: an unreliable answer, or under 40 letters, is `und`, and `und`
      is never a language jump. **Still to do: check it on the real library**
      with `neighbeats lyrics langs -samples 5`, which lists the distribution and a
      few tracks per language to judge by eye; record the result here.
- [x] Language-jump penalty in the path cost: `-lang-penalty` (default 0.5, in
      the units of the taste bias; 0 = off). Applies only between two known,
      different languages; the hop reason says "language eng to fra".
- [x] Keyword overlap: `-explain` adds `both about: sea, night` to a hop when
      both lyrics repeat (at least twice each) a word found in under 4% of the
      library's lyrics (TF-IDF over all lyrics, built when needed, about 0.1 s).
      Needs at least 50 lyrics; a reason only appears when there is a telling word.
- [x] Endpoints from a lyric line: `path -from-lyric "..." -to-lyric "..."`
      (phrase search, then all-words search, among tracks that have an audio
      embedding; several matches are listed with their ids, pick one with
      `-from-id` / `-to-id`).

## L3: lyric embeddings (built 2026-10-11)

Done after `path` worked on audio alone, so there is a baseline to compare with.

- [x] **Model: `intfloat/multilingual-e5-small`** (MIT licence, 12 layers, 384
      dimensions, 100+ languages, 512 tokens, 470 MB fp32 ONNX). Chosen over an
      English-only model because the library is "mostly" English, not only, and the
      same vectors then also serve other languages; over the larger e5 models
      because 9,200 lyrics do not need them. It runs through the same ONNX Runtime
      setup as the music model (`embed.OpenSession`), CUDA with CPU fallback.
      Files go in `lyric_model_dir`: `model.onnx` and `tokenizer.json`
      (`onnx/` folder of the Hugging Face repo, see the README).
- [x] **Tokenizer in Go** (`internal/textembed`): the model's SentencePiece Unigram
      `tokenizer.json`, with the precompiled NFKC character map, read by about 250
      lines of Go (no cgo, no Rust binding). Checked against the Hugging Face
      `tokenizers` library on 18,214 strings (multilingual lyric lines, whitespace
      and Unicode edge cases, one string per BMP character below U+3000): **identical
      ids** except two known classes, skipped in the test (special-token strings typed
      in a lyric, which Go drops, and decomposed Hangul jamo). Vectors match the
      Python reference pipeline at cosine 1.000000 on four texts including a 512-token
      truncation. Both checks are tests that run when the model files are given
      (`NEIGHBEATS_TEST_LYRICMODEL`, see the test comments).
- [x] **What is embedded:** each distinct line once, in order (so a repeated chorus
      does not eat the budget), prefixed `passage: ` as the e5 models expect, cut at
      512 tokens. `embed-lyrics` is resumable (the `.lrc` size and mtime of each
      vector are stored) and prints how many lyrics were cut. The model id
      `multilingual-e5-small-passage-512` is stored with every vector; change the
      preparation and the id changes, so old vectors are recomputed.
      Speed on the dev laptop CPU: 160 short synthetic lyrics in 0.8 s.
- [x] **Blend into the hop, only where both tracks have lyrics:** `-lyric-weight`
      (default 0.2; 0 = audio only). The roadmap's `(1-w)*audio + w*lyric` would
      penalise every pair with lyrics against every pair without (a lyric cosine is
      lower than an audio cosine for the same good neighbour), so a track *lacking*
      lyrics would win. Instead the hop cosine is `audio + w*(lyric - typical)`,
      where *typical* is the mean lyric cosine between a track and its audio
      neighbours, measured on 40 spread tracks at start-up (about 1 s on the NAS):
      lyrics that agree better than usual help, worse than usual hurt, and a pair
      without lyrics is exactly the audio cosine. Lyric vectors are centred on the
      library mean like the audio ones. Candidates still come from the audio search;
      lyrics only re-rank them (and the smoothing term of `-length`).
- [x] **Steering by theme:** `path -theme "sea, night"` embeds the phrase (`query: `
      prefix, on the CPU) and takes `-theme-weight` (default 3, in taste-bias
      units) off the cost of entering a track per unit of cosine between the phrase
      and its lyrics. Tracks without lyrics get nothing. The hop reason shows
      `theme 0.39`.
- [ ] **Listening test:** does the blend improve 10 hand-picked pairs, or only add
      noise? Not done: it needs ears and the embedded library. To run it, make
      each route twice, `-lyric-weight 0 -lang-penalty 0` and the defaults, and
      note which sounds better.

### Defaults, and why they are guesses

The user asked for the lyric features to be on by default with a small weight, so
`-lyric-weight 0.2` and `-lang-penalty 0.5` apply as soon as lyric vectors exist.
Without them (before `embed-lyrics`) every route is the audio-only route as before.
All three weights (0.2, 0.5, 3) are first guesses, set without a real-library
listening test; tune them from the listening test above. Switch a feature off with
its flag set to 0.

### What a synthetic check showed

On a copy of the dev database with 160 made-up lyrics in four languages (seven
themes), a theme phrase scored its own theme highest, e.g. mean centred cosine
0.45 for "ocean waves at night, the sea" against the sea lyrics, 0.08 to 0.0 for
others. But **the language dominates the similarity**: the same sea lines in French,
Spanish or German scored -0.10 to -0.18 against that English phrase (and "la mer et la
nuit" matched the English sea lyrics, 0.18, better than the French ones, 0.06). So a
theme written in English steers toward English lyrics, and lyric similarity between two
languages is low by construction. That agrees with the language penalty, but it means
`-theme` should be written in the language of the lyrics you want, and a cross-language
hop gets a lyric nudge downwards. If this shows on the real library, compute the centring
per language.

## Known limits

- Lyric embeddings capture topic and language more than mood or sound.
- Lyric similarity between two languages is low, and a theme phrase matches lyrics in
  its own language best (see the synthetic check above).
- Language detection is conservative: Spanish, Italian and Russian passages were
  often left undetermined in the test sample; those tracks get no language penalty.
- Instrumentals have no lyrics and can never get a lyric score.
- Timestamps could help with transitions (where the vocals start and stop), but
  that is speculative and not planned.
