#!/bin/sh
set -eu

cd "$(dirname "$0")"

DIST_DIR="./dist"
mkdir -p "$DIST_DIR"

echo "Building SmallTalkServer binaries..."

go mod tidy

echo "  - macOS arm64"
GOOS=darwin GOARCH=arm64 go build -o "$DIST_DIR/SmallTalkServer_MacOS" ./src

echo "  - linux arm64"
GOOS=linux GOARCH=arm64 go build -o "$DIST_DIR/SmallTalkServer_Linux_Arm64" ./src

echo "  - linux amd64"
GOOS=linux GOARCH=amd64 go build -o "$DIST_DIR/SmallTalkServer_Linux_X64" ./src

echo "  - windows amd64"
GOOS=windows GOARCH=amd64 go build -o "$DIST_DIR/SmallTalkServer_Windows_X64.exe" ./src

chmod +x "$DIST_DIR/SmallTalkServer_MacOS" "$DIST_DIR/SmallTalkServer_Linux_Arm64" "$DIST_DIR/SmallTalkServer_Linux_X64"

echo ""
echo "Build complete:"
echo "  $DIST_DIR/SmallTalkServer_MacOS"
echo "  $DIST_DIR/SmallTalkServer_Linux_Arm64"
echo "  $DIST_DIR/SmallTalkServer_Linux_X64"
echo "  $DIST_DIR/SmallTalkServer_Windows_X64.exe"
