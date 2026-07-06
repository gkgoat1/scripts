#!/usr/bin/env bats

load helpers

setup() {
  setup_workspace
  setup_path
  mock_git
  mock_sudo
  mock_pkexec
}

@test "git.sh delegates to git" {
  run sh "${GIT_DIR}/git.sh" status
  [ "$status" -eq 0 ]
  grep -q "git status" "${GIT_LOG}"
}

@test "pullall.sh runs git pull in each subdir" {
  mock_go_for_forfiles
  mock_git_repo "${WORKSPACE}/repo-a"
  mock_git_repo "${WORKSPACE}/repo-b"
  cd "${WORKSPACE}"
  run sh "${GIT_DIR}/pullall.sh"
  [ "$status" -eq 0 ]
  grep -q "pull" "${GIT_LOG}"
}

@test "pushall.sh runs git push in each subdir" {
  mock_go_for_forfiles
  mock_git_repo "${WORKSPACE}/repo-a"
  cd "${WORKSPACE}"
  run sh "${GIT_DIR}/pushall.sh"
  [ "$status" -eq 0 ]
  grep -q "push" "${GIT_LOG}"
}

@test "commitandpushall.sh commits and pushes" {
  mock_go_for_forfiles
  mock_git_repo "${WORKSPACE}/repo-a"
  cd "${WORKSPACE}"
  run sh "${GIT_DIR}/commitandpushall.sh"
  [ "$status" -eq 0 ]
  grep -q "commit" "${GIT_LOG}"
  grep -q "push" "${GIT_LOG}"
}
