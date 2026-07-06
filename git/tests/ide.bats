#!/usr/bin/env bats

load helpers

setup() {
  setup_workspace
  setup_path
}

@test "codeall.sh opens each repo in code" {
  mock_go_for_forfiles
  mock_code
  cd "${WORKSPACE}"
  run sh "${GIT_DIR}/codeall.sh"
  [ "$status" -eq 0 ]
  grep -q "code ." "${CODE_LOG}"
}

@test "codiumall.sh opens each repo in codium" {
  mock_codium
  cd "${WORKSPACE}"
  run sh "${GIT_DIR}/codiumall.sh"
  [ "$status" -eq 0 ]
  grep -q "codium ." "${CODIUM_LOG}"
}

@test "copilot-commit.sh invokes copilot" {
  mock_copilot
  cd "${WORKSPACE}"
  run sh "${GIT_DIR}/copilot-commit.sh"
  [ "$status" -eq 0 ]
  grep -q "copilot" "${COPILOT_LOG}"
}

@test "ocrall.sh runs ocrmypdf on files" {
  mock_ocrmypdf
  touch "${WORKSPACE}/scan.pdf"
  cd "${WORKSPACE}"
  run sh "${GIT_DIR}/ocrall.sh"
  [ "$status" -eq 0 ]
  grep -q "ocrmypdf" "${OCR_LOG}"
}
