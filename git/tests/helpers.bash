# Shared helpers for git/*.sh bats tests.

SCRIPTS_ROOT="$(cd "${BATS_TEST_DIRNAME}/../.." && pwd)"
if echo "${SCRIPTS_ROOT}" | grep -q ' '; then
  SCRIPTS_LINK="$(mktemp -d)/scripts"
  ln -sf "${SCRIPTS_ROOT}" "${SCRIPTS_LINK}"
  SCRIPTS_ROOT="${SCRIPTS_LINK}"
fi
GIT_DIR="${SCRIPTS_ROOT}/git"

setup() {
  WORKSPACE=""
  BIN=""
}

teardown() {
  if [[ -n "${WORKSPACE:-}" && -d "${WORKSPACE}" ]]; then
    rm -rf "${WORKSPACE}"
  fi
}

setup_workspace() {
  WORKSPACE="$(mktemp -d "${BATS_TMPDIR}/workspace.XXXXXX")"
  mkdir -p "${WORKSPACE}/repo-a" "${WORKSPACE}/repo-b"
}

setup_path() {
  BIN="$(mktemp -d "${BATS_TMPDIR}/bin.XXXXXX")"
  export PATH="${BIN}:${PATH}"
}

build_forfiles() {
  go build -o "${BIN}/forfiles" "${SCRIPTS_ROOT}/forfiles/forfiles.go"
}

mock_go() {
  mock_go_for_forfiles
}

mock_go_for_forfiles() {
  build_forfiles
  local real_go
  real_go="$(command -v go | while read -r g; do [ "$g" != "${BIN}/go" ] && echo "$g" && break; done)"
  if [ -z "$real_go" ]; then
    real_go="$(PATH="/usr/local/bin:/usr/bin:/bin:/opt/homebrew/bin" command -v go)"
  fi
  cat >"${BIN}/go" <<EOF
#!/bin/sh
if [ "\$1" = run ]; then
  shift
  while [ \$# -gt 0 ]; do
    case "\$1" in
      *forfiles.go)
        shift
        exec "${BIN}/forfiles" "\$@"
        ;;
    esac
    shift
  done
fi
exec "${real_go}" "\$@"
EOF
  chmod +x "${BIN}/go"
}

mock_git() {
  cat >"${BIN}/git" <<'EOF'
#!/bin/sh
echo "git $*" >> "${GIT_LOG:-/dev/null}"
case "$1" in
  add|commit|push|pull|clone|config|init) exit 0 ;;
  *) exit 0 ;;
esac
EOF
  chmod +x "${BIN}/git"
  export GIT_LOG="${WORKSPACE}/git.log"
  : >"${GIT_LOG}"
}

mock_git_repo() {
  local dir="$1"
  mkdir -p "${dir}/.git"
}

mock_gh() {
  cat >"${BIN}/gh" <<'EOF'
#!/bin/sh
if [ "$1" = repo ] && [ "$2" = list ]; then
  echo '[{"name":"demo"}]'
  exit 0
fi
echo "gh $*" >> "${GH_LOG:-/dev/null}"
exit 0
EOF
  chmod +x "${BIN}/gh"
  export GH_LOG="${WORKSPACE}/gh.log"
  : >"${GH_LOG}"
}

mock_curl() {
  cat >"${BIN}/curl" <<'EOF'
#!/bin/sh
echo '[{"name":"demo","fork":false}]'
exit 0
EOF
  chmod +x "${BIN}/curl"
}

mock_cargo() {
  cat >"${BIN}/cargo" <<'EOF'
#!/bin/sh
echo "cargo $*" >> "${CARGO_LOG:-/dev/null}"
exit 0
EOF
  chmod +x "${BIN}/cargo"
  export CARGO_LOG="${WORKSPACE}/cargo.log"
  : >"${CARGO_LOG}"
}

mock_code() {
  cat >"${BIN}/code" <<'EOF'
#!/bin/sh
echo "code $*" >> "${CODE_LOG:-/dev/null}"
exit 0
EOF
  chmod +x "${BIN}/code"
  export CODE_LOG="${WORKSPACE}/code.log"
  : >"${CODE_LOG}"
}

mock_codium() {
  cat >"${BIN}/codium" <<'EOF'
#!/bin/sh
echo "codium $*" >> "${CODIUM_LOG:-/dev/null}"
exit 0
EOF
  chmod +x "${BIN}/codium"
  export CODIUM_LOG="${WORKSPACE}/codium.log"
  : >"${CODIUM_LOG}"
}

mock_copilot() {
  cat >"${BIN}/copilot" <<'EOF'
#!/bin/sh
echo "copilot $*" >> "${COPILOT_LOG:-/dev/null}"
exit 0
EOF
  chmod +x "${BIN}/copilot"
  export COPILOT_LOG="${WORKSPACE}/copilot.log"
  : >"${COPILOT_LOG}"
}

mock_ocrmypdf() {
  cat >"${BIN}/ocrmypdf" <<'EOF'
#!/bin/sh
echo "ocrmypdf $*" >> "${OCR_LOG:-/dev/null}"
exit 0
EOF
  chmod +x "${BIN}/ocrmypdf"
  export OCR_LOG="${WORKSPACE}/ocr.log"
  : >"${OCR_LOG}"
}

mock_sudo() {
  cat >"${BIN}/sudo" <<'EOF'
#!/bin/sh
"$@"
EOF
  chmod +x "${BIN}/sudo"
}

mock_pkexec() {
  cat >"${BIN}/pkexec" <<'EOF'
#!/bin/sh
"$@"
EOF
  chmod +x "${BIN}/pkexec"
}

script_shellcheck() {
  local script="$1"
  if command -v shellcheck >/dev/null 2>&1; then
    run shellcheck -e SC2086,SC2046,SC2010 "${script}"
    [ "$status" -eq 0 ]
  else
    skip "shellcheck not installed"
  fi
}
