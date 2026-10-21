"""Describe, assemble, and check round-trip evidence for the iPhone app.

    evidence.py simulator-info SIMULATORS_JSON UDID
    evidence.py device-info DEVICES_JSON UDID SIGNING_TEAM
    evidence.py build-info APP BUILD_NUMBER
    evidence.py suite SUMMARY_JSON TESTS_JSON [REQUIRED_TEST...]
    evidence.py assemble TARGET OUTPUT_DIR RUN EXPECTED_JSON SOURCE_FP BRIDGE_FP
    evidence.py check RECORD_JSON TARGET EXPECTED_JSON SOURCE_FP BRIDGE_FP

ios/scripts/roundtrip.sh produces the pieces and calls `assemble`, which
writes OUTPUT_DIR/roundtrip.json only when the record holds. `check` holds a
record written earlier to the same rules and to the sources as they are now;
scripts/verify_mobile_session.sh uses it for the committed device evidence.

A record holds when every problem `problems()` can name is absent. Missing
evidence is a problem: nothing here treats an absent value as a pass.
"""

import datetime
import hashlib
import json
import os
import platform
import subprocess
import sys

FORMAT = "seneca-mobile-roundtrip"
FORMAT_VERSION = 1
PHASES = {
    "seed": "testSeedSavesAReflectionThroughGo()",
    "reopen": "testReopenFindsTheReflectionAfterRedeployment()",
}
# What the app reports about the journal, against the desktop's expectation.
JOURNAL_FIELDS = ("today", "entry_count", "entry_revision", "reflections_sha256", "archive_sha256")


def load(path):
    with open(path, encoding="utf-8") as handle:
        return json.load(handle)


def digest(path):
    with open(path, "rb") as handle:
        return hashlib.sha256(handle.read()).hexdigest()


def simulator_info(simulators_json, udid):
    for runtime, devices in load(simulators_json)["devices"].items():
        for device in devices:
            if device["udid"] == udid and device.get("isAvailable"):
                if "iPhone" not in device["deviceTypeIdentifier"]:
                    sys.exit(f"{udid} is not an iPhone simulator")
                return {
                    "udid": udid,
                    "name": device["name"],
                    "model": device["deviceTypeIdentifier"].rsplit(".", 1)[-1],
                    "os_version": runtime.rsplit(".", 1)[-1],
                    "reality": "simulated",
                }
    sys.exit(f"no available simulator {udid}")


def device_info(devices_json, udid, team):
    for device in load(devices_json)["result"]["devices"]:
        hardware = device.get("hardwareProperties", {})
        if hardware.get("udid") != udid:
            continue
        if hardware.get("reality") != "physical" or hardware.get("deviceType") != "iPhone":
            sys.exit(f"{udid} is not a physical iPhone")
        properties = device.get("deviceProperties", {})
        return {
            "udid": udid,
            "name": properties.get("name"),
            "model": hardware.get("productType"),
            "marketing_name": hardware.get("marketingName"),
            "os_version": properties.get("osVersionNumber"),
            "reality": "physical",
            "signing_team": team,
        }
    sys.exit(f"no paired device {udid}")


def build_info(app, number):
    """The executables of a built app, named as the app names them itself."""
    executables = {}
    name = os.path.basename(app)[: -len(".app")]
    candidates = [name] + sorted(entry for entry in os.listdir(app) if entry.endswith(".dylib"))
    plugins = os.path.join(app, "PlugIns")
    if os.path.isdir(plugins):
        for bundle in sorted(os.listdir(plugins)):
            if bundle.endswith(".xctest"):
                candidates.append(os.path.join("PlugIns", bundle, bundle[: -len(".xctest")]))
    for relative in candidates:
        path = os.path.join(app, relative)
        if os.path.isfile(path):
            executables[relative] = digest(path)
    if name not in executables:
        sys.exit(f"{app} has no executable {name}")
    plist = subprocess.run(
        ["plutil", "-extract", "CFBundleVersion", "raw", "-o", "-", os.path.join(app, "Info.plist")],
        check=True, capture_output=True, text=True,
    ).stdout.strip()
    if plist != str(number):
        sys.exit(f"{app} carries build {plist}, not {number}")
    return {"number": str(number), "executables": executables}


def test_cases(node):
    if node.get("nodeType") == "Test Case":
        yield {"name": node.get("name"), "result": node.get("result")}
    for child in node.get("children", []):
        yield from test_cases(child)


def xcresult(summary, tests):
    devices = [entry.get("device", {}) for entry in summary.get("devicesAndConfigurations", [])]
    return {
        "result": summary.get("result"),
        "total": summary.get("totalTestCount"),
        "passed": summary.get("passedTests"),
        "failed": summary.get("failedTests"),
        "skipped": summary.get("skippedTests"),
        "expected_failures": summary.get("expectedFailures"),
        "devices": [
            {"id": device.get("deviceId"), "platform": device.get("platform"), "os_version": device.get("osVersion")}
            for device in devices
        ],
        "tests": [case for node in tests.get("testNodes", []) for case in test_cases(node)],
    }


def suite(summary_json, tests_json, required):
    """Hold a whole suite run to: everything executed, everything passed."""
    result = xcresult(load(summary_json), load(tests_json))
    found = []
    if result["result"] != "Passed":
        found.append(f"the result bundle says {result['result']}")
    if not result["total"] or result["passed"] != result["total"]:
        found.append(f"{result['passed']} of {result['total']} tests passed")
    if result["failed"] or result["skipped"] or result["expected_failures"]:
        found.append(f"{result['failed']} failed, {result['skipped']} skipped, {result['expected_failures']} expected failures")
    names = {}
    for node in load(tests_json).get("testNodes", []):
        collect_identified(node, names)
    for name, outcome in sorted(names.items()):
        if outcome != "Passed":
            found.append(f"{name}: {outcome}")
    for name in required:
        if names.get(name) != "Passed":
            found.append(f"required test {name} did not run and pass")
    for problem in found:
        print(f"evidence: {problem}", file=sys.stderr)
    if found:
        sys.exit(1)
    print(f"evidence: {result['passed']} tests ran and passed, none skipped")


def collect_identified(node, into):
    if node.get("nodeType") == "Test Case":
        into[node.get("nodeIdentifier")] = node.get("result")
    for child in node.get("children", []):
        collect_identified(child, into)


def assemble(target, out, run, expected_json, source_fp, bridge_fp):
    record = {
        "format": FORMAT,
        "format_version": FORMAT_VERSION,
        "target": target,
        "run": run,
        "recorded_at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "source_fingerprint": source_fp,
        "bridge_fingerprint": bridge_fp,
        "mac": {"os_version": platform.mac_ver()[0], "machine": platform.machine()},
        "device": load(os.path.join(out, "target.json")),
        "phases": {},
    }
    for phase in PHASES:
        reported = os.path.join(out, "container", f"evidence-{phase}.json")
        if not os.path.isfile(reported):
            # A device copy may nest the directory it was asked for.
            for root, _dirs, files in os.walk(os.path.join(out, "container")):
                if f"evidence-{phase}.json" in files:
                    reported = os.path.join(root, f"evidence-{phase}.json")
        if not os.path.isfile(reported):
            sys.exit(f"evidence: the {phase} phase left no evidence-{phase}.json")
        record["phases"][phase] = {
            "build": load(os.path.join(out, f"build-{phase}.json")),
            "reported": load(reported),
            "xcresult": xcresult(
                load(os.path.join(out, f"{phase}-summary.json")), load(os.path.join(out, f"{phase}-tests.json"))
            ),
        }
    found = problems(record, target, load(expected_json), source_fp, bridge_fp)
    if found:
        with open(os.path.join(out, "roundtrip-rejected.json"), "w", encoding="utf-8") as handle:
            json.dump(record, handle, indent=2, sort_keys=True)
            handle.write("\n")
        for problem in found:
            print(f"evidence: {problem}", file=sys.stderr)
        sys.exit(1)
    with open(os.path.join(out, "roundtrip.json"), "w", encoding="utf-8") as handle:
        json.dump(record, handle, indent=2, sort_keys=True)
        handle.write("\n")


def problems(record, target, expected, source_fp, bridge_fp):
    """Everything that keeps a record from proving the round trip."""
    found = []

    def require(condition, message):
        if not condition:
            found.append(message)

    require(record.get("format") == FORMAT and record.get("format_version") == FORMAT_VERSION,
            f"not a {FORMAT} v{FORMAT_VERSION} record")
    require(record.get("target") == target, f"recorded on a {record.get('target')}, not a {target}")
    require(record.get("source_fingerprint") == source_fp,
            f"recorded from sources {record.get('source_fingerprint')}; they are now {source_fp}")
    require(record.get("bridge_fingerprint") == bridge_fp,
            f"recorded with bridge {record.get('bridge_fingerprint')}; it is now {bridge_fp}")
    run = record.get("run")
    require(bool(run), "names no run")

    device = record.get("device") or {}
    reality = "simulated" if target == "simulator" else "physical"
    require(device.get("reality") == reality, f"the target is {device.get('reality')}, not {reality}")
    require(bool(device.get("udid")), "names no target device")
    if target == "device":
        require(bool(device.get("signing_team")), "names no signing team: the device build was not signed")

    phases = record.get("phases") or {}
    for phase, test in PHASES.items():
        entry = phases.get(phase)
        if not entry:
            found.append(f"{phase}: the phase is missing")
            continue
        result, reported, build = entry.get("xcresult") or {}, entry.get("reported") or {}, entry.get("build") or {}

        # Exactly the one test, executed and passed: not built, not skipped.
        require(result.get("result") == "Passed", f"{phase}: the result bundle says {result.get('result')}")
        require(result.get("total") == 1 and result.get("passed") == 1,
                f"{phase}: {result.get('passed')} of {result.get('total')} tests passed, want 1 of 1")
        require(result.get("failed") == 0 and result.get("skipped") == 0 and result.get("expected_failures") == 0,
                f"{phase}: {result.get('failed')} failed, {result.get('skipped')} skipped")
        require(result.get("tests") == [{"name": test, "result": "Passed"}],
                f"{phase}: ran {result.get('tests')}, want {test} passed")
        ran_on = [d.get("id") for d in result.get("devices") or []]
        require(ran_on == [device.get("udid")], f"{phase}: ran on {ran_on}, not on {device.get('udid')}")

        # What the app itself reported from inside the phase.
        require(reported.get("phase") == phase and reported.get("run") == run,
                f"{phase}: the app reported phase {reported.get('phase')} of run {reported.get('run')}")
        require(reported.get("simulator") is (target == "simulator"),
                f"{phase}: the app says simulator={reported.get('simulator')}")
        require(reported.get("replayed_after_reopen") is True, f"{phase}: the repeated save was not answered from the ledger")
        require(isinstance(reported.get("bridge_version"), int) and reported.get("bridge_version") >= 1,
                f"{phase}: no bridge version reported")
        for field in JOURNAL_FIELDS:
            require(reported.get(field) == expected.get(field),
                    f"{phase}: {field} is {reported.get(field)!r}; the desktop writes {expected.get(field)!r}")
        if target == "device":
            require(str(device.get("model")) in str(reported.get("device_model")),
                    f"{phase}: the app ran on {reported.get('device_model')}, not on {device.get('model')}")

        # Which build ran: its number, and the executables this Mac built.
        require(bool(build.get("number")) and reported.get("bundle_build") == build.get("number"),
                f"{phase}: the app reports build {reported.get('bundle_build')}, built {build.get('number')}")
        ran, built = reported.get("executables") or {}, build.get("executables") or {}
        require(len(ran) >= 2, f"{phase}: the app reported {len(ran)} executables")
        for name, value in ran.items():
            require(built.get(name) == value, f"{phase}: {name} on the target is not the one this Mac built")

    seed, reopen = phases.get("seed") or {}, phases.get("reopen") or {}
    seed_number = (seed.get("build") or {}).get("number")
    reopen_number = (reopen.get("build") or {}).get("number")
    require(seed_number and reopen_number and seed_number != reopen_number,
            f"both phases ran build {seed_number}: nothing was redeployed between them")
    return found


def main(argv):
    if len(argv) < 2:
        sys.exit(__doc__)
    command, args = argv[1], argv[2:]
    if command == "simulator-info" and len(args) == 2:
        json.dump(simulator_info(*args), sys.stdout, indent=2)
    elif command == "device-info" and len(args) == 3:
        json.dump(device_info(*args), sys.stdout, indent=2)
    elif command == "build-info" and len(args) == 2:
        json.dump(build_info(*args), sys.stdout, indent=2)
    elif command == "suite" and len(args) >= 2:
        suite(args[0], args[1], args[2:])
    elif command == "assemble" and len(args) == 6:
        assemble(*args)
    elif command == "check" and len(args) == 5:
        record_json, target, expected_json, source_fp, bridge_fp = args
        if not os.path.isfile(record_json):
            sys.exit(f"evidence: {record_json} is missing")
        found = problems(load(record_json), target, load(expected_json), source_fp, bridge_fp)
        for problem in found:
            print(f"evidence: {record_json}: {problem}", file=sys.stderr)
        if found:
            sys.exit(1)
        print(f"evidence: {record_json} holds")
    else:
        sys.exit(__doc__)


if __name__ == "__main__":
    main(sys.argv)
