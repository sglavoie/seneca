#!/usr/bin/env bash
#
# Archive Seneca for the App Store, signed by your team, and export it for
# App Store Connect. With --upload, the export goes straight to App Store
# Connect, where it becomes a TestFlight build once processed; without it, the
# .ipa is left in the output directory to inspect or upload by hand. The
# archive's path is the only output on stdout.
#
#   SENECA_IOS_DEVELOPMENT_TEAM=TEAMID ios/scripts/testflight.sh [--upload]
#
# Uploading uses the Apple ID signed in to Xcode, which must belong to the
# team and have access to the app's record in App Store Connect
# (io.sglavoie.seneca.mobile).
#
# Environment:
#   SENECA_IOS_DEVELOPMENT_TEAM  signing team (default: the previous device build's team)
#   SENECA_IOS_BUILD_NUMBER      build number (default: the time, which only grows)
#   SENECA_IOS_CACHE             cache root (default ios/.cache, gitignored)

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ios_dir=$(dirname "$script_dir")
# shellcheck source=ios/scripts/lib.sh
. "$script_dir/lib.sh"

fail() {
	echo "testflight: $*" >&2
	if [ -n "${log:-}" ] && [ -s "$log" ]; then
		tail -40 "$log" >&2
		echo "testflight: full log: $log" >&2
	fi
	exit 1
}

upload=false
if [ "${1:-}" = --upload ]; then
	upload=true
	shift
fi
[ "$#" -eq 0 ] || fail "usage: $0 [--upload]"
[ "$(uname -s)" = Darwin ] || fail "needs macOS with Xcode"
for tool in xcrun xcodebuild codesign plutil; do
	command -v "$tool" >/dev/null 2>&1 || fail "required tool not found: $tool"
done

cache_root="${SENECA_IOS_CACHE:-$ios_dir/.cache}"
team=$(signing_team "$cache_root/DeviceDerivedData/Build/Products/Debug-iphoneos/$app_name.app") ||
	fail "no signing team; run just ios-install TEAMID once, or set SENECA_IOS_DEVELOPMENT_TEAM"
build=${SENECA_IOS_BUILD_NUMBER:-$(date -u +%Y%m%d%H%M)}

mkdir -p "$cache_root/results"
output=$(mktemp -d "$cache_root/results/testflight.XXXXXX")
log="$output/testflight.log"
archive="$output/$app_name.xcarchive"

echo "testflight: archiving build $build with team $team (log: $log)" >&2
"$script_dir/generate-project.sh" >>"$log" 2>&1 || fail "project generation failed"
xcodebuild archive -project "$ios_dir/SenecaMobile.xcodeproj" -scheme SenecaMobile \
	-configuration Release -destination "generic/platform=iOS" \
	-archivePath "$archive" -allowProvisioningUpdates \
	"DEVELOPMENT_TEAM=$team" "CURRENT_PROJECT_VERSION=$build" >>"$log" 2>&1 || fail "the archive failed"

app="$archive/Products/Applications/$app_name.app"
[ -f "$app/PrivacyInfo.xcprivacy" ] || fail "the archive carries no privacy manifest"
# The debug-only screens never reach a build others install.
if strings "$app/$app_name" | grep -q "evaluation-link"; then
	fail "the archive carries the debug-only Model Evaluation screen"
fi

destination=export
"$upload" && destination=upload
plutil -create xml1 "$output/ExportOptions.plist"
plutil -insert method -string app-store-connect "$output/ExportOptions.plist"
plutil -insert destination -string "$destination" "$output/ExportOptions.plist"
plutil -insert teamID -string "$team" "$output/ExportOptions.plist"
plutil -insert signingStyle -string automatic "$output/ExportOptions.plist"
plutil -insert manageAppVersionAndBuildNumber -bool NO "$output/ExportOptions.plist"

echo "testflight: exporting for App Store Connect ($destination)" >&2
# The export copies the app with rsync and accepts only the one macOS ships;
# a newer rsync earlier on PATH (Homebrew's) fails it with "Copy failed".
PATH="/usr/bin:/bin:/usr/sbin:/sbin:$PATH" xcodebuild -exportArchive -archivePath "$archive" -exportPath "$output/export" \
	-exportOptionsPlist "$output/ExportOptions.plist" -allowProvisioningUpdates >>"$log" 2>&1 ||
	fail "the export failed"

if "$upload"; then
	echo "testflight: build $build uploaded; it appears in TestFlight once App Store Connect has processed it (log: $log)" >&2
else
	echo "testflight: $output/export/$app_name.ipa is ready; rerun with --upload to send it (log: $log)" >&2
fi
echo "$archive"
