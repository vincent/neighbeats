# Operations guide

## First run

0. Shortcut for steps 1 and 2: `cp config.example.yaml config.yaml`, then
   `docker compose run --rm -it neighbeats onboard`. It asks whether `path_map.remote` is the
   folder as your player sees it, whether to download the music model (CC BY-NC-SA) and the
   lyric model (MIT), and whether to use the GPU or only the CPU; it edits only those two
   config lines (a `.bak` of the old file is kept) and never overwrites a model that is already
   there. `-yes` accepts the defaults without asking.
1. `cp config.example.yaml config.yaml`; set `path_map.remote` (the music folder as Jellyfin sees it).
2. Put the music model in `models/` and, optionally, the lyric model (see the README).
3. `docker compose run --rm neighbeats scan` (minutes), then `analyze` (hours on CPU, much less
   on the GPU; resumable, Ctrl-C is safe), then `embed-lyrics` if you use lyrics.
4. Optional taste: set `jellyfin.url` and `jellyfin.user`, put `NEIGHBEATS_JELLYFIN_KEY` in `.env`,
   run `sync-jellyfin`.

## Keeping it up to date: `update`

`neighbeats update` runs, in order: `backup`, `scan`, `analyze`, `embed-lyrics`, `sync-jellyfin`.
Every step only touches new or changed files, so after adding an album it takes minutes, and a run
with nothing new takes seconds. `embed-lyrics` is skipped if the lyric model is missing and
`sync-jellyfin` if the Jellyfin URL, user or key is not set. The first failing step stops the run
and the exit code is non-zero. `-no-backup` skips the backup, `-keep N` sets how many copies stay.

Do not run two `update` (or `analyze`) at once: the second waits on the database lock.

### cron

```
# every night at 03:30
30 3 * * *  cd /path/to/neighbeats && docker compose run --rm -T neighbeats update >> data/update.log 2>&1
```

`-T` turns off the terminal, so the log gets plain lines instead of the live progress line.

### systemd timer

`/etc/systemd/system/neighbeats-update.service`:

```
[Unit]
Description=neighbeats update
After=docker.service

[Service]
Type=oneshot
WorkingDirectory=/path/to/neighbeats
ExecStart=/usr/bin/docker compose run --rm -T neighbeats update
```

`/etc/systemd/system/neighbeats-update.timer`:

```
[Unit]
Description=neighbeats nightly update

[Timer]
OnCalendar=*-*-* 03:30
Persistent=true

[Install]
WantedBy=timers.target
```

Then `systemctl enable --now neighbeats-update.timer`; logs: `journalctl -u neighbeats-update`.

### Synology / Unraid / other NAS schedulers

Create a scheduled task that runs as the user who owns the folder, with the command
`cd /path/to/neighbeats && docker compose run --rm -T neighbeats update`
(Synology: Control Panel, Task Scheduler, User-defined script; Unraid: User Scripts plugin).
Enable the mail-on-error option so a failed run is noticed.

## Backups

`neighbeats backup` writes a consistent copy of the database (safe while something else is
using it) to `<db folder>/backups/neighbeats-<time>.sqlite` and keeps the newest 7
(`-keep`, `-dir`). `update` does it first. The database is the only state: embeddings, lyrics,
plays and skips. Embeddings take hours to recompute and the rest comes back from the music
files and Jellyfin, but a backup saves all of that. Copy the `backups` folder off the NAS from time to time.
To restore, stop everything and copy a backup over `neighbeats.sqlite` (delete any
`neighbeats.sqlite-wal` and `-shm` next to it).

## Upgrades

Rebuild the image (`docker compose build`) and run any command: the database upgrades itself
on open. Take a `backup` first; going back to an older version needs the backup.

## The music share is not mounted

`scan` removes tracks whose files are gone, but only after a complete walk, and it aborts
when that would remove more than half of the library. An unmounted share therefore leaves the
index alone. Remount and run `update` again. `--allow-mass-remove` is only for when you really
deleted most of the music.

## Changing the model

Embeddings are stored per model id. A new model means: put it in `models/`, adjust the code's
model id, and `analyze` embeds everything again (old vectors stay until you delete them).
Lyric vectors work the same way with `embed-lyrics`. Routes use only the current model's vectors.

## Tuning

The route constants (`Base`, `Scale`, `Hop`, `K`, `Smooth`, taste and lyric weights) are the
defaults in `internal/route` and the flags of `path`. They are not in the config on purpose;
the current values were judged good on the real library and are frozen.
