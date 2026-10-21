#!/usr/bin/env bash
#
# Run the end-to-end exchange on a physical iPhone and this Mac through iCloud
# Drive, and keep its record as the evidence scripts/verify_mobile_session.sh
# holds session 10 to. The record names the sources it was made from, so it
# has to be recorded again after any change to the engine, the bridge, the
# app, the `seneca` program, or these scripts.
#
#   ios/scripts/record-sync-evidence.sh
#
# See ios/scripts/sync-evidence.sh for what the iPhone needs first.
#
# Environment: as ios/scripts/sync-evidence.sh, and
#   SENECA_SYNC_EVIDENCE   output (default docs/mobile/evidence/session-10-device-sync.json)

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ios_dir=$(dirname "$script_dir")
repo_root=$(dirname "$ios_dir")

evidence=${SENECA_SYNC_EVIDENCE:-"$repo_root/docs/mobile/evidence/session-10-device-sync.json"}
out="${SENECA_IOS_CACHE:-$ios_dir/.cache}/results/sync-device"

"$script_dir/sync-evidence.sh" device "$out"
mkdir -p "$(dirname "$evidence")"
cp "$out/sync.json" "$evidence"
echo "record-sync-evidence: wrote $evidence" >&2
