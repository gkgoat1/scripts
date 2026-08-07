#!/bin/sh
set -eu
# Installs cleanartifacts as a background LaunchAgent that runs the
# -targets-only daemon on an interval. Reuses installer/launchagent.sh for the
# plist mechanics, mirroring install-pulse.sh.
ROOT="$(CDPATH= cd "$(dirname "$0")" && pwd)"
. "${ROOT}/installer/launchagent.sh"

DEST="${HOME}/.local/bin/cleanartifacts"
INTERVAL="${CLEANARTIFACTS_INTERVAL:-30m}"
ROOT_ARG="${CLEANARTIFACTS_ROOT:-$HOME}"

UNINSTALL=0
for arg in "$@"; do
  case "$arg" in
    --uninstall) UNINSTALL=1 ;;
  esac
done

if [ "$UNINSTALL" -eq 1 ]; then
  launchagent_remove cleanartifacts
  rm -f "$DEST"
  echo "Unloaded and removed the cleanartifacts LaunchAgent"
  echo "Removed ${DEST}"
  exit 0
fi

mkdir -p "$(dirname "$DEST")"

echo "Building cleanartifacts..."
go build -o "$DEST" "${ROOT}/cleanartifacts"

launchagent_install cleanartifacts "$DEST" -daemon -daemon-interval "$INTERVAL" "$ROOT_ARG"

echo ""
echo "Installed and loaded the cleanartifacts LaunchAgent (label: com.gkgoat.scripts.cleanartifacts)"
echo "Binary:   ${DEST}"
echo "Mode:     -daemon -targets-only (Pi-owned node_modules preserved)"
echo "Interval: ${INTERVAL}"
echo "Root:     ${ROOT_ARG}"
echo "Logs:     ${HOME}/Library/Logs/com.gkgoat.scripts.cleanartifacts/{stdout,stderr}.log"
echo ""
echo "Set CLEANARTIFACTS_INTERVAL (default 30m) or CLEANARTIFACTS_ROOT (default \$HOME)"
echo "and re-run this script to change them."
echo "Run '$0 --uninstall' to unload and remove the LaunchAgent and binary."