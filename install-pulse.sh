#!/bin/sh
set -eu
ROOT="$(cd "$(dirname "$0")" && pwd)"
. "${ROOT}/installer/launchagent.sh"

DEST="${HOME}/.local/bin/pulse"
CONFIG="${HOME}/.config/pulse/jobs"
TASKS_CONFIG="${HOME}/.config/pulse/tasks"

UNINSTALL=0
for arg in "$@"; do
  case "$arg" in
    --uninstall) UNINSTALL=1 ;;
  esac
done

if [ "$UNINSTALL" -eq 1 ]; then
  launchagent_remove pulse
  rm -f "$DEST"
  echo "Unloaded and removed the pulse LaunchAgent"
  echo "Removed ${DEST}"
  echo "Left ${CONFIG} in place (remove it yourself if you no longer need it)."
  exit 0
fi

mkdir -p "$(dirname "$DEST")" "$(dirname "$CONFIG")"

echo "Building pulse..."
go build -o "$DEST" "${ROOT}/pulse"

if [ ! -f "$CONFIG" ]; then
  cat >"$CONFIG" <<'EOF'
# pulse job config — see docs/pulse.md
#
# job: llmtrim-restart
# interval: 30m
# command: killall -9 llmtrim; llmtrim stop && llmtrim start
# max-load1: 4.0
EOF
  echo "Created a template config at ${CONFIG} with no jobs enabled yet."
  echo "Uncomment/edit at least one job, then re-run '$0' to load the LaunchAgent."
  echo "(Not loading it now — an empty config would make pulse exit immediately and"
  echo " launchd would keep restarting it in a crash loop.)"
  exit 0
fi

if [ ! -f "$TASKS_CONFIG" ]; then
  cat >"$TASKS_CONFIG" <<'EOF'
# Pulse v2 task domains — see docs/pulse-task-domains-plan.md
#
# task: event-drainer
# domain: rapid-service
# command: event-drainer --one-batch
# restart: always
# restart-min-delay: 0s
EOF
  echo "Created a v2 task template at ${TASKS_CONFIG}."
fi

launchagent_install pulse "$DEST" -config "$CONFIG"

echo ""
echo "Installed and loaded the pulse LaunchAgent (label: com.gkgoat.scripts.pulse)"
echo "Binary:  ${DEST}"
echo "Config:  ${CONFIG}"
echo "Logs:    ${HOME}/Library/Logs/com.gkgoat.scripts.pulse/{stdout,stderr}.log"
echo ""
echo "Run '$0 --uninstall' to unload and remove the LaunchAgent and binary."
