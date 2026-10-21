# Shared by the device and round-trip scripts; sourced, not run.
# shellcheck shell=bash

app_name=Seneca
bundle_id=io.sglavoie.seneca.mobile
export app_name bundle_id

# select_iphone OUTPUT_DIR prints the UDID of the physical iPhone to use:
# SENECA_IOS_DEVICE when it is paired, else the only one paired. It leaves
# the device list at OUTPUT_DIR/devices.json.
select_iphone() {
	local out=$1
	xcrun devicectl list devices --timeout 30 --json-output "$out/devices.json" >"$out/devices.log" 2>&1 || {
		echo "could not list paired devices: $out/devices.log" >&2
		return 1
	}
	python3 -I - "$out/devices.json" "${SENECA_IOS_DEVICE:-}" <<'PY'
import json, sys
phones = []
for d in json.load(open(sys.argv[1]))["result"]["devices"]:
    hw = d.get("hardwareProperties", {})
    if hw.get("reality") == "physical" and hw.get("platform") == "iOS" and hw.get("deviceType") == "iPhone":
        phones.append(hw["udid"])
requested = sys.argv[2]
if requested:
    if requested not in phones:
        sys.exit(f"iPhone {requested} is not paired; connect it, unlock it, and trust this Mac")
    print(requested)
elif len(phones) == 1:
    print(phones[0])
elif not phones:
    sys.exit("no physical iPhone is paired; connect it, unlock it, and trust this Mac")
else:
    sys.exit("several iPhones are paired; set SENECA_IOS_DEVICE to one of: " + ", ".join(phones))
PY
}

# signing_team APP prints the team to sign with: SENECA_IOS_DEVELOPMENT_TEAM,
# else the team that signed the previous device build at APP.
signing_team() {
	local app=$1 team=${SENECA_IOS_DEVELOPMENT_TEAM:-}
	if [ -z "$team" ] && [ -d "$app" ]; then
		team=$(codesign -d --verbose=4 "$app" 2>&1 | awk -F= '/^TeamIdentifier=/ && $2 != "not set" { print $2 }') || true
	fi
	[ -n "$team" ] || return 1
	echo "$team"
}
