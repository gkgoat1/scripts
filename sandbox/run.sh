#!/usr/bin/env bash
set -euo pipefail
root_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd); build="$root_dir/sandbox/.build"; mkdir -p "$build"
cache_dir="${HOME}/Library/Caches/sandbox"
mkdir -p "$cache_dir"
shim="$cache_dir/sandbox.dylib"
socket="/tmp/sandboxd-${UID}-$$.sock"
if [[ ! -x "$build/sandboxd" || "$root_dir/sandbox/daemon/main.go" -nt "$build/sandboxd" ]]; then (cd "$root_dir" && go build -o "$build/sandboxd" ./sandbox/daemon); fi

if [[ "$(uname -s)" == Darwin ]]; then
  codesign_identity="${SANDBOX_CODESIGN_IDENTITY:-}"
  codesign_keychain="${SANDBOX_CODESIGN_KEYCHAIN:-}"
  daemon_args=(--socket "$socket" --shim "$shim" --codesign-identity "$codesign_identity")
  [[ -n "$codesign_keychain" ]] && daemon_args+=(--codesign-keychain "$codesign_keychain")
  if [[ -n "${SANDBOX_HASH_UPDATERS:-}" ]]; then
    IFS=',' read -ra hash_rules <<< "$SANDBOX_HASH_UPDATERS"
    for rule in "${hash_rules[@]}"; do daemon_args+=(--hash-updater "$rule"); done
  fi
  if [[ -n "${SANDBOX_ENV_ALLOW:-}" ]]; then
    IFS=',' read -ra env_rules <<< "$SANDBOX_ENV_ALLOW"
    for rule in "${env_rules[@]}"; do daemon_args+=(--env-allow "$rule"); done
  fi
  if [[ "${SANDBOX_ALLOW_GET_TASK_ALLOW:-0}" == 1 ]]; then daemon_args+=(--allow-get-task-allow); fi
else
  daemon_args=(--socket "$socket")
fi

"$build/sandboxd" "${daemon_args[@]}" >/dev/null 2>&1 &
daemon_pid=$!
cleanup() {
  "$root_dir/sandbox/daemon/client.py" --socket "$socket" killall >/dev/null 2>&1 || true
  kill "$daemon_pid" 2>/dev/null || true
  rm -f -- "$socket"
}
trap cleanup EXIT
ready=0
for _ in {1..30}; do
  if "$root_dir/sandbox/daemon/client.py" --socket "$socket" ping >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep .05
done
if [[ "$ready" != 1 ]]; then
  echo "sandbox: daemon did not start" >&2
  exit 1
fi

export SANDBOX_DAEMON_SOCKET="$socket"
if [[ "$(uname -s)" == Darwin ]]; then
  for injected in DYLD_INSERT_LIBRARIES DYLD_LIBRARY_PATH DYLD_FRAMEWORK_PATH DYLD_FALLBACK_LIBRARY_PATH DYLD_SHARED_REGION DYLD_FORCE_FLAT_NAMESPACE DYLD_VERSIONED_LIBRARY_PATH DYLD_VERSIONED_FRAMEWORK_PATH; do
    if [[ -n "${!injected:-}" ]]; then
      echo "sandbox: refusing pre-existing $injected" >&2
      exit 1
    fi
  done
  unset DYLD_INSERT_LIBRARIES DYLD_LIBRARY_PATH DYLD_FRAMEWORK_PATH DYLD_FALLBACK_LIBRARY_PATH DYLD_SHARED_REGION DYLD_FORCE_FLAT_NAMESPACE DYLD_VERSIONED_LIBRARY_PATH DYLD_VERSIONED_FRAMEWORK_PATH
fi
if [[ -n "${SANDBOX_ENV_ALLOW:-}" ]]; then export SANDBOX_ENV_POLICY=1; fi
if [[ -n "${SANDBOX_HASH_UPDATERS:-}" ]]; then export SANDBOX_HASH_POLICY=1; fi

child_status=0
case "$(uname -s)" in
 Linux) "$root_dir/sandbox/linux/sandbox.sh" "$@" || child_status=$?;;
 Darwin) "$root_dir/sandbox/macos/sandbox_wrapper.sh" "$@" || child_status=$?;;
 *) echo "unsupported OS" >&2; exit 1;;
esac
exit "$child_status"