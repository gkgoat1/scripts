#!/usr/bin/env bats

setup() {
  SCRIPTS_ROOT="$(cd "${BATS_TEST_DIRNAME}/../.." && pwd)"
  if echo "${SCRIPTS_ROOT}" | grep -q ' '; then
    SCRIPTS_LINK="$(mktemp -d)/scripts"
    ln -sf "${SCRIPTS_ROOT}" "${SCRIPTS_LINK}"
    SCRIPTS_ROOT="${SCRIPTS_LINK}"
  fi
  # shellcheck source=../rcblock.sh
  . "${SCRIPTS_ROOT}/installer/rcblock.sh"
  WORKSPACE="$(mktemp -d "${BATS_TMPDIR}/rcblock.XXXXXX")"
  TARGET="${WORKSPACE}/profile"
}

teardown() {
  [ -n "${WORKSPACE:-}" ] && [ -d "${WORKSPACE}" ] && rm -rf "${WORKSPACE}"
}

@test "install appends a block to a fresh file" {
  rcblock_install "${TARGET}" demo <<'EOF'
export FOO=bar
EOF
  grep -qxF "# >>> scripts:demo >>>" "${TARGET}"
  grep -qxF "# <<< scripts:demo <<<" "${TARGET}"
  grep -qxF "export FOO=bar" "${TARGET}"
}

@test "install creates the file and parent dir if missing" {
  NESTED="${WORKSPACE}/nested/dir/rc"
  rcblock_install "${NESTED}" demo <<'EOF'
export FOO=bar
EOF
  [ -f "${NESTED}" ]
  grep -qxF "export FOO=bar" "${NESTED}"
}

@test "install preserves unrelated existing content" {
  printf 'existing line one\nexisting line two\n' >"${TARGET}"
  rcblock_install "${TARGET}" demo <<'EOF'
export FOO=bar
EOF
  grep -qxF "existing line one" "${TARGET}"
  grep -qxF "existing line two" "${TARGET}"
  grep -qxF "export FOO=bar" "${TARGET}"
}

@test "re-install is idempotent and updates content in place" {
  rcblock_install "${TARGET}" demo <<'EOF'
export FOO=old
EOF
  rcblock_install "${TARGET}" demo <<'EOF'
export FOO=new
EOF
  [ "$(grep -c 'scripts:demo' "${TARGET}")" -eq 2 ]
  ! grep -qxF "export FOO=old" "${TARGET}"
  grep -qxF "export FOO=new" "${TARGET}"
}

@test "repeated install/remove cycles do not accumulate blank lines" {
  printf 'existing line\n' >"${TARGET}"
  i=0
  while [ "$i" -lt 4 ]; do
    rcblock_install "${TARGET}" demo <<'EOF'
export FOO=bar
EOF
    rcblock_remove "${TARGET}" demo
    i=$((i + 1))
  done
  [ "$(cat "${TARGET}")" = "existing line" ]
}

@test "install manages independent ids in the same file without colliding" {
  rcblock_install "${TARGET}" demo-a <<'EOF'
export A=1
EOF
  rcblock_install "${TARGET}" demo-b <<'EOF'
export B=2
EOF
  grep -qxF "export A=1" "${TARGET}"
  grep -qxF "export B=2" "${TARGET}"

  rcblock_remove "${TARGET}" demo-a
  ! grep -qxF "export A=1" "${TARGET}"
  grep -qxF "export B=2" "${TARGET}"
}

@test "remove strips exactly the managed block" {
  printf 'before\n' >"${TARGET}"
  rcblock_install "${TARGET}" demo <<'EOF'
export FOO=bar
EOF
  printf 'after\n' >>"${TARGET}"

  rcblock_remove "${TARGET}" demo

  grep -qxF "before" "${TARGET}"
  grep -qxF "after" "${TARGET}"
  ! grep -q "scripts:demo" "${TARGET}"
  ! grep -qxF "export FOO=bar" "${TARGET}"
}

@test "remove is a no-op when the block is absent" {
  printf 'untouched\n' >"${TARGET}"
  run rcblock_remove "${TARGET}" demo
  [ "$status" -eq 0 ]
  [ "$(cat "${TARGET}")" = "untouched" ]
}

@test "remove is a no-op when the file is absent" {
  run rcblock_remove "${WORKSPACE}/does-not-exist" demo
  [ "$status" -eq 0 ]
  [ ! -e "${WORKSPACE}/does-not-exist" ]
}

@test "install refuses to touch a file with an unterminated block" {
  printf '# >>> scripts:demo >>>\nstray\n' >"${TARGET}"
  run rcblock_install "${TARGET}" demo <<'EOF'
export FOO=bar
EOF
  [ "$status" -ne 0 ]
  [ "$(cat "${TARGET}")" = "$(printf '# >>> scripts:demo >>>\nstray')" ]
}
