#!/usr/bin/env bash
#
# Run the app's UI tests on a simulator: the built app driven from outside
# over a journal the tests stage, stopped and relaunched, at the largest text
# size, by its accessibility labels, and from the keyboard.
#
#   ios/scripts/uitest.sh OUTPUT_DIR [REQUIRED_TEST...]
#
# OUTPUT_DIR gets SenecaMobileUITests.xcresult, xcodebuild-uitest.log,
# summary.json and tests.json. The run holds only when the result bundle says
# every test executed and passed: none failed, none skipped, at least one
# ran, and each REQUIRED_TEST ("Class/testName()") is among them.
#
# Environment:
#   SENECA_IOS_SIMULATOR    simulator UDID (default: see simulator.sh)
#   SENECA_IOS_CACHE        cache root (default ios/.cache, gitignored)
#   SENECA_UITEST_ONLY      space-separated "Class" or "Class/testName" to run
#                           instead of every UI test; the REQUIRED_TESTs must
#                           still all be among them

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ios_dir=$(dirname "$script_dir")

fail() {
	echo "uitest: $*" >&2
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

only=()
for selected in ${SENECA_UITEST_ONLY:-}; do
	only+=("-only-testing:SenecaMobileUITests/$selected")
done

rm -rf "$out/SenecaMobileUITests.xcresult" "$out/summary.json" "$out/tests.json"
echo "uitest: running the UI tests on simulator $udid..." >&2
xcodebuild test -project "$ios_dir/SenecaMobile.xcodeproj" -scheme SenecaMobileUITests \
	-destination "platform=iOS Simulator,id=$udid" -derivedDataPath "$cache_root/UITestDerivedData" \
	-parallel-testing-enabled NO ${only[@]+"${only[@]}"} \
	-resultBundlePath "$out/SenecaMobileUITests.xcresult" CODE_SIGNING_ALLOWED=NO \
	>"$out/xcodebuild-uitest.log" 2>&1 || {
	grep -E 'error:' "$out/xcodebuild-uitest.log" | head -40 >&2 || true
	tail -20 "$out/xcodebuild-uitest.log" >&2
	fail "xcodebuild test failed; full log at $out/xcodebuild-uitest.log"
}
xcrun xcresulttool get test-results summary --path "$out/SenecaMobileUITests.xcresult" --format json >"$out/summary.json" ||
	fail "the result bundle could not be read"
xcrun xcresulttool get test-results tests --path "$out/SenecaMobileUITests.xcresult" --format json >"$out/tests.json" ||
	fail "the result bundle lists no tests"
python3 -I "$script_dir/evidence.py" suite "$out/summary.json" "$out/tests.json" "$@" || fail "the UI tests did not hold; see $out"
