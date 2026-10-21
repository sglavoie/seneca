#!/usr/bin/env bash
#
# Run the end-to-end exchange between the iPhone app and this Mac: a
# reflection saved on the phone with no folder in reach arrives in the Mac's
# Markdown and its Git history, an edit made on the Mac returns, both sides
# edit at once, and a publication and a download are each stopped part-way
# and finished. The phone's half is SyncEvidenceTests, hosted in the app; the
# Mac's half is the `seneca` program built from these sources, over a
# synthetic journal in a Git repository of the run's own.
#
#   ios/scripts/sync-evidence.sh device OUTPUT_DIR
#   ios/scripts/sync-evidence.sh simulator OUTPUT_DIR
#
# On a device the two share a folder of the run's own inside the iCloud Drive
# folder chosen in the app (Settings, Sync, Choose Exchange Folder). Choose it
# once before the first run; the iPhone must be paired, unlocked, online, and
# signed in to the same iCloud account as this Mac. The run's folder is
# removed afterwards. On a simulator they share a plain directory: a
# rehearsal of the scenario that proves nothing about iCloud.
#
# OUTPUT_DIR/sync.json is the record, written only when every check in
# ios/scripts/sync_evidence.py holds. Neither the installation's journal nor
# the author's own exchange is opened.
#
# Environment:
#   SENECA_IOS_SIMULATOR, SENECA_IOS_DEVICE, SENECA_IOS_DEVELOPMENT_TEAM,
#   SENECA_IOS_CACHE     as ios/scripts/roundtrip.sh
#   SENECA_SYNC_KEEP=1   leave the run's folder in iCloud Drive afterwards

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

[ "$#" -eq 2 ] || {
	echo "usage: $0 simulator|device OUTPUT_DIR" >&2
	exit 1
}
[ "$(uname -s)" = Darwin ] || {
	echo "sync-evidence: needs macOS with Xcode" >&2
	exit 1
}
for tool in xcrun xcodebuild python3 go git shasum; do
	command -v "$tool" >/dev/null 2>&1 || {
		echo "sync-evidence: required tool not found: $tool" >&2
		exit 1
	}
done
exec python3 -I "$script_dir/sync_evidence.py" run "$1" "$2"
