#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

REMOTE_NAME="${REMOTE_NAME:-space}"
TARGET_BRANCH="${TARGET_BRANCH:-main}"
MAX_FILE_BYTES="${MAX_FILE_BYTES:-10485760}" # 10 MiB (HF hard limit for normal git blobs)
DRY_RUN="${DRY_RUN:-0}"

info() {
  printf '[INFO] %s\n' "$1"
}

warn() {
  printf '[WARN] %s\n' "$1" >&2
}

fail() {
  printf '[ERROR] %s\n' "$1" >&2
  exit 1
}

require_clean_tree() {
  if ! git -C "${REPO_ROOT}" diff --quiet --ignore-submodules --; then
    fail "working tree has unstaged changes; commit or stash them before publishing HF snapshot"
  fi
  if ! git -C "${REPO_ROOT}" diff --cached --quiet --ignore-submodules --; then
    fail "index has staged but uncommitted changes; commit them before publishing HF snapshot"
  fi
}

resolve_remote_url() {
  git -C "${REPO_ROOT}" remote get-url "${REMOTE_NAME}" 2>/dev/null || true
}

configure_snapshot_identity() {
  local name email
  name="$(git -C "${REPO_ROOT}" config user.name || true)"
  email="$(git -C "${REPO_ROOT}" config user.email || true)"
  if [[ -z "${name}" ]]; then
    name="Codex HF Publisher"
  fi
  if [[ -z "${email}" ]]; then
    email="codex@example.invalid"
  fi
  git config user.name "${name}"
  git config user.email "${email}"
}

check_large_files() {
  local offenders
  offenders="$(find . -path ./.git -prune -o -type f -size +"$((MAX_FILE_BYTES / 1048576))"M -print)"
  if [[ -n "${offenders}" ]]; then
    printf '%s\n' "${offenders}" >&2
    fail "snapshot contains files larger than ${MAX_FILE_BYTES} bytes; Hugging Face will reject the push"
  fi
}

main() {
  require_clean_tree

  local remote_url head_sha temp_dir commit_msg
  remote_url="$(resolve_remote_url)"
  [[ -n "${remote_url}" ]] || fail "remote '${REMOTE_NAME}' is not configured"

  head_sha="$(git -C "${REPO_ROOT}" rev-parse --short HEAD)"
  temp_dir="$(mktemp -d "${TMPDIR:-/tmp}/hf-publish.XXXXXX")"
  trap 'rm -rf "${temp_dir:-}"' EXIT

  info "publishing snapshot from ${head_sha} to ${REMOTE_NAME}/${TARGET_BRANCH}"
  info "temporary snapshot repo: ${temp_dir}"

  git -C "${REPO_ROOT}" archive --format=tar HEAD | tar -xf - -C "${temp_dir}"

  (
    cd "${temp_dir}"
    git init -b "${TARGET_BRANCH}" >/dev/null
    configure_snapshot_identity
    check_large_files
    git add -A
    commit_msg="HF deployment snapshot from ${head_sha}"
    git commit -m "${commit_msg}" >/dev/null

    if [[ "${DRY_RUN}" == "1" ]]; then
      info "dry run enabled; snapshot commit created locally only"
      git --no-pager log --oneline -1
      return 0
    fi

    git remote add "${REMOTE_NAME}" "${remote_url}"
    git push --force "${REMOTE_NAME}" "HEAD:${TARGET_BRANCH}"
  )

  info "HF snapshot publish complete"
}

main "$@"
