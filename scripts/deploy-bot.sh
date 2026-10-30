#!/usr/bin/env bash
set -euo pipefail

# Run in /srv/giraffe, including when supplied to bash over SSH stdin.
compose=(docker compose --env-file .env --env-file deploy.env)
mode=${1:-deploy}
if [[ $# -gt 1 || ( "$mode" != deploy && "$mode" != --check-redis ) ]]; then
    echo "Usage: deploy-bot.sh [--check-redis]" >&2
    exit 2
fi

redis_snapshot() {
    local ids snapshot id image running state started health rest
    if ! ids=$("${compose[@]}" ps --all --quiet redis); then
        echo "Cannot inspect Redis in the existing Compose project; deployment refused." >&2
        return 1
    fi
    if [[ -z "$ids" || "$ids" == *$'\n'* ]]; then
        echo "Expected exactly one existing Redis container; bootstrap/repair must be done separately." >&2
        return 1
    fi
    if ! snapshot=$(docker inspect --type container --format \
        '{{.Id}}|{{.Image}}|{{.State.Running}}|{{.State.Status}}|{{.State.StartedAt}}|{{if .State.Health}}{{.State.Health.Status}}{{else}}missing{{end}}|{{.RestartCount}}|{{json .HostConfig.RestartPolicy}}|{{json .NetworkSettings.Networks}}{{range .Mounts}}{{printf "\n"}}mount:{{json .}}{{end}}' "$ids"); then
        echo "Cannot inspect the existing Redis container; deployment refused." >&2
        return 1
    fi
    IFS='|' read -r id image running state started health rest <<< "$snapshot"
    if [[ -z "$id" || -z "$image" || -z "$started" || "$running" != true || "$state" != running || "$health" != healthy ]]; then
        echo "Redis must already be running and healthy; deployment refused (state=$state, health=$health)." >&2
        return 1
    fi
    # Docker may return the same mounts in a different order between inspections.
    printf '%s\n' "$snapshot" | LC_ALL=C sort
}

redis_before=$(redis_snapshot)
if [[ "$mode" == --check-redis ]]; then
    echo "Redis preflight passed."
    exit 0
fi

check_redis_unchanged() {
    local current
    current=$(redis_snapshot) || return 1
    if [[ "$current" != "$redis_before" ]]; then
        echo "Redis changed during deployment (identity, image, runtime, mounts, networks or restart policy); investigate before retrying." >&2
        return 1
    fi
}

finish() {
    local result=$?
    trap - EXIT
    # Check even if pull/up failed; never try to repair Redis automatically.
    if ! check_redis_unchanged; then
        exit 1
    fi
    if [[ "$result" == 0 ]]; then
        echo "Bot deployment completed; Redis is healthy and unchanged."
    fi
    exit "$result"
}
trap finish EXIT

"${compose[@]}" pull bot
check_redis_unchanged
"${compose[@]}" up -d --no-deps --no-build --pull never bot
