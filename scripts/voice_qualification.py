"""Record and check voice capture and the on-phone model evaluation on the author's iPhone.

    voice_qualification.py install                   build these sources for the paired iPhone and install them
    voice_qualification.py take CASE passed|failed --note TEXT [--lost-seconds N] [--take ID]
    voice_qualification.py latency [--take ID]       time a take of three minutes or more becoming proposed answers
    voice_qualification.py battery start|finish --percent N
    voice_qualification.py waive CASE|latency|battery --note TEXT   the author waives a check; the record says so
    voice_qualification.py evaluation [EXPORT_DIR]   replay the Model Evaluation screen's responses and record them
    voice_qualification.py status                    say what is done and what remains
    voice_qualification.py check                     hold both records; exit 1 naming every gap
    voice_qualification.py cases                     list the take cases and what each means

docs/product/apple-models.md ("Running it on the iPhone") and
docs/mobile/qualification.md ("Voice on the iPhone") are the procedure; this
program keeps two records under docs/mobile/evidence/:

  session-18-device-voice.json        the takes, the latency, and the battery
  session-18-model-evaluation.json    the evaluation's replayed validation report

Every step reads what it records off the iPhone: the build installed (from
devicectl) and the take's sidecar (copied out of the app's container). A
take is only recorded when it was made on a build of these sources installed
on a physical iPhone paired at that moment, after that build was installed.
Whether nothing was lost and whether the transcript is right are the
author's word, said in the note. Neither record holds a word of a transcript
or a response: the sidecar's timings, states and languages, and the
validator's verdicts, only.

Both records name the fingerprints of the sources they were made from; a
change to the app, the bridge, or `seneca` makes them stale and `check`
refuses them. Nothing absent is read as a pass. A check the author waives
is recorded as waived, with the author's reason, and `check` names it as
waived rather than measured.
"""

import argparse
import glob
import os
import shutil
import subprocess
import sys

SCRIPTS = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, SCRIPTS)
sys.path.insert(0, os.path.join(SCRIPTS, "apple-models"))
import evaluation  # noqa: E402
import qualification as q  # noqa: E402

RECORD = os.path.join(q.EVIDENCE, "session-18-device-voice.json")
EVALUATION_RECORD = os.path.join(q.EVIDENCE, "session-18-model-evaluation.json")
FORMAT = "seneca-voice-qualification"
EVALUATION_FORMAT = "seneca-model-evaluation-record"
FORMAT_VERSION = 1
PHONE_AUDIO = "Library/Application Support/Seneca/audio"
EXPORT = os.path.join(q.REPO, ".cache", "apple-models", "export")

INTERRUPTIONS = {
    "force-quit": "the app force-quit from the app switcher while recording; the take is recovered at the next launch",
    "lock": "the iPhone locked while recording and unlocked after; the take carries on and is kept",
    "call": "an incoming call answered while recording; what came before the call is kept",
    "headset": "a headset connected or disconnected while recording; the take is kept",
}
TRANSCRIPTIONS = {
    "en-quiet": "English, in a quiet room",
    "en-noisy": "English, in a noisy room (kitchen, street, music)",
    "fr-quiet": "Québec French, in a quiet room",
    "fr-noisy": "Québec French, in a noisy room",
    "mixed-quiet": "French and English mixed in one take, in a quiet room",
    "mixed-noisy": "French and English mixed in one take, in a noisy room",
}
CASES = {**INTERRUPTIONS, **TRANSCRIPTIONS}
LOCALES = {"en": {"en-US"}, "fr": {"fr-CA"}, "mixed": {"en-US", "fr-CA"}}
MAX_LOST_SECONDS = 1.0
LATENCY_MIN_DURATION = 170
LATENCY_TARGET = 15.0
BATTERY_TAKES = 10
PCC_LEVELS = ("light", "moderate", "deep")


class Failed(Exception):
    pass


def note(message):
    print(f"voice-qualification: {message}", file=sys.stderr, flush=True)


class Record:
    """The voice record, opened against the sources as they are."""

    def __init__(self):
        self.prints = q.fingerprints()
        self.record = q.load(RECORD)

    def require_current(self):
        if not self.record:
            raise Failed(f"no voice record yet; start with `just voice-qualify install` ({RECORD})")
        for name in ("source", "bridge", "desktop"):
            if self.record.get(f"{name}_fingerprint") != self.prints[name]:
                raise Failed(f"the record was made from other {name} sources; install a build of these with "
                             "`just voice-qualify install`, which starts a new record")
        return self.record

    def save(self):
        q.write(RECORD, self.record)

    def phone(self, out):
        """The paired iPhone, which must be the record's and run one of its builds."""
        record = self.require_current()
        device = q.paired_iphone(out)
        if device["udid"] != record["device"]["udid"]:
            raise Failed(f"the paired iPhone is {device['udid']}, the record's is {record['device']['udid']}")
        build = q.installed_build(device["udid"], out)
        builds = {b["number"]: b for b in record.get("builds", [])}
        if build not in builds:
            raise Failed(f"the iPhone runs build {build}, not one of these sources; run `just voice-qualify install`")
        return device, builds[build]


def install():
    """Builds these sources, installs them on the paired iPhone, and opens or extends the record."""
    out = q.scratch("voice-install")
    for step in ("build-bridge.sh", "generate-project.sh"):
        subprocess.run([os.path.join(q.IOS_SCRIPTS, step)], check=True, capture_output=True, text=True)
    prints = q.fingerprints()
    device = q.paired_iphone(out)
    if device.get("reality") != "physical" or device.get("device_type") != "iPhone":
        raise Failed(f"{device['udid']} is not a physical iPhone")
    env = dict(os.environ, SENECA_IOS_DEVICE=device["udid"])
    done = subprocess.run([os.path.join(q.IOS_SCRIPTS, "install-device.sh"), "--launch"], env=env,
                          stdout=subprocess.PIPE, text=True)
    if done.returncode != 0:
        raise Failed("the build or its installation failed; nothing was recorded")
    app = done.stdout.strip().splitlines()[-1]
    number = subprocess.run(["plutil", "-extract", "CFBundleVersion", "raw", "-o", "-", os.path.join(app, "Info.plist")],
                            check=True, capture_output=True, text=True).stdout.strip()
    build = q.evidence.build_info(app, number)
    signed = subprocess.run(["codesign", "-d", "--verbose=4", app], capture_output=True, text=True).stderr
    team = next((line.split("=", 1)[1] for line in signed.splitlines() if line.startswith("TeamIdentifier=")), None)
    if q.installed_build(device["udid"], out) != number:
        raise Failed(f"the iPhone does not report build {number} after installing it")
    if q.fingerprints() != prints:
        raise Failed("the sources changed while building; install again")

    record = q.load(RECORD)
    if record and any(record.get(f"{n}_fingerprint") != prints[n] for n in ("source", "bridge", "desktop")):
        aside = os.path.join(q.CACHE, "results", f"voice-stale-{q.now().replace(':', '')}.json")
        shutil.copy(RECORD, aside)
        note(f"the record was made from other sources; it is kept at {aside} and a new one starts")
        record = None
    if record and record["device"]["udid"] != device["udid"]:
        raise Failed(f"the record is for iPhone {record['device']['udid']}; this is {device['udid']}")
    if not record:
        record = {
            "format": FORMAT, "format_version": FORMAT_VERSION, "opened_at": q.now(),
            "source_fingerprint": prints["source"], "bridge_fingerprint": prints["bridge"],
            "desktop_fingerprint": prints["desktop"], "device": device, "builds": [], "takes": {},
        }
    record["device"] = dict(device, signing_team=team if team and team != "not set" else None)
    record["builds"].append(dict(build, installed_at=q.now()))
    q.write(RECORD, record)
    note(f"build {number} of these sources is on {device['name']}; record the takes with it")


def phone_takes(udid, out):
    """Every take's sidecar on the iPhone now, newest first."""
    into = os.path.join(out, "audio")
    shutil.rmtree(into, ignore_errors=True)
    done = subprocess.run(
        ["xcrun", "devicectl", "device", "copy", "from", "--device", udid, "--timeout", "300",
         "--domain-type", "appDataContainer", "--domain-identifier", q.BUNDLE_ID,
         "--source", PHONE_AUDIO, "--destination", into],
        capture_output=True, text=True,
    )
    if done.returncode != 0:
        raise Failed(f"could not copy the takes' sidecars off the iPhone; unlock it: {done.stderr.strip()}")
    takes = []
    for path in glob.glob(os.path.join(into, "**", "*.json"), recursive=True):
        name = os.path.basename(path)[:-5]
        if len(name) != 36 or name != name.lower():
            continue
        try:
            take = q.evidence.load(path)
        except (OSError, ValueError):
            continue
        if take.get("id") == name:
            takes.append(take)
    return sorted(takes, key=lambda t: t.get("created_at") or "", reverse=True)


def chosen(takes, wanted, build):
    """The take asked for, else the newest; made after `build` was installed."""
    if wanted:
        take = next((t for t in takes if t.get("id") == wanted), None)
        if not take:
            raise Failed(f"no take {wanted} on the iPhone")
    elif takes:
        take = takes[0]
    else:
        raise Failed("the iPhone holds no take; record one in Seneca first")
    made, installed = q.instant(take.get("created_at")), q.instant(build.get("installed_at"))
    if not made or not installed or made < installed:
        raise Failed(f"take {take.get('id')} was recorded before build {build['number']} was installed; record it again")
    return take


def summary(take):
    """What the record keeps of a take: never its words."""
    keep = ("id", "date", "created_at", "stopped_at", "duration_seconds", "byte_count", "locale", "state",
            "transcriber", "transcribed_at", "recovered", "proposal_seconds", "proposal_unavailable")
    out = {key: take.get(key) for key in keep}
    out["transcript_characters"] = len(take.get("transcript") or "")
    return out


def take_case(case, result, text, lost, wanted):
    if case not in CASES:
        raise Failed(f"no case {case}; `voice_qualification.py cases` lists them")
    if not text.strip():
        raise Failed("say what was done and heard with --note")
    if case in INTERRUPTIONS and lost is None:
        raise Failed("say how many seconds of speech were lost with --lost-seconds")
    record = Record()
    out = q.scratch("voice-take")
    device, build = record.phone(out)
    take = chosen(phone_takes(device["udid"], out), wanted, build)
    used = {entry["take"]["id"]: name for name, entry in record.record["takes"].items() if name != case}
    if take["id"] in used:
        raise Failed(f"take {take['id']} already stands for {used[take['id']]}; record a take of its own")
    entry = {"result": result, "at": q.now(), "note": text.strip(), "device": device["udid"],
             "build": build["number"], "take": summary(take)}
    if lost is not None:
        entry["lost_seconds"] = lost
    record.record["takes"][case] = entry
    record.save()
    found = take_problems(case, entry)
    for problem in found:
        note(f"{case}: {problem}")
    note(f"{case}: {result} with take {take['id']} ({take.get('duration_seconds', 0):.0f} s, {take.get('state')}) on build {build['number']}")


def take_problems(case, entry):
    found = []
    take = entry.get("take") or {}
    if entry.get("result") != "passed":
        found.append(f"recorded as {entry.get('result')}")
    if not (entry.get("note") or "").strip():
        found.append("says nothing of what was done and heard")
    if not (take.get("duration_seconds") or 0) > 0 or not (take.get("byte_count") or 0) > 0:
        found.append("the take holds no audio")
    if case in INTERRUPTIONS:
        lost = entry.get("lost_seconds")
        if lost is None or lost > MAX_LOST_SECONDS:
            found.append(f"lost {lost} s of speech; at most {MAX_LOST_SECONDS} s may be lost")
        if case == "force-quit" and take.get("recovered") is not True:
            found.append("the take was not recovered from a partial: the app was not stopped while recording")
    else:
        if take.get("state") != "transcribed" or not take.get("transcript_characters"):
            found.append(f"the take is {take.get('state')}, not transcribed")
        allowed = LOCALES[case.split("-")[0]]
        if take.get("locale") not in allowed:
            found.append(f"transcribed as {take.get('locale')}, not {' or '.join(sorted(allowed))}")
    return found


def latency(wanted):
    record = Record()
    out = q.scratch("voice-latency")
    device, build = record.phone(out)
    takes = phone_takes(device["udid"], out)
    if not wanted:
        takes = [t for t in takes if (t.get("duration_seconds") or 0) >= LATENCY_MIN_DURATION and
                 (t.get("proposal_seconds") is not None or t.get("proposal_unavailable"))]
        if not takes:
            raise Failed(f"no take of {LATENCY_MIN_DURATION} s or more has been sorted into the questions yet; record a "
                         "three-minute take, then Sort Into Your Questions as soon as the transcript shows")
    take = chosen(takes, wanted, build)
    stopped, transcribed = q.instant(take.get("stopped_at")), q.instant(take.get("transcribed_at"))
    proposal, unavailable = take.get("proposal_seconds"), take.get("proposal_unavailable")
    if not stopped or not transcribed or (proposal is None and not unavailable):
        raise Failed(f"take {take['id']} has no stop, transcript, or proposal timing")
    transcription = (transcribed - stopped).total_seconds()
    # Without Apple Intelligence the transcript is the end of the way: the
    # answers are copied from it, and the model is said to be unavailable.
    total = round(transcription + (proposal or 0), 1)
    record.record["latency"] = {
        "at": q.now(), "device": device["udid"], "build": build["number"], "take": summary(take),
        "transcription_seconds": round(transcription, 1),
        "proposal_seconds": round(proposal, 1) if proposal is not None else None,
        "proposal_unavailable": unavailable if proposal is None else None,
        "total_seconds": total, "target_seconds": LATENCY_TARGET, "meets_target": total < LATENCY_TARGET,
    }
    record.save()
    model = f"model {proposal:.1f} s" if proposal is not None else f"no proposal: {unavailable}"
    note(f"a {take['duration_seconds']:.0f} s take reached the questions in {total} s "
         f"(transcription {transcription:.1f} s, {model}); target under {LATENCY_TARGET:.0f} s")


def battery(step, percent):
    if not 0 <= percent <= 100:
        raise Failed("--percent is the battery level iOS shows, 0 to 100")
    record = Record()
    out = q.scratch("voice-battery")
    device, build = record.phone(out)
    if step == "start":
        record.record["battery"] = {"start": {"at": q.now(), "percent": percent, "build": build["number"]}}
        record.save()
        note(f"battery at {percent}%; now record {BATTERY_TAKES} takes of about a minute, transcribe each, then "
             "`voice_qualification.py battery finish --percent N`")
        return
    started = (record.record.get("battery") or {}).get("start")
    if not started:
        raise Failed("start with `voice_qualification.py battery start --percent N`")
    since = q.instant(started["at"])
    takes = [t for t in phone_takes(device["udid"], out) if (q.instant(t.get("created_at")) or since) > since]
    record.record["battery"]["finish"] = {
        "at": q.now(), "percent": percent, "build": build["number"],
        "takes": [summary(t) for t in takes],
    }
    record.save()
    seconds = sum(t.get("duration_seconds") or 0 for t in takes)
    note(f"{len(takes)} takes ({seconds / 60:.1f} min of speech) cost {started['percent'] - percent} points of battery")


WAIVABLE = (*CASES, "latency", "battery")


def waive(name, text):
    if name not in WAIVABLE:
        raise Failed(f"no check {name}; one of {', '.join(WAIVABLE)}")
    if not text.strip():
        raise Failed("say why the check is waived with --note")
    record = Record()
    voice = record.require_current()
    builds = voice.get("builds") or []
    if not builds:
        raise Failed("no build of these sources was installed; run `just voice-qualify install`")
    voice.setdefault("waived", {})[name] = {"at": q.now(), "note": text.strip(), "build": builds[-1]["number"]}
    record.save()
    note(f"{name} is waived by the author on build {builds[-1]['number']}: {text.strip()}")


def waivers(record):
    """The checks the author waived, for these sources' builds."""
    builds = {b.get("number") for b in (record or {}).get("builds") or []}
    return {name: entry for name, entry in ((record or {}).get("waived") or {}).items()
            if name in WAIVABLE and entry.get("build") in builds and (entry.get("note") or "").strip()}


def record_evaluation(export):
    record = Record()
    voice = record.require_current()
    replay_path = os.path.join(export, "replay.json")
    responses_path = os.path.join(export, "responses.json")
    if not os.path.isfile(responses_path):
        raise Failed(f"no {responses_path}; run the screen on the iPhone, then `run.sh pull`")
    out = q.scratch("voice-evaluation")
    seneca = q.built_seneca(out)
    evaluation.replay(export, seneca)
    report = q.evidence.load(replay_path)
    device = report.get("device") or {}
    builds = {b["number"]: b for b in voice.get("builds", [])}
    if device.get("build") not in builds:
        raise Failed(f"the responses came from build {device.get('build')}, not one of these sources")
    entry = dict(report, format=EVALUATION_FORMAT, format_version=FORMAT_VERSION, recorded_at=q.now(),
                 replay_format=report.get("format"), source_fingerprint=record.prints["source"],
                 bridge_fingerprint=record.prints["bridge"], desktop_fingerprint=record.prints["desktop"],
                 udid=voice["device"]["udid"], seneca_from_sources=True)
    q.write(EVALUATION_RECORD, entry)
    note(f"the evaluation is recorded at {EVALUATION_RECORD}")


def problems(record, prints):
    found = []

    def require(condition, message):
        if not condition:
            found.append(message)

    if not record:
        return [f"no voice record at {RECORD}; start with `just voice-qualify install`"] + \
            [f"take {name} is not done: {what}" for name, what in CASES.items()] + \
            ["the latency of a three-minute take is not measured", "the battery cost of ten takes is not measured"]
    require(record.get("format") == FORMAT and record.get("format_version") == FORMAT_VERSION,
            f"not a {FORMAT} v{FORMAT_VERSION} record")
    stale = [n for n in ("source", "bridge", "desktop") if record.get(f"{n}_fingerprint") != prints[n]]
    if stale:
        return [f"the voice record was made from other {', '.join(stale)} sources than these; it is stale: "
                "install a build of these sources and record again"]
    device = record.get("device") or {}
    builds = {b.get("number"): b for b in record.get("builds") or []}
    require(device.get("reality") == "physical" and device.get("device_type") == "iPhone",
            f"the device is {device.get('reality')} {device.get('device_type')}, not a physical iPhone")
    require(bool(device.get("signing_team")), "the build was not signed by a team")
    require(bool(builds), "no build of these sources was installed on the iPhone")

    takes = record.get("takes") or {}
    waived = waivers(record)
    ids = {}
    for name, what in CASES.items():
        entry = takes.get(name)
        if not entry and name in waived:
            continue
        if not entry:
            found.append(f"take {name} is not done: {what}")
            continue
        require(entry.get("device") == device.get("udid"), f"take {name} was made on another iPhone")
        require(entry.get("build") in builds, f"take {name} was made on a build of other sources")
        take_id = (entry.get("take") or {}).get("id")
        require(take_id not in ids, f"take {name} uses the same recording as {ids.get(take_id)}")
        ids[take_id] = name
        found += [f"take {name}: {problem}" for problem in take_problems(name, entry)]

    timing = record.get("latency")
    if not timing and "latency" in waived:
        pass
    elif not timing:
        found.append("the latency of a three-minute take is not measured")
    else:
        require(timing.get("build") in builds, "the latency was measured on a build of other sources")
        require((timing.get("take") or {}).get("duration_seconds", 0) >= LATENCY_MIN_DURATION,
                f"the latency take is shorter than {LATENCY_MIN_DURATION} s")
        require(isinstance(timing.get("total_seconds"), (int, float)), "the latency has no total")

    power = record.get("battery") or {}
    start, finish = power.get("start"), power.get("finish")
    if (not start or not finish) and "battery" in waived:
        pass
    elif not start or not finish:
        found.append("the battery cost of ten takes is not measured")
    else:
        require(start.get("build") in builds and finish.get("build") in builds, "the battery was measured on a build of other sources")
        require(len(finish.get("takes") or []) >= BATTERY_TAKES,
                f"the battery run holds {len(finish.get('takes') or [])} takes, not {BATTERY_TAKES}")
        require(start.get("percent", -1) >= finish.get("percent", 101), "the battery rose during the run")
    return found


def evaluation_problems(entry, voice, prints):
    found = []

    def require(condition, message):
        if not condition:
            found.append(message)

    if not entry:
        return [f"no model evaluation record at {EVALUATION_RECORD}; export, push, run the screen, pull, then "
                "`voice_qualification.py evaluation`"]
    require(entry.get("format") == EVALUATION_FORMAT and entry.get("replay_format") == evaluation.REPLAY_FORMAT,
            f"not a {EVALUATION_FORMAT} record")
    stale = [n for n in ("source", "bridge", "desktop") if entry.get(f"{n}_fingerprint") != prints[n]]
    if stale:
        return [f"the evaluation was recorded from other {', '.join(stale)} sources than these; replay and record it again"]
    device = entry.get("device") or {}
    builds = {b.get("number") for b in (voice or {}).get("builds") or []}
    require(device.get("simulator") is False, "the responses came from a simulator")
    require(device.get("build") in builds, f"the responses came from build {device.get('build')}, not one in the voice record")
    require(entry.get("udid") == ((voice or {}).get("device") or {}).get("udid"), "the evaluation names another iPhone")
    require(entry.get("seneca_from_sources") is True, "the responses were not replayed through seneca built from these sources")
    prompts = entry.get("prompts") or 0
    require(prompts > 0, "no prompts were evaluated")
    results = entry.get("results") or []
    for result in results:
        if result.get("outcome") == "ok":
            require(result.get("validation") in ("valid", "invalid"),
                    f"{result.get('target')} {result.get('prompt_id')}: {result.get('validation') or 'not replayed'}")

    def ran(target, reasoning=None):
        return [r for r in results if r.get("target") == target and r.get("reasoning") == reasoning]

    on_device = entry.get("on_device")
    require(bool(on_device), "the record does not say whether the on-device model was available")
    if on_device == "available":
        require(len(ran("ondevice")) == prompts, f"the on-device model ran {len(ran('ondevice'))} of {prompts} prompts")
    cloud = entry.get("private_cloud")
    require(bool(cloud), "the record does not say whether Private Cloud Compute was reachable")
    if cloud == "available":
        for level in PCC_LEVELS:
            require(len(ran("pcc", level)) == prompts, f"Private Cloud Compute at {level} ran {len(ran('pcc', level))} of {prompts} prompts")
    return found


def gaps():
    prints = q.fingerprints()
    voice = q.load(RECORD)
    return problems(voice, prints) + evaluation_problems(q.load(EVALUATION_RECORD), voice, prints)


def main(argv):
    parser = argparse.ArgumentParser(prog="voice_qualification.py", description=__doc__.splitlines()[0])
    sub = parser.add_subparsers(dest="command", required=True)
    for name in ("install", "status", "check", "cases"):
        sub.add_parser(name)
    taking = sub.add_parser("take")
    taking.add_argument("case")
    taking.add_argument("result", choices=("passed", "failed"))
    taking.add_argument("--note", required=True)
    taking.add_argument("--lost-seconds", type=float)
    taking.add_argument("--take")
    timing = sub.add_parser("latency")
    timing.add_argument("--take")
    power = sub.add_parser("battery")
    power.add_argument("step", choices=("start", "finish"))
    power.add_argument("--percent", type=int, required=True)
    waiving = sub.add_parser("waive")
    waiving.add_argument("check")
    waiving.add_argument("--note", required=True)
    evaluating = sub.add_parser("evaluation")
    evaluating.add_argument("export", nargs="?", default=EXPORT)
    args = parser.parse_args(argv[1:])
    try:
        if args.command == "install":
            install()
        elif args.command == "take":
            take_case(args.case, args.result, args.note, args.lost_seconds, args.take)
        elif args.command == "latency":
            latency(args.take)
        elif args.command == "battery":
            battery(args.step, args.percent)
        elif args.command == "waive":
            waive(args.check, args.note)
        elif args.command == "evaluation":
            record_evaluation(os.path.abspath(args.export))
        elif args.command == "cases":
            for name, what in CASES.items():
                print(f"{name:12} {what}")
        elif args.command == "status":
            found = gaps()
            for name, entry in waivers(q.load(RECORD)).items():
                print(f"  ~ {name} is waived by the author: {entry['note']}")
            print("voice-qualification: everything holds" if not found else "\n".join(f"  - {gap}" for gap in found))
        elif args.command == "check":
            found = gaps()
            for gap in found:
                print(f"voice-qualification: {gap}", file=sys.stderr)
            if found:
                sys.exit(f"voice-qualification: {len(found)} things are not recorded for these sources; "
                         "docs/mobile/qualification.md (Voice on the iPhone) says how to do each")
            waived = waivers(q.load(RECORD))
            if waived:
                print(f"voice-qualification: waived by the author, not measured: {', '.join(waived)}")
            print("voice-qualification: the voice and model evaluation records hold")
    except (Failed, q.Failed, evaluation.Failed, q.sync_evidence.Failed) as failure:
        sys.exit(f"voice-qualification: {failure}")
    except subprocess.CalledProcessError as failure:
        sys.exit(f"voice-qualification: {' '.join(map(str, failure.cmd))} failed: {failure.stderr or failure.stdout or ''}")


if __name__ == "__main__":
    main(sys.argv)
