#!/bin/zsh
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"
SERVER_DIR="$PROJECT_DIR/Server"
cd "$SERVER_DIR"

PID_FILE="$SERVER_DIR/.smalltalkserver.pid"
PORTS=(18790 18791 18792)

kill_pid() {
  local pid="$1"
  if [[ -n "${pid:-}" ]] && kill -0 "$pid" 2>/dev/null; then
    echo "停止 PID: $pid"
    kill "$pid" 2>/dev/null || true
    for _ in {1..20}; do
      if ! kill -0 "$pid" 2>/dev/null; then
        break
      fi
      sleep 0.2
    done
    if kill -0 "$pid" 2>/dev/null; then
      echo "強制停止 PID: $pid"
      kill -9 "$pid" 2>/dev/null || true
    fi
  fi
}

if [[ -f "$PID_FILE" ]]; then
  pid_from_file="$(cat "$PID_FILE" 2>/dev/null || true)"
  kill_pid "$pid_from_file"
  rm -f "$PID_FILE"
fi

for port in "${PORTS[@]}"; do
  pids="$(lsof -ti tcp:"$port" 2>/dev/null | sort -u || true)"
  if [[ -n "$pids" ]]; then
    echo "偵測到 port $port 被占用: $pids"
    while IFS= read -r pid; do
      [[ -n "$pid" ]] && kill_pid "$pid"
    done <<< "$pids"
  fi
done

echo "==> SmallTalk Server 已停止"
