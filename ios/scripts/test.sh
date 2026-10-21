#!/usr/bin/env bash
#
# Run the app's hosted suite on a simulator, against the real gomobile-bound
# engine: the typed bridge wrappers, the serialized engine service, and the
# round trip. The redeployment tests are roundtrip.sh's, the sync evidence
# phases are sync-evidence.sh's, and the notification evidence is
# notify-evidence.sh's; all three are left out.
#
#   ios/scripts/test.sh OUTPUT_DIR [REQUIRED_TEST...]
#
# OUTPUT_DIR gets SenecaMobile.xcresult, xcodebuild-test.log, summary.json and
# tests.json. The run holds only when the result bundle says every test
# executed and passed: none failed, none skipped, at least one ran, and each
# REQUIRED_TEST ("Class/testName()") is among them.
#
# Environment:
#   SENECA_IOS_SIMULATOR    simulator UDID (default: see simulator.sh)
#   SENECA_IOS_CACHE        cache root (default ios/.cache, gitignored)

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ios_dir=$(dirname "$script_dir")

fail() {
	echo "test: $*" >&2
	exit 1
}

[ "$#" -ge 1 ] || fail "usage: $0 OUTPUT_DIR [REQUIRED_TEST...]"
for tool in xcrun xcodebuild python3; do
	command -v "$tool" >/dev/null 2>&1 || fail "required tool not found: $tool"
done
mkdir -p "$1"
out=$(cd "$1" && pwd)
shift
cache_root="${SENECA_IOS_CACHE:-$ios_dir/.cache}"

"$script_dir/generate-project.sh"
udid=$("$script_dir/simulator.sh")
xcrun simctl boot "$udid" >/dev/null 2>&1 || true
xcrun simctl bootstatus "$udid" -b >/dev/null || fail "simulator $udid did not boot"

rm -rf "$out/SenecaMobile.xcresult" "$out/summary.json" "$out/tests.json"
echo "test: running the hosted suite on simulator $udid..." >&2
xcodebuild test -project "$ios_dir/SenecaMobile.xcodeproj" -scheme SenecaMobile \
	-destination "platform=iOS Simulator,id=$udid" -derivedDataPath "$cache_root/SimDerivedData" \
	-skip-testing:SenecaMobileTests/RedeploymentTests \
	-skip-testing:SenecaMobileTests/SyncEvidenceTests \
	-skip-testing:SenecaMobileTests/NotificationEvidenceTests \
	-resultBundlePath "$out/SenecaMobile.xcresult" CODE_SIGNING_ALLOWED=NO \
	>"$out/xcodebuild-test.log" 2>&1 || {
	grep -E 'error:' "$out/xcodebuild-test.log" | head -40 >&2 || true
	tail -20 "$out/xcodebuild-test.log" >&2
	fail "xcodebuild test failed; full log at $out/xcodebuild-test.log"
}
xcrun xcresulttool get test-results summary --path "$out/SenecaMobile.xcresult" --format json >"$out/summary.json" ||
	fail "the result bundle could not be read"
xcrun xcresulttool get test-results tests --path "$out/SenecaMobile.xcresult" --format json >"$out/tests.json" ||
	fail "the result bundle lists no tests"
python3 -I "$script_dir/evidence.py" suite "$out/summary.json" "$out/tests.json" "$@" || fail "the suite did not hold; see $out"
