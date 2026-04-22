#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
OUT_DIR="${OUT_DIR:-${REPO_ROOT}/release/hf}"
TARGET_OS="${TARGET_OS:-linux}"
TARGET_ARCH="${TARGET_ARCH:-amd64}"
GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
GOSUMDB="${GOSUMDB:-sum.golang.google.cn}"

info() {
  printf '[INFO] %s\n' "$1"
}

fail() {
  printf '[ERROR] %s\n' "$1" >&2
  exit 1
}

command -v pnpm >/dev/null 2>&1 || fail "pnpm is required to build the frontend"
command -v go >/dev/null 2>&1 || fail "go is required to build the backend"
command -v tar >/dev/null 2>&1 || fail "tar is required to create the package"

HEAD_SHA="$(git -C "${REPO_ROOT}" rev-parse --short HEAD)"
VERSION_VALUE="${VERSION:-}"
if [[ -z "${VERSION_VALUE}" ]]; then
  VERSION_VALUE="$(tr -d '\r\n' < "${REPO_ROOT}/backend/cmd/server/VERSION")"
fi
DATE_VALUE="${DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"

PKG_NAME="${PKG_NAME:-sub2api-hf-${HEAD_SHA}-${TARGET_OS}-${TARGET_ARCH}.tar.gz}"
PKG_PATH="${OUT_DIR}/${PKG_NAME}"
WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/sub2api-hf-package.XXXXXX")"
trap 'rm -rf "${WORK_DIR:-}"' EXIT

info "building frontend"
(
  cd "${REPO_ROOT}/frontend"
  pnpm run build
)

info "building backend ${TARGET_OS}/${TARGET_ARCH}"
mkdir -p "${WORK_DIR}/app"
(
  cd "${REPO_ROOT}/backend"
  CGO_ENABLED=0 GOOS="${TARGET_OS}" GOARCH="${TARGET_ARCH}" GOPROXY="${GOPROXY}" GOSUMDB="${GOSUMDB}" go build \
    -tags embed \
    -ldflags="-s -w -X main.Version=${VERSION_VALUE} -X main.Commit=${HEAD_SHA} -X main.Date=${DATE_VALUE} -X main.BuildType=release" \
    -trimpath \
    -o "${WORK_DIR}/app/sub2api" \
    ./cmd/server
)

info "collecting runtime files"
cp -R "${REPO_ROOT}/backend/resources" "${WORK_DIR}/app/resources"
cp "${REPO_ROOT}/deploy/docker-entrypoint.sh" "${WORK_DIR}/app/docker-entrypoint.sh"
mkdir -p "${WORK_DIR}/app"
cp "${REPO_ROOT}/deploy/redis/redis-hf.conf" "${WORK_DIR}/app/redis-hf.conf"
chmod +x "${WORK_DIR}/app/sub2api" "${WORK_DIR}/app/docker-entrypoint.sh"

mkdir -p "${OUT_DIR}"
info "creating ${PKG_PATH}"
tar -C "${WORK_DIR}/app" -czf "${PKG_PATH}" .

info "package ready: ${PKG_PATH}"
