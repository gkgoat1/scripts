#!/usr/bin/env bash
set -euo pipefail
root_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd); build="$root_dir/.build"; mkdir -p "$build"
[[ $# -gt 0 ]] || { echo 'usage: sandbox/run.sh command [args...]' >&2; exit 2; }
shim="$build/sandbox.dylib"
if [[ ! -f "$shim" || "$root_dir/macos/sandbox.dylib.c" -nt "$shim" ]]; then cc -dynamiclib -Wall -Wextra -O2 -o "$shim" "$root_dir/macos/sandbox.dylib.c"; fi
# The daemon owns Mach-O parsing, cache invalidation, rewriting, and signing.
target=$(command -v -- "$1" || realpath -- "$1"); shift
socket="${SANDBOX_DAEMON_SOCKET:?SANDBOX_DAEMON_SOCKET is not set}"
response=$(python3 - "$socket" "$target" <<'PY'
import socket, sys
s=socket.socket(socket.AF_UNIX, socket.SOCK_STREAM); s.connect(sys.argv[1]); s.sendall(("REWRITE " + sys.argv[2] + "\n").encode()); print(s.makefile().readline(), end="")
PY
)
[[ "$response" == OK\ * ]] || { echo "sandbox: daemon rewrite failed: $response" >&2; exit 1; }
cached=${response#OK }
exec "$cached" "$@"