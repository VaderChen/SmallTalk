#!/bin/zsh
set -euo pipefail

PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"
SERVER_DIR="$PROJECT_DIR/Server"
cd "$SERVER_DIR"

PID_FILE="$SERVER_DIR/.smalltalkserver.pid"
LOG_DIR="$SERVER_DIR/logs"
LOG_FILE="$LOG_DIR/server.log"


mkdir -p "$LOG_DIR"

echo "==> SmallTalk Server 啟動流程"

echo "==> 檢查既有服務"
"$PROJECT_DIR/stop.command"

echo "==> 建置"
"$SERVER_DIR/build.command"

echo "==> 背景啟動"
nohup "$SERVER_DIR/SmallTalkServer" >> "$LOG_FILE" 2>&1 &
server_pid=$!
echo "$server_pid" > "$PID_FILE"

sleep 2

if kill -0 "$server_pid" 2>/dev/null; then
  echo "==> 啟動成功"
  echo "PID: $server_pid"
  echo "LOG: $LOG_FILE"
  echo "HTTP: http://127.0.0.1:18790"
  echo "MCP:  http://127.0.0.1:18792/mcp"
  else
  echo "==> 啟動失敗，請檢查日誌"
  echo "LOG: $LOG_FILE"
  exit 1
fi
