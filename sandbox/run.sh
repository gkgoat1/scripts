#!/usr/bin/env bash
set -euo pipefail
root_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd); build="$root_dir/sandbox/.build"; mkdir -p "$build"
sandbox_home="${HOME}"
if [[ "${1:-}" == "--home" ]]; then
  [[ $# -ge 2 ]] || { echo 'sandbox: --home requires a directory' >&2; exit 2; }
  sandbox_home="$2"; shift 2
fi
mkdir -p "$sandbox_home/tmp"
runtime_dir=$(mktemp -d "$sandbox_home/tmp/sandbox.XXXXXX")
shim="$runtime_dir/sandbox.dylib"
socket="/tmp/sandboxd-${UID}-$$.sock"
if [[ -n "${SANDBOX_HASH_UPDATERS:-}" || -n "${SANDBOX_ENV_ALLOW:-}" ]]; then
  echo 'sandbox: SANDBOX_HASH_UPDATERS and SANDBOX_ENV_ALLOW are obsolete; use committed sandbox config.json' >&2
  exit 2
fi

if [[ "$(uname -s)" == Darwin ]]; then
  codesign_identity="${SANDBOX_CODESIGN_IDENTITY:-}"
  codesign_keychain="${SANDBOX_CODESIGN_KEYCHAIN:-}"
  daemon_args=(--socket "$socket" --home "$sandbox_home" --shim "$shim" --codesign-identity "$codesign_identity")
  [[ -n "$codesign_keychain" ]] && daemon_args+=(--codesign-keychain "$codesign_keychain")
  if [[ "${SANDBOX_ALLOW_GET_TASK_ALLOW:-0}" == 1 ]]; then daemon_args+=(--allow-get-task-allow); fi
else
  daemon_args=(--socket "$socket" --home "$sandbox_home")
fi

"$build/sandboxd" "${daemon_args[@]}" >/dev/null 2>&1 &
daemon_pid=$!
cleanup() {
  "$root_dir/sandbox/daemon/client.py" --socket "$socket" killall >/dev/null 2>&1 || true
  kill "$daemon_pid" 2>/dev/null || true
  rm -rf -- "$runtime_dir"
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
export SANDBOX_SHIM="$shim"
if [[ "$(uname -s)" == Darwin ]]; then
  for injected in DYLD_INSERT_LIBRARIES DYLD_LIBRARY_PATH DYLD_FRAMEWORK_PATH DYLD_FALLBACK_LIBRARY_PATH DYLD_SHARED_REGION DYLD_FORCE_FLAT_NAMESPACE DYLD_VERSIONED_LIBRARY_PATH DYLD_VERSIONED_FRAMEWORK_PATH; do
    if [[ -n "${!injected:-}" ]]; then
      echo "sandbox: refusing pre-existing $injected" >&2
      exit 1
    fi
  done
  unset DYLD_INSERT_LIBRARIES DYLD_LIBRARY_PATH DYLD_FRAMEWORK_PATH DYLD_FALLBACK_LIBRARY_PATH DYLD_SHARED_REGION DYLD_FORCE_FLAT_NAMESPACE DYLD_VERSIONED_LIBRARY_PATH DYLD_VERSIONED_FRAMEWORK_PATH
fi
child_status=0
case "$(uname -s)" in
 Linux) "$root_dir/sandbox/linux/sandbox.sh" "$@" || child_status=$?;;
 Darwin) "$root_dir/sandbox/macos/sandbox_wrapper.sh" "$@" || child_status=$?;;
 *) echo "unsupported OS" >&2; exit 1;;
esac
exit "$child_status"