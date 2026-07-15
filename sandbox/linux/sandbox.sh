#!/usr/bin/env bash
set -euo pipefail
root_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
build="$root_dir/.build"
probe="$build/linux-probe"
mkdir -p "$build"
if [[ ! -x "$probe" || "$root_dir/linux/probe.c" -nt "$probe" ]]; then
  cc -std=c11 -Wall -Wextra -O2 "$root_dir/linux/probe.c" -o "$probe"
fi
root=$(mktemp -d "${TMPDIR:-/tmp}/sandbox-root.XXXXXX")
cleanup(){ rm -rf -- "$root"; }
trap cleanup EXIT
binds=()
while (($#)); do
 case "$1" in
  --allow) [[ $# -ge 2 ]] || { echo 'missing path for --allow' >&2; exit 2; }; p=$(realpath -- "$2"); binds+=(--bind "$p" "$p" rw); shift 2;;
  --allow-ro) [[ $# -ge 2 ]] || { echo 'missing path for --allow-ro' >&2; exit 2; }; p=$(realpath -- "$2"); binds+=(--bind "$p" "$p" ro); shift 2;;
  --workdir) work=$(realpath -- "$2"); shift 2;;
  --net) net=1; shift;;
  --) shift; break;;
  -*) echo "unknown option: $1" >&2; exit 2;;
  *) break;;
 esac
done
(($#)) || { echo 'no command' >&2; exit 2; }
# The executable and its ELF dependencies are read-only binds. This is deliberately
# explicit rather than exposing the host root through a recursive mount.
cmd=$(command -v -- "$1" || realpath -- "$1")
binds+=(--bind "$cmd" "$cmd" ro)
if command -v ldd >/dev/null 2>&1; then
 while read -r dep; do [[ -e "$dep" ]] && binds+=(--bind "$dep" "$dep" ro); done < <(ldd "$cmd" 2>/dev/null | awk '/=> \/|^\// {for(i=1;i<=NF;i++) if ($i ~ /^\//) {print $i;break}}' | sort -u)
fi
if [[ -n "${work:-}" ]]; then binds+=(--bind "$work" "$work" rw); cd "$work"; fi
# --net is accepted for compatibility; the default C probe creates CLONE_NEWNET.
exec "$probe" --root "$root" "${binds[@]}" -- "$@"