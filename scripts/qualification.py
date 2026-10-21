"""Record and check the release qualification of Seneca for iPhone and the Mac.

    qualification.py suites                         run every simulator suite now and record it
    qualification.py install                        build these sources for the paired iPhone and install them
    qualification.py cli                            hold the installed `seneca` to these sources
    qualification.py mark CHECK passed|failed --note TEXT
    qualification.py defect open CHECK --data-loss yes|no --summary TEXT
    qualification.py defect close ID --resolution TEXT
    qualification.py trial start --journal PATH --exchange PATH
    qualification.py trial checkpoint
    qualification.py rollback --mac-export DIR --phone-export DIR
    qualification.py rollout backup --journal PATH --exchange PATH --backup DIR
    qualification.py rollout enrolled|phone-fresh|joined|verified|background
    qualification.py status                         say what is done and what remains
    qualification.py check                          hold both records; exit 1 naming every gap
    qualification.py checks                         list the device checks and what each means

docs/mobile/qualification.md is the procedure; this program keeps its record.
Two files under docs/mobile/evidence/:

  session-13-suites.json         every simulator suite, run in full by `suites`
  session-13-qualification.json  the physical iPhone, the multi-day trial, the
                                 rollback rehearsal, and the personal rollout

Every step observes what it records: the build installed on the iPhone (from
devicectl), the journal on the iPhone (copied off it), the Mac's journal and
its sync state, the backup's manifest, the LaunchAgent. The device checks are
the author's own word, and a check is only recorded against a build of these
sources installed on a physical iPhone that is paired at that moment.

Both records name the fingerprints of the sources they were made from; a
change to the app, the bridge, or the `seneca` program makes them stale and
`check` refuses them. Nothing absent is read as a pass, and no step changes a
journal: backing up, enrolling, joining, and installing background sync are
the author's to do, by hand, as the procedure says. A step the author waives
is recorded as waived, with the author's reason, and `check` names it as
waived rather than done.
"""

import argparse
import datetime
import glob
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile

SCRIPTS = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(SCRIPTS)
IOS_SCRIPTS = os.path.join(REPO, "ios", "scripts")
sys.path.insert(0, IOS_SCRIPTS)
import evidence  # noqa: E402
import sync_evidence  # noqa: E402

EVIDENCE = os.path.join(REPO, "docs", "mobile", "evidence")
# SENECA_QUALIFICATION_SUITES puts the suites record elsewhere: the gate's
# live run writes and holds its own instead of the committed one.
SUITES_RECORD = os.environ.get("SENECA_QUALIFICATION_SUITES") or os.path.join(EVIDENCE, "session-13-suites.json")
RECORD = os.path.join(EVIDENCE, "session-13-qualification.json")
CACHE = os.environ.get("SENECA_IOS_CACHE") or os.path.join(REPO, "ios", ".cache")
BUNDLE_ID = sync_evidence.BUNDLE_ID

SUITES_FORMAT = "seneca-release-suites"
FORMAT = "seneca-release-qualification"
FORMAT_VERSION = 1

# Hosted test classes the hosted suite leaves to other runs: the round trip
# across a redeployment, the sync scenario, and delivery on an iPhone.
HOSTED_ELSEWHERE = {"RedeploymentTests", "SyncEvidenceTests", "NotificationEvidenceTests"}

# The device checks. Each is the author's word that the procedure of the same
# name in docs/mobile/qualification.md held on a physical iPhone.
CHECKS = {
    "airplane-mode": "writing and saving with no network, then the day reaching the Mac once it returns",
    "termination": "the app force-quit while writing and while exchanging; nothing written is lost",
    "cloud-delay": "a change held back by iCloud for hours arrives whole, and the waiting is said",
    "simultaneous-edits": "the same answer changed on both devices offline; settled independently on each, they converge",
    "access-revocation": "the folder grant taken away and iCloud Drive turned off; writing goes on, the finding is shown, access comes back",
    "upgrade": "a build of these sources installed over an earlier one; journal, drafts, and sync carried over",
    "mac-restart": "the Mac restarted with background sync installed; it resumes by itself and imports what waited",
    "representative-size": "the synthetic three-year journal on the iPhone: joining, scrolling, searching, saving",
    "accessibility": "VoiceOver and the largest text size through writing, history, conflicts, and recovery",
    "date-boundary": "a reflection started before midnight and saved after, and the reminders around it",
    "time-zone-travel": "the iPhone's zone changed: today, history, and reminders follow; written days stay put",
    "draft-restoration": "an unsaved draft back after termination, a restart of the iPhone, and an upgrade",
}

TRIAL_CHECKPOINTS = 3
TRIAL_DAYS = 3
TRIAL_SPAN_HOURS = 48
DATE_LINE = re.compile(r"(?m)^## (\d{4}-\d{2}-\d{2})\r?$")


class Failed(Exception):
    pass


def note(message):
    print(f"qualification: {message}", file=sys.stderr, flush=True)


def now():
    return datetime.datetime.now(datetime.timezone.utc).astimezone().isoformat(timespec="seconds")


def instant(value):
    try:
        return datetime.datetime.fromisoformat(str(value).replace("Z", "+00:00"))
    except ValueError:
        return None


def sha256_file(path):
    with open(path, "rb") as handle:
        return hashlib.sha256(handle.read()).hexdigest()


def fingerprints():
    return sync_evidence.fingerprints()


def load(path):
    return evidence.load(path) if os.path.isfile(path) else None


def write(path, record):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    sync_evidence.write(path, record)


def scratch(name):
    os.makedirs(os.path.join(CACHE, "results"), exist_ok=True)
    return tempfile.mkdtemp(prefix=f"qualification-{name}-", dir=os.path.join(CACHE, "results"))


# --- the simulator suites -----------------------------------------------------


def declared_tests(directory, skip=()):
    """Every `Class/testName()` the Swift sources of a test target declare."""
    declared = set()
    for path in sorted(glob.glob(os.path.join(directory, "*.swift"))):
        current = None
        with open(path, encoding="utf-8") as handle:
            for line in handle:
                found = re.match(r"\s*(?:final\s+)?class\s+(\w+)\s*:\s*(\w+)", line)
                if found:
                    current = found.group(1)
                    continue
                test = re.match(r"\s*(?:@MainActor\s+)?func\s+(test\w+)\s*\(\s*\)", line)
                if test and current and current not in skip:
                    declared.add(f"{current}/{test.group(1)}()")
    return declared


def identified(tests_json):
    names = {}
    for node in tests_json.get("testNodes", []):
        evidence.collect_identified(node, names)
    return names


def suite_result(out):
    summary, tests = evidence.load(os.path.join(out, "summary.json")), evidence.load(os.path.join(out, "tests.json"))
    result = evidence.xcresult(summary, tests)
    result["identified"] = identified(tests)
    return result


def run_suites():
    prints = fingerprints()
    out = scratch("suites")
    record = {
        "format": SUITES_FORMAT,
        "format_version": FORMAT_VERSION,
        "recorded_at": now(),
        "source_fingerprint": prints["source"],
        "bridge_fingerprint": prints["bridge"],
        "desktop_fingerprint": prints["desktop"],
    }
    steps = [
        ("hosted", [os.path.join(IOS_SCRIPTS, "test.sh"), os.path.join(out, "hosted")]),
        ("ui", [os.path.join(IOS_SCRIPTS, "uitest.sh"), os.path.join(out, "ui")]),
        ("roundtrip", [os.path.join(IOS_SCRIPTS, "roundtrip.sh"), "simulator", os.path.join(out, "roundtrip")]),
        ("sync_rehearsal", [os.path.join(IOS_SCRIPTS, "sync-evidence.sh"), "simulator", os.path.join(out, "sync")]),
    ]
    env = dict(os.environ)
    env.pop("SENECA_UITEST_ONLY", None)
    for name, argv in steps:
        note(f"running the {name} suite (log under {out})")
        done = subprocess.run(argv, env=env)
        if done.returncode != 0:
            raise Failed(f"the {name} suite failed; nothing was recorded (see {out})")
    record["hosted"] = suite_result(os.path.join(out, "hosted"))
    record["ui"] = suite_result(os.path.join(out, "ui"))
    record["roundtrip"] = evidence.load(os.path.join(out, "roundtrip", "roundtrip.json"))
    record["sync_rehearsal"] = evidence.load(os.path.join(out, "sync", "sync.json"))
    found = suites_problems(record, fingerprints())
    if found:
        rejected = os.path.join(out, "suites-rejected.json")
        write(rejected, record)
        raise Failed("the suites ran and their record does not hold:\n  " + "\n  ".join(found))
    write(SUITES_RECORD, record)
    note(f"every suite held: {SUITES_RECORD}")


def suites_problems(record, prints):
    found = []

    def require(condition, message):
        if not condition:
            found.append(message)

    if not record:
        return [f"no suites record at {SUITES_RECORD}; run `just mobile-qualify suites`"]
    require(record.get("format") == SUITES_FORMAT and record.get("format_version") == FORMAT_VERSION,
            f"not a {SUITES_FORMAT} v{FORMAT_VERSION} record")
    for name in ("source", "bridge", "desktop"):
        require(record.get(f"{name}_fingerprint") == prints[name],
                f"suites: recorded from {name} sources {record.get(f'{name}_fingerprint')}; they are now {prints[name]}")
    for name, directory, skip in (
        ("hosted", os.path.join(REPO, "ios", "SenecaMobileTests"), HOSTED_ELSEWHERE),
        ("ui", os.path.join(REPO, "ios", "SenecaMobileUITests"), ()),
    ):
        result = record.get(name) or {}
        require(result.get("result") == "Passed", f"suites: the {name} suite says {result.get('result')}")
        require(bool(result.get("total")) and result.get("passed") == result.get("total"),
                f"suites: {result.get('passed')} of {result.get('total')} {name} tests passed")
        require(not result.get("failed") and not result.get("skipped") and not result.get("expected_failures"),
                f"suites: {name}: {result.get('failed')} failed, {result.get('skipped')} skipped")
        ran = result.get("identified") or {}
        for test in sorted(declared_tests(directory, skip)):
            require(ran.get(test) == "Passed", f"suites: {name} test {test} did not run and pass")
        require(all((d.get("platform") or "").endswith("Simulator") for d in result.get("devices") or [{}]),
                f"suites: the {name} suite did not run on a simulator")
    expected = evidence.load(os.path.join(REPO, "mobile", "testdata", "roundtrip", "expected.json"))
    roundtrip = record.get("roundtrip")
    if roundtrip:
        for problem in evidence.problems(roundtrip, "simulator", expected, prints["source"], prints["bridge"]):
            found.append(f"suites: round trip: {problem}")
    else:
        found.append("suites: the round trip is missing")
    rehearsal = record.get("sync_rehearsal")
    if rehearsal:
        for problem in sync_evidence.problems(rehearsal, "simulator", prints):
            found.append(f"suites: sync rehearsal: {problem}")
    else:
        found.append("suites: the sync rehearsal is missing")
    return found


# --- the paired iPhone ----------------------------------------------------------


def paired_iphone(out):
    devices = os.path.join(out, "devices.json")
    subprocess.run(["xcrun", "devicectl", "list", "devices", "--timeout", "30", "--json-output", devices],
                   check=True, capture_output=True)
    udid = sync_evidence.Phone.select(devices)
    for device in evidence.load(devices)["result"]["devices"]:
        hardware = device.get("hardwareProperties", {})
        if hardware.get("udid") == udid:
            properties = device.get("deviceProperties", {})
            return {
                "udid": udid,
                "name": properties.get("name"),
                "model": hardware.get("productType"),
                "marketing_name": hardware.get("marketingName"),
                "os_version": properties.get("osVersionNumber"),
                "reality": hardware.get("reality"),
                "device_type": hardware.get("deviceType"),
            }
    raise Failed(f"iPhone {udid} vanished from the device list")


def installed_build(udid, out):
    """The build number of Seneca as installed on the iPhone now, or None."""
    listing = os.path.join(out, "apps.json")
    done = subprocess.run(
        ["xcrun", "devicectl", "device", "info", "apps", "--device", udid, "--bundle-id", BUNDLE_ID,
         "--timeout", "30", "--json-output", listing],
        capture_output=True, text=True,
    )
    if done.returncode != 0:
        raise Failed(f"could not ask the iPhone what is installed; unlock it and keep it paired: {done.stderr.strip()}")
    apps = evidence.load(listing)["result"].get("apps") or []
    return str(apps[0].get("bundleVersion")) if apps else None


def copy_from_phone(udid, relative, destination):
    """One file of the app's container, copied off the iPhone; False when it is absent."""
    os.makedirs(os.path.dirname(destination), exist_ok=True)
    done = subprocess.run(
        ["xcrun", "devicectl", "device", "copy", "from", "--device", udid, "--timeout", "120",
         "--domain-type", "appDataContainer", "--domain-identifier", BUNDLE_ID,
         "--source", relative, "--destination", destination],
        capture_output=True, text=True,
    )
    if done.returncode != 0:
        if "No such file" in done.stderr or "doesn't exist" in done.stderr or "not found" in done.stderr.lower():
            return False
        raise Failed(f"could not copy {relative} off the iPhone: {done.stderr.strip()}")
    if os.path.isdir(destination):
        found = [p for p in glob.glob(os.path.join(destination, "**", os.path.basename(relative)), recursive=True) if os.path.isfile(p)]
        if not found:
            return False
        shutil.move(found[0], destination + ".file")
        shutil.rmtree(destination)
        shutil.move(destination + ".file", destination)
    return os.path.isfile(destination)


PHONE_JOURNAL = "Documents/Journal"


def phone_journal(udid, out):
    """The iPhone's journal as it is now: its reflections file and its sync state."""
    into = os.path.join(out, "phone")
    shutil.rmtree(into, ignore_errors=True)
    reflections = os.path.join(into, "reflections.md")
    state = os.path.join(into, "state.json")
    has_reflections = copy_from_phone(udid, f"{PHONE_JOURNAL}/reflections.md", reflections)
    has_state = copy_from_phone(udid, f"{PHONE_JOURNAL}/.seneca/journals/reflections.md/sync/state.json", state)
    return (reflections if has_reflections else None), (evidence.load(state) if has_state else None)


class Record:
    """The qualification record, opened against the sources as they are."""

    def __init__(self):
        self.prints = fingerprints()
        self.record = load(RECORD)

    def require_current(self):
        if not self.record:
            raise Failed(f"no qualification record yet; start with `just mobile-qualify install` ({RECORD})")
        for name in ("source", "bridge", "desktop"):
            if self.record.get(f"{name}_fingerprint") != self.prints[name]:
                raise Failed(f"the record was made from other {name} sources; install a build of these with "
                             "`just mobile-qualify install`, which starts a new record")
        return self.record

    def save(self):
        write(RECORD, self.record)

    def phone(self, out):
        """The paired iPhone, which must be the record's and run one of its builds."""
        record = self.require_current()
        device = paired_iphone(out)
        if device["udid"] != record["device"]["udid"]:
            raise Failed(f"the paired iPhone is {device['udid']}, the record's is {record['device']['udid']}")
        build = installed_build(device["udid"], out)
        if build not in [b["number"] for b in record.get("builds", [])]:
            raise Failed(f"the iPhone runs build {build}, not one of these sources; run `just mobile-qualify install`")
        return device, build


def install():
    out = scratch("install")
    for step in ("build-bridge.sh", "generate-project.sh"):
        subprocess.run([os.path.join(IOS_SCRIPTS, step)], check=True, capture_output=True, text=True)
    prints = fingerprints()
    device = paired_iphone(out)
    if device.get("reality") != "physical" or device.get("device_type") != "iPhone":
        raise Failed(f"{device['udid']} is not a physical iPhone")
    replaced = installed_build(device["udid"], out)
    env = dict(os.environ, SENECA_IOS_DEVICE=device["udid"])
    done = subprocess.run([os.path.join(IOS_SCRIPTS, "install-device.sh"), "--launch"], env=env,
                          stdout=subprocess.PIPE, text=True)
    if done.returncode != 0:
        raise Failed("the build or its installation failed; nothing was recorded")
    app = done.stdout.strip().splitlines()[-1]
    number = subprocess.run(["plutil", "-extract", "CFBundleVersion", "raw", "-o", "-", os.path.join(app, "Info.plist")],
                            check=True, capture_output=True, text=True).stdout.strip()
    build = evidence.build_info(app, number)
    signed = subprocess.run(["codesign", "-d", "--verbose=4", app], capture_output=True, text=True).stderr
    team = re.search(r"(?m)^TeamIdentifier=(\S+)$", signed)
    if installed_build(device["udid"], out) != number:
        raise Failed(f"the iPhone does not report build {number} after installing it")
    if fingerprints() != prints:
        raise Failed("the sources changed while building; install again")

    record = load(RECORD)
    if record and any(record.get(f"{n}_fingerprint") != prints[n] for n in ("source", "bridge", "desktop")):
        aside = os.path.join(CACHE, "results", f"qualification-stale-{datetime.datetime.now():%Y%m%dT%H%M%S}.json")
        shutil.copy(RECORD, aside)
        note(f"the record was made from other sources; it is kept at {aside} and a new one starts")
        record = None
    if record and record["device"]["udid"] != device["udid"]:
        raise Failed(f"the record is for iPhone {record['device']['udid']}; this is {device['udid']}")
    if not record:
        record = {
            "format": FORMAT,
            "format_version": FORMAT_VERSION,
            "opened_at": now(),
            "source_fingerprint": prints["source"],
            "bridge_fingerprint": prints["bridge"],
            "desktop_fingerprint": prints["desktop"],
            "device": device,
            "builds": [],
            "checks": {},
            "defects": [],
        }
    record["device"] = dict(device, signing_team=team.group(1) if team else None)
    record["builds"].append(dict(build, installed_at=now(), replaced=replaced))
    write(RECORD, record)
    note(f"build {number} of these sources is on {device['name']}; it replaced build {replaced}")


def built_seneca(out):
    """`seneca` built from these sources the way `just install` builds it."""
    path = os.path.join(out, "seneca")
    subprocess.run(["go", "build", "-o", path, "."], cwd=REPO, check=True, capture_output=True, text=True)
    return path


def installed_seneca():
    found = shutil.which("seneca")
    if not found:
        raise Failed("no seneca on PATH; run `just install`")
    return os.path.realpath(found)


def cli():
    record = Record()
    record.require_current()
    out = scratch("cli")
    installed = installed_seneca()
    built = built_seneca(out)
    if sha256_file(installed) != sha256_file(built):
        raise Failed(f"{installed} is not built from these sources; run `just install`")
    record.record["cli"] = {"at": now(), "path": installed, "sha256": sha256_file(installed), "matches_sources": True}
    record.save()
    note(f"{installed} is built from these sources")


# --- the device checks and defects ------------------------------------------------


def mark(check, result, text):
    if check not in CHECKS:
        raise Failed(f"no check {check}; `qualification.py checks` lists them")
    if not text.strip():
        raise Failed("say what was done and seen with --note")
    record = Record()
    out = scratch("mark")
    device, build = record.phone(out)
    entry = {"result": result, "at": now(), "note": text.strip(), "device": device["udid"], "build": build,
             "zone": datetime.datetime.now().astimezone().tzname()}
    if check == "upgrade":
        builds = {b["number"]: b for b in record.record["builds"]}
        current = builds[build]
        if not current.get("replaced") or current.get("replaced") == build:
            raise Failed("the build on the iPhone was not installed over an earlier one; install an earlier "
                         "build first, use the app, then `just mobile-qualify install`")
        entry["replaced"] = current["replaced"]
    record.record["checks"][check] = entry
    if result == "failed":
        note(f"{check} failed; open a defect for it with `qualification.py defect open {check} ...`")
    record.save()
    note(f"{check}: {result} on build {build}")


def defect_open(check, data_loss, summary):
    if check not in CHECKS and check not in ("trial", "rollout", "rollback"):
        raise Failed(f"no check {check}")
    record = Record()
    record.require_current()
    number = len(record.record["defects"]) + 1
    record.record["defects"].append({"id": number, "check": check, "data_loss": data_loss == "yes",
                                      "summary": summary, "opened_at": now()})
    record.save()
    note(f"defect {number} opened")


def defect_close(number, resolution):
    record = Record()
    record.require_current()
    for defect in record.record["defects"]:
        if defect["id"] == number:
            if defect.get("closed_at"):
                raise Failed(f"defect {number} is already closed")
            defect.update(closed_at=now(), resolution=resolution)
            record.save()
            note(f"defect {number} closed")
            return
    raise Failed(f"no defect {number}")


# --- comparing the two copies -----------------------------------------------------


def mac_env(journal, exchange):
    env = dict(os.environ, SENECA_FILE=journal)
    if exchange:
        env["SENECA_SYNC_EXCHANGE"] = exchange
    else:
        env.pop("SENECA_SYNC_EXCHANGE", None)
    return env


def seneca(env, *args, check=True):
    done = subprocess.run([installed_seneca(), *args], env=env, capture_output=True, text=True,
                          cwd=os.path.dirname(env["SENECA_FILE"]))
    if check and done.returncode != 0:
        raise Failed(f"seneca {' '.join(args)} failed: {done.stderr.strip() or done.stdout.strip()}")
    return done


def entry_digests(path):
    """Each day of a reflections file with the digest of its bytes, as `seneca show` reads it."""
    with open(path, encoding="utf-8", newline="") as handle:
        dates = DATE_LINE.findall(handle.read())
    if len(dates) != len(set(dates)):
        raise Failed(f"{path} holds a day twice")
    home = tempfile.mkdtemp(prefix="qualification-home-")
    env = {"HOME": home, "PATH": os.environ.get("PATH", ""), "SENECA_FILE": path}
    digests = {}
    try:
        for date in dates:
            shown = seneca(env, "show", f"--date={date}").stdout
            digests[date] = hashlib.sha256(shown.encode()).hexdigest()
    finally:
        shutil.rmtree(home, ignore_errors=True)
    return digests


def mac_state(journal):
    path = os.path.join(os.path.dirname(journal), ".seneca", "journals", os.path.basename(journal), "sync", "state.json")
    return load(path)


def compare(journal, exchange, udid, out):
    """Both copies of a journal, day by day, and where sync stands between them."""
    phone_file, phone_state = phone_journal(udid, out)
    mac = entry_digests(journal)
    phone = entry_digests(phone_file) if phone_file else {}
    differing = sorted(d for d in set(mac) | set(phone) if mac.get(d) != phone.get(d))
    status = seneca(mac_env(journal, exchange), "sync", "status").stdout
    applied = [(int(a), int(b)) for a, b in re.findall(r"applied on .*?: (\d+) of (\d+) recorded here", status)]
    state = mac_state(journal) or {}
    phone_state = phone_state or {}
    observed = {
        "mac_days": len(mac),
        "phone_days": len(phone),
        "differing_days": len(differing),
        "mac_journal": state.get("journal"),
        "phone_journal": phone_state.get("journal"),
        "mac_conflicts": len(state.get("conflicts") or {}),
        "phone_conflicts": len(phone_state.get("conflicts") or {}),
        "phone_applied_all": bool(applied) and all(a == b for a, b in applied),
        "unpublished": int((re.search(r"not published yet: (\d+)", status) or [0, -1])[1]),
        "unimported": int((re.search(r"not imported yet: (\d+)", status) or [0, -1])[1]),
    }
    observed["agree"] = (observed["differing_days"] == 0 and observed["mac_days"] > 0
                         and observed["mac_journal"] and observed["mac_journal"] == observed["phone_journal"]
                         and observed["mac_conflicts"] == 0 and observed["phone_conflicts"] == 0
                         and observed["phone_applied_all"] and observed["unpublished"] == 0 and observed["unimported"] == 0)
    for date in differing[:20]:
        note(f"  {date} differs between the Mac and the iPhone")
    return observed


def import_commits(journal):
    done = subprocess.run(["git", "-C", os.path.dirname(journal), "log", "--format=%s", "--", journal],
                          capture_output=True, text=True)
    return sum(1 for line in done.stdout.splitlines() if line == sync_evidence.IMPORT_SUBJECT) if done.returncode == 0 else 0


# --- the trial ---------------------------------------------------------------------


def configured_journal():
    """The journal `seneca` opens with nothing set, which a trial must not use."""
    home = os.path.expanduser("~")
    env = dict(os.environ)
    env.pop("SENECA_FILE", None)
    done = subprocess.run([installed_seneca(), "doctor"], env=env, capture_output=True, text=True, cwd=home)
    found = re.search(r"reflections path (/\S+)", done.stdout)
    return os.path.realpath(found.group(1)) if found else None


def trial_start(journal, exchange):
    record = Record()
    record.require_current()
    journal, exchange = os.path.realpath(journal), os.path.realpath(exchange)
    rollout = (record.record.get("rollout") or {}).get("backup") or {}
    if journal in (configured_journal(), rollout.get("journal")):
        raise Failed(f"{journal} is the author's journal; the trial uses disposable data only")
    if not mac_state(journal):
        raise Failed(f"{journal} is not enrolled yet; `seneca sync enroll` it first (docs/mobile/qualification.md)")
    record.record["trial"] = {"journal": journal, "exchange": exchange, "started_at": now(), "checkpoints": []}
    record.save()
    note("the trial started; take a checkpoint at least once a day")


def trial_checkpoint():
    record = Record()
    trial = record.require_current().get("trial")
    if not trial:
        raise Failed("no trial started")
    out = scratch("trial")
    device, build = record.phone(out)
    observed = compare(trial["journal"], trial["exchange"], device["udid"], out)
    observed.update(at=now(), local_date=datetime.date.today().isoformat(), build=build,
                    imports=import_commits(trial["journal"]))
    trial["checkpoints"].append(observed)
    record.save()
    note(f"checkpoint {len(trial['checkpoints'])}: {observed['mac_days']} days on the Mac, {observed['phone_days']} on the iPhone, "
         f"{'they agree' if observed['agree'] else 'THEY DO NOT AGREE'}")


# --- the rollback rehearsal ----------------------------------------------------------


def manifest_holds(directory, sources=None):
    manifest = load(os.path.join(directory, "MANIFEST.json"))
    if not manifest:
        return False, {}
    kinds = {}
    for file in manifest.get("files") or []:
        path = os.path.join(directory, *file["path"].split("/"))
        if not os.path.isfile(path) or "sha256:" + sha256_file(path) != file["digest"]:
            return False, {}
        kinds.setdefault(file["kind"], []).append(file["digest"])
    for kind, source in (sources or {}).items():
        if not os.path.isfile(source) or "sha256:" + sha256_file(source) not in kinds.get(kind, []):
            return False, kinds
    return True, kinds


def launch_agent():
    return os.path.expanduser("~/Library/LaunchAgents/io.sglavoie.seneca.sync.plist")


def rollback(mac_export, phone_export):
    record = Record()
    trial = record.require_current().get("trial")
    if not trial:
        raise Failed("rehearse the rollback on the trial's journal; no trial started")
    journal = trial["journal"]
    mac_ok, mac_kinds = manifest_holds(mac_export, {"reflections": journal})
    phone_ok, phone_kinds = manifest_holds(phone_export)
    left = glob.glob(os.path.join(os.path.dirname(journal), ".seneca", "journals", os.path.basename(journal), "sync-left-*"))
    plain = mac_env(journal, None)
    status = seneca(plain, "sync", "status", check=False)
    shown = seneca(plain, "show", check=False)
    observed = {
        "at": now(),
        "service_removed": not os.path.exists(launch_agent()),
        "mac_export_holds": mac_ok,
        "mac_export_conflicts": len(mac_kinds.get("conflict", [])),
        "phone_export_holds": phone_ok and bool(phone_kinds.get("reflections")),
        "phone_export_drafts": len(phone_kinds.get("draft", [])),
        "left_sync": not mac_state(journal) and len(left) >= 1,
        "local_cli_works": status.returncode == 0 and "does not take part in sync" in status.stdout and shown.returncode == 0,
        "backups_retained": [],
    }
    backup = ((record.record.get("rollout") or {}).get("backup") or {}).get("backup")
    for directory in [mac_export, phone_export] + ([backup] if backup else []):
        observed["backups_retained"].append({"path": directory, "holds": manifest_holds(directory)[0]})
    record.record["rollback"] = observed
    record.save()
    for key, value in observed.items():
        if value is False:
            note(f"rollback: {key} does not hold")
    note("rollback recorded")


# --- the rollout --------------------------------------------------------------------


def rollout_step(step, journal=None, exchange=None, backup=None):
    record = Record()
    current = record.require_current()
    rollout = current.setdefault("rollout", {})
    out = scratch(f"rollout-{step}")
    if step == "backup":
        journal, exchange, backup = os.path.realpath(journal), os.path.realpath(exchange), os.path.realpath(backup)
        archive = os.path.join(os.path.dirname(journal), "seneca-reviews.md")
        sources = {"reflections": journal}
        if os.path.isfile(archive):
            sources["archive"] = archive
        holds, _ = manifest_holds(backup, sources)
        if not holds:
            raise Failed(f"{backup} is not a backup of {journal} as it is now; `seneca backup {backup}` first")
        dirty = subprocess.run(["git", "-C", os.path.dirname(journal), "status", "--porcelain", "--", *sources.values()],
                               capture_output=True, text=True)
        if mac_state(journal):
            raise Failed(f"{journal} already takes part in sync; the backup has to come first")
        rollout.clear()
        rollout["backup"] = {"at": now(), "journal": journal, "exchange": exchange, "backup": backup,
                             "manifest_holds": True, "committed": dirty.returncode == 0 and not dirty.stdout.strip()}
    else:
        base = rollout.get("backup")
        if not base:
            raise Failed("record the backup first: `qualification.py rollout backup ...`")
        journal, exchange = base["journal"], base["exchange"]
        if step == "enrolled":
            state = mac_state(journal)
            if not state:
                raise Failed(f"{journal} is not enrolled; `seneca sync enroll --name Mac` with [sync].exchange set")
            cli_ok = (current.get("cli") or {}).get("sha256") == sha256_file(installed_seneca())
            rollout["enrolled"] = {"at": now(), "journal_id": state.get("journal"), "device": state.get("device"),
                                   "cli_from_sources": cli_ok}
        elif step in ("phone-fresh", "joined", "verified"):
            device, build = record.phone(out)
            if step == "phone-fresh":
                phone_file, phone_state = phone_journal(device["udid"], out)
                days = len(entry_digests(phone_file)) if phone_file else 0
                rollout["phone_fresh"] = {"at": now(), "build": build, "phone_days": days,
                                          "phone_enrolled": bool(phone_state)}
            elif step == "joined":
                _, phone_state = phone_journal(device["udid"], out)
                enrolled = rollout.get("enrolled") or {}
                rollout["joined"] = {"at": now(), "build": build,
                                     "phone_journal": (phone_state or {}).get("journal"),
                                     "same_journal": bool(phone_state) and phone_state.get("journal") == enrolled.get("journal_id")}
            else:
                observed = compare(journal, exchange, device["udid"], out)
                observed.update(at=now(), build=build, background_installed=os.path.exists(launch_agent()))
                rollout["verified"] = observed
        elif step == "background":
            listed = subprocess.run(["launchctl", "print", f"gui/{os.getuid()}/io.sglavoie.seneca.sync"],
                                    capture_output=True, text=True)
            plist = launch_agent()
            agent = open(plist, encoding="utf-8").read() if os.path.isfile(plist) else ""
            rollout["background"] = {"at": now(), "installed": bool(agent), "running": listed.returncode == 0,
                                     "for_this_journal": f"<string>{journal}</string>" in agent}
        else:
            raise Failed(f"no rollout step {step}")
    record.save()
    note(f"rollout {step} recorded")


# --- holding the records ---------------------------------------------------------------


def waivable():
    return (*CHECKS, "trial", "rollback", "rollout")


def waive(name, text):
    if name not in waivable():
        raise Failed(f"no step {name}; one of {', '.join(waivable())}")
    if not text.strip():
        raise Failed("say why the step is waived with --note")
    record = Record()
    current = record.require_current()
    builds = current.get("builds") or []
    if not builds:
        raise Failed("no build of these sources was installed; run `just mobile-qualify install`")
    current.setdefault("waived", {})[name] = {"at": now(), "note": text.strip(), "build": builds[-1]["number"]}
    record.save()
    note(f"{name} is waived by the author on build {builds[-1]['number']}: {text.strip()}")


def waivers(record):
    """The steps the author waived, on these sources' builds."""
    builds = {b.get("number") for b in (record or {}).get("builds") or []}
    return {name: entry for name, entry in ((record or {}).get("waived") or {}).items()
            if name in waivable() and entry.get("build") in builds and (entry.get("note") or "").strip()}


def problems(record, prints):
    found = []

    def require(condition, message):
        if not condition:
            found.append(message)

    if not record:
        # Nothing is recorded yet: every step below is named as missing.
        found.append(f"no qualification record at {RECORD}; start with `just mobile-qualify install`")
        record = {}
    else:
        require(record.get("format") == FORMAT and record.get("format_version") == FORMAT_VERSION,
                f"not a {FORMAT} v{FORMAT_VERSION} record")
        stale = [n for n in ("source", "bridge", "desktop") if record.get(f"{n}_fingerprint") != prints[n]]
        if stale:
            return [f"the record was made from other {', '.join(stale)} sources than these; everything in it is stale: "
                    "install a build of these sources and qualify again"]
    device = record.get("device") or {}
    builds = {b.get("number"): b for b in record.get("builds") or []}
    if record:
        require(device.get("reality") == "physical" and device.get("device_type") == "iPhone",
                f"the device is {device.get('reality')} {device.get('device_type')}, not a physical iPhone")
        require(bool(device.get("udid")), "names no iPhone")
        require(bool(device.get("signing_team")), "the build was not signed by a team")
        require(bool(builds), "no build of these sources was installed on the iPhone")
        for number, build in builds.items():
            require(bool(build.get("executables")), f"build {number} names no executables")

    cli = record.get("cli") or {}
    require(cli.get("matches_sources") is True and bool(cli.get("sha256")),
            "the installed seneca was not held to these sources (`qualification.py cli`)")

    checks = record.get("checks") or {}
    waived = waivers(record)
    for name in CHECKS:
        entry = checks.get(name)
        if not entry and name in waived:
            continue
        if not entry:
            found.append(f"device check {name} is not done: {CHECKS[name]}")
            continue
        require(entry.get("result") == "passed", f"device check {name} {entry.get('result')}")
        require(entry.get("device") == device.get("udid"), f"device check {name} was made on another iPhone")
        require(entry.get("build") in builds, f"device check {name} was made on a build of other sources")
        require(bool((entry.get("note") or "").strip()), f"device check {name} says nothing of what was seen")
        if name == "upgrade":
            require(bool(entry.get("replaced")) and entry.get("replaced") != entry.get("build"),
                    "the upgrade check was not made on a build installed over an earlier one")
    for defect in record.get("defects") or []:
        kind = "data-loss defect" if defect.get("data_loss") else "defect"
        require(bool(defect.get("closed_at")) and bool((defect.get("resolution") or "").strip()),
                f"{kind} {defect.get('id')} ({defect.get('check')}) is open: {defect.get('summary')}")

    trial = record.get("trial")
    if not trial and "trial" in waived:
        pass
    elif not trial:
        found.append("the multi-day trial has not started")
    else:
        points = trial.get("checkpoints") or []
        days = {p.get("local_date") for p in points}
        first, last = (instant(points[0].get("at")), instant(points[-1].get("at"))) if points else (None, None)
        span = (last - first).total_seconds() / 3600 if first and last else 0
        require(len(points) >= TRIAL_CHECKPOINTS and len(days) >= TRIAL_DAYS and span >= TRIAL_SPAN_HOURS,
                f"the trial has {len(points)} checkpoints on {len(days)} days over {span:.0f} h; it needs "
                f"{TRIAL_CHECKPOINTS} on {TRIAL_DAYS} days over {TRIAL_SPAN_HOURS} h")
        for index, point in enumerate(points, 1):
            require(point.get("agree") is True, f"trial checkpoint {index} found the two copies apart "
                    f"({point.get('differing_days')} days differ, {point.get('mac_conflicts')}/{point.get('phone_conflicts')} conflicts)")
            require(point.get("build") in builds, f"trial checkpoint {index} ran a build of other sources")
        if len(points) >= 2:
            require(points[-1].get("mac_days", 0) > points[0].get("mac_days", 0),
                    "nothing was written during the trial")
            require(points[-1].get("imports", 0) > points[0].get("imports", 0),
                    "nothing written on the iPhone reached the Mac's Git history during the trial")

    rollback_seen = record.get("rollback")
    rollout = record.get("rollout") or {}
    if not rollback_seen and "rollback" in waived:
        pass
    elif not rollback_seen:
        found.append("the rollback has not been rehearsed")
    else:
        for key in ("service_removed", "mac_export_holds", "phone_export_holds", "left_sync", "local_cli_works"):
            require(rollback_seen.get(key) is True, f"rollback: {key} did not hold")
        require(rollback_seen.get("mac_export_conflicts", 0) > 0,
                "rollback: the Mac's export held no conflict; rehearse it with one open")
        require(all(b.get("holds") for b in rollback_seen.get("backups_retained") or [{}]),
                "rollback: a backup no longer holds")
        enrolled_at = instant((rollout.get("enrolled") or {}).get("at"))
        require(not enrolled_at or instant(rollback_seen.get("at")) < enrolled_at,
                "the rollback was rehearsed after the author's journal was enrolled")

    order = [("backup", "the author's journal backed up"), ("enrolled", "enrolled with the new seneca"),
             ("phone_fresh", "the iPhone found empty before joining"), ("joined", "the iPhone joined"),
             ("verified", "both copies verified"), ("background", "background sync enabled")]
    if "rollout" in waived and not rollout:
        order = []
    for key, what in order:
        require(key in rollout, f"rollout: {what} is not done")
    if order and all(key in rollout for key, _ in order):
        at = {key: instant(rollout[key].get("at")) for key, _ in order}
        require(at["backup"] < at["enrolled"] < at["joined"] < at["verified"] < at["background"],
                "rollout: the steps were not done in order")
        require(at["phone_fresh"] < at["joined"], "rollout: the iPhone was not checked empty before it joined")
        trial_journal = (trial or {}).get("journal")
        require(rollout["backup"].get("journal") != trial_journal, "rollout: the journal rolled out is the trial's")
        require(rollout["backup"].get("committed") is True, "rollout: the journal had uncommitted changes when backed up")
        require(rollout["enrolled"].get("cli_from_sources") is True, "rollout: enrolled with a seneca not built from these sources")
        require(rollout["phone_fresh"].get("phone_days") == 0 and rollout["phone_fresh"].get("phone_enrolled") is False,
                "rollout: the iPhone held writing or a sync history before joining")
        require(rollout["joined"].get("same_journal") is True, "rollout: the iPhone joined another journal")
        verified = rollout["verified"]
        require(verified.get("agree") is True, "rollout: the two copies did not agree when verified")
        require(verified.get("background_installed") is False, "rollout: background sync was on before both copies were verified")
        require(verified.get("mac_journal") == rollout["enrolled"].get("journal_id"), "rollout: verified another journal")
        background = rollout["background"]
        require(background.get("installed") and background.get("running") and background.get("for_this_journal"),
                "rollout: background sync is not installed and running for the author's journal")
    return found


def status():
    prints = fingerprints()
    gaps = suites_problems(load(SUITES_RECORD), prints) + problems(load(RECORD), prints)
    for name, entry in waivers(load(RECORD)).items():
        print(f"  ~ {name} is waived by the author: {entry['note']}")
    if not gaps:
        print("qualification: everything holds")
    for gap in gaps:
        print(f"  - {gap}")
    return gaps


def main(argv):
    parser = argparse.ArgumentParser(prog="qualification.py", description=__doc__.splitlines()[0])
    sub = parser.add_subparsers(dest="command", required=True)
    for name in ("suites", "install", "cli", "status", "check", "checks"):
        sub.add_parser(name)
    marking = sub.add_parser("mark")
    marking.add_argument("check")
    marking.add_argument("result", choices=("passed", "failed"))
    marking.add_argument("--note", required=True)
    defect = sub.add_parser("defect").add_subparsers(dest="action", required=True)
    opening = defect.add_parser("open")
    opening.add_argument("check")
    opening.add_argument("--data-loss", choices=("yes", "no"), required=True)
    opening.add_argument("--summary", required=True)
    closing = defect.add_parser("close")
    closing.add_argument("id", type=int)
    closing.add_argument("--resolution", required=True)
    trial = sub.add_parser("trial").add_subparsers(dest="action", required=True)
    starting = trial.add_parser("start")
    starting.add_argument("--journal", required=True)
    starting.add_argument("--exchange", required=True)
    trial.add_parser("checkpoint")
    back = sub.add_parser("rollback")
    back.add_argument("--mac-export", required=True)
    back.add_argument("--phone-export", required=True)
    waiving = sub.add_parser("waive")
    waiving.add_argument("step")
    waiving.add_argument("--note", required=True)
    rollout = sub.add_parser("rollout")
    rollout.add_argument("step", choices=("backup", "enrolled", "phone-fresh", "joined", "verified", "background"))
    rollout.add_argument("--journal")
    rollout.add_argument("--exchange")
    rollout.add_argument("--backup")
    args = parser.parse_args(argv[1:])
    try:
        if args.command == "suites":
            run_suites()
        elif args.command == "install":
            install()
        elif args.command == "cli":
            cli()
        elif args.command == "mark":
            mark(args.check, args.result, args.note)
        elif args.command == "defect":
            if args.action == "open":
                defect_open(args.check, args.data_loss, args.summary)
            else:
                defect_close(args.id, args.resolution)
        elif args.command == "trial":
            trial_start(args.journal, args.exchange) if args.action == "start" else trial_checkpoint()
        elif args.command == "rollback":
            rollback(os.path.realpath(args.mac_export), os.path.realpath(args.phone_export))
        elif args.command == "rollout":
            if args.step == "backup" and not (args.journal and args.exchange and args.backup):
                raise Failed("rollout backup needs --journal, --exchange, and --backup")
            rollout_step(args.step, args.journal, args.exchange, args.backup)
        elif args.command == "waive":
            waive(args.step, args.note)
        elif args.command == "checks":
            for name, what in CHECKS.items():
                print(f"{name:20} {what}")
        elif args.command == "status":
            status()
        elif args.command == "check":
            prints = fingerprints()
            gaps = suites_problems(load(SUITES_RECORD), prints) + problems(load(RECORD), prints)
            for gap in gaps:
                print(f"qualification: {gap}", file=sys.stderr)
            if gaps:
                sys.exit(f"qualification: {len(gaps)} things keep the release from qualifying; "
                         "docs/mobile/qualification.md says how to do each")
            waived = waivers(load(RECORD))
            if waived:
                print(f"qualification: waived by the author, not done: {', '.join(waived)}")
            print("qualification: the suites and the device, trial, rollback, and rollout records hold")
    except (Failed, sync_evidence.Failed) as failure:
        sys.exit(f"qualification: {failure}")
    except subprocess.CalledProcessError as failure:
        sys.exit(f"qualification: {' '.join(map(str, failure.cmd))} failed: {failure.stderr or failure.stdout or ''}")


if __name__ == "__main__":
    main(sys.argv)
