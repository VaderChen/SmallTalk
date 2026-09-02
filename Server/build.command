#!/bin/sh
set -eu

cd "$(dirname "$0")"

DIST_DIR="./dist"
mkdir -p "$DIST_DIR"

# Compute automatic version from current timestamp (0.YY.MMDD build HHMM)
NOW_VER="0.$(date +%y).$(date +%m%d)"
NOW_BUILD="$(date +%H%M)"
VERSION_STR="ver. ${NOW_VER} build ${NOW_BUILD}"

echo "Building SmallTalkServer (${VERSION_STR})..."

# Update version in website/talk.html
python3 -c "
import re
path = 'website/talk.html'
with open(path, 'r', encoding='utf-8') as f:
    content = f.read()
updated = re.sub(r'<div class=\"versionText\"[^>]*>.*?</div>', '<div class=\"versionText\" id=\"versionText\" aria-label=\"目前版本\">' + '${VERSION_STR}' + '</div>', content)
with open(path, 'w', encoding='utf-8') as f:
    f.write(updated)
"

LDFLAGS="-X main.AppVersion=${NOW_VER} -X main.AppBuild=${NOW_BUILD}"

go mod tidy

echo "  - macOS arm64"
GOOS=darwin GOARCH=arm64 go build -buildvcs=false -ldflags "$LDFLAGS" -o "$DIST_DIR/SmallTalkServer_MacOS" ./src

echo "  - linux arm64"
GOOS=linux GOARCH=arm64 go build -buildvcs=false -ldflags "$LDFLAGS" -o "$DIST_DIR/SmallTalkServer_Linux_Arm64" ./src

echo "  - linux amd64"
GOOS=linux GOARCH=amd64 go build -buildvcs=false -ldflags "$LDFLAGS" -o "$DIST_DIR/SmallTalkServer_Linux_X64" ./src

echo "  - windows amd64"
GOOS=windows GOARCH=amd64 go build -buildvcs=false -ldflags "$LDFLAGS" -o "$DIST_DIR/SmallTalkServer_Windows_X64.exe" ./src

chmod +x "$DIST_DIR/SmallTalkServer_MacOS" "$DIST_DIR/SmallTalkServer_Linux_Arm64" "$DIST_DIR/SmallTalkServer_Linux_X64"

echo ""
echo "Build complete (${VERSION_STR}):"
echo "  $DIST_DIR/SmallTalkServer_MacOS"
echo "  $DIST_DIR/SmallTalkServer_Linux_Arm64"
echo "  $DIST_DIR/SmallTalkServer_Linux_X64"
echo "  $DIST_DIR/SmallTalkServer_Windows_X64.exe"
