# Later (unscheduled)

Ideas worth keeping, none of them committed to.

- **MusicBrainz factual links** as extra edges (same work, covers, remixes, shared
  credits), weighted like the reference project's knowledge graph. Needs a local MusicBrainz
  data source and a check of which relationship types actually exist for the
  library.
- **Journey stages**: "calm then energetic then forgotten" as waypoints, as in
  the reference project's journey designer.
- **Live replanning** when playing through a Jellyfin client: replan the tail after
  a skip. Needs a way to see playback, so probably a small server mode.
- **Navidrome/Jellyfin playlist push** instead of files.
- **Transition quality**: key and tempo compatibility, crossfade hints in M3U8
  extended tags.
- **Sample/cover databases**: no open WhoSampled equivalent was found; MusicBrainz
  relations are the realistic source.
- **Approximate nearest-neighbour index (HNSW)** and sharded analysis, only if the library grows
  roughly tenfold; at 25k tracks exact search is enough.
- **Several clips per track** or a learned track-level pooling for better embeddings.
- **Tag repair**: report tracks with no MBIDs so they can be run through Picard.
