# M5: Tuning, upkeep, packaging

The whole library is already embedded in M1, so this milestone is about making
the tool pleasant to live with.

## Tasks

- [x] Tuning: current constants are kept (decided with the user); see docs/operations.md
- [x] Incremental upkeep: `neighbeats update` (backup, scan, analyze, embed-lyrics,
      sync-jellyfin; optional steps skipped when not set up).
- [x] Startup time with all vectors loaded; if it is slow, cache a packed vector
      file next to the database. => start time is ok
- [x] Packaging: cron, systemd timer and NAS task examples, `backup` command
      (VACUUM INTO, keeps the newest N).
- [x] Docs (docs/operations.md): operations guide (first run, upgrades, what to do when the share is
      unmounted, how to rebuild the embeddings after changing model).

## Exit criteria

- A path request answers in seconds.
- A re-scan after adding an album picks it up without a full re-run.
