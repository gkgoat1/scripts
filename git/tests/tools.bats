#!/usr/bin/env bats

load helpers

setup() {
  setup_workspace
  setup_path
}

@test "addignores.sh appends ignore entries" {
  mock_go
  touch "${WORKSPACE}/repo-a/.gitignore"
  cd "${WORKSPACE}"
  run sh "${GIT_DIR}/addignores.sh"
  echo "addignores status=$status out=$output" >&2
  [ "$status" -eq 0 ]
  grep -q target "${WORKSPACE}/repo-a/.gitignore"
}

@test "sortallignores.sh sorts gitignore" {
  mock_go
  printf 'b\na\na\n' >"${WORKSPACE}/repo-a/.gitignore"
  cd "${WORKSPACE}"
  run sh "${GIT_DIR}/sortallignores.sh"
  [ "$status" -eq 0 ]
  run cat "${WORKSPACE}/repo-a/.gitignore"
  [ "$output" = $'a\nb' ]
}

@test "updateallcargo.sh runs cargo update" {
  mock_go
  mock_cargo
  mock_git
  mock_git_repo "${WORKSPACE}/repo-a"
  cd "${WORKSPACE}"
  run sh "${GIT_DIR}/updateallcargo.sh"
  [ "$status" -eq 0 ]
  grep -q "cargo update" "${CARGO_LOG}"
}

@test "updateallcargoplus.sh loops cargo and push" {
  mock_go
  mock_cargo
  mock_git
  mock_git_repo "${WORKSPACE}/repo-a"
  cd "${WORKSPACE}"
  run sh "${GIT_DIR}/updateallcargoplus.sh"
  [ "$status" -eq 0 ]
  grep -q "cargo update" "${CARGO_LOG}"
}
