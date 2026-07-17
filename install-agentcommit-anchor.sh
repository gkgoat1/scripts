#!/bin/sh
set -eu
ROOT="$(cd "$(dirname "$0")" && pwd)"
. "${ROOT}/installer/launchagent.sh"

DEST="${HOME}/.local/bin/agentcommit"
PULSE_CONFIG="${HOME}/.config/pulse/jobs"

UNINSTALL=0
for arg in "$@"; do
  case "$arg" in
    --uninstall) UNINSTALL=1 ;;
  esac
done

if [ "$UNINSTALL" -eq 1 ]; then
  launchagent_remove agentcommit-anchor
  rm -f "$DEST"
  echo "Unloaded and removed the agentcommit-anchor LaunchAgent"
  echo "Removed ${DEST}"
  echo "Left any *.proof sidecar files in place — without the anchor, commitment"
  echo "verification just goes back to 'never adopted' (off) for every registrant."
  exit 0
fi

mkdir -p "$(dirname "$DEST")"

echo "Building agentcommit..."
go build -o "$DEST" "${ROOT}/agentcommit"

echo "Committing the current spawnable-command and policy config..."
root="$("$DEST" commit -pulse-config "$PULSE_CONFIG")"

launchagent_install agentcommit-anchor "$DEST" anchor -root "$root"

echo ""
echo "Installed and loaded the agentcommit-anchor LaunchAgent (label: com.gkgoat.scripts.agentcommit-anchor)"
echo "Binary: ${DEST}"
echo "Root:   ${root}"
echo "Logs:   ${HOME}/Library/Logs/com.gkgoat.scripts.agentcommit-anchor/{stdout,stderr}.log"
echo ""
echo "This write to ~/Library/LaunchAgents should be visible to any persistence"
echo "monitor you already run (e.g. BlockBlock/LuLu) — that's the point: re-running"
echo "this script is how a legitimate commitment update happens, and it happens in"
echo "the open. See docs/agentcommit.md."
echo ""
echo "Run '$0 --uninstall' to unload and remove the LaunchAgent and binary."
