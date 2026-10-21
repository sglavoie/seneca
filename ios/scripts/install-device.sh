#!/usr/bin/env bash
#
# Build Seneca for a physical iPhone, signed by your team, and install it over
# the cable or the network. Installing over an earlier build keeps the app's
# data, so the same command is the upgrade. --launch also opens the updated
# app. The built app's path is the only output on stdout.
#
#   SENECA_IOS_DEVELOPMENT_TEAM=TEAMID ios/scripts/install-device.sh [--launch]
#
# Environment:
#   SENECA_IOS_DEVELOPMENT_TEAM  signing team (default: the previous device build's team)
#   SENECA_IOS_DEVICE            iPhone UDID (required when several are paired)
#   SENECA_IOS_CACHE             cache root (default ios/.cache, gitignored)

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ios_dir=$(dirname "$script_dir")
# shellcheck source=ios/scripts/lib.sh
. "$script_dir/lib.sh"

fail() {
	echo "install-device: $*" >&2
	if [ -n "${log:-}" ] && [ -s "$log" ]; then
		tail -40 "$log" >&2
		echo "install-device: full log: $log" >&2
	fi
	exit 1
}

launch=false
if [ "${1:-}" = --launch ]; then
	launch=true
	shift
fi
[ "$#" -eq 0 ] || fail "usage: $0 [--launch]"
[ "$(uname -s)" = Darwin ] || fail "needs macOS with Xcode"
for tool in xcrun xcodebuild codesign python3; do
	command -v "$tool" >/dev/null 2>&1 || fail "required tool not found: $tool"
done

cache_root="${SENECA_IOS_CACHE:-$ios_dir/.cache}"
derived="$cache_root/DeviceDerivedData"
app="$derived/Build/Products/Debug-iphoneos/$app_name.app"
team=$(signing_team "$app") || fail "no previous signing team; run just ios-install TEAMID once, or set SENECA_IOS_DEVELOPMENT_TEAM"

mkdir -p "$cache_root/results"
output=$(mktemp -d "$cache_root/results/device-install.XXXXXX")
log="$output/install.log"
device=$(select_iphone "$output") || fail "could not select an iPhone"

echo "install-device: building for $device with team $team (log: $log)" >&2
"$script_dir/generate-project.sh" >>"$log" 2>&1 || fail "project generation failed"
# The build number counts builds, so the installed app says which one it is.
xcodebuild build -project "$ios_dir/SenecaMobile.xcodeproj" -scheme SenecaMobile \
	-configuration Debug -destination "id=$device" -destination-timeout 60 \
	-derivedDataPath "$derived" -allowProvisioningUpdates \
	"DEVELOPMENT_TEAM=$team" "CURRENT_PROJECT_VERSION=$(date +%s)" >>"$log" 2>&1 || fail "the signed build failed"
[ -d "$app" ] || fail "no app at $app"
codesign --verify --deep --strict "$app" >>"$log" 2>&1 || fail "$app is not validly signed"

echo "install-device: installing (keep the iPhone unlocked)" >&2
xcrun devicectl device install app --device "$device" --timeout 120 \
	--json-output "$output/install.json" "$app" >>"$log" 2>&1 ||
	fail "installation failed; connect and unlock the iPhone, then rerun"
if "$launch"; then
	echo "install-device: launching $app_name" >&2
	xcrun devicectl device process launch --device "$device" --timeout 60 \
		--terminate-existing --json-output "$output/launch.json" "$bundle_id" >>"$log" 2>&1 ||
		fail "the update is installed, but launch failed; unlock the iPhone and open $app_name"
fi
echo "install-device: installed; the app's data was kept (log: $log)" >&2
echo "$app"
