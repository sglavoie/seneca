#!/usr/bin/env bash
#
# Run the reminder delivery scenario on the paired iPhone and keep its record
# as the evidence scripts/verify_mobile_session.sh holds session 12 to. The
# record names the sources it was made from, so it has to be recorded again
# after any change to the engine, the bridge, the app, or these scripts.
#
#   ios/scripts/record-notify-evidence.sh
#
# See ios/scripts/notify-evidence.sh for what the iPhone needs first.
#
# Environment: as ios/scripts/notify-evidence.sh, and
#   SENECA_NOTIFY_EVIDENCE   output (default docs/mobile/evidence/session-12-device-notification.json)

set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
ios_dir=$(dirname "$script_dir")
repo_root=$(dirname "$ios_dir")

evidence=${SENECA_NOTIFY_EVIDENCE:-"$repo_root/docs/mobile/evidence/session-12-device-notification.json"}
out="${SENECA_IOS_CACHE:-$ios_dir/.cache}/results/notify-device"

"$script_dir/notify-evidence.sh" "$out"
mkdir -p "$(dirname "$evidence")"
cp "$out/notify.json" "$evidence"
echo "record-notify-evidence: wrote $evidence" >&2
