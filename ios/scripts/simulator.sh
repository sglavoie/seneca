#!/usr/bin/env sh
#
# Print the UDID of the simulator the iOS recipes run on: SENECA_IOS_SIMULATOR
# when set, else the first available iPhone on an iOS 26 or later runtime, the
# app's deployment target.

set -eu

if [ -n "${SENECA_IOS_SIMULATOR:-}" ]; then
	echo "$SENECA_IOS_SIMULATOR"
	exit 0
fi
command -v xcrun >/dev/null 2>&1 || {
	echo "simulator: required tool not found: xcrun" >&2
	exit 1
}
# The first iPhone under an "-- iOS N.M --" heading with N >= 26.
simulator=$(xcrun simctl list devices available | awk '
	/^-- iOS [0-9]+/ { split($3, version, "."); eligible = (version[1] + 0 >= 26); next }
	/^-- / { eligible = 0; next }
	eligible && /iPhone/ && match($0, /\([0-9A-F-]{36}\)/) {
		print substr($0, RSTART + 1, RLENGTH - 2); exit
	}')
if [ -z "$simulator" ]; then
	echo "simulator: no available iPhone simulator on iOS 26 or later; set SENECA_IOS_SIMULATOR" >&2
	exit 1
fi
echo "$simulator"
