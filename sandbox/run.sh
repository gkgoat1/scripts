#!/usr/bin/env bash
set -euo pipefail
root_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd); build="$root_dir/sandbox/.build"; mkdir -p "$build"
socket="${XDG_RUNTIME_DIR:-/tmp}/sandboxd-${UID}.sock"
if [[ "$(uname -s)" == Darwin && (! -f "$build/sandbox.dylib" || "$root_dir/sandbox/macos/sandbox.dylib.c" -nt "$build/sandbox.dylib") ]]; then
  cc -dynamiclib -Wall -Wextra -O2 -o "$build/sandbox.dylib" "$root_dir/sandbox/macos/sandbox.dylib.c"
fi
if [[ ! -x "$build/sandboxd" || "$root_dir/sandbox/daemon/main.go" -nt "$build/sandboxd" ]]; then (cd "$root_dir" && go build -o "$build/sandboxd" ./sandbox/daemon); fi
if ! "$root_dir/sandbox/daemon/client.py" --socket "$socket" ping >/dev/null 2>&1; then
  if [[ "$(uname -s)" == Darwin ]]; then
    daemon_args=(--socket "$socket" --shim "$build/sandbox.dylib")
    if [[ -n "${SANDBOX_ENV_ALLOW:-}" ]]; then
      IFS=',' read -ra env_rules <<< "$SANDBOX_ENV_ALLOW"
      for rule in "${env_rules[@]}"; do daemon_args+=(--env-allow "$rule"); done
    fi
    if [[ "${SANDBOX_ALLOW_GET_TASK_ALLOW:-0}" == 1 ]]; then daemon_args+=(--allow-get-task-allow); fi
    "$build/sandboxd" "${daemon_args[@]}" >/dev/null 2>&1 &
  else
    "$build/sandboxd" --socket "$socket" >/dev/null 2>&1 &
  fi
  daemon_pid=$!; trap 'kill "$daemon_pid" 2>/dev/null || true' EXIT
  for _ in {1..30}; do "$root_dir/sandbox/daemon/client.py" --socket "$socket" ping >/dev/null 2>&1 && break; sleep .05; done
fi
export SANDBOX_DAEMON_SOCKET="$socket"
if [[ -n "${SANDBOX_ENV_ALLOW:-}" ]]; then export SANDBOX_ENV_POLICY=1; fi
case "$(uname -s)" in
 Linux) exec "$root_dir/sandbox/linux/sandbox.sh" "$@";;
 Darwin) exec "$root_dir/sandbox/macos/sandbox_wrapper.sh" "$@";;
 *) echo "unsupported OS" >&2; exit 1;;
esac