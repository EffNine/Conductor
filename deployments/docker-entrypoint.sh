#!/bin/sh
# Ensure the SQLite data directory is writable when a volume is mounted
# (Fly/Railway volumes are often root-owned; the app runs as user novexa).
set -e

DATA_DIR="${NOVEXA_DATA_DIR:-/app/data}"
mkdir -p "$DATA_DIR"
chown -R novexa:novexa "$DATA_DIR" 2>/dev/null || true

if command -v su-exec >/dev/null 2>&1; then
  exec su-exec novexa ./gateway "$@"
fi

# Fallback if su-exec is unavailable
exec ./gateway "$@"
