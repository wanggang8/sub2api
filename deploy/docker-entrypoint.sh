#!/bin/sh
set -e

start_embedded_redis() {
    if [ "${EMBEDDED_REDIS_ENABLED:-false}" != "true" ]; then
        return 0
    fi

    redis_config="${EMBEDDED_REDIS_CONFIG:-/app/redis-hf.conf}"
    redis_port="${REDIS_PORT:-6379}"

    if [ ! -f "${redis_config}" ]; then
        echo "Embedded Redis config not found: ${redis_config}" >&2
        exit 1
    fi

    set -- redis-server "${redis_config}" --port "${redis_port}"
    if [ -n "${REDIS_PASSWORD:-}" ]; then
        set -- "$@" --requirepass "${REDIS_PASSWORD}"
    fi

    "$@" &
    redis_pid=$!

    i=0
    while [ "$i" -lt 15 ]; do
        if ! kill -0 "${redis_pid}" 2>/dev/null; then
            echo "Embedded Redis exited before becoming ready" >&2
            exit 1
        fi

        if REDISCLI_AUTH="${REDIS_PASSWORD:-}" redis-cli -h 127.0.0.1 -p "${redis_port}" ping >/dev/null 2>&1; then
            return 0
        fi

        i=$((i + 1))
        sleep 1
    done

    echo "Embedded Redis did not become ready within 15 seconds" >&2
    exit 1
}

resolve_data_dir() {
    if [ -n "${DATA_DIR:-}" ]; then
        echo "${DATA_DIR}"
        return 0
    fi

    if { [ -n "${SPACE_ID:-}" ] || [ -n "${SPACE_HOST:-}" ]; } && [ -d /data ]; then
        echo "/data"
        return 0
    fi

    echo "/app/data"
}

# Fix data directory permissions when running as root.
# Docker named volumes / host bind-mounts may be owned by root,
# preventing the non-root sub2api user from writing files.
if [ "$(id -u)" = "0" ]; then
    data_dir="$(resolve_data_dir)"
    export DATA_DIR="${data_dir}"
    mkdir -p "${data_dir}" /app/data
    # Use || true to avoid failure on read-only mounted files (e.g. config.yaml:ro)
    chown -R sub2api:sub2api "${data_dir}" /app/data 2>/dev/null || true
    # Re-invoke this script as sub2api so the flag-detection below
    # also runs under the correct user.
    exec su-exec sub2api "$0" "$@"
fi

export DATA_DIR="$(resolve_data_dir)"

# Compatibility: if the first arg looks like a flag (e.g. --help),
# prepend the default binary so it behaves the same as the old
# ENTRYPOINT ["/app/sub2api"] style.
if [ "${1#-}" != "$1" ]; then
    set -- /app/sub2api "$@"
fi

start_embedded_redis

exec "$@"
