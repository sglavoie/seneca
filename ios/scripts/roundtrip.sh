#!/usr/bin/env bash
#
# Run the round trip across a redeployment, on a simulator or on a physical
# iPhone: install the app, save a synthetic reflection through the Go engine
# inside it, install a build with another build number over it, and reopen the
# journal from the second build. The journal is a copy of
# mobile/testdata/roundtrip in a directory of the run's own inside the app's
# container; the installation's real journal is never opened.
#
#   ios/scripts/roundtrip.sh simulator OUTPUT_DIR
#   ios/scripts/roundtrip.sh device OUTPUT_DIR
#
# OUTPUT_DIR/roundtrip.json is the record: what each phase reported from
# inside the app, the builds that ran, and the result bundles' counts. It is
# written only when every check below holds:
#
#   - each phase ran exactly one test, which passed, with none skipped;
#   - both phases report the journal the desktop engine writes for the same
#     scenario (mobile/testdata/roundtrip/expected.json);
#   - the phases ran from two builds, each reporting the build number it was
#     built with and the executables this Mac built for it;
#   - the target was the kind asked for: a simulator, or a physical iPhone.
#
# Environment:
#   SENECA_IOS_SIMULATOR          simulator UDID (default: see simulator.sh)
#   SENECA_IOS_DEVELOPMENT_TEAM   signing team, for device (default: the team
#                                 of the previous device build)
#   SENECA_IOS_DEVICE             iPhone UDID (required when several are paired)
#   SENECA_IOS_CACHE              cache root (default ios/.cache, gitignored)

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ios_dir=$(dirname "$script_dir")
repo_root=$(dirname "$ios_dir")
# shellcheck source=ios/scripts/lib.sh
. "$script_dir/lib.sh"

fail() {
	echo "roundtrip: $*" >&2
	exit 1
}

note() {
	echo "roundtrip: $*" >&2
}

[ "$#" -eq 2 ] || fail "usage: $0 simulator|device OUTPUT_DIR"
target=$1
case "$target" in simulator | device) ;; *) fail "the target is simulator or device, not $target" ;; esac
[ "$(uname -s)" = Darwin ] || fail "needs macOS with Xcode"
for tool in xcrun xcodebuild python3 shasum; do
	command -v "$tool" >/dev/null 2>&1 || fail "required tool not found: $tool"
done

mkdir -p "$2"
out=$(cd "$2" && pwd)
rm -rf "$out"/*.xcresult "$out"/*.json "$out"/*.log "$out/container"
cache_root="${SENECA_IOS_CACHE:-$ios_dir/.cache}"
run="$(date -u +%Y%m%dT%H%M%SZ)-$$"
seed_build=$(date +%s)
reopen_build=$((seed_build + 1))
tests=SenecaMobileTests/RedeploymentTests

"$script_dir/generate-project.sh" >"$out/generate.log" 2>&1 || {
	tail -20 "$out/generate.log" >&2
	fail "project generation failed: $out/generate.log"
}

if [ "$target" = simulator ]; then
	udid=$("$script_dir/simulator.sh")
	destination="platform=iOS Simulator,id=$udid"
	derived="$cache_root/RoundTripSimDerivedData"
	products="$derived/Build/Products/Debug-iphonesimulator"
	signing=(CODE_SIGNING_ALLOWED=NO)
	xcrun simctl boot "$udid" >/dev/null 2>&1 || true
	xcrun simctl bootstatus "$udid" -b >/dev/null || fail "simulator $udid did not boot"
	xcrun simctl list devices --json >"$out/simulators.json"
	python3 -I "$script_dir/evidence.py" simulator-info "$out/simulators.json" "$udid" >"$out/target.json" ||
		fail "simulator $udid is not an available iPhone simulator"
else
	udid=$(select_iphone "$out") || fail "could not select an iPhone"
	derived="$cache_root/DeviceDerivedData"
	products="$derived/Build/Products/Debug-iphoneos"
	team=$(signing_team "$products/$app_name.app") ||
		fail "no signing team; set SENECA_IOS_DEVELOPMENT_TEAM, or run just ios-install TEAMID once"
	destination="id=$udid"
	signing=(-allowProvisioningUpdates "DEVELOPMENT_TEAM=$team")
	python3 -I "$script_dir/evidence.py" device-info "$out/devices.json" "$udid" "$team" >"$out/target.json" ||
		fail "device $udid is not a physical iPhone"
	note "keep the iPhone unlocked until this finishes"
fi
app="$products/$app_name.app"

# build PHASE BUILD_NUMBER builds the app and its tests with that build number
# and records the digests of what was built.
build() {
	local phase=$1 number=$2
	note "building build $number for the $phase phase"
	xcodebuild build-for-testing -project "$ios_dir/SenecaMobile.xcodeproj" -scheme SenecaMobile \
		-configuration Debug -destination "$destination" -destination-timeout 60 \
		-derivedDataPath "$derived" "CURRENT_PROJECT_VERSION=$number" "${signing[@]}" \
		>"$out/build-$phase.log" 2>&1 || {
		grep -E 'error:' "$out/build-$phase.log" >&2 || tail -40 "$out/build-$phase.log" >&2
		fail "the $phase build failed: $out/build-$phase.log"
	}
	[ -d "$app" ] || fail "the build left no app at $app"
	if [ "$target" = device ]; then
		codesign --verify --deep --strict "$app" >>"$out/build-$phase.log" 2>&1 || fail "$app is not validly signed"
	fi
	python3 -I "$script_dir/evidence.py" build-info "$app" "$number" >"$out/build-$phase.json" ||
		fail "could not describe the $phase build"
}

# run_phase PHASE TEST runs exactly one test inside the installed app.
run_phase() {
	local phase=$1 test=$2
	note "running the $phase phase on $udid"
	TEST_RUNNER_SENECA_ROUNDTRIP_RUN="$run" \
		xcodebuild test-without-building -project "$ios_dir/SenecaMobile.xcodeproj" -scheme SenecaMobile \
		-destination "$destination" -destination-timeout 60 -derivedDataPath "$derived" \
		"-only-testing:$tests/$test" -resultBundlePath "$out/$phase.xcresult" \
		>"$out/test-$phase.log" 2>&1 || {
		grep -E 'error:|failed' "$out/test-$phase.log" | head -20 >&2 || true
		tail -20 "$out/test-$phase.log" >&2
		fail "the $phase phase failed: $out/test-$phase.log"
	}
	xcrun xcresulttool get test-results summary --path "$out/$phase.xcresult" --format json >"$out/$phase-summary.json" ||
		fail "the $phase result bundle could not be read"
	xcrun xcresulttool get test-results tests --path "$out/$phase.xcresult" --format json >"$out/$phase-tests.json" ||
		fail "the $phase result bundle lists no tests"
}

build seed "$seed_build"
run_phase seed testSeedSavesAReflectionThroughGo

# The redeployment: another build, installed over the one that holds the data.
build reopen "$reopen_build"
note "installing build $reopen_build over build $seed_build"
if [ "$target" = simulator ]; then
	xcrun simctl install "$udid" "$app" >"$out/install.log" 2>&1 || fail "installing on the simulator failed: $out/install.log"
else
	xcrun devicectl device install app --device "$udid" --timeout 120 \
		--json-output "$out/install.json" "$app" >"$out/install.log" 2>&1 ||
		fail "installing on the iPhone failed; connect and unlock it, then rerun ($out/install.log)"
fi
run_phase reopen testReopenFindsTheReflectionAfterRedeployment

# What each phase wrote from inside the app.
mkdir -p "$out/container"
evidence_dir="Library/Application Support/SenecaEvidence/$run"
if [ "$target" = simulator ]; then
	container=$(xcrun simctl get_app_container "$udid" "$bundle_id" data) || fail "the simulator has no container for $bundle_id"
	cp "$container/$evidence_dir"/evidence-*.json "$out/container/" || fail "the app left no evidence in $container/$evidence_dir"
else
	xcrun devicectl device copy from --device "$udid" --timeout 120 \
		--domain-type appDataContainer --domain-identifier "$bundle_id" \
		--source "$evidence_dir" --destination "$out/container" >"$out/copy.log" 2>&1 ||
		fail "could not copy the evidence off the iPhone ($out/copy.log)"
fi

python3 -I "$script_dir/evidence.py" assemble "$target" "$out" "$run" \
	"$repo_root/mobile/testdata/roundtrip/expected.json" \
	"$("$script_dir/source-fingerprint.sh")" "$("$script_dir/build-bridge.sh" --fingerprint)" ||
	fail "the round trip did not hold; see $out"
note "$target round trip held across redeployment: $out/roundtrip.json"
