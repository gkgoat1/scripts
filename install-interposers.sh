#!/bin/sh
set -eu
ROOT="$(cd "$(dirname "$0")" && pwd)"
. "${ROOT}/installer/rcblock.sh"

RCBLOCK_ID="interposers"

UNINSTALL=0
DEST=""
for arg in "$@"; do
  case "$arg" in
    --uninstall) UNINSTALL=1 ;;
    *) DEST="$arg" ;;
  esac
done
DEST="${DEST:-${HOME}/.local/bin/interposers}"

# Profile files this installer manages. .profile is always targeted (created
# if missing, as the POSIX-portable fallback); .zshrc/.bashrc are only
# touched if the user already has them.
profile_targets() {
  echo "${HOME}/.profile"
  [ -f "${HOME}/.zshrc" ] && echo "${HOME}/.zshrc"
  [ -f "${HOME}/.bashrc" ] && echo "${HOME}/.bashrc"
  return 0
}

if [ "$UNINSTALL" -eq 1 ]; then
  start_marker="$(rcblock_start_marker "$RCBLOCK_ID")"
  profile_targets | while IFS= read -r target; do
    if [ -f "$target" ] && grep -qxF "$start_marker" "$target" 2>/dev/null; then
      rcblock_remove "$target" "$RCBLOCK_ID"
      echo "Removed interposer PATH block from ${target}"
    fi
  done
  rm -rf "$DEST"
  echo "Removed ${DEST}"
  exit 0
fi

mkdir -p "$(dirname "$DEST")" "$DEST"

echo "Building interpose..."
go build -o "${DEST}/interpose" "${ROOT}/interpose"

for cmd in git find grep; do
  ln -sf interpose "${DEST}/${cmd}"
done

echo ""
echo "Installed interposers to ${DEST}"

profile_targets | while IFS= read -r target; do
  rcblock_install "$target" "$RCBLOCK_ID" <<EOF
export PATH="${DEST}:\$PATH"
EOF
  echo "Updated PATH block in ${target}"
done

echo ""
echo "Ensure real binaries appear later on PATH (e.g. /usr/bin)."
echo "Restart your shell (or re-source your rc file) to pick up the PATH change."
echo "Run '$0 --uninstall' to remove the installed interposers and PATH blocks."
