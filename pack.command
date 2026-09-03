#!/bin/zsh

set -euo pipefail

SCRIPT_DIR="${0:A:h}"
PROJECT_ROOT="$SCRIPT_DIR"
DIST_ROOT="$PROJECT_ROOT/dist"
BUILD_COMMAND="$PROJECT_ROOT/build.command"
PACK_VERSION="${SMALLTALK_VERSION:-}"
SKIP_BUILD="false"
PACK_STAGE=""

cleanup_stage() {
	if [[ -n "$PACK_STAGE" && -d "$PACK_STAGE" ]]; then
		/bin/rm -rf -- "$PACK_STAGE"
	fi
}
trap cleanup_stage EXIT INT TERM

print_usage() {
	cat <<'USAGE'
用法：./pack.command [--no-build]

預設會先執行 build.command，再產生 macOS DMG。

選項：
  --no-build  使用 dist 中指定版本或最新版本，不重新編譯
  -h, --help  顯示本說明

可用環境變數：
  SMALLTALK_VERSION        指定發行版本，例如 1.26.0903 build 1805
USAGE
}

for argument in "$@"; do
	case "$argument" in
		--no-build)
			SKIP_BUILD="true"
			;;
		-h|--help)
			print_usage
			exit 0
			;;
		*)
			print -u2 "錯誤：不支援的參數：$argument"
			print_usage >&2
			exit 1
			;;
	esac
done

require_command() {
	local name="$1"
	if ! command -v "$name" >/dev/null 2>&1; then
		print -u2 "錯誤：找不到必要工具：$name"
		return 1
	fi
}

validate_project() {
	if [[ ! -f "$PROJECT_ROOT/go.work" && ! -f "$PROJECT_ROOT/Server/go.mod" ]]; then
		print -u2 "錯誤：$PROJECT_ROOT 不是完整的 SmallTalk 專案目錄。"
		return 1
	fi
	if [[ ! -x "$BUILD_COMMAND" ]]; then
		print -u2 "錯誤：找不到可執行的 build.command：$BUILD_COMMAND"
		return 1
	fi
	if [[ -L "$DIST_ROOT" ]]; then
		print -u2 "錯誤：dist 不可為符號連結：$DIST_ROOT"
		return 1
	fi
	if [[ -e "$DIST_ROOT" && ! -d "$DIST_ROOT" ]]; then
		print -u2 "錯誤：dist 路徑存在但不是目錄：$DIST_ROOT"
		return 1
	fi
}

release_directory_name() {
	local version="$1"
	if [[ ! "$version" =~ '^[01]\.[0-9]{2}\.[0-9]{4} build [0-9]{4}$' ]]; then
		print -u2 "錯誤：版本格式必須為 1.YY.MMDD build HHmm：$version"
		return 1
	fi
	print -r -- "${version/ build /-build-}"
}

latest_release_directory() {
	local latest=""
	if [[ -d "$DIST_ROOT" ]]; then
		latest="$(/usr/bin/find "$DIST_ROOT" -mindepth 1 -maxdepth 1 -type d -name '[01].*-build-*' -print | LC_ALL=C /usr/bin/sort | /usr/bin/tail -n 1)"
	fi
	if [[ -z "$latest" ]]; then
		print -u2 "錯誤：dist 中沒有可封裝的發行版本；請先執行 build.command。"
		return 1
	fi
	print -r -- "$latest"
}

checksum_file() {
	local file_path="$1"
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$file_path" | /usr/bin/awk '{print $1}'
		return
	fi
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$file_path" | /usr/bin/awk '{print $1}'
		return
	fi
	print -u2 "錯誤：找不到 shasum 或 sha256sum。"
	return 1
}

write_manifest() {
	local scan_root="$1"
	local manifest="$scan_root/SHA256SUMS"
	local temporary_manifest
	local relative_path
	local checksum

	temporary_manifest="$(mktemp "${TMPDIR:-/tmp}/smalltalk-sha256.XXXXXX")"
	while IFS= read -r relative_path; do
		checksum="$(checksum_file "$scan_root/$relative_path")"
		print -r -- "$checksum  $relative_path" >> "$temporary_manifest"
	done < <(
		cd "$scan_root"
		/usr/bin/find . -type f ! -name SHA256SUMS ! -name PACKAGES-SHA256SUMS -print |
			/usr/bin/sed 's#^\./##' |
			LC_ALL=C /usr/bin/sort
	)
	/bin/mv -f -- "$temporary_manifest" "$manifest"
	/bin/chmod 644 "$manifest"
	print "已更新：$manifest"
}

write_package_manifest() {
	local release_directory="$1"
	shift
	local manifest="$release_directory/PACKAGES-SHA256SUMS"
	local temporary_manifest
	local package_path
	local checksum

	temporary_manifest="$(mktemp "${TMPDIR:-/tmp}/smalltalk-package-sha256.XXXXXX")"
	for package_path in "$@"; do
		checksum="$(checksum_file "$package_path")"
		print -r -- "$checksum  ${package_path:t}" >> "$temporary_manifest"
	done
	/bin/mv -f -- "$temporary_manifest" "$manifest"
	/bin/chmod 644 "$manifest"
	print "已更新：$manifest"
}

build_dmg() {
	local platform_directory="$1"
	local release_name="$2"
	local platform_name="${platform_directory:t}"
	local app_source="$platform_directory/SmallTalk.app"
	local output_name="SmallTalk-$release_name-$platform_name.dmg"
	local output_path="$platform_directory/$output_name"
	local temporary_output="$PACK_STAGE/$output_name"
	local image_root="$PACK_STAGE/$platform_name"

	if [[ ! -d "$app_source/Contents" ]]; then
		print -u2 "錯誤：缺少可封裝的 macOS App：$app_source"
		return 1
	fi

	/bin/rm -rf -- "$image_root"
	/bin/mkdir -p "$image_root"
	/usr/bin/ditto "$app_source" "$image_root/SmallTalk.app"
	/bin/ln -s /Applications "$image_root/Applications"

	print "建立 DMG：$output_path"
	/usr/bin/hdiutil create \
		-volname "SmallTalk" \
		-srcfolder "$image_root" \
		-ov \
		-format UDZO \
		"$temporary_output"
	/bin/mv -f -- "$temporary_output" "$output_path"
}

validate_project
require_command go
require_command mktemp
require_command hdiutil
require_command ditto
if ! command -v shasum >/dev/null 2>&1 && ! command -v sha256sum >/dev/null 2>&1; then
	print -u2 "錯誤：找不到 shasum 或 sha256sum。"
	exit 1
fi

if [[ "$SKIP_BUILD" == "false" ]]; then
	if [[ -z "$PACK_VERSION" ]]; then
		PACK_VERSION="$(TZ=Asia/Taipei date '+1.%y.%m%d build %H%M')"
	fi
	export SMALLTALK_VERSION="$PACK_VERSION"
	print "開始建立 SmallTalk 發行檔：$PACK_VERSION"
	"$BUILD_COMMAND"
	RELEASE_DIRECTORY="$DIST_ROOT/$(release_directory_name "$PACK_VERSION")"
else
	if [[ -n "$PACK_VERSION" ]]; then
		RELEASE_DIRECTORY="$DIST_ROOT/$(release_directory_name "$PACK_VERSION")"
	else
		RELEASE_DIRECTORY="$(latest_release_directory)"
		PACK_VERSION="${RELEASE_DIRECTORY:t}"
	fi
fi

if [[ ! -d "$RELEASE_DIRECTORY" || "$RELEASE_DIRECTORY" != "$DIST_ROOT/"* ]]; then
	print -u2 "錯誤：找不到安全的發行目錄：$RELEASE_DIRECTORY"
	exit 1
fi

RELEASE_NAME="${RELEASE_DIRECTORY:t}"
PACK_STAGE="$(mktemp -d "${TMPDIR:-/tmp}/smalltalk-pack.XXXXXX")"

typeset -a mac_directories
typeset -a dmg_files
mac_directories=("$RELEASE_DIRECTORY"/macos-*(/N))

if (( ${#mac_directories[@]} == 0 )); then
	print -u2 "錯誤：$RELEASE_DIRECTORY 中沒有 macOS 發行目錄。"
	exit 1
fi

for platform_directory in "${mac_directories[@]}"; do
	build_dmg "$platform_directory" "$RELEASE_NAME"
	write_manifest "$platform_directory"
done

dmg_files=("$RELEASE_DIRECTORY"/macos-*/*.dmg(N))
if (( ${#dmg_files[@]} == 0 )); then
	print -u2 "錯誤：封裝結果不完整，未產生 DMG。"
	exit 1
fi

write_package_manifest "$RELEASE_DIRECTORY" "${dmg_files[@]}"
write_manifest "$RELEASE_DIRECTORY"

print ""
print "封裝完成："
for package_path in "${dmg_files[@]}"; do
	print "  $package_path"
done
