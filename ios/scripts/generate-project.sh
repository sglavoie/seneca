#!/usr/bin/env sh
#
# Prepare ios/SenecaMobile.xcodeproj: build the Go bridge XCFramework (nothing
# to do while its fingerprint is current), resolve the pinned XcodeGen, and
# generate the project from ios/project.yml.
#
# Environment:
#   SENECA_IOS_CACHE        cache root (default ios/.cache, gitignored)

set -eu

script_dir=$(CDPATH="" cd -- "$(dirname -- "$0")" && pwd)
ios_dir=$(dirname -- "$script_dir")

"$script_dir/build-bridge.sh"

xcodegen=$("$script_dir/resolve-xcodegen.sh")
"$xcodegen" generate --quiet --spec "$ios_dir/project.yml" --project "$ios_dir"
