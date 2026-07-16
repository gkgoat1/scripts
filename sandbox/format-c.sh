#!/usr/bin/env bash
set -euo pipefail

if ! command -v clang-format >/dev/null 2>&1; then
    echo 'clang-format is required but not installed' >&2
    exit 1
fi

root_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)

dry_run=0
if [[ "${1:-}" == "--dry-run" ]]; then
    dry_run=1
    shift
fi

files=()
while IFS= read -r -d '' f; do
    files+=("$f")
done < <(find "$root_dir/sandbox/common" "$root_dir/sandbox/linux" \
              "$root_dir/sandbox/macos/dylib" \
              -type f \( -name '*.c' -o -name '*.h' \) -print0 2>/dev/null || true)

if ((${#files[@]} == 0)); then
    echo 'No C source files found to format' >&2
    exit 0
fi

if [[ $dry_run -eq 1 ]]; then
    clang-format --dry-run --Werror -- "${files[@]}"
else
    clang-format -i -- "${files[@]}"
fi