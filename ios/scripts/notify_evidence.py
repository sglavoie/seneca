"""Run and check the reminder delivery scenario on a physical iPhone.

    notify_evidence.py run OUTPUT_DIR
    notify_evidence.py check RECORD_JSON

`run` is what ios/scripts/notify-evidence.sh calls. It builds the app from the
sources as they are, signed, runs NotificationEvidenceTests on the paired
iPhone, collects what the test reported from inside the app, and writes
OUTPUT_DIR/notify.json only when the whole record holds: iOS delivered the
first reminder the app scheduled, and a reflection saved before the second
made the app cancel it, so iOS never delivered it.

There is no simulator form. A simulator's notification center proves nothing
about delivery on an iPhone, and `check` refuses any record not made on one.

`check` holds a record written earlier to the same rules and to the sources as
they are now. Nothing absent is read as a pass.
"""

import datetime
import json
import os
import shutil
import subprocess
import sys

SCRIPTS = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, SCRIPTS)
import evidence  # noqa: E402
import sync_evidence  # noqa: E402  (the paired iPhone, its build, and its container)

FORMAT = "seneca-mobile-notification"
FORMAT_VERSION = 1
SUITE = "SenecaMobileTests/NotificationEvidenceTests"
TEST = "testAReminderIsDeliveredAndASaveCancelsTheNext"
# iOS fires a calendar trigger within its minute; allow it a little latitude.
EARLY = 2
LATE = 90


class Failed(Exception):
    pass


def note(message):
    print(f"notify-evidence: {message}", file=sys.stderr, flush=True)


def fingerprints():
    return {
        "source": sync_evidence.output(os.path.join(SCRIPTS, "source-fingerprint.sh")),
        "bridge": sync_evidence.output(os.path.join(SCRIPTS, "build-bridge.sh"), "--fingerprint"),
    }


class Phone(sync_evidence.Phone):
    """The paired iPhone, running the notification test instead of a sync phase."""

    def deliver(self):
        note("phone: scheduling two reminders and waiting for iOS (about four minutes; keep the iPhone unlocked)")
        env = dict(os.environ, TEST_RUNNER_SENECA_NOTIFY_RUN=self.run)
        bundle = os.path.join(self.out, "deliver.xcresult")
        shutil.rmtree(bundle, ignore_errors=True)
        log = os.path.join(self.out, "test-deliver.log")
        with open(log, "w", encoding="utf-8") as handle:
            done = subprocess.run(
                ["xcodebuild", "test-without-building", "-project", self.project, "-scheme", "SenecaMobile",
                 "-destination", self.destination, "-destination-timeout", "60", "-derivedDataPath", self.derived,
                 f"-only-testing:{SUITE}/{TEST}", "-resultBundlePath", bundle],
                stdout=handle, stderr=subprocess.STDOUT, env=env,
            )
        reported = None
        try:
            reported = self.collect("deliver")
        except sync_evidence.Failed:
            pass
        if done.returncode != 0:
            with open(log, encoding="utf-8") as handle:
                failures = [line.strip() for line in handle if "error:" in line or "XCTAssert" in line]
            raise Failed(f"the delivery test failed ({log}):\n  " + "\n  ".join(failures[:12]))
        if reported is None:
            raise Failed(f"the delivery test left no evidence in the app's container; did it run? ({log})")
        summary = json.loads(sync_evidence.output("xcrun", "xcresulttool", "get", "test-results", "summary", "--path", bundle, "--format", "json"))
        tests = json.loads(sync_evidence.output("xcrun", "xcresulttool", "get", "test-results", "tests", "--path", bundle, "--format", "json"))
        return {"reported": reported, "xcresult": evidence.xcresult(summary, tests)}


def run(out):
    os.makedirs(out, exist_ok=True)
    out = os.path.abspath(out)
    stamp = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    run_id = f"notify-{stamp}-{os.getpid()}"
    for step in ("build-bridge.sh", "generate-project.sh"):
        subprocess.run([os.path.join(SCRIPTS, step)], check=True, capture_output=True, text=True)
    prints = fingerprints()
    phone = Phone("device", out, run_id)
    build = phone.build()
    record = {
        "format": FORMAT,
        "format_version": FORMAT_VERSION,
        "target": "device",
        "run": run_id,
        "recorded_at": datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="seconds"),
        "source_fingerprint": prints["source"],
        "bridge_fingerprint": prints["bridge"],
        "device": phone.info,
        "build": build,
        "deliver": phone.deliver(),
    }
    found = problems(record, prints)
    if found:
        rejected = os.path.join(out, "notify-rejected.json")
        sync_evidence.write(rejected, record)
        for problem in found:
            print(f"notify-evidence: {problem}", file=sys.stderr)
        raise Failed(f"the scenario ran and its record does not hold; see {rejected}")
    sync_evidence.write(os.path.join(out, "notify.json"), record)
    note(f"delivery held on the iPhone: {os.path.join(out, 'notify.json')}")


def instant(value):
    try:
        return datetime.datetime.fromisoformat(str(value).replace("Z", "+00:00"))
    except ValueError:
        return None


def problems(record, prints):
    """Everything that keeps a record from proving delivery on an iPhone."""
    found = []

    def require(condition, message):
        if not condition:
            found.append(message)

    require(record.get("format") == FORMAT and record.get("format_version") == FORMAT_VERSION,
            f"not a {FORMAT} v{FORMAT_VERSION} record")
    require(record.get("target") == "device", f"recorded on a {record.get('target')}, not an iPhone")
    for name in ("source", "bridge"):
        require(record.get(f"{name}_fingerprint") == prints[name],
                f"recorded from {name} sources {record.get(f'{name}_fingerprint')}; they are now {prints[name]}")
    run_id = record.get("run")
    require(bool(run_id), "names no run")

    device, build = record.get("device") or {}, record.get("build") or {}
    require(device.get("reality") == "physical", f"the target is {device.get('reality')}, not a physical iPhone")
    require(bool(device.get("udid")), "names no device")
    require(bool(device.get("signing_team")), "names no signing team: the build was not signed")

    entry = record.get("deliver") or {}
    result, said = entry.get("xcresult") or {}, entry.get("reported") or {}
    require(result.get("result") == "Passed" and result.get("total") == 1 and result.get("passed") == 1,
            f"{result.get('passed')} of {result.get('total')} tests passed ({result.get('result')}), want 1 of 1")
    require(result.get("failed") == 0 and result.get("skipped") == 0 and result.get("expected_failures") == 0,
            f"{result.get('failed')} failed, {result.get('skipped')} skipped")
    require(result.get("tests") == [{"name": f"{TEST}()", "result": "Passed"}], f"ran {result.get('tests')}, want {TEST}() passed")
    ran_on = [d.get("id") for d in result.get("devices") or []]
    require(ran_on == [device.get("udid")], f"ran on {ran_on}, not on {device.get('udid')}")

    require(said.get("run") == run_id and said.get("phase") == "deliver", f"the app reported run {said.get('run')}")
    require(said.get("simulator") is False, "the app says it ran on a simulator")
    require(said.get("passed") is True, "the app did not finish the scenario")
    require(str(device.get("model")) in str(said.get("device_model")), f"the app ran on {said.get('device_model')}, not on {device.get('model')}")
    require(bool(build.get("number")) and said.get("bundle_build") == build.get("number"),
            f"the app reports build {said.get('bundle_build')}, built {build.get('number')}")
    ran, built = said.get("executables") or {}, build.get("executables") or {}
    require(len(ran) >= 2, f"the app reported {len(ran)} executables")
    for binary, value in ran.items():
        require(built.get(binary) == value, f"{binary} on the iPhone is not the one this Mac built")
    require(said.get("permission") == "allowed", f"notifications were {said.get('permission')}")

    scheduled = said.get("scheduled") or []
    require(len(scheduled) == 2, f"scheduled {scheduled}, want two reminders")
    if len(scheduled) == 2:
        first, second = scheduled
        today = said.get("today") or ""
        require(today and today in first.get("identifier", "") and today in second.get("identifier", ""),
                "the reminders are not about the day the journal was on")
        delivered = said.get("delivered") or {}
        due, at = instant(first.get("fire_at")), instant(delivered.get("delivered_at"))
        require(delivered.get("identifier") == first.get("identifier"), f"delivered {delivered.get('identifier')}, not the first reminder")
        require(delivered.get("presented") is True or delivered.get("in_notification_center") is True,
                "iOS neither showed nor kept the first reminder")
        if due and at:
            lag = (at - due).total_seconds()
            require(-EARLY <= lag <= LATE, f"the first reminder came {lag:.0f} s from when it was due")
        else:
            found.append("the first reminder's times are missing")
        saved, second_due = instant(said.get("saved_at")), instant(second.get("fire_at"))
        require(bool(saved and second_due and at and at < saved < second_due),
                "the reflection was not saved between the two reminders")
        require(second.get("identifier") not in (said.get("pending_after_save") or []), "the save left the second reminder pending")
        require(today in (said.get("complete") or []), "the plan after the save does not count the day as written")
        suppressed = said.get("suppressed") or {}
        checked = instant(suppressed.get("checked_at"))
        require(suppressed.get("identifier") == second.get("identifier"), "the second reminder was not watched")
        require(bool(checked and second_due and (checked - second_due).total_seconds() >= 20),
                "the second reminder was not watched past its time")
        require(suppressed.get("presented") is False and suppressed.get("in_notification_center") is False,
                "iOS delivered the reminder the save cancelled")
    return found


def main(argv):
    try:
        if len(argv) == 3 and argv[1] == "run":
            run(argv[2])
        elif len(argv) == 3 and argv[1] == "check":
            if not os.path.isfile(argv[2]):
                sys.exit(f"notify-evidence: {argv[2]} is missing")
            found = problems(evidence.load(argv[2]), fingerprints())
            for problem in found:
                print(f"notify-evidence: {argv[2]}: {problem}", file=sys.stderr)
            if found:
                sys.exit(1)
            print(f"notify-evidence: {argv[2]} holds")
        else:
            sys.exit(__doc__)
    except (Failed, sync_evidence.Failed) as failure:
        sys.exit(f"notify-evidence: {failure}")
    except subprocess.CalledProcessError as failure:
        sys.exit(f"notify-evidence: {' '.join(map(str, failure.cmd))} failed: {failure.stderr or failure.stdout or ''}")


if __name__ == "__main__":
    main(sys.argv)
