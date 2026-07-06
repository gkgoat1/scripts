#!/usr/bin/env bats

load helpers

@test "all git scripts exist" {
  for script in addignores.sh codeall.sh codiumall.sh commitandpushall.sh copilot-commit.sh \
    fetch-repos.sh fetch-repos-gh.sh git.sh ocrall.sh pull.sh pullall.sh pushall.sh \
    sortallignores.sh updateallcargo.sh updateallcargoplus.sh; do
    [ -f "${GIT_DIR}/${script}" ]
  done
}

@test "shellcheck git.sh" {
  script_shellcheck "${GIT_DIR}/git.sh"
}

@test "shellcheck pullall.sh" {
  script_shellcheck "${GIT_DIR}/pullall.sh"
}

@test "shellcheck pushall.sh" {
  script_shellcheck "${GIT_DIR}/pushall.sh"
}

@test "shellcheck commitandpushall.sh" {
  script_shellcheck "${GIT_DIR}/commitandpushall.sh"
}

@test "shellcheck fetch-repos.sh" {
  script_shellcheck "${GIT_DIR}/fetch-repos.sh"
}

@test "shellcheck fetch-repos-gh.sh" {
  script_shellcheck "${GIT_DIR}/fetch-repos-gh.sh"
}

@test "shellcheck updateallcargo.sh" {
  script_shellcheck "${GIT_DIR}/updateallcargo.sh"
}

@test "shellcheck updateallcargoplus.sh" {
  script_shellcheck "${GIT_DIR}/updateallcargoplus.sh"
}

@test "shellcheck addignores.sh" {
  script_shellcheck "${GIT_DIR}/addignores.sh"
}

@test "shellcheck sortallignores.sh" {
  script_shellcheck "${GIT_DIR}/sortallignores.sh"
}

@test "shellcheck codeall.sh" {
  script_shellcheck "${GIT_DIR}/codeall.sh"
}

@test "shellcheck codiumall.sh" {
  script_shellcheck "${GIT_DIR}/codiumall.sh"
}

@test "shellcheck copilot-commit.sh" {
  script_shellcheck "${GIT_DIR}/copilot-commit.sh"
}

@test "shellcheck ocrall.sh" {
  script_shellcheck "${GIT_DIR}/ocrall.sh"
}
