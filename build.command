#!/bin/zsh

set -euo pipefail

SCRIPT_DIR="${0:A:h}"
PROJECT_ROOT="$SCRIPT_DIR"
SERVER_DIR="$PROJECT_ROOT/Server"
DIST_ROOT="$PROJECT_ROOT/dist"
ICON_PATH="$PROJECT_ROOT/assets/app-icon.icns"
RELEASE_VERSION="${SMALLTALK_VERSION:-}"

if [[ -z "$RELEASE_VERSION" ]]; then
	RELEASE_VERSION="$(TZ=Asia/Taipei date '+1.%y.%m%d build %H%M')"
fi

version_core="$(echo "$RELEASE_VERSION" | awk '{print $1}')"
version_build="$(echo "$RELEASE_VERSION" | awk '{print $3}')"
if [[ -z "$version_build" ]]; then
	version_build="$(TZ=Asia/Taipei date '+%H%M')"
fi

release_dir_name="${version_core}-build-${version_build}"
RELEASE_DIRECTORY="$DIST_ROOT/$release_dir_name"

print "開始建置 SmallTalk 發行檔：$RELEASE_VERSION ($release_dir_name)"

# 確保 dist 目錄結構
mkdir -p "$RELEASE_DIRECTORY/macos-arm64"
mkdir -p "$RELEASE_DIRECTORY/linux-arm64"
mkdir -p "$RELEASE_DIRECTORY/linux-x64"
mkdir -p "$RELEASE_DIRECTORY/windows-x64"

# 更新 website/talk.html 版本字串
VERSION_STR="ver. ${version_core} build ${version_build}"
print "更新網頁版本文字：$VERSION_STR"
python3 -c "
import re
path = '${SERVER_DIR}/website/talk.html'
with open(path, 'r', encoding='utf-8') as f:
    content = f.read()
updated = re.sub(r'<div class=\"versionText\"[^>]*>.*?</div>', '<div class=\"versionText\" id=\"versionText\" aria-label=\"目前版本\">' + '${VERSION_STR}' + '</div>', content)
with open(path, 'w', encoding='utf-8') as f:
    f.write(updated)
"

LDFLAGS="-s -w -X main.AppVersion=${version_core} -X main.AppBuild=${version_build}"

cd "$SERVER_DIR"
go mod tidy

# 1. 建置 macOS arm64 執行檔
print "  [1/4] 建置 macOS arm64"
GOOS=darwin GOARCH=arm64 go build -buildvcs=false -ldflags "$LDFLAGS" -o "$RELEASE_DIRECTORY/macos-arm64/SmallTalkServer" ./src
chmod +x "$RELEASE_DIRECTORY/macos-arm64/SmallTalkServer"
# 同步更新 Server/SmallTalkServer 便於 run.command 開發使用
cp -f "$RELEASE_DIRECTORY/macos-arm64/SmallTalkServer" "$SERVER_DIR/SmallTalkServer"

# 2. 建置 Linux arm64 執行檔
print "  [2/4] 建置 Linux arm64"
GOOS=linux GOARCH=arm64 go build -buildvcs=false -ldflags "$LDFLAGS" -o "$RELEASE_DIRECTORY/linux-arm64/SmallTalkServer_linux_arm64" ./src
chmod +x "$RELEASE_DIRECTORY/linux-arm64/SmallTalkServer_linux_arm64"
cp -f "$RELEASE_DIRECTORY/linux-arm64/SmallTalkServer_linux_arm64" "$SERVER_DIR/SmallTalkServer_linux_arm64"

# 3. 建置 Linux x64 執行檔
print "  [3/4] 建置 Linux x64"
GOOS=linux GOARCH=amd64 go build -buildvcs=false -ldflags "$LDFLAGS" -o "$RELEASE_DIRECTORY/linux-x64/SmallTalkServer_linux_x64" ./src
chmod +x "$RELEASE_DIRECTORY/linux-x64/SmallTalkServer_linux_x64"

# 4. 建置 Windows x64 執行檔
print "  [4/4] 建置 Windows x64"
GOOS=windows GOARCH=amd64 go build -buildvcs=false -ldflags "$LDFLAGS" -o "$RELEASE_DIRECTORY/windows-x64/SmallTalkServer_windows_x64.exe" ./src

# 5. 封裝 macOS SmallTalk.app
print "建立 macOS App Bundle (SmallTalk.app)..."
APP_DIR="$RELEASE_DIRECTORY/macos-arm64/SmallTalk.app"
CONTENTS_DIR="$APP_DIR/Contents"
MACOS_DIR="$CONTENTS_DIR/MacOS"
RESOURCES_DIR="$CONTENTS_DIR/Resources"

rm -rf "$APP_DIR"
mkdir -p "$MACOS_DIR"
mkdir -p "$RESOURCES_DIR"

# 複製執行檔與資源
cp -f "$RELEASE_DIRECTORY/macos-arm64/SmallTalkServer" "$MACOS_DIR/SmallTalkServer"
cp -f "$SERVER_DIR/agent.properties" "$RESOURCES_DIR/agent.properties"
cp -R "$SERVER_DIR/website" "$RESOURCES_DIR/website"

if [[ -f "$ICON_PATH" ]]; then
	cp -f "$ICON_PATH" "$RESOURCES_DIR/AppIcon.icns"
fi

# 建立啟動 Launcher Script
cat << 'LAUNCHER' > "$MACOS_DIR/SmallTalk"
#!/bin/bash
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)"
CONTENTS_DIR="$(cd "$DIR/.." && pwd)"
RESOURCES_DIR="$CONTENTS_DIR/Resources"

DATA_DIR="$HOME/Library/Application Support/SmallTalk"
mkdir -p "$DATA_DIR/data" "$DATA_DIR/boards" "$DATA_DIR/logs"

if [[ ! -f "$DATA_DIR/agent.properties" ]]; then
	cp "$RESOURCES_DIR/agent.properties" "$DATA_DIR/agent.properties"
fi

# 確保網頁資源為最新版
rm -rf "$DATA_DIR/website"
cp -R "$RESOURCES_DIR/website" "$DATA_DIR/website"

cd "$DATA_DIR"

# 背景啟動伺服器
"$DIR/SmallTalkServer" >> "$DATA_DIR/logs/server.log" 2>&1 &
SERVER_PID=$!

# 等候伺服器監聽後自動開啟預設瀏覽器
for i in {1..30}; do
	if /usr/bin/curl -s http://127.0.0.1:18790/ >/dev/null 2>&1; then
		break
	fi
	sleep 0.2
done

/usr/bin/open "http://127.0.0.1:18790"

wait "$SERVER_PID"
LAUNCHER

chmod 755 "$MACOS_DIR/SmallTalk"
chmod 755 "$MACOS_DIR/SmallTalkServer"

# 建立 Info.plist
cat << PLIST > "$CONTENTS_DIR/Info.plist"
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "https://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key>
  <string>zh-Hant</string>
  <key>CFBundleDisplayName</key>
  <string>SmallTalk</string>
  <key>CFBundleExecutable</key>
  <string>SmallTalk</string>
  <key>CFBundleIconFile</key>
  <string>AppIcon.icns</string>
  <key>CFBundleIdentifier</key>
  <string>com.mars-cloud.smalltalk</string>
  <key>CFBundleName</key>
  <string>SmallTalk</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
  <key>CFBundleShortVersionString</key>
  <string>${version_core}</string>
  <key>CFBundleVersion</key>
  <string>${version_build}</string>
  <key>LSMinimumSystemVersion</key>
  <string>12.0</string>
  <key>NSHighResolutionCapable</key>
  <true/>
</dict>
</plist>
PLIST

echo -n "APPLSMTK" > "$CONTENTS_DIR/PkgInfo"

# 套用簽章
CODESIGN_IDENTITY="${SMALLTALK_CODESIGN_IDENTITY:-}"
if [[ -n "$CODESIGN_IDENTITY" && "$CODESIGN_IDENTITY" != "-" ]] && command -v codesign >/dev/null 2>&1; then
	print "套用 Developer ID 簽章與 Hardened Runtime：$CODESIGN_IDENTITY"
	codesign --force --options runtime --timestamp --sign "$CODESIGN_IDENTITY" "$MACOS_DIR/SmallTalkServer"
	codesign --force --deep --options runtime --timestamp --sign "$CODESIGN_IDENTITY" "$APP_DIR"
	print "已完成 macOS App 簽章"
elif command -v codesign >/dev/null 2>&1; then
	codesign --force --deep --sign - "$APP_DIR" >/dev/null 2>&1 || true
	print "已完成 macOS App 簽章 (ad-hoc)"
fi

# 產生 SHA256SUMS
checksum_file() {
	local f="$1"
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$f" | awk '{print $1}'
	else
		sha256sum "$f" | awk '{print $1}'
	fi
}

for pdir in "$RELEASE_DIRECTORY"/macos-* "$RELEASE_DIRECTORY"/linux-* "$RELEASE_DIRECTORY"/windows-*; do
	[[ -d "$pdir" ]] || continue
	(
		cd "$pdir"
		rm -f SHA256SUMS
		find . -maxdepth 1 -type f ! -name SHA256SUMS | sort | while read -r item; do
			rel="${item#./}"
			cs="$(checksum_file "$rel")"
			echo "$cs  $rel" >> SHA256SUMS
		done
		chmod 644 SHA256SUMS
	)
done

print "建置完成：$RELEASE_DIRECTORY"
