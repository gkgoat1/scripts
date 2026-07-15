#!/usr/bin/env bash
set -euo pipefail
root_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd); build="$root_dir/.build"; mkdir -p "$build" "$HOME/Library/Caches/sandbox/binaries"
shim="$build/sandbox.dylib"
if [[ ! -f "$shim" || "$root_dir/macos/sandbox.dylib.c" -nt "$shim" ]]; then cc -dynamiclib -Wall -Wextra -O2 -o "$shim" "$root_dir/macos/sandbox.dylib.c"; codesign --force --sign - --timestamp=none "$shim"; fi
if [[ ! -x "$build/macho-rewriter" || "$root_dir/macos/rewriter.c" -nt "$build/macho-rewriter" ]]; then cc -Wall -Wextra -O2 -o "$build/macho-rewriter" "$root_dir/macos/rewriter.c"; fi
[[ $# -gt 0 ]] || { echo 'usage: sandbox/run.sh command [args...]' >&2; exit 2; }
target=$(command -v -- "$1" || realpath -- "$1"); shift
# The cache key covers both inputs and the rewriter format. Never execute a stale rewrite.
key=$(cat "$target" "$shim" | shasum -a 256 | awk '{print $1}'); cached="$HOME/Library/Caches/sandbox/binaries/$key"
if [[ ! -x "$cached" ]]; then
  tmp="$cached.tmp.$$"
  "$build/macho-rewriter" "$target" "$tmp" "$shim"
  chmod 755 "$tmp"; codesign --force --sign - --timestamp=none "$tmp"; mv "$tmp" "$cached"
fi
exec "$cached" "$@"