#!/bin/sh
set -e
if [ -d /run/systemd/system ]; then
  systemctl daemon-reload || true
fi
# The database, models and the neighbeats user stay, also on purge: the
# embeddings take hours to rebuild. Remove /var/lib/neighbeats by hand.
