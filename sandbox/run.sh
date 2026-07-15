#!/usr/bin/env bash
set -euo pipefail
root_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd); build="$root_dir/sandbox/.build"; mkdir -p "$build"
socket="${XDG_RUNTIME_DIR:-/tmp}/sandboxd-${UID}.sock"
if [[ ! -x "$build/sandboxd" || "$root_dir/sandbox/daemon/main.go" -nt "$build/sandboxd" ]]; then (cd "$root_dir" && go build -o "$build/sandboxd" ./sandbox/daemon); fi
if ! "$root_dir/sandbox/daemon/client.py" --socket "$socket" ping >/dev/null 2>&1; then "$build/sandboxd" --socket "$socket" >/dev/null 2>&1 & daemon_pid=$!; trap 'kill "$daemon_pid" 2>/dev/null || true' EXIT; for _ in {1..30}; do "$root_dir/sandbox/daemon/client.py" --socket "$socket" ping >/dev/null 2>&1 && break; sleep .05; done; fi
export SANDBOX_DAEMON_SOCKET="$socket"
case "$(uname -s)" in
 Linux) exec "$root_dir/sandbox/linux/sandbox.sh" "$@";;
 Darwin) exec "$root_dir/sandbox/macos/sandbox_wrapper.sh" "$@";;
 *) echo "unsupported OS" >&2; exit 1;;
esac