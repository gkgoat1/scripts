#!/usr/bin/env bats

setup() {
  SCRIPTS_ROOT="$(cd "${BATS_TEST_DIRNAME}/../.." && pwd)"
  if echo "${SCRIPTS_ROOT}" | grep -q ' '; then
    SCRIPTS_LINK="$(mktemp -d)/scripts"
    ln -sf "${SCRIPTS_ROOT}" "${SCRIPTS_LINK}"
    SCRIPTS_ROOT="${SCRIPTS_LINK}"
  fi
  # shellcheck source=../launchagent.sh
  . "${SCRIPTS_ROOT}/installer/launchagent.sh"

  WORKSPACE="$(mktemp -d "${BATS_TMPDIR}/launchagent.XXXXXX")"
  HOME="${WORKSPACE}/home"
  mkdir -p "${HOME}"

  BIN="${WORKSPACE}/bin"
  mkdir -p "${BIN}"
  LAUNCHCTL_LOG="${WORKSPACE}/launchctl.log"
  cat >"${BIN}/launchctl" <<EOF
#!/bin/sh
echo "launchctl \$*" >> "${LAUNCHCTL_LOG}"
exit 0
EOF
  chmod +x "${BIN}/launchctl"
  PATH="${BIN}:${PATH}"
}

teardown() {
  [ -n "${WORKSPACE:-}" ] && [ -d "${WORKSPACE}" ] && rm -rf "${WORKSPACE}"
}

@test "install writes a plist with the expected label and program arguments" {
  launchagent_install demo /usr/bin/true -config /tmp/foo

  plist="$(launchagent_plist_path demo)"
  [ -f "${plist}" ]
  grep -qF "<string>com.gkgoat.scripts.demo</string>" "${plist}"
  grep -qF "<string>/usr/bin/true</string>" "${plist}"
  grep -qF "<string>-config</string>" "${plist}"
  grep -qF "<string>/tmp/foo</string>" "${plist}"
}

@test "install calls launchctl unload before load" {
  launchagent_install demo /usr/bin/true

  [ -f "${LAUNCHCTL_LOG}" ]
  first_verb="$(awk '{print $2; exit}' "${LAUNCHCTL_LOG}")"
  [ "${first_verb}" = "unload" ]
  grep -q '^launchctl load ' "${LAUNCHCTL_LOG}"
}

@test "re-install is idempotent and replaces the previous plist content" {
  launchagent_install demo /usr/bin/true -config /tmp/old
  launchagent_install demo /usr/bin/true -config /tmp/new

  plist="$(launchagent_plist_path demo)"
  ! grep -qF "<string>/tmp/old</string>" "${plist}"
  grep -qF "<string>/tmp/new</string>" "${plist}"
  [ "$(grep -c '<key>Label</key>' "${plist}")" -eq 1 ]
}

@test "remove calls launchctl unload and deletes the plist" {
  launchagent_install demo /usr/bin/true
  plist="$(launchagent_plist_path demo)"
  [ -f "${plist}" ]

  launchagent_remove demo

  [ ! -f "${plist}" ]
  grep -q "unload ${plist}" "${LAUNCHCTL_LOG}"
}

@test "remove is a no-op when the plist is absent" {
  run launchagent_remove demo
  [ "$status" -eq 0 ]
  [ ! -f "${LAUNCHCTL_LOG}" ]
}

@test "install creates a log directory for stdout/stderr" {
  launchagent_install demo /usr/bin/true

  logdir="$(launchagent_log_dir demo)"
  [ -d "${logdir}" ]
  plist="$(launchagent_plist_path demo)"
  grep -qF "<string>${logdir}/stdout.log</string>" "${plist}"
  grep -qF "<string>${logdir}/stderr.log</string>" "${plist}"
}
