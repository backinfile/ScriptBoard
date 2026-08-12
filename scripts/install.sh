#!/bin/sh
set -eu

release_root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
scriptboard="$release_root/scriptboard"

if [ ! -f "$scriptboard" ]; then
    echo "This installer must remain beside scriptboard in a complete release package." >&2
    exit 1
fi
if [ "$(id -u)" -ne 0 ]; then
    echo "Run install.sh as root (for example: sudo sh ./install.sh)." >&2
    exit 1
fi

exec "$scriptboard" service install --start "$@"
