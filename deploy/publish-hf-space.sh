#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

REMOTE_NAME="${REMOTE_NAME:-space}"
TARGET_BRANCH="${TARGET_BRANCH:-main}"
MAX_FILE_BYTES="${MAX_FILE_BYTES:-10485760}" # 10 MiB (HF hard limit for normal git blobs)
DRY_RUN="${DRY_RUN:-0}"
HF_PACKAGE_URL="${HF_PACKAGE_URL:-}"

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

remove_hf_disallowed_binaries() {
  local removed=0
  local pattern path
  local patterns=(
    "assets/partners/logos/*.png"
    "assets/partners/logos/*.jpg"
    "assets/partners/logos/*.jpeg"
    "assets/partners/logos/*.gif"
    "assets/partners/logos/*.webp"
    "assets/partners/logos/*.ico"
    "frontend/public/*.png"
    "frontend/public/*.jpg"
    "frontend/public/*.jpeg"
    "frontend/public/*.gif"
    "frontend/public/*.webp"
    "frontend/public/*.ico"
  )

  shopt -s nullglob
  for pattern in "${patterns[@]}"; do
    for path in ${pattern}; do
      if [[ -f "${path}" ]]; then
        rm -f "${path}"
        info "excluded HF-disallowed binary asset: ${path}"
        removed=1
      fi
    done
  done
  shopt -u nullglob

  if [[ "${removed}" == "1" ]]; then
    find assets/partners -type d -empty -delete 2>/dev/null || true
    find frontend/public -type d -empty -delete 2>/dev/null || true
  fi
}

ensure_hf_readme_front_matter() {
  local readme tmpfile
  readme="README.md"
  [[ -f "${readme}" ]] || fail "snapshot is missing README.md"

  if head -n 1 "${readme}" | grep -qx -- '---'; then
    info "README.md already contains HF front matter"
    return 0
  fi

  tmpfile="$(mktemp "${TMPDIR:-/tmp}/hf-readme.XXXXXX")"
  cat >"${tmpfile}" <<'EOF'
---
title: Sub2API
emoji: "🚀"
colorFrom: blue
colorTo: indigo
sdk: docker
app_port: 7860
---

EOF
  cat "${readme}" >>"${tmpfile}"
  mv "${tmpfile}" "${readme}"
  info "injected HF Space metadata into README.md"
}

write_hf_readme() {
  local output_dir
  output_dir="$1"
  cat >"${output_dir}/README.md" <<'EOF'
---
title: Sub2API
emoji: "🚀"
colorFrom: blue
colorTo: indigo
sdk: docker
app_port: 7860
---

# Sub2API

This Hugging Face Space runs a prebuilt Sub2API package.
EOF
}

write_package_dockerfile() {
  local output_dir package_url
  output_dir="$1"
  package_url="$2"
  [[ -n "${package_url}" ]] || fail "HF_PACKAGE_URL is required for package mode"

  cat >"${output_dir}/Dockerfile" <<EOF
FROM alpine:3.21

RUN apk add --no-cache \\
    ca-certificates \\
    tzdata \\
    redis \\
    su-exec \\
    libpq \\
    zstd-libs \\
    lz4-libs \\
    krb5-libs \\
    libldap \\
    libedit \\
    wget \\
    tar \\
    && rm -rf /var/cache/apk/*

WORKDIR /app

RUN wget -O /tmp/sub2api-hf.tar.gz "${package_url}" \\
    && tar -xzf /tmp/sub2api-hf.tar.gz -C /app \\
    && rm /tmp/sub2api-hf.tar.gz \\
    && chmod +x /app/sub2api /app/docker-entrypoint.sh

RUN mkdir -p /app/data /data && chown -R 1000:1000 /app /data

ENV AUTO_SETUP=true \\
    EMBEDDED_REDIS_ENABLED=true \\
    EMBEDDED_REDIS_CONFIG=/app/redis-hf.conf \\
    SERVER_HOST=0.0.0.0 \\
    SERVER_PORT=7860

EXPOSE 7860

HEALTHCHECK --interval=30s --timeout=10s --start-period=10s --retries=3 \\
    CMD wget -q -T 5 -O /dev/null http://localhost:\${SERVER_PORT:-7860}/health || exit 1

ENTRYPOINT ["/app/docker-entrypoint.sh"]
CMD ["/app/sub2api"]
EOF
}

prepare_package_snapshot() {
  local output_dir package_url
  output_dir="$1"
  package_url="$2"
  write_hf_readme "${output_dir}"
  write_package_dockerfile "${output_dir}" "${package_url}"
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

  if [[ -n "${HF_PACKAGE_URL}" ]]; then
    info "package mode enabled; HF will download ${HF_PACKAGE_URL}"
    prepare_package_snapshot "${temp_dir}" "${HF_PACKAGE_URL}"
  else
    git -C "${REPO_ROOT}" archive --format=tar HEAD | tar -xf - -C "${temp_dir}"
  fi

  (
    cd "${temp_dir}"
    git init -b "${TARGET_BRANCH}" >/dev/null
    configure_snapshot_identity
    if [[ -z "${HF_PACKAGE_URL}" ]]; then
      remove_hf_disallowed_binaries
      ensure_hf_readme_front_matter
    fi
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
