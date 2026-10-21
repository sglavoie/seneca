#!/usr/bin/env bash
#
# Build and run the iPhone app on a simulator from the command line.
#
#   ios/scripts/app.sh build   generate the project and build Seneca.app for
#                              the simulator; prints the app's path
#   ios/scripts/app.sh run     build, boot the simulator, install, and launch.
#                              Installing over an earlier build keeps the
#                              simulator's journal.
#   ios/scripts/app.sh stop    terminate the app
#   ios/scripts/app.sh build-device
#                              build Seneca.app for a physical iPhone, signed
#                              by your team, without installing it; prints the
#                              app's path. install-device.sh builds and installs.
#
# Environment:
#   SENECA_IOS_SIMULATOR          simulator UDID (default: see simulator.sh)
#   SENECA_IOS_DEVELOPMENT_TEAM   signing team for build-device (default: the
#                                 previous device build's team)
#   SENECA_IOS_CACHE              cache root (default ios/.cache, gitignored)

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ios_dir=$(dirname "$script_dir")
# shellcheck source=ios/scripts/lib.sh
. "$script_dir/lib.sh"

cache_root="${SENECA_IOS_CACHE:-$ios_dir/.cache}"
derived="$cache_root/AppDerivedData"
app="$derived/Build/Products/Debug-iphonesimulator/$app_name.app"
results="$cache_root/results/build"

fail() {
	echo "app: $*" >&2
	exit 1
}

build() {
	for tool in xcrun xcodebuild; do
		command -v "$tool" >/dev/null 2>&1 || fail "required tool not found: $tool"
	done
	mkdir -p "$results"
	"$script_dir/generate-project.sh"
	echo "app: building $app_name.app for the simulator..." >&2
	xcodebuild build -project "$ios_dir/SenecaMobile.xcodeproj" -scheme SenecaMobile \
		-configuration Debug -destination "generic/platform=iOS Simulator" \
		-derivedDataPath "$derived" CODE_SIGNING_ALLOWED=NO >"$results/xcodebuild.log" 2>&1 || {
		grep -E 'error:' "$results/xcodebuild.log" >&2 || tail -40 "$results/xcodebuild.log" >&2
		fail "xcodebuild build failed; log at $results/xcodebuild.log"
	}
	[ -d "$app" ] || fail "the build left no app at $app"
	echo "$app"
}

build_device() {
	for tool in xcrun xcodebuild codesign lipo; do
		command -v "$tool" >/dev/null 2>&1 || fail "required tool not found: $tool"
	done
	local device_derived="$cache_root/DeviceDerivedData"
	local device_app="$device_derived/Build/Products/Debug-iphoneos/$app_name.app"
	local team
	team=$(signing_team "$device_app") || fail "no signing team; set SENECA_IOS_DEVELOPMENT_TEAM"
	mkdir -p "$results"
	"$script_dir/generate-project.sh"
	echo "app: building $app_name.app for an iPhone, signed by team $team..." >&2
	xcodebuild build -project "$ios_dir/SenecaMobile.xcodeproj" -scheme SenecaMobile \
		-configuration Debug -destination "generic/platform=iOS" \
		-derivedDataPath "$device_derived" -allowProvisioningUpdates \
		"DEVELOPMENT_TEAM=$team" >"$results/xcodebuild-device.log" 2>&1 || {
		grep -E 'error:' "$results/xcodebuild-device.log" >&2 || tail -40 "$results/xcodebuild-device.log" >&2
		fail "the signed device build failed; log at $results/xcodebuild-device.log"
	}
	[ -d "$device_app" ] || fail "the build left no app at $device_app"
	codesign --verify --deep --strict "$device_app" || fail "$device_app is not validly signed"
	signed_by=$(codesign -d --verbose=4 "$device_app" 2>&1 | awk -F= '/^TeamIdentifier=/ { print $2 }')
	[ "$signed_by" = "$team" ] || fail "$device_app is signed by ${signed_by:-nobody}, not by team $team"
	case " $(lipo -archs "$device_app/$app_name") " in
	*" arm64 "*) ;;
	*) fail "$device_app/$app_name has no arm64 slice" ;;
	esac
	echo "$device_app"
}

command=${1:-}
case "$command" in
build)
	build
	;;
build-device)
	build_device
	;;
run)
	build >/dev/null
	udid=$("$script_dir/simulator.sh")
	xcrun simctl boot "$udid" >/dev/null 2>&1 || true
	xcrun simctl bootstatus "$udid" -b >/dev/null
	xcrun simctl install "$udid" "$app"
	xcrun simctl launch --terminate-running-process "$udid" "$bundle_id" >/dev/null
	echo "app: $app_name is running on simulator $udid" >&2
	;;
stop)
	xcrun simctl terminate "$("$script_dir/simulator.sh")" "$bundle_id"
	;;
*)
	fail "usage: $0 build|run|stop|build-device"
	;;
esac
