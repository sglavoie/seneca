#!/usr/bin/env bash
#
# Prove reminder delivery on the paired iPhone: the app plans two reminders
# for this evening and schedules them with iOS, iOS delivers the first, and a
# reflection saved before the second makes the app cancel it so iOS never
# delivers it. The phone's half is NotificationEvidenceTests, hosted in the
# app, over a synthetic journal of the run's own; the installation's journal
# is never opened.
#
#   ios/scripts/notify-evidence.sh OUTPUT_DIR
#
# The iPhone must be paired and unlocked, and notifications allowed for
# Seneca; on the first run iOS asks, and someone has to tap Allow. A run takes
# about four minutes. The run schedules under a prefix of its own, so the
# installation's reminders are left alone.
#
# OUTPUT_DIR/notify.json is the record, written only when every check in
# ios/scripts/notify_evidence.py holds.
#
# Environment: SENECA_IOS_DEVICE, SENECA_IOS_DEVELOPMENT_TEAM, SENECA_IOS_CACHE
# as ios/scripts/roundtrip.sh.

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

[ "$#" -eq 1 ] || {
	echo "usage: $0 OUTPUT_DIR" >&2
	exit 1
}
[ "$(uname -s)" = Darwin ] || {
	echo "notify-evidence: needs macOS with Xcode" >&2
	exit 1
}
for tool in xcrun xcodebuild python3 codesign; do
	command -v "$tool" >/dev/null 2>&1 || {
		echo "notify-evidence: required tool not found: $tool" >&2
		exit 1
	}
done
exec python3 -I "$script_dir/notify_evidence.py" run "$1"
