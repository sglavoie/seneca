#!/usr/bin/env bash
#
# Run the vendored capture package's own tests (ios/Packages/Capture) on a
# simulator: the capture engine and its recovery, the audio file store, the
# transcriber chain, the simulated devices, and the recorder adapter and
# fragmented writer, which are iOS-only.
#
#   ios/scripts/capture-test.sh OUTPUT_DIR [REQUIRED_TEST...]
#
# OUTPUT_DIR gets Capture.xcresult, xcodebuild-test.log, summary.json and
# tests.json. The run holds only when the result bundle says every test
# executed and passed: none failed, none skipped, at least one ran, and each
# REQUIRED_TEST ("Suite/testName()") is among them.
#
# Environment:
#   SENECA_IOS_SIMULATOR    simulator UDID (default: see simulator.sh)
#   SENECA_IOS_CACHE        cache root (default ios/.cache, gitignored)

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ios_dir=$(dirname "$script_dir")
package_dir="$ios_dir/Packages/Capture"

fail() {
	echo "capture-test: $*" >&2
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
mkdir -p "$cache_root"
cache_root=$(cd "$cache_root" && pwd)

udid=$("$script_dir/simulator.sh")
xcrun simctl boot "$udid" >/dev/null 2>&1 || true
xcrun simctl bootstatus "$udid" -b >/dev/null || fail "simulator $udid did not boot"

rm -rf "$out/Capture.xcresult" "$out/summary.json" "$out/tests.json"
echo "capture-test: running the capture package's tests on simulator $udid..." >&2
(cd "$package_dir" && xcodebuild test -scheme Capture-Package \
	-destination "platform=iOS Simulator,id=$udid" -derivedDataPath "$cache_root/CaptureDerivedData" \
	-resultBundlePath "$out/Capture.xcresult" CODE_SIGNING_ALLOWED=NO) \
	>"$out/xcodebuild-test.log" 2>&1 || {
	grep -E 'error:' "$out/xcodebuild-test.log" | head -40 >&2 || true
	tail -20 "$out/xcodebuild-test.log" >&2
	fail "xcodebuild test failed; full log at $out/xcodebuild-test.log"
}
xcrun xcresulttool get test-results summary --path "$out/Capture.xcresult" --format json >"$out/summary.json" ||
	fail "the result bundle could not be read"
xcrun xcresulttool get test-results tests --path "$out/Capture.xcresult" --format json >"$out/tests.json" ||
	fail "the result bundle lists no tests"
python3 -I "$script_dir/evidence.py" suite "$out/summary.json" "$out/tests.json" "$@" || fail "the suite did not hold; see $out"
