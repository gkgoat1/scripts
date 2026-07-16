#!/usr/bin/env bash
set -euo pipefail
root_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cache_dir="${HOME}/Library/Caches/sandbox"
mkdir -p "$cache_dir"
[[ $# -gt 0 ]] || { echo 'usage: sandbox/run.sh command [args...]' >&2; exit 2; }
shim="$cache_dir/sandbox.dylib"
codesign_identity="${SANDBOX_CODESIGN_IDENTITY:-}"
codesign_keychain="${SANDBOX_CODESIGN_KEYCHAIN:-}"
if [[ -z "$codesign_identity" ]]; then
  echo 'sandbox: set SANDBOX_CODESIGN_IDENTITY or use SANDBOX_CODESIGN_IDENTITY=- for isolated ad-hoc mode' >&2
  exit 1
fi
common_sources=("$root_dir/common/path.c" "$root_dir/common/message.c" "$root_dir/common/socket.c")
dylib_sources=("$root_dir/macos/dylib/init.c" "$root_dir/macos/dylib/fs.c" "$root_dir/macos/dylib/exec.c" "$root_dir/macos/dylib/env.c" "$root_dir/macos/dylib/net.c")
rebuild=0
if [[ ! -f "$shim" ]]; then
  rebuild=1
else
  for f in "${common_sources[@]}" "${dylib_sources[@]}"; do
    if [[ "$f" -nt "$shim" ]]; then rebuild=1; break; fi
  done
fi
if [[ "$rebuild" == 1 ]]; then
  cc -dynamiclib -Wall -Wextra -O2 -o "$shim" "${common_sources[@]}" "${dylib_sources[@]}"
fi
if command -v codesign >/dev/null 2>&1; then
  sign_args=(--force --sign "$codesign_identity" --timestamp=none --options runtime)
if [[ "$codesign_identity" == "-" ]]; then
  echo 'sandbox: ad-hoc mode enables only the exact shim cdhash constraint and requires disable-library-validation' >&2
fi
  [[ -n "$codesign_keychain" ]] && sign_args+=(--keychain "$codesign_keychain")
  codesign "${sign_args[@]}" "$shim"
else
  echo 'sandbox: codesign is required on macOS' >&2
  exit 1
fi
# The daemon owns Mach-O parsing, cache invalidation, rewriting, and signing.
target=$(command -v -- "$1" || realpath -- "$1"); shift
for injected in DYLD_INSERT_LIBRARIES DYLD_LIBRARY_PATH DYLD_FRAMEWORK_PATH DYLD_FALLBACK_LIBRARY_PATH DYLD_SHARED_REGION DYLD_FORCE_FLAT_NAMESPACE DYLD_VERSIONED_LIBRARY_PATH DYLD_VERSIONED_FRAMEWORK_PATH; do
  if [[ -n "${!injected:-}" ]]; then
    echo "sandbox: refusing pre-existing $injected" >&2
    exit 1
  fi
done
unset DYLD_INSERT_LIBRARIES DYLD_LIBRARY_PATH DYLD_FRAMEWORK_PATH DYLD_FALLBACK_LIBRARY_PATH DYLD_SHARED_REGION DYLD_FORCE_FLAT_NAMESPACE DYLD_VERSIONED_LIBRARY_PATH DYLD_VERSIONED_FRAMEWORK_PATH
socket="${SANDBOX_DAEMON_SOCKET:?SANDBOX_DAEMON_SOCKET is not set}"
response=$(python3 - "$socket" "$target" <<'PY'
import socket, sys
s=socket.socket(socket.AF_UNIX, socket.SOCK_STREAM); s.connect(sys.argv[1]); s.sendall(("REWRITE " + sys.argv[2] + "\n").encode()); print(s.makefile().readline(), end="")
PY
)
[[ "$response" == OK\ * ]] || { echo "sandbox: daemon rewrite failed: $response" >&2; exit 1; }
cached=${response#OK }
exec "$cached" "$@"