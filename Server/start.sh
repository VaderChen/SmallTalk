#!/bin/bash

set -euo pipefail

SERVICE_DIR="$(cd "$(dirname "$0")" && pwd)"
ENV_FILE="/etc/smalltalk/bbsService.env"

sudo /bin/bash -s -- "$SERVICE_DIR" "$ENV_FILE" <<'ROOT'
set -euo pipefail

service_dir="$1"
env_file="$2"
cd "$service_dir"

if [[ -r "$env_file" ]]; then
	set -a
	# shellcheck disable=SC1090
	. "$env_file"
	set +a
fi

find_server_pids() {
	local proc exe
	for proc in /proc/[0-9]*; do
		exe="$(readlink "$proc/exe" 2>/dev/null || true)"
		case "$exe" in
			"$service_dir/SmallTalkServer_linux_arm64"|"$service_dir/SmallTalkServer_linux_arm64 (deleted)")
				printf '%s\n' "${proc##*/}"
				;;
		esac
	done
}

old_pids="$(find_server_pids)"
if [[ -n "$old_pids" ]]; then
	kill -TERM $old_pids
	for _ in {1..20}; do
		remaining="$(find_server_pids)"
		[[ -z "$remaining" ]] && break
		sleep 0.25
	done
	remaining="$(find_server_pids)"
	if [[ -n "$remaining" ]]; then
		kill -KILL $remaining
	fi
fi

nohup ./SmallTalkServer_linux_arm64 >> ./server-sudo.log 2>&1 &
pid=$!
printf '%s\n' "$pid" > ./server.pid
sleep 2
kill -0 "$pid"
printf 'SmallTalkServer started: pid=%s\n' "$pid"
ROOT
