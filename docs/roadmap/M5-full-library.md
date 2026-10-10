# M5: Tuning, upkeep, packaging

The whole library is already embedded in M1, so this milestone is about making
the tool pleasant to live with.

## Tasks

- [ ] Tune cost constants on the real library with 20+ listening tests; freeze
      them in a documented config block.
- [ ] Incremental upkeep: a scheduled `scan` then `analyze` that only touches new
      or changed files (an album added should take minutes, not a full re-run).
- [ ] Startup time with all vectors loaded; if it is slow, cache a packed vector
      file next to the database.
- [ ] Packaging: compose file, a cron/systemd timer example, backup advice for the
      SQLite file.
- [ ] Docs: operations guide (first run, upgrades, what to do when the share is
      unmounted, how to rebuild the embeddings after changing model).

## Exit criteria

- A path request answers in seconds.
- A re-scan after adding an album picks it up without a full re-run.
