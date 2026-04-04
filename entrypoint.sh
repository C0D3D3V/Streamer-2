#!/bin/sh
set -e

PUID=${PUID:-1000}
PGID=${PGID:-1000}
UMASK=${UMASK:-022}

echo "Starting streamer with UID=${PUID} GID=${PGID} UMASK=${UMASK}"

# Ensure the data directory is owned by the target user so the app can write
# its SQLite database, HLS segments, and MP4 archives.
chown -R "${PUID}:${PGID}" /data

# Set the umask before exec so all files created by the app inherit it.
umask "${UMASK}"

# Drop from root to the requested UID:GID and replace this shell with the app.
# su-exec is a minimal setuid helper (Alpine equivalent of gosu) that does a
# clean exec without leaving a shell process in the process tree.
exec su-exec "${PUID}:${PGID}" /app/streamer "$@"
