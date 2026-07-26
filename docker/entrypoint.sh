#!/bin/sh
# Ion container entrypoint.
#
# Responsibilities:
#   - Optionally initialize the data directory on first run.
#   - Forward all arguments to the `ion` binary, defaulting to `dashboard`.
#
# Initialization is opt-in. Production deployments should run `ion init`
# explicitly against a host-protected key source. The development file KEK is
# never a production mechanism.
#
# Environment:
#   ION_DATA_DIR       Data directory (default /data).
#   ION_WEB_LISTEN     Web operator listen address (loopback only for plain HTTP).
#   ION_WEB_ORIGIN     Exact browser origin when a TLS proxy serves Ion remotely.
#   ION_AUTO_INIT      When "1", initialize the data directory if it looks empty.
#   ION_DEV_FILE_KEK   When "1", initialize with the development-only file KEK.
set -eu

DATA_DIR="${ION_DATA_DIR:-/data}"

if [ "${ION_AUTO_INIT:-0}" = "1" ]; then
    # "Empty" means no files other than dotfiles the platform may have created.
    if [ -z "$(ls -A "$DATA_DIR" 2>/dev/null || true)" ]; then
        echo "ion: initializing data directory at $DATA_DIR"
        if [ "${ION_DEV_FILE_KEK:-0}" = "1" ]; then
            ion init --data-dir "$DATA_DIR" --dev-file-kek
        else
            ion init --data-dir "$DATA_DIR"
        fi
    fi
fi

# If the first argument is a flag, prepend the default subcommand.
case "${1:-}" in
    ""|-*)
        set -- dashboard "$@"
        ;;
esac

# The dashboard subcommand honors ION_DATA_DIR/ION_WEB_LISTEN, but pass the data
# dir explicitly so the value is unambiguous in `ps` output.
if [ "${1:-}" = "dashboard" ]; then
    shift
    if [ -n "${ION_WEB_ORIGIN:-}" ]; then
        set -- --origin "$ION_WEB_ORIGIN" "$@"
    fi
    exec ion dashboard --data-dir "$DATA_DIR" --listen "${ION_WEB_LISTEN:-127.0.0.1:4174}" "$@"
fi

exec ion "$@"
