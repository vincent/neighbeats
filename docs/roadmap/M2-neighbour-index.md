# M2: Neighbour search and `similar`

Goal: fast "what sounds like this" over all embeddings, and a way to judge
embedding quality by ear before building paths on it.

## Why this is simple

The library has 25,125 tracks. With a vector of a few hundred to ~1,300 floats
that is roughly 30-130 MB, which fits in memory with room to spare, and an exact
scan over every vector is a matter of milliseconds to tens of milliseconds
(estimate; measure it). So:

- **No approximate index (HNSW) and no index persistence.** Exact search has
  perfect recall and nothing to tune or rebuild.
- Load all vectors from SQLite into one contiguous `[]float32` at startup
  (normalised, so cosine similarity is a dot product).
- Hide it behind a small `Neighbours(id, k)` interface so an approximate index can
  replace it if the library ever grows by an order of magnitude (see
  [later](later.md)).

## Tasks

- [x] `internal/vecindex`: load vectors, exact top-k by dot product, optional
      per-artist cap. Unit tests against a hand-made set of vectors.
- [x] Benchmark (laptop and NAS done): query time and load time at 25k x the chosen vector size on the
      NAS CPU (no AVX2). Only if a query takes more than ~100 ms, look at
      unrolling the loop or using the GPU; do not do it earlier.
- [ ] Optional: precompute the full k-NN graph (top 10 per track) once and cache it
      in SQLite, if building neighbours on the fly turns out slow in M3. 25k
      queries x 25k vectors is a one-off job, not a per-run cost.
- [x] `similar <track> [-n 20]` prints neighbours with distance, title, artist.
      Track lookup by path, by "artist - title" search, or by recording MBID.
- [x] Similarity function: start from the reference's `exp(-k * distance)` shape and
      tune `k` so scores spread usefully between 0 and 1.
- [ ] Judge quality: 20 seed tracks, listen to the top 10 each, note failures
      (same-artist flooding, genre jumps, silence/intro artefacts).
- [ ] Mitigations if needed: cap results per artist; use several clips per track;
      normalise vectors; combine with tempo/key if the embedding ignores them.

## Exit criteria

- `similar` answers in well under a second.
- A written note on what the embedding gets right and wrong, with examples.

## Built so far (2026-10-11)

- `internal/vecindex`: exact top-k over unit vectors in one `[]float32`, behind the
  `Neighbourer` interface (`Neighbours(id, k)`) that M3 will use. `Search` adds a
  per-artist cap. Vectors are mean-centred then L2-normalised at load; nothing is
  persisted. `Score(cosine, k)` is `exp(-k * (1 - cosine))`.
- `neighbeats similar [-n N] [-max-per-artist N] [-raw] [-k K] <path | recording MBID | words...>`
  (flags may go anywhere). Several matches list ids; pick one with `-id`. The cap
  defaults to 2 per artist (0 = off, to see flooding) and consecutive results never
  share an artist (`-allow-adjacent` turns that off), after a first listening check
  on the full library looked good.
- **Timing (i7-13620H, 25,000 x 1280, `go test -bench 25k ./internal/vecindex`):**
  12.6 ms per query, 17 ms with the artist cap. Reading and building the index for
  the 500-track slice takes 16 ms. NAS (i7-3770K, no AVX2): 20.8 ms per
  query, 25.5 ms with the cap, so the ~100 ms threshold for looking at SIMD or the GPU is
  not close.
- **Centring (500-track slice):** it does not change the ranking (top-1 same artist
  82%, top-10 71%, both ways) but removes the shared component, which squeezed all
  cosines into 0.3-0.8: random pairs sit at about 0.34 raw and -0.04 centred, rank-1
  neighbours at 0.79 raw and 0.66 centred.
- **k = 2.3** (flag `-k`): maps an unrelated pair (cosine ~0 centred) to about 0.1.
  Rank-1 neighbours then score ~0.45, rank-10 ~0.3. A single exponential cannot also
  push good neighbours towards 1; revisit when M3 sets its edge costs.

Still to do: the listening test (20 seeds x top 10) and the written quality note,
which need your ears and the full library.
