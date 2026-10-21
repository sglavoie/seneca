#!/usr/bin/env bash
#
# Run the round trip across a signed redeployment on a physical iPhone and
# keep its record as the evidence scripts/verify_mobile_session.sh holds
# session 8 to. The record names the sources it was built from, so it has to
# be recorded again after any change to the bridge, the app, or these scripts.
#
#   SENECA_IOS_DEVELOPMENT_TEAM=TEAMID ios/scripts/record-device-evidence.sh
#
# The iPhone must be paired, unlocked, and in Developer Mode. Only a synthetic
# journal in a directory of the run's own is written on it.
#
# Environment: as ios/scripts/roundtrip.sh, and
#   SENECA_DEVICE_EVIDENCE   output (default docs/mobile/evidence/session-8-device-roundtrip.json)

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ios_dir=$(dirname "$script_dir")
repo_root=$(dirname "$ios_dir")

evidence=${SENECA_DEVICE_EVIDENCE:-"$repo_root/docs/mobile/evidence/session-8-device-roundtrip.json"}
out="${SENECA_IOS_CACHE:-$ios_dir/.cache}/results/roundtrip-device"

"$script_dir/roundtrip.sh" device "$out"
mkdir -p "$(dirname "$evidence")"
cp "$out/roundtrip.json" "$evidence"
echo "record-device-evidence: wrote $evidence" >&2
