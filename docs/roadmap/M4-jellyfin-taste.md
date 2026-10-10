# M4: Jellyfin sync and taste bias

Goal: make paths prefer what the listener likes and avoid what they just heard
or skipped.

Jellyfin is 12.2. The Playback Reporting plugin is **not installed yet**.

## Tasks

- [ ] Read the live API (Swagger on the server) before writing the client; do not
      rely on remembered field names.
- [ ] Client: API key from an environment variable, never in the config file.
      Per user: items with play count, favourite flag, last played date.
- [ ] Match Jellyfin items to indexed tracks: first by file path through `pathmap`
      `ToLocal`, then by MusicBrainz IDs as a fallback. Report unmatched counts.
- [ ] `taste` table: per track plays, favourite, last played, source, synced_at.
- [ ] `sync-jellyfin` command, idempotent, incremental where the API allows.
- [ ] Decide on skips: install Playback Reporting and check whether it exposes
      play duration per event; a play far shorter than the track is a skip
      (the reference project uses < 30 s). Until then taste uses plays and favourites only.
- [ ] Cost terms (from the reference project's session and taste scoring, simplified):
      favourites get a bonus; recently played tracks and over-used artists a
      penalty; a `--mode comfort|explore|rediscover` flag picks the weights.
- [ ] Rediscovery: a mild bonus for tracks last played long ago (the reference peaks
      around 120 days).
- [ ] Each hop's reason includes the taste term that mattered.

## Exit criteria

- Same A/B pair gives visibly different paths per mode, and the reasons say why.
- Unmatched Jellyfin items are below an agreed threshold, or understood.
