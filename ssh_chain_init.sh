#!/bin/sh
set -eu
ROOT="$(CDPATH= cd "$(dirname "$0")" && pwd)"
cd "$ROOT"
exec ssh -L 8822:192.168.1.111:22 grahamk@grahamk-tower.local "$@"
