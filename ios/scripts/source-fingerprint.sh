#!/usr/bin/env sh
#
# Print the fingerprint of everything the iPhone app and its round trip are
# built from: the bridge fingerprint (the Go sources, the gomobile pin, the
# toolchains), the project spec, the Swift sources of the engine package, the
# vendored capture package, the app, and its tests, the round-trip scenario and synthetic journal, and the
# scripts that build and run them. Device evidence records it, and the
# verifier refuses evidence recorded from other sources.

set -eu

script_dir=$(CDPATH="" cd -- "$(dirname -- "$0")" && pwd)
ios_dir=$(dirname -- "$script_dir")
repo_root=$(dirname -- "$ios_dir")

cd "$repo_root"
{
	echo "bridge $("$script_dir/build-bridge.sh" --fingerprint)"
	{
		echo ios/project.yml
		echo ios/XCODEGEN_VERSION
		echo ios/SenecaEngine/Package.swift
		echo ios/Packages/Capture/Package.swift
		find ios/SenecaEngine/Sources ios/Packages/Capture/Sources ios/SenecaMobile ios/SenecaMobileTests mobile/testdata/roundtrip -type f
		find ios/scripts -type f \( -name '*.sh' -o -name '*.py' \)
	} | grep -v '/\.DS_Store$' | LC_ALL=C sort | while IFS= read -r file; do
		shasum -a 256 "$file"
	done
} | shasum -a 256 | awk '{print $1}'
