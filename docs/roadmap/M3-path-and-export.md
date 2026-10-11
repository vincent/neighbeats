# M3: `path A B` and M3U8 export

Goal: the headline feature. A playlist that moves from A to B.

## Design (from the reference route engine)

- Search over the neighbour index on the fly instead of a precomputed graph:
  expanding a node asks the index for its top-k neighbours (k ~ 6-10).
- Edge cost: `base + scale * (1 - similarity)` plus a constant per-hop penalty,
  floored so a hop is never free (reference: 0.68 + 1.18*(1-s) + 0.17 per hop).
- Search: Dijkstra, or A* with the embedding distance to B as the heuristic
  (check admissibility; fall back to Dijkstra if it is not). Cap hops (default 12).
- Forbidden set: tracks that may not appear as intermediates; A and B are always
  allowed. Dedupe by recording MBID so two copies of a song never both appear.
- Spread: limit tracks per artist along the path.
- Each hop stores a reason ("similarity 0.81", later "same work", "favourite").
- Honest failure: when no path exists within the hop limit, report the closest
  point reached and why.

## Tasks

- [x] `internal/route`: the search, pure functions over an interface
      (`Neighbours(id, k)`, `Similarity`), so tests use a tiny in-memory graph.
- [x] Tests ported in spirit from the reference's pathfinder tests: hops through
      intermediates, forbidden tracks, forbidden destination still allowed, missing
      endpoints rejected, A == B is a zero-hop route.
- [x] Track resolution for A and B (search by text, path, or MBID); clear message
      when ambiguous.
- [x] `path A B [-length N] [-avoid-artist X] [-out file.m3u8]`.
- [x] M3U8 writer (`internal/m3u`): `#EXTM3U`, `#EXTINF` with duration and
      "artist - title", paths translated with `path_map` to the media server's view
      (always the remote side; no `--for` flag). First use: `similar -out file.m3u8`
      writes the seed then its neighbours. `path` will reuse the writer.
- [x] Optional: `-explain` prints the reason per hop.
- [ ] Optional: "waypoint" mode, A -> W -> B, by chaining two searches.

## Exit criteria

- Quality test from the roadmap: 10 hand-chosen pairs, listened to, judged.
- Notes on which cost constants changed and why.

## Built so far (2026-10-11)

- `internal/route`: `Find` is A* over lazily expanded neighbour lists (top 10,
  max 2 tracks per artist per list), edge cost `0.68 + 1.18*(1-score) + 0.17`, hop
  cap 12, expansion cap 400, `score = exp(-2.3*(1-cos))`. The heuristic
  (`5 * (1 - cos to destination)`) is not admissible, on purpose: it keeps the
  search to hundreds of expansions (one library scan each, ~20 ms on the NAS). A
  track in the destination's own top 30 may hop straight to it. Constraints along
  the route: forbidden artists, no two copies of a song (recording MBID, else
  artist+title), at most 2 tracks per artist, never two in a row. On failure it
  returns the closest point reached and why.
- `Spread` (`-length N`): exactly N tracks. N-2 waypoints evenly spaced along the
  arc between A and B in embedding space, each snapped to the nearest track that
  satisfies the constraints. Added because the cheapest route is short by
  construction (cost grows with every hop), so it is not a playlist of a chosen
  length.
- `neighbeats path [flags] <from> <to>` (text, path or MBID, or `-from-id/-to-id`),
  `-explain`, `-out file.m3u8`, `-avoid-artist a,b`, `-hop-penalty`, `-max-hops`.
- Not done: waypoint mode A -> W -> B, the 10-pair listening test, tuning notes.
