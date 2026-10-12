#!/bin/sh
set -e
if ! getent passwd neighbeats >/dev/null; then
  adduser --system --group --home /var/lib/neighbeats --no-create-home \
    --gecos "neighbeats" neighbeats >/dev/null
fi
install -d -o neighbeats -g neighbeats -m 0755 /var/lib/neighbeats /var/lib/neighbeats/models
# The service user edits it (`sudo -u neighbeats neighbeats onboard`).
chown neighbeats:neighbeats /etc/neighbeats/config.yaml
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
fi
cat <<MSG
neighbeats installed. Next:
  1. edit /etc/neighbeats/config.yaml (music_roots, path_map)
  2. sudo -u neighbeats neighbeats onboard     (downloads the models)
  3. sudo -u neighbeats neighbeats update      (first run: hours)
  4. sudo systemctl enable --now neighbeats-update.timer
MSG
