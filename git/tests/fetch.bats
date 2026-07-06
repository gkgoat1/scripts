#!/usr/bin/env bats

load helpers

setup() {
  setup_workspace
  setup_path
  mock_git
}

@test "pull.sh clones when directory missing" {
  cd "${WORKSPACE}"
  run sh "${GIT_DIR}/pull.sh" testorg demo
  [ "$status" -eq 0 ]
}

@test "pull.sh pulls when directory exists" {
  mkdir -p "${WORKSPACE}/demo/.git"
  cd "${WORKSPACE}"
  run sh "${GIT_DIR}/pull.sh" testorg demo
  [ "$status" -eq 0 ]
}

@test "fetch-repos.sh uses curl and pull.sh" {
  mock_go_for_forfiles
  mock_curl
  cd "${WORKSPACE}"
  run sh "${GIT_DIR}/fetch-repos.sh" testorg
  [ "$status" -eq 0 ]
}

@test "fetch-repos-gh.sh uses gh and pull.sh" {
  mock_go_for_forfiles
  mock_gh
  cd "${WORKSPACE}"
  run sh "${GIT_DIR}/fetch-repos-gh.sh" testorg
  [ "$status" -eq 0 ]
}
