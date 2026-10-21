#!/usr/bin/env sh
#
# Print the path of the pinned XcodeGen, downloading it into the cache first
# when neither PATH nor the cache holds that version. The download is checked
# against the digest ios/XCODEGEN_VERSION pins before anything is unpacked.
#
# Environment:
#   SENECA_IOS_CACHE        cache root (default ios/.cache, gitignored)

set -eu

require_tool() {
	if ! command -v "$1" >/dev/null 2>&1; then
		echo "resolve-xcodegen: required tool not found: $1" >&2
		exit 1
	fi
}

script_dir=$(CDPATH="" cd -- "$(dirname -- "$0")" && pwd)
ios_dir=$(dirname -- "$script_dir")

# shellcheck disable=SC1091
. "$ios_dir/XCODEGEN_VERSION"
: "${XCODEGEN_VERSION:?missing in XCODEGEN_VERSION}"
: "${XCODEGEN_SHA256:?missing in XCODEGEN_VERSION}"

cache_root="${SENECA_IOS_CACHE:-$ios_dir/.cache}"
mkdir -p "$cache_root"

matches() {
	[ -n "$1" ] && [ "$("$1" --version 2>/dev/null || true)" = "Version: $XCODEGEN_VERSION" ]
}

# A copy on PATH is used only when it reports the pinned version.
if matches "$(command -v xcodegen 2>/dev/null || true)"; then
	command -v xcodegen
	exit 0
fi
# XcodeGen reads its templates from the share/ directory beside bin/, so the
# release is kept whole.
xcodegen_dir="$cache_root/xcodegen-$XCODEGEN_VERSION"
if matches "$xcodegen_dir/bin/xcodegen"; then
	echo "$xcodegen_dir/bin/xcodegen"
	exit 0
fi

require_tool curl
require_tool shasum
require_tool unzip

archive="$cache_root/xcodegen-$XCODEGEN_VERSION.zip"
echo "resolve-xcodegen: downloading XcodeGen $XCODEGEN_VERSION..." >&2
curl -fL --retry 3 -o "$archive.part" \
	"https://github.com/yonaskolb/XcodeGen/releases/download/$XCODEGEN_VERSION/xcodegen.zip" >&2
actual=$(shasum -a 256 "$archive.part" | awk '{print $1}')
if [ "$actual" != "$XCODEGEN_SHA256" ]; then
	rm -f "$archive.part"
	echo "resolve-xcodegen: checksum mismatch: expected $XCODEGEN_SHA256, got $actual" >&2
	exit 1
fi
unpack="$cache_root/xcodegen-unpack"
rm -rf "$unpack" "$xcodegen_dir"
unzip -q "$archive.part" -d "$unpack"
mv "$unpack/xcodegen" "$xcodegen_dir"
rm -rf "$unpack" "$archive.part"
if ! matches "$xcodegen_dir/bin/xcodegen"; then
	echo "resolve-xcodegen: the unpacked xcodegen does not report $XCODEGEN_VERSION" >&2
	exit 1
fi
echo "$xcodegen_dir/bin/xcodegen"
