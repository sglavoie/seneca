"""Run and check the end-to-end sync scenario between the iPhone app and a Mac.

    sync_evidence.py run TARGET OUTPUT_DIR
    sync_evidence.py check RECORD_JSON TARGET
    sync_evidence.py desktop-fingerprint

`run` is what ios/scripts/sync-evidence.sh calls. It builds the app and the
`seneca` program from the sources as they are, then alternates between a phase
on the phone (one test of SyncEvidenceTests, hosted in the app) and the Mac's
part (the real program over a synthetic journal in a Git repository of the
run's own), holding what each side reports against the other. It writes
OUTPUT_DIR/sync.json only when the whole record holds.

TARGET is `device` or `simulator`. On a device the two sides share nothing but
a folder of the run's own in iCloud Drive: the phone reaches it through the
grant the author gave the app, the Mac through ~/Library/Mobile Documents. On
a simulator they share a plain directory, which rehearses the scenario and
proves nothing about iCloud; `check ... device` refuses such a record.

`check` holds a record written earlier to the same rules and to the sources as
they are now. A record holds when every problem `problems()` can name is
absent, and nothing absent is read as a pass.
"""

import datetime
import glob
import hashlib
import json
import os
import platform
import re
import shutil
import subprocess
import sys
import tempfile
import time

SCRIPTS = os.path.dirname(os.path.abspath(__file__))
IOS = os.path.dirname(SCRIPTS)
REPO = os.path.dirname(IOS)
sys.path.insert(0, SCRIPTS)
import evidence  # noqa: E402  (the round trip's helpers, beside this file)

FORMAT = "seneca-mobile-sync"
FORMAT_VERSION = 1
BUNDLE_ID = "io.sglavoie.seneca.mobile"
SUITE = "SenecaMobileTests/SyncEvidenceTests"
# Each phase on the phone, in order, and the one test that is it.
PHASES = [
    ("probe", "testProbe"),
    ("join", "testJoin"),
    ("save-offline", "testSaveOffline"),
    ("publish", "testPublish"),
    ("confirm", "testConfirm"),
    ("receive", "testReceive"),
    ("simultaneous", "testSimultaneous"),
    ("interrupt", "testInterrupt"),
    ("resume", "testResume"),
    ("receive-interrupted", "testReceiveInterrupted"),
    ("final", "testFinal"),
]
IMPORT_SUBJECT = "seneca: sync import"
SF_DATALESS = 0x40000000
WAIT = 420

SAVED = "2026-04-02"  # the scenario's day, written on the phone
EDITED = "2026-03-05"  # edited on the Mac, returned to the phone
CONFLICTED = "2026-03-04"  # the same answer changed on both sides
INTERRUPTED = "2026-04-01"  # written on the phone, published in two halves
LATE = "2026-03-02"  # edited on the Mac, downloaded in two halves
DESK_EDIT = ("Synthetic: took the walk anyway.", "Synthetic: took the walk anyway, and wrote it down at the desk.")
DESK_MERGE = ("once the phone was put away.", "once the phone was put away. (desk note)")
DESK_CONFLICT = ("Synthetic: the morning held together.", "Synthetic: the morning held together. (desk version)")
DESK_LATE = ("Start with ten quiet minutes.", "Start with twelve quiet minutes.")
PHONE_CONFLICT = "(phone version)"


class Failed(Exception):
    pass


def note(message):
    print(f"sync-evidence: {message}", file=sys.stderr, flush=True)


def sha256_bytes(data):
    return hashlib.sha256(data).hexdigest()


def desktop_fingerprint():
    """Everything the `seneca` program is built from."""
    files = ["go.mod", "go.sum", "main.go"]
    for package in ("cmd", "engine"):
        for root, _dirs, names in os.walk(os.path.join(REPO, package)):
            for name in names:
                if name.endswith(".go") and not name.endswith("_test.go"):
                    files.append(os.path.relpath(os.path.join(root, name), REPO))
    digest = hashlib.sha256()
    for name in sorted(files):
        with open(os.path.join(REPO, name), "rb") as handle:
            digest.update(f"{sha256_bytes(handle.read())}  {name}\n".encode())
    return digest.hexdigest()


def output(*argv, **kwargs):
    return subprocess.run(argv, check=True, capture_output=True, text=True, **kwargs).stdout.strip()


def fingerprints():
    return {
        "source": output(os.path.join(SCRIPTS, "source-fingerprint.sh")),
        "bridge": output(os.path.join(SCRIPTS, "build-bridge.sh"), "--fingerprint"),
        "desktop": desktop_fingerprint(),
    }


# --- the Mac's side -----------------------------------------------------------


class Desk:
    """The Mac: the real program over a synthetic journal in a Git repository."""

    def __init__(self, out, seneca):
        self.out = out
        self.seneca = seneca
        self.root = os.path.realpath(tempfile.mkdtemp(prefix="seneca-sync-evidence-"))
        self.home = os.path.join(self.root, "home")
        self.repo = os.path.join(self.root, "repo")
        self.journal = os.path.join(self.repo, "journal")
        self.reflections = os.path.join(self.journal, "reflections.md")
        self.exchange = None
        self.log = open(os.path.join(out, "desk.log"), "a", encoding="utf-8")
        os.makedirs(os.path.join(self.home, ".config", "seneca"))
        os.makedirs(self.journal)

        fixture = os.path.join(REPO, "mobile", "testdata", "roundtrip")
        scenario = evidence.load(os.path.join(fixture, "scenario.json"))
        with open(os.path.join(self.home, ".config", "seneca", "config.toml"), "w", encoding="utf-8") as handle:
            handle.write("[seneca]\nquestions = " + json.dumps(scenario["questions"]) + "\n")
            handle.write(f"anchor_question_index = {scenario['anchor_question_index']}\n")
        for name in ("reflections.md", "seneca-reviews.md"):
            shutil.copy(os.path.join(fixture, "journal", name), os.path.join(self.journal, name))
        self.git("init", "--quiet", "--initial-branch=main")
        self.git("add", "-A")
        self.git("commit", "--quiet", "-m", "the synthetic journal")

    def env(self):
        env = {
            "HOME": self.home,
            "PATH": os.environ.get("PATH", ""),
            "TMPDIR": os.environ.get("TMPDIR", "/tmp"),
            "SENECA_FILE": self.reflections,
            "GIT_CONFIG_GLOBAL": "/dev/null",
            "GIT_CONFIG_SYSTEM": "/dev/null",
            "GIT_AUTHOR_NAME": "Seneca Evidence",
            "GIT_AUTHOR_EMAIL": "evidence@example.invalid",
            "GIT_COMMITTER_NAME": "Seneca Evidence",
            "GIT_COMMITTER_EMAIL": "evidence@example.invalid",
        }
        if self.exchange:
            env["SENECA_SYNC_EXCHANGE"] = self.exchange
        return env

    def git(self, *args):
        done = subprocess.run(["git", "-C", self.repo, *args], env=self.env(), capture_output=True, text=True)
        if done.returncode != 0:
            raise Failed(f"git {' '.join(args)} failed: {done.stderr.strip()}")
        return done.stdout

    def run(self, *args):
        done = subprocess.run([self.seneca, *args], env=self.env(), capture_output=True, text=True, cwd=self.repo)
        self.log.write(f"$ seneca {' '.join(args)}  -> {done.returncode}\n{done.stdout}{done.stderr}\n")
        self.log.flush()
        return done

    def must(self, *args):
        done = self.run(*args)
        if done.returncode != 0:
            raise Failed(f"seneca {' '.join(args)} failed: {done.stderr.strip() or done.stdout.strip()}")
        return done.stdout

    def text(self):
        with open(self.reflections, encoding="utf-8") as handle:
            return handle.read()

    def has(self, date):
        return f"## {date}\n" in self.text()

    def revision(self, date):
        """An entry's revision as the phone names it: the digest of its bytes."""
        shown = self.must("show", f"--date={date}")
        return "sha256:" + sha256_bytes(shown[:-1].encode() if shown.endswith("\n") else shown.encode())

    def entries(self):
        return {date: self.revision(date) for date in re.findall(r"(?m)^## (\d{4}-\d{2}-\d{2})$", self.text())}

    def edit(self, date, old, new):
        """An edit made by hand in an editor, and committed by hand."""
        text = self.text()
        start = text.index(f"## {date}\n")
        end = text.find("\n---\n", start)
        entry = text[start:end]
        if entry.count(old) != 1:
            raise Failed(f"the entry of {date} does not hold {old!r} exactly once")
        with open(self.reflections, "w", encoding="utf-8") as handle:
            handle.write(text[:start] + entry.replace(old, new) + text[end:])
        self.git("commit", "--quiet", "-am", f"edit {date} by hand")

    def imports(self):
        """The commits sync made, newest first: subject and the files each names."""
        commits = []
        log = self.git("log", "--format=commit %H%x09%s", "--name-only", "--", "journal")
        for line in log.splitlines():
            if line.startswith("commit "):
                commit, _, subject = line[len("commit "):].partition("\t")
                commits.append({"commit": commit, "subject": subject, "files": []})
            elif line and commits:
                commits[-1]["files"].append(line)
        return [commit for commit in commits if commit["subject"] == IMPORT_SUBJECT]

    def dirty(self):
        return self.git("status", "--porcelain", "--", "journal/reflections.md", "journal/seneca-reviews.md").strip()

    def sync_until(self, what, done, timeout=WAIT):
        """`seneca sync once`, again and again, until `done` holds."""
        deadline = time.time() + timeout
        downloading = False
        last = None
        while True:
            last = self.run("sync", "once")
            downloading = downloading or "still downloading from iCloud" in last.stderr
            if done():
                return downloading
            if time.time() > deadline:
                raise Failed(f"the Mac timed out waiting for {what}: {last.stderr.strip() or last.stdout.strip()}")
            time.sleep(3)

    def close(self):
        self.log.close()
        shutil.rmtree(self.root, ignore_errors=True)


def wait_for(what, found, timeout=WAIT):
    deadline = time.time() + timeout
    while True:
        value = found()
        if value:
            return value
        if time.time() > deadline:
            raise Failed(f"timed out waiting for {what}")
        time.sleep(3)


def dataless(path):
    try:
        return bool(os.stat(path).st_flags & SF_DATALESS)
    except OSError:
        return False


def evict(paths):
    """Send files back to iCloud, the way macOS does to reclaim space."""
    evicted = []
    deadline = time.time() + 120
    pending = list(paths)
    while pending and time.time() < deadline:
        for path in list(pending):
            # A file is evictable only once iCloud has it.
            done = subprocess.run(["brctl", "evict", path], capture_output=True, text=True)
            if done.returncode == 0 and dataless(path):
                evicted.append(path)
                pending.remove(path)
        if pending:
            time.sleep(3)
    return evicted


# --- the phone's side ---------------------------------------------------------


class Phone:
    def __init__(self, target, out, run):
        self.target, self.out, self.run = target, out, run
        self.cache = os.environ.get("SENECA_IOS_CACHE") or os.path.join(IOS, ".cache")
        self.project = os.path.join(IOS, "SenecaMobile.xcodeproj")
        self.folder = None
        if target == "simulator":
            self.udid = output(os.path.join(SCRIPTS, "simulator.sh"))
            self.destination = f"platform=iOS Simulator,id={self.udid}"
            self.derived = os.path.join(self.cache, "SyncSimDerivedData")
            self.products = os.path.join(self.derived, "Build", "Products", "Debug-iphonesimulator")
            self.signing = ["CODE_SIGNING_ALLOWED=NO"]
            subprocess.run(["xcrun", "simctl", "boot", self.udid], capture_output=True)
            subprocess.run(["xcrun", "simctl", "bootstatus", self.udid, "-b"], check=True, capture_output=True)
            listing = os.path.join(out, "simulators.json")
            with open(listing, "w", encoding="utf-8") as handle:
                handle.write(output("xcrun", "simctl", "list", "devices", "--json"))
            self.info = evidence.simulator_info(listing, self.udid)
        else:
            devices = os.path.join(out, "devices.json")
            subprocess.run(
                ["xcrun", "devicectl", "list", "devices", "--timeout", "30", "--json-output", devices],
                check=True, capture_output=True,
            )
            self.udid = self.select(devices)
            self.derived = os.path.join(self.cache, "DeviceDerivedData")
            self.products = os.path.join(self.derived, "Build", "Products", "Debug-iphoneos")
            team = self.team()
            self.destination = f"id={self.udid}"
            self.signing = ["-allowProvisioningUpdates", f"DEVELOPMENT_TEAM={team}"]
            self.info = evidence.device_info(devices, self.udid, team)
        self.app = os.path.join(self.products, "Seneca.app")

    @staticmethod
    def select(devices):
        phones = []
        for device in evidence.load(devices)["result"]["devices"]:
            hardware = device.get("hardwareProperties", {})
            if hardware.get("reality") == "physical" and hardware.get("deviceType") == "iPhone":
                phones.append(hardware["udid"])
        wanted = os.environ.get("SENECA_IOS_DEVICE", "")
        if wanted:
            if wanted not in phones:
                raise Failed(f"iPhone {wanted} is not paired; connect it, unlock it, and trust this Mac")
            return wanted
        if len(phones) == 1:
            return phones[0]
        if not phones:
            raise Failed("no physical iPhone is paired; connect it, unlock it, and trust this Mac")
        raise Failed("several iPhones are paired; set SENECA_IOS_DEVICE to one of: " + ", ".join(phones))

    def team(self):
        team = os.environ.get("SENECA_IOS_DEVELOPMENT_TEAM", "")
        app = os.path.join(self.products, "Seneca.app")
        if not team and os.path.isdir(app):
            signed = subprocess.run(["codesign", "-d", "--verbose=4", app], capture_output=True, text=True).stderr
            match = re.search(r"(?m)^TeamIdentifier=(\S+)$", signed)
            if match and match.group(1) != "not":
                team = match.group(1)
        if not team:
            raise Failed("no signing team; set SENECA_IOS_DEVELOPMENT_TEAM, or run just ios-install TEAMID once")
        return team

    def build(self):
        number = str(int(time.time()))
        note(f"building build {number} for the {self.target}")
        log = os.path.join(self.out, "build.log")
        with open(log, "w", encoding="utf-8") as handle:
            done = subprocess.run(
                ["xcodebuild", "build-for-testing", "-project", self.project, "-scheme", "SenecaMobile",
                 "-configuration", "Debug", "-destination", self.destination, "-destination-timeout", "60",
                 "-derivedDataPath", self.derived, f"CURRENT_PROJECT_VERSION={number}", *self.signing],
                stdout=handle, stderr=subprocess.STDOUT,
            )
        if done.returncode != 0 or not os.path.isdir(self.app):
            raise Failed(f"the build failed: {log}")
        if self.target == "device":
            subprocess.run(["codesign", "--verify", "--deep", "--strict", self.app], check=True, capture_output=True)
        return evidence.build_info(self.app, number)

    def phase(self, name, test, expect=None):
        note(f"phone: {name}")
        env = dict(os.environ, TEST_RUNNER_SENECA_SYNC_RUN=self.run)
        if self.folder and self.target == "simulator":
            env["TEST_RUNNER_SENECA_SYNC_FOLDER"] = self.folder
        if expect:
            env["TEST_RUNNER_SENECA_SYNC_EXPECT"] = json.dumps(expect)
        bundle = os.path.join(self.out, f"{name}.xcresult")
        shutil.rmtree(bundle, ignore_errors=True)
        log = os.path.join(self.out, f"test-{name}.log")
        with open(log, "w", encoding="utf-8") as handle:
            done = subprocess.run(
                ["xcodebuild", "test-without-building", "-project", self.project, "-scheme", "SenecaMobile",
                 "-destination", self.destination, "-destination-timeout", "60", "-derivedDataPath", self.derived,
                 f"-only-testing:{SUITE}/{test}", "-resultBundlePath", bundle],
                stdout=handle, stderr=subprocess.STDOUT, env=env,
            )
        if done.returncode != 0:
            with open(log, encoding="utf-8") as handle:
                failures = [line.strip() for line in handle if "error:" in line or "XCTAssert" in line]
            raise Failed(f"the {name} phase failed ({log}):\n  " + "\n  ".join(failures[:12]))
        summary = json.loads(output("xcrun", "xcresulttool", "get", "test-results", "summary", "--path", bundle, "--format", "json"))
        tests = json.loads(output("xcrun", "xcresulttool", "get", "test-results", "tests", "--path", bundle, "--format", "json"))
        return {"reported": self.collect(name), "xcresult": evidence.xcresult(summary, tests)}

    def collect(self, name):
        """What the phase wrote from inside the app."""
        relative = f"Library/Application Support/SenecaEvidence/{self.run}"
        wanted = f"evidence-{name}.json"
        if self.target == "simulator":
            container = output("xcrun", "simctl", "get_app_container", self.udid, BUNDLE_ID, "data")
            path = os.path.join(container, relative, wanted)
        else:
            into = os.path.join(self.out, "container", name)
            shutil.rmtree(into, ignore_errors=True)
            os.makedirs(into)
            done = subprocess.run(
                ["xcrun", "devicectl", "device", "copy", "from", "--device", self.udid, "--timeout", "120",
                 "--domain-type", "appDataContainer", "--domain-identifier", BUNDLE_ID,
                 "--source", f"{relative}/{wanted}", "--destination", os.path.join(into, wanted)],
                capture_output=True, text=True,
            )
            if done.returncode != 0:
                raise Failed(f"could not copy the {name} evidence off the iPhone: {done.stderr.strip()}")
            found = [path for path in glob.glob(os.path.join(into, "**", wanted), recursive=True) if os.path.isfile(path)]
            path = found[0] if found else os.path.join(into, wanted)
        if not os.path.isfile(path):
            raise Failed(f"the {name} phase left no {wanted}")
        return evidence.load(path)


def find_run_folder(target, out, run, cloud_path):
    """Where the Mac sees the folder the phone just made."""
    name = f"seneca-evidence-{run}"
    if target == "simulator":
        return os.path.join(out, "folder")
    drive = os.path.expanduser("~/Library/Mobile Documents/com~apple~CloudDocs")
    if not cloud_path or os.path.basename(cloud_path) != name or ".." in cloud_path.split("/"):
        raise Failed(
            "the folder chosen on the iPhone is not in iCloud Drive; open Seneca, Settings, Sync, "
            "and choose a folder under iCloud Drive"
        )
    folder = os.path.join(drive, cloud_path)
    note(f"waiting for {cloud_path} to reach this Mac through iCloud Drive")
    wait_for(f"{cloud_path} in iCloud Drive on this Mac", lambda: os.path.isfile(os.path.join(folder, "probe.json")))
    return folder


# --- the scenario -------------------------------------------------------------


def run_scenario(target, out):
    os.makedirs(out, exist_ok=True)
    out = os.path.realpath(out)
    for stale in glob.glob(os.path.join(out, "*.json")) + glob.glob(os.path.join(out, "*.log")):
        os.remove(stale)
    run = datetime.datetime.now(datetime.timezone.utc).strftime("%Y%m%dT%H%M%SZ") + f"-{os.getpid()}"
    prints = fingerprints()

    subprocess.run([os.path.join(SCRIPTS, "generate-project.sh")], check=True, capture_output=True)
    phone = Phone(target, out, run)
    build = phone.build()
    seneca = os.path.join(out, "bin", "seneca")
    subprocess.run(["go", "build", "-o", seneca, "."], check=True, cwd=REPO)
    with open(seneca, "rb") as handle:
        seneca_digest = sha256_bytes(handle.read())

    record = {
        "format": FORMAT,
        "format_version": FORMAT_VERSION,
        "target": target,
        "run": run,
        "recorded_at": datetime.datetime.now(datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ"),
        "source_fingerprint": prints["source"],
        "bridge_fingerprint": prints["bridge"],
        "desktop_fingerprint": prints["desktop"],
        "mac": {"os_version": platform.mac_ver()[0], "machine": platform.machine(), "seneca_sha256": seneca_digest},
        "device": phone.info,
        "build": build,
        "phases": {},
        "desktop": {},
    }
    phases, desktop = record["phases"], record["desktop"]
    tests = dict(PHASES)
    desk = Desk(out, seneca)
    folder = None

    def phase(name, expect=None):
        phases[name] = phone.phase(name, tests[name], expect)
        return phases[name]["reported"]

    try:
        if target == "simulator":
            phone.folder = os.path.join(out, "folder")
            shutil.rmtree(phone.folder, ignore_errors=True)
            os.makedirs(phone.folder)

        # The folder: made by the phone, found by the Mac.
        probed = phase("probe")
        folder = find_run_folder(target, out, run, (probed.get("folder") or {}).get("cloud_path"))
        drive = os.path.expanduser("~/Library/Mobile Documents/")
        record["mac"]["folder"] = folder.replace(os.path.expanduser("~"), "~", 1)
        record["mac"]["in_icloud_drive"] = os.path.realpath(folder).startswith(os.path.realpath(drive) + os.sep)
        desk.exchange = folder

        note("mac: enroll and publish")
        desk.must("sync", "enroll", "--name", "mac")
        desk.must("sync", "once")
        joined = phase("join")
        desktop["enrolled"] = {"entries": desk.entries()}

        # An offline reflection reaches the desktop's Markdown and its Git.
        saved = phase("save-offline")
        desk.run("sync", "once")
        desktop["before_publish"] = {"has_entry": desk.has(SAVED)}
        phase("publish")
        note("mac: import the reflection")
        desk.sync_until(f"the reflection of {SAVED}", lambda: desk.has(SAVED))
        desktop["import"] = {
            "date": SAVED, "revision": desk.revision(SAVED), "commits": desk.imports(), "uncommitted": desk.dirty(),
        }
        phase("confirm")

        # A desktop edit returns to the phone.
        note("mac: edit a day by hand")
        desk.edit(EDITED, *DESK_EDIT)
        desk.must("sync", "once")
        desktop["edit"] = {"date": EDITED, "before": joined["entries"].get(EDITED), "revision": desk.revision(EDITED)}
        phase("receive", {"date": EDITED, "revision": desktop["edit"]["revision"]})

        # Both sides edit before either hears of the other.
        note("mac: edit two days the phone is about to edit too")
        desk.edit(SAVED, *DESK_MERGE)
        desk.edit(CONFLICTED, *DESK_CONFLICT)
        desk.must("sync", "once")
        both = phase("simultaneous", {
            "merge_date": SAVED, "conflict_date": CONFLICTED,
            "mac_merge_text": "(desk note)", "mac_conflict_text": "(desk version)",
        })
        note("mac: meet the phone's edits")
        desk.sync_until(
            "the phone's edits",
            lambda: desk.revision(SAVED) == both["merged_revision"] and f"entry:{CONFLICTED}" in desk.must("sync", "conflicts"),
        )
        listed = desk.must("sync", "conflicts")
        named = re.search(rf"(?m)^Conflict (\S+): entry:{CONFLICTED}", listed)
        text = desk.text()
        start = text.index(f"## {CONFLICTED}\n")
        kept = text[start:text.find("\n---\n", start)]
        desktop["simultaneous"] = {
            "merged_revision": desk.revision(SAVED),
            "merged_holds_both": "(desk note)" in desk.must("show", f"--date={SAVED}") and "- added on the phone" in desk.must("show", f"--date={SAVED}"),
            "conflict_id": named.group(1) if named else "",
            "keeps_own": "(desk version)" in kept and PHONE_CONFLICT not in kept,
            "holds_theirs": PHONE_CONFLICT in listed and "(desk version)" in listed,
            "uncommitted": desk.dirty(),
        }

        # A publication interrupted between its contents and its manifest.
        cut = phase("interrupt", {"date": INTERRUPTED})
        partial = [os.path.join(folder, path) for path in cut["published"]]
        note("mac: wait for the half that was sent, and import nothing of it")
        wait_for("the interrupted publication's contents", lambda: all(os.path.exists(path) for path in partial))
        before = desk.run("sync", "once")
        desktop["interrupted"] = {
            "partial_arrived": [os.path.relpath(path, folder) for path in partial],
            "has_entry": desk.has(INTERRUPTED),
            "sync_once_failed": before.returncode != 0,
        }
        resumed = phase("resume")
        manifests = os.path.join(folder, "transactions", resumed["sync"]["device"], f"{resumed['sync']['sequence']:08d}-*.json")
        arrived = wait_for("the finished publication's manifest", lambda: glob.glob(manifests))
        cloud = {"evicted": 0, "reported_downloading": False}
        if target == "device":
            # The files are sent back to iCloud first, so the Mac has to ask
            # for them again: its own half of an interrupted transfer.
            cloud["evicted"] = len(evict(arrived + partial))
            note(f"mac: {cloud['evicted']} arrived files were sent back to iCloud")
        cloud["reported_downloading"] = desk.sync_until(f"the reflection of {INTERRUPTED}", lambda: desk.has(INTERRUPTED))
        desktop["resumed"] = {
            "has_entry": desk.has(INTERRUPTED), "revision": desk.revision(INTERRUPTED), "cloud": cloud,
            "commits": len(desk.imports()), "uncommitted": desk.dirty(),
        }

        # A download interrupted on the phone.
        note("mac: edit another day by hand")
        desk.edit(LATE, *DESK_LATE)
        desk.must("sync", "once")
        desktop["late_edit"] = {"date": LATE, "revision": desk.revision(LATE)}
        phase("receive-interrupted", {"date": LATE, "revision": desktop["late_edit"]["revision"]})

        final = phase("final")
        note("mac: settle")
        recorded = lambda: re.search(r"(?m)^Recorded here: (\d+) change", desk.must("sync", "status"))  # noqa: E731
        desk.sync_until(
            "the phone's receipt",
            lambda: f'applied on "phone": {recorded().group(1)} of {recorded().group(1)} recorded here' in desk.must("sync", "status")
            and "not imported yet: 0" in desk.must("sync", "status"),
        )
        desktop["final"] = {
            "entries": desk.entries(), "status": desk.must("sync", "status").replace(desk.root, "<run>").replace(folder, "<exchange>"),
            "recorded": int(recorded().group(1)), "commits": len(desk.imports()), "uncommitted": desk.dirty(),
            "phone_entries": final["entries"],
        }
    finally:
        desk.close()
        if folder and target == "device" and not os.environ.get("SENECA_SYNC_KEEP"):
            shutil.rmtree(folder, ignore_errors=True)

    found = problems(record, target, prints)
    rejected = os.path.join(out, "sync-rejected.json")
    if found:
        write(rejected, record)
        for problem in found:
            print(f"sync-evidence: {problem}", file=sys.stderr)
        raise Failed(f"the scenario ran and its record does not hold; see {rejected}")
    write(os.path.join(out, "sync.json"), record)
    note(f"{target} sync held end to end: {os.path.join(out, 'sync.json')}")


def write(path, record):
    with open(path, "w", encoding="utf-8") as handle:
        json.dump(record, handle, indent=2, sort_keys=True)
        handle.write("\n")


# --- what a record has to show ------------------------------------------------


def problems(record, target, prints):
    """Everything that keeps a record from proving the exchange end to end."""
    found = []

    def require(condition, message):
        if not condition:
            found.append(message)

    require(record.get("format") == FORMAT and record.get("format_version") == FORMAT_VERSION,
            f"not a {FORMAT} v{FORMAT_VERSION} record")
    require(record.get("target") == target, f"recorded on a {record.get('target')}, not a {target}")
    for name in ("source", "bridge", "desktop"):
        require(record.get(f"{name}_fingerprint") == prints[name],
                f"recorded from {name} sources {record.get(f'{name}_fingerprint')}; they are now {prints[name]}")
    run = record.get("run")
    require(bool(run), "names no run")

    device, mac, build = record.get("device") or {}, record.get("mac") or {}, record.get("build") or {}
    reality = "simulated" if target == "simulator" else "physical"
    require(device.get("reality") == reality, f"the target is {device.get('reality')}, not {reality}")
    require(bool(device.get("udid")), "names no target device")
    require(bool(mac.get("seneca_sha256")), "names no seneca program")
    if target == "device":
        require(bool(device.get("signing_team")), "names no signing team: the device build was not signed")
        require(mac.get("in_icloud_drive") is True, "the Mac's side of the folder is not in iCloud Drive")

    phases = record.get("phases") or {}
    reported = {}
    for name, test in PHASES:
        entry = phases.get(name)
        if not entry:
            found.append(f"{name}: the phase is missing")
            reported[name] = {}
            continue
        result, said = entry.get("xcresult") or {}, entry.get("reported") or {}
        reported[name] = said
        require(result.get("result") == "Passed" and result.get("total") == 1 and result.get("passed") == 1,
                f"{name}: {result.get('passed')} of {result.get('total')} tests passed ({result.get('result')}), want 1 of 1")
        require(result.get("failed") == 0 and result.get("skipped") == 0 and result.get("expected_failures") == 0,
                f"{name}: {result.get('failed')} failed, {result.get('skipped')} skipped")
        require(result.get("tests") == [{"name": f"{test}()", "result": "Passed"}],
                f"{name}: ran {result.get('tests')}, want {test}() passed")
        ran_on = [d.get("id") for d in result.get("devices") or []]
        require(ran_on == [device.get("udid")], f"{name}: ran on {ran_on}, not on {device.get('udid')}")
        require(said.get("phase") == name and said.get("run") == run,
                f"{name}: the app reported phase {said.get('phase')} of run {said.get('run')}")
        require(said.get("simulator") is (target == "simulator"), f"{name}: the app says simulator={said.get('simulator')}")
        require(bool(build.get("number")) and said.get("bundle_build") == build.get("number"),
                f"{name}: the app reports build {said.get('bundle_build')}, built {build.get('number')}")
        ran, built = said.get("executables") or {}, build.get("executables") or {}
        require(len(ran) >= 2, f"{name}: the app reported {len(ran)} executables")
        for binary, value in ran.items():
            require(built.get(binary) == value, f"{name}: {binary} on the target is not the one this Mac built")
        folder = said.get("folder") or {}
        if target == "device":
            require(str(device.get("model")) in str(said.get("device_model")),
                    f"{name}: the app ran on {said.get('device_model')}, not on {device.get('model')}")
            require(folder.get("granted") is True, f"{name}: the folder was not reached through the author's grant")
            require(folder.get("ubiquitous") is True, f"{name}: the folder the phone exchanged through is not in iCloud")

    desktop = record.get("desktop") or {}

    def part(name):
        value = desktop.get(name)
        if not isinstance(value, dict):
            found.append(f"the Mac's {name} step is missing")
            return {}
        return value

    def delivery(name):
        return reported[name].get("delivery") or {}

    def applied_on_mac(name):
        for peer in delivery(name).get("peers") or []:
            if peer.get("name") == "mac":
                return peer.get("applied")
        return None

    # Joined: the same journal on both sides.
    enrolled = part("enrolled")
    require(bool(enrolled.get("entries")) and reported["join"].get("entries") == enrolled.get("entries"),
            "join: the phone does not hold the journal the Mac enrolled")

    # An offline reflection reaches the desktop's Markdown and Git.
    saved, sent = reported["save-offline"], delivery("save-offline")
    require(saved.get("folder_opened") == 0, "save-offline: the folder was opened while saving")
    require(isinstance(sent.get("recorded"), int) and sent.get("published") == sent.get("recorded", 0) - 1,
            f"save-offline: the save was not reported as saved on the phone only: {sent}")
    require(part("before_publish").get("has_entry") is False, "the Mac held the reflection before the phone published it")
    published = delivery("publish")
    require(published.get("recorded") and published.get("published") == published.get("recorded"),
            f"publish: not everything recorded is in the folder: {published}")
    require(applied_on_mac("publish") != published.get("recorded"),
            "publish: the reflection was reported as applied on the Mac before the Mac imported it")
    imported = part("import")
    require(bool(saved.get("revision")) and imported.get("revision") == saved.get("revision"),
            f"the Mac's Markdown holds {imported.get('revision')} for {imported.get('date')}; the phone saved {saved.get('revision')}")
    commits = imported.get("commits") or []
    require(len(commits) == 1 and commits[0].get("subject") == IMPORT_SUBJECT
            and "journal/reflections.md" in (commits[0].get("files") or []),
            f"the import is not one `{IMPORT_SUBJECT}` commit of the reflections file: {commits}")
    require(imported.get("uncommitted") == "", "the Mac's journal has uncommitted changes after the import")
    confirmed = delivery("confirm")
    require(confirmed.get("recorded") and applied_on_mac("confirm") == confirmed.get("recorded"),
            f"confirm: the Mac's receipt does not cover what the phone recorded: {confirmed}")

    # A desktop edit returns.
    edit = part("edit")
    require(bool(edit.get("revision")) and edit.get("revision") != edit.get("before"), "the Mac's edit changed nothing")
    require((reported["receive"].get("entries") or {}).get(edit.get("date")) == edit.get("revision"),
            "receive: the phone does not hold the Mac's edit")

    # Simultaneous edits keep both sides.
    both, theirs = reported["simultaneous"], part("simultaneous")
    require(both.get("merged_holds_both") is True and theirs.get("merged_holds_both") is True,
            "simultaneous: an edit was lost from the day both sides changed in different places")
    require(bool(both.get("merged_revision")) and both.get("merged_revision") == theirs.get("merged_revision"),
            "simultaneous: the two sides merged the same day differently")
    require(both.get("conflict_holds_both") is True and theirs.get("keeps_own") is True and theirs.get("holds_theirs") is True,
            "simultaneous: a version was lost from the day both sides changed in the same place")
    require(bool(theirs.get("conflict_id")) and str(both.get("conflict_id", "")).startswith(theirs.get("conflict_id") or "?"),
            f"simultaneous: the sides name different conflicts: {both.get('conflict_id')} and {theirs.get('conflict_id')}")

    # Interrupted transfers.
    cut, half = reported["interrupt"], part("interrupted")
    sent_before = cut.get("published") or []
    require(cut.get("cancelled") is True and len(sent_before) >= 1 and all(p.startswith("objects/") for p in sent_before),
            f"interrupt: the publication was not stopped between contents and manifest: {sent_before}")
    stopped = delivery("interrupt")
    require(stopped.get("published") == stopped.get("recorded", 0) - 1,
            f"interrupt: an unfinished publication was counted as published: {stopped}")
    require(half.get("partial_arrived") == sent_before, "the half that was published did not reach the Mac")
    require(half.get("has_entry") is False and half.get("sync_once_failed") is False,
            "the Mac applied, or failed on, a publication that was not finished")
    resumed = part("resumed")
    require(resumed.get("has_entry") is True and bool(cut.get("revision")) and resumed.get("revision") == cut.get("revision"),
            "the Mac does not hold the reflection whose publication was finished")
    require(resumed.get("uncommitted") == "", "the Mac's journal has uncommitted changes after the resumed import")
    if target == "device":
        cloud = resumed.get("cloud") or {}
        require((cloud.get("evicted") or 0) >= 1 and cloud.get("reported_downloading") is True,
                f"the Mac was not made to download from iCloud again: {cloud}")
    late, behind = reported["receive-interrupted"], part("late_edit")
    require(late.get("cancelled") is True and late.get("unchanged_between") is True and len(late.get("read_before_cancel") or []) >= 1,
            "receive-interrupted: the download was not stopped part-way, or the journal changed while it was")
    require(bool(behind.get("revision")) and (late.get("entries") or {}).get(behind.get("date")) == behind.get("revision"),
            "receive-interrupted: the phone does not hold the edit whose download was interrupted")

    # In the end both hold the same journal, but for the day they disagree on.
    final, ending = reported["final"], part("final")
    mine, yours = final.get("entries") or {}, ending.get("entries") or {}
    require(len(mine) >= 6 and set(mine) == set(yours), f"final: the sides hold different days: {sorted(mine)} and {sorted(yours)}")
    conflict_date = both.get("conflict_date")
    for date in sorted(set(mine) & set(yours)):
        if date == conflict_date:
            require(mine[date] != yours[date], f"final: the conflicted day {date} was settled by nobody")
        else:
            require(mine[date] == yours[date], f"final: the sides hold different versions of {date}")
    done = delivery("final")
    require(done.get("recorded") and done.get("published") == done.get("recorded") and applied_on_mac("final") == done.get("recorded"),
            f"final: not everything the phone recorded is applied on the Mac: {done}")
    require((final.get("sync") or {}).get("pending") == 0, "final: the phone holds transactions it could not apply")
    recorded = ending.get("recorded")
    require(bool(recorded) and f'applied on "phone": {recorded} of {recorded} recorded here' in str(ending.get("status")),
            "final: the phone's receipt does not cover what the Mac recorded")
    require(ending.get("uncommitted") == "" and (ending.get("commits") or 0) >= 3,
            f"final: the Mac's imports are not all in Git: {ending.get('commits')} commits, uncommitted {ending.get('uncommitted')!r}")
    return found


def main(argv):
    try:
        if len(argv) == 4 and argv[1] == "run" and argv[2] in ("device", "simulator"):
            run_scenario(argv[2], argv[3])
        elif len(argv) == 4 and argv[1] == "check" and argv[3] in ("device", "simulator"):
            if not os.path.isfile(argv[2]):
                sys.exit(f"sync-evidence: {argv[2]} is missing")
            found = problems(evidence.load(argv[2]), argv[3], fingerprints())
            for problem in found:
                print(f"sync-evidence: {argv[2]}: {problem}", file=sys.stderr)
            if found:
                sys.exit(1)
            print(f"sync-evidence: {argv[2]} holds")
        elif len(argv) == 2 and argv[1] == "desktop-fingerprint":
            print(desktop_fingerprint())
        else:
            sys.exit(__doc__)
    except Failed as failure:
        sys.exit(f"sync-evidence: {failure}")
    except subprocess.CalledProcessError as failure:
        sys.exit(f"sync-evidence: {' '.join(map(str, failure.cmd))} failed: {failure.stderr or failure.stdout or ''}")


if __name__ == "__main__":
    main(sys.argv)
