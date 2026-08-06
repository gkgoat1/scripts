#!/bin/sh
set -eu
ROOT="$(CDPATH= cd "$(dirname "$0")" && pwd)"
cd "$ROOT"
exec env PORT=9090 npx vibe-kanban@latest "$@"
