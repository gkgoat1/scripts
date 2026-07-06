#!/bin/sh
set -eu
ROOT="$(cd "$(dirname "$0")" && pwd)"
DEST="${1:-${HOME}/.local/bin/interposers}"
mkdir -p "$(dirname "$DEST")" "$DEST"

echo "Building interpose..."
go build -o "${DEST}/interpose" "${ROOT}/interpose"

for cmd in git find grep; do
  ln -sf interpose "${DEST}/${cmd}"
done

echo ""
echo "Installed interposers to ${DEST}"
echo "Add to your shell rc:"
echo "  export PATH=\"${DEST}:\$PATH\""
echo ""
echo "Ensure real binaries appear later on PATH (e.g. /usr/bin)."
