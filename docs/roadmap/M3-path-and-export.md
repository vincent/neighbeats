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

- [ ] `internal/path`: the search, pure functions over an interface
      (`Neighbours(id, k)`, `Similarity`), so tests use a tiny in-memory graph.
- [ ] Tests ported in spirit from the reference's pathfinder tests: hops through
      intermediates, forbidden tracks, forbidden destination still allowed, missing
      endpoints rejected, A == B is a zero-hop route.
- [ ] Track resolution for A and B (search by text, path, or MBID); clear message
      when ambiguous.
- [ ] `path A B [--length N] [--avoid-artist X] [--out file.m3u8]`.
- [ ] M3U8 writer: `#EXTM3U`, `#EXTINF` with duration and "artist - title", paths
      translated with `pathmap` for the target system (`--for jellyfin|local`).
- [ ] Optional: `--explain` prints the reason per hop.
- [ ] Optional: "waypoint" mode, A -> W -> B, by chaining two searches.

## Exit criteria

- Quality test from the roadmap: 10 hand-chosen pairs, listened to, judged.
- Notes on which cost constants changed and why.
