#!/bin/sh

# Fast path: feature is off by default — exec immediately with no side effects.
if [ "${REPLICATION_MANAGER_CREATE_MISSING_CONFIG:-}" != "true" ]; then
    exec "$@"
fi

CONFIG_FILE="/etc/replication-manager/config.toml"
TEMPLATE_FILE="/usr/share/replication-manager/config.toml.default"

# Any existing filesystem entry at the config path (file, dir, symlink, broken symlink) — nothing to do.
if [ -e "$CONFIG_FILE" ] || [ -L "$CONFIG_FILE" ]; then
    exec "$@"
fi

# Template not packaged in this image — log and proceed.
if [ ! -f "$TEMPLATE_FILE" ]; then
    echo "[entrypoint] REPLICATION_MANAGER_CREATE_MISSING_CONFIG=true but $TEMPLATE_FILE not found; skipping auto-create" >&2
    exec "$@"
fi

# Target directory not writable (e.g. read-only mount) — log and proceed.
CONFIG_DIR=$(dirname "$CONFIG_FILE")
if [ ! -w "$CONFIG_DIR" ]; then
    echo "[entrypoint] REPLICATION_MANAGER_CREATE_MISSING_CONFIG=true but $CONFIG_DIR is not writable; skipping auto-create" >&2
    exec "$@"
fi

# All conditions met — create the config from the default template.
if cp "$TEMPLATE_FILE" "$CONFIG_FILE"; then
    echo "[entrypoint] created $CONFIG_FILE from default template" >&2
else
    echo "[entrypoint] REPLICATION_MANAGER_CREATE_MISSING_CONFIG=true but copy failed; continuing without auto-create" >&2
fi

exec "$@"
