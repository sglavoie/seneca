"""The Mac's half of the on-phone model evaluation (run.sh export, push, pull, replay).

    evaluation.py capture ARGS...          the `claude` stand-in in export mode
    evaluation.py answer ARGS...           the `claude` stand-in in replay mode
    evaluation.py pack EXPORT_DIR          gather the captured prompts into prompts.json
    evaluation.py push EXPORT_DIR          copy prompts.json onto the paired iPhone
    evaluation.py pull EXPORT_DIR          copy responses.json off it
    evaluation.py replay EXPORT_DIR SENECA [RESPONSES]
                                           hold every response to `seneca review`'s
                                           validator; writes EXPORT_DIR/replay.json
    evaluation.py selftest                 export the synthetic week in testdata/ and
                                           replay a valid, an invalid, a refused, and a
                                           stale response through it; no iPhone

Export runs Seneca's real review path over each window with a stand-in
harness that keeps the prompt and the schema it was handed and answers
nothing, so nothing is reviewed and nothing is written. The debug-only Model
Evaluation screen in the app runs the prompts on Apple's models and writes
responses.json beside them. Replay runs the same review path again with a
stand-in that answers with the phone's response, after checking that the
prompt Seneca builds now is the one the phone was given, so `seneca review`'s
own parser and grounding checks decide what is valid.

replay.json says what was valid, how long each took, and why anything was
not, and holds no response text: it is what device qualification records
(scripts/voice_qualification.py).
"""

import datetime
import hashlib
import json
import os
import re
import shutil
import statistics
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(os.path.dirname(HERE))

PROMPTS_FORMAT = "seneca-apple-models-prompts"
RESPONSES_FORMAT = "seneca-apple-models-responses"
REPLAY_FORMAT = "seneca-apple-models-replay"
BUNDLE_ID = "io.sglavoie.seneca.mobile"
PHONE_DIR = "Library/Application Support/Seneca/evaluation"
PROMPT_CHANGED = 3


class Failed(Exception):
    pass


def sha256(data):
    return hashlib.sha256(data).hexdigest()


def load(path):
    with open(path, encoding="utf-8") as handle:
        return json.load(handle)


def write(path, value):
    partial = path + ".partial"
    with open(partial, "w", encoding="utf-8") as handle:
        json.dump(value, handle, indent=2, sort_keys=True, ensure_ascii=False)
        handle.write("\n")
    os.replace(partial, path)


def harness_args(argv):
    """The schema, the model, and the prompt `seneca review` handed `claude`."""
    schema = model = None
    args = list(argv)
    prompt = args.pop() if args else ""
    for index, arg in enumerate(args):
        if arg == "--json-schema" and index + 1 < len(args):
            schema = args[index + 1]
        if arg == "--model" and index + 1 < len(args):
            model = args[index + 1]
    return schema, model, prompt


def capture(argv):
    schema, _, prompt = harness_args(argv)
    target = os.environ.get("FM_EXPORT_FILE")
    if not target or not schema or not prompt:
        sys.exit("evaluation capture: no FM_EXPORT_FILE, schema, or prompt")
    write(target, {"prompt": prompt, "schema": schema})
    # Answering nothing makes the review stop before it writes anything.
    sys.exit("evaluation capture: prompt exported; no review is made")


def answer(argv):
    _, model, prompt = harness_args(argv)
    responses = load(os.environ["FM_RESPONSES"])
    result = responses["results"][int(model)]
    if sha256(prompt.encode()) != result.get("prompt_sha256"):
        print("evaluation answer: the prompt seneca builds now is not the one the iPhone ran", file=sys.stderr)
        sys.exit(PROMPT_CHANGED)
    print(result.get("text") or "")


def pack(export):
    windows = os.path.join(export, "windows")
    prompts = []
    for name in sorted(os.listdir(windows)):
        captured = os.path.join(windows, name, "captured.json")
        if not os.path.isfile(captured):
            raise Failed(f"window {name} exported no prompt; see {os.path.join(windows, name, 'export.log')}")
        found = load(captured)
        prompts.append({"id": name, "prompt": found["prompt"], "schema": found["schema"],
                        "sha256": sha256(found["prompt"].encode())})
    if not prompts:
        raise Failed("no window has five or more entries within one month")
    write(os.path.join(export, "prompts.json"), {
        "format": PROMPTS_FORMAT, "format_version": 1,
        "exported_at": datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="seconds"),
        "prompts": prompts,
    })
    print(f"evaluation: {len(prompts)} prompts in {os.path.join(export, 'prompts.json')}")


def phone():
    """The paired iPhone: SENECA_IOS_DEVICE, else the only one."""
    with tempfile.TemporaryDirectory() as scratch:
        listing = os.path.join(scratch, "devices.json")
        subprocess.run(["xcrun", "devicectl", "list", "devices", "--timeout", "30", "--json-output", listing],
                       check=True, capture_output=True)
        phones = []
        for device in load(listing)["result"]["devices"]:
            hardware = device.get("hardwareProperties", {})
            if hardware.get("reality") == "physical" and hardware.get("deviceType") == "iPhone":
                phones.append(hardware["udid"])
    wanted = os.environ.get("SENECA_IOS_DEVICE")
    if wanted:
        if wanted not in phones:
            raise Failed(f"iPhone {wanted} is not paired")
        return wanted
    if len(phones) != 1:
        raise Failed("pair exactly one iPhone, or set SENECA_IOS_DEVICE" if phones else "no iPhone is paired")
    return phones[0]


def devicectl_copy(direction, udid, phone_path, mac_path):
    flags = ["--source", mac_path, "--destination", phone_path] if direction == "to" else \
        ["--source", phone_path, "--destination", mac_path]
    done = subprocess.run(
        ["xcrun", "devicectl", "device", "copy", direction, "--device", udid, "--timeout", "120",
         "--domain-type", "appDataContainer", "--domain-identifier", BUNDLE_ID] + flags,
        capture_output=True, text=True,
    )
    if done.returncode != 0:
        raise Failed(f"devicectl could not copy {direction} the iPhone (unlock it, and install a debug build): {done.stderr.strip()}")


def push(export):
    prompts = os.path.join(export, "prompts.json")
    if not os.path.isfile(prompts):
        raise Failed(f"no {prompts}; run `run.sh export` first")
    udid = phone()
    devicectl_copy("to", udid, f"{PHONE_DIR}/prompts.json", prompts)
    # An iPhone that cannot run Apple's model offers no screen; relaunching
    # the app has it record the device's answer by itself. On an eligible
    # iPhone the relaunch does nothing and the author runs the screen.
    subprocess.run(["xcrun", "devicectl", "device", "process", "launch", "--device", udid,
                    "--terminate-existing", BUNDLE_ID], capture_output=True, text=True)
    print("evaluation: the prompts are on the iPhone. On an iPhone that runs Apple Intelligence, open "
          "Settings > Model Evaluation and run it; on one that cannot, the relaunched app has recorded "
          "its answer: `run.sh pull` once it has opened")


def pull(export):
    target = os.path.join(export, "responses.json")
    with tempfile.TemporaryDirectory() as scratch:
        landed = os.path.join(scratch, "responses.json")
        devicectl_copy("from", phone(), f"{PHONE_DIR}/responses.json", landed)
        if os.path.isdir(landed):
            found = [os.path.join(d, f) for d, _, files in os.walk(landed) for f in files if f == "responses.json"]
            if not found:
                raise Failed("the iPhone has no responses yet")
            landed = found[0]
        shutil.copy(landed, target)
    print(f"evaluation: responses copied to {target}")


def reason(text):
    """What seneca said was wrong, without anything quoted from the response."""
    lines = [line.strip() for line in text.splitlines() if line.strip()]
    said = next((line for line in lines if re.search(r"pars|invalid|missing|cites|must|empty|schema|unexpected", line, re.I)),
                lines[-1] if lines else "")
    return re.sub(r'"[^"]*"|“[^”]*”|\'[^\']*\'', "…", said)[:240]


def replay(export, seneca, responses_path=None):
    responses_path = responses_path or os.path.join(export, "responses.json")
    prompts_path = os.path.join(export, "prompts.json")
    prompts, responses = load(prompts_path), load(responses_path)
    if responses.get("format") != RESPONSES_FORMAT or responses.get("format_version") != 1:
        raise Failed(f"{responses_path} is not a {RESPONSES_FORMAT} v1 file")
    with open(prompts_path, "rb") as handle:
        prompts_digest = sha256(handle.read())
    if responses.get("prompts_sha256") != prompts_digest:
        raise Failed("the responses were made from other prompts than these; push these and run again")
    by_id = {p["id"]: p for p in prompts["prompts"]}

    bin_dir = tempfile.mkdtemp(prefix="replay-bin-")
    stub = os.path.join(bin_dir, "claude")
    with open(stub, "w", encoding="utf-8") as handle:
        handle.write(f'#!/bin/sh\nexec python3 -I "{os.path.abspath(__file__)}" answer "$@"\n')
    os.chmod(stub, 0o755)

    results = []
    for index, result in enumerate(responses.get("results") or []):
        entry = {key: result.get(key) for key in ("prompt_id", "target", "reasoning", "outcome", "seconds", "prompt_tokens")}
        entry["validation"] = None
        entry["reason"] = None
        if result.get("prompt_id") not in by_id or by_id[result["prompt_id"]]["sha256"] != result.get("prompt_sha256"):
            entry["validation"], entry["reason"] = "prompt-changed", "the response names a prompt this export does not hold"
        elif result.get("outcome") != "ok":
            entry["reason"] = reason(result.get("error") or "")
        else:
            window = os.path.join(export, "windows", result["prompt_id"])
            with tempfile.TemporaryDirectory() as copy:
                for name in ("reflections.md", "seneca-reviews.md"):
                    if os.path.isfile(os.path.join(window, name)):
                        shutil.copy(os.path.join(window, name), copy)
                with open(os.path.join(window, "month"), encoding="utf-8") as handle:
                    month = handle.read().strip()
                env = dict(os.environ, SENECA_FILE=os.path.join(copy, "reflections.md"), FM_RESPONSES=os.path.abspath(responses_path),
                           PATH=bin_dir + os.pathsep + os.environ.get("PATH", ""))
                # The stand-in is a CLI harness, which refuses a provider effort.
                for name in ("SENECA_HARNESS", "SENECA_EFFORT"):
                    env.pop(name, None)
                done = subprocess.run([seneca, "review", f"--month={month}", "--harness", "claude", "--model", str(index)],
                                      cwd=copy, env=env, capture_output=True, text=True, stdin=subprocess.DEVNULL)
            if done.returncode == 0:
                entry["validation"] = "valid"
            elif "not the one the iPhone ran" in done.stderr:
                entry["validation"], entry["reason"] = "prompt-changed", "seneca builds a different prompt for this window now"
            else:
                entry["validation"], entry["reason"] = "invalid", reason(done.stderr + "\n" + done.stdout)
        results.append(entry)
    shutil.rmtree(bin_dir, ignore_errors=True)

    summary = {}
    for entry in results:
        key = entry["target"] + (f"/{entry['reasoning']}" if entry.get("reasoning") else "")
        group = summary.setdefault(key, {"runs": 0, "ok": 0, "valid": 0, "invalid": 0, "refused": 0, "other": 0, "seconds": []})
        group["runs"] += 1
        if entry["outcome"] == "ok":
            group["ok"] += 1
            group["seconds"].append(entry["seconds"])
        elif entry["outcome"] == "refused":
            group["refused"] += 1
        else:
            group["other"] += 1
        if entry["validation"] == "valid":
            group["valid"] += 1
        elif entry["validation"] in ("invalid", "prompt-changed"):
            group["invalid"] += 1
    for group in summary.values():
        seconds = group.pop("seconds")
        group["median_seconds"] = round(statistics.median(seconds), 1) if seconds else None
        group["max_seconds"] = max(seconds) if seconds else None

    with open(seneca, "rb") as handle:
        seneca_digest = sha256(handle.read())
    report = {
        "format": REPLAY_FORMAT, "format_version": 1,
        "replayed_at": datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="seconds"),
        "prompts_sha256": prompts_digest,
        "responses_sha256": sha256(open(responses_path, "rb").read()),
        "seneca_sha256": seneca_digest,
        "prompts": len(prompts["prompts"]),
        "device": responses.get("device"),
        "on_device": responses.get("on_device"),
        "private_cloud": responses.get("private_cloud"),
        "started": responses.get("started"),
        "finished": responses.get("finished"),
        "results": results,
        "summary": summary,
    }
    write(os.path.join(export, "replay.json"), report)
    for key, group in sorted(summary.items()):
        print(f"{key}: {group['valid']} of {group['runs']} valid, {group['refused']} refused, "
              f"median {group['median_seconds']} s, max {group['max_seconds']} s")
    if not summary:
        print(f"no responses: on-device {responses.get('on_device')}, Private Cloud Compute {responses.get('private_cloud')}")
    print(f"evaluation: {os.path.join(export, 'replay.json')}")


def selftest():
    """Export and replay on a synthetic week, with responses written here."""
    work = tempfile.mkdtemp(prefix="apple-models-selftest-")
    export = os.path.join(work, "export")
    env = dict(os.environ, APPLE_MODELS_WORK=work)
    subprocess.run(["sh", os.path.join(HERE, "run.sh"), "export", os.path.join(HERE, "testdata", "reflections.md"), "", export],
                   check=True, env=env)
    with open(os.path.join(export, "prompts.json"), "rb") as handle:
        raw = handle.read()
    prompts = json.loads(raw)["prompts"]
    if [p["id"] for p in prompts] != ["2026-W10"]:
        raise Failed(f"the synthetic week exported {[p['id'] for p in prompts]}")
    prompt = prompts[0]
    if "Date: 2026-03-02" not in prompt["prompt"] or json.loads(prompt["schema"]).get("type") != "object":
        raise Failed("the exported prompt or schema is not the review's")
    item = {"theme": "Rushing", "summary": "Mornings were rushed.", "evidence": [{"date": "2026-03-02", "snippet": "rushed the first task"}]}
    valid = {"focus_assessment": None, "struggles": [item], "wins": [], "carry_forward": [], "focus_recommendation": "Start slowly."}
    invented = dict(valid, struggles=[dict(item, evidence=[{"date": "2022-08-30", "snippet": "rushed"}])])

    def result(outcome, text=None, digest=None, error=None):
        return {"prompt_id": prompt["id"], "prompt_sha256": digest or prompt["sha256"], "target": "ondevice", "reasoning": None,
                "outcome": outcome, "seconds": 1.0, "text": text, "error": error, "prompt_tokens": 10}

    responses = {
        "format": RESPONSES_FORMAT, "format_version": 1, "prompts_sha256": sha256(raw),
        "device": {"model": "selftest", "system": "none", "simulator": True, "build": "0"},
        "on_device": "available", "private_cloud": "not-entitled", "started": None, "finished": None,
        "results": [result("ok", json.dumps(valid)), result("ok", json.dumps(invented)),
                    result("refused", error='refusal("quoted words")'), result("ok", json.dumps(valid), digest="0" * 64)],
    }
    write(os.path.join(export, "responses.json"), responses)
    seneca = os.path.join(work, "seneca")
    subprocess.run(["go", "build", "-o", seneca, "."], cwd=REPO, check=True)
    replay(export, seneca)
    report = load(os.path.join(export, "replay.json"))
    got = [(r["outcome"], r["validation"]) for r in report["results"]]
    want = [("ok", "valid"), ("ok", "invalid"), ("refused", None), ("ok", "prompt-changed")]
    if got != want:
        raise Failed(f"replay judged {got}, want {want}")
    if "2022-08-30" not in (report["results"][1]["reason"] or "") or "quoted words" in json.dumps(report):
        raise Failed("the replay report does not say why, or quotes a response")
    if any("text" in r for r in report["results"]):
        raise Failed("the replay report holds response text")
    # The prompt Seneca builds is checked against the one the phone ran.
    with open(os.path.join(export, "windows", "2026-W10", "reflections.md"), "a", encoding="utf-8") as handle:
        handle.write("\n## 2026-03-09\n\n### What did I do well?\nSynthetic: a day the phone never saw.\n")
    replay(export, seneca)
    changed = [r["validation"] for r in load(os.path.join(export, "replay.json"))["results"]]
    if changed[0] != "prompt-changed":
        raise Failed(f"a window changed after export replayed as {changed[0]}")
    shutil.rmtree(work, ignore_errors=True)
    print("evaluation: selftest held: export, replay, and the stale-prompt check")


def main(argv):
    try:
        command = argv[1] if len(argv) > 1 else ""
        if command == "capture":
            capture(argv[2:])
        elif command == "answer":
            answer(argv[2:])
        elif command == "pack" and len(argv) == 3:
            pack(argv[2])
        elif command == "push" and len(argv) == 3:
            push(argv[2])
        elif command == "pull" and len(argv) == 3:
            pull(argv[2])
        elif command == "selftest":
            selftest()
        elif command == "replay" and len(argv) in (4, 5):
            replay(argv[2], argv[3], argv[4] if len(argv) == 5 else None)
        else:
            sys.exit(__doc__)
    except Failed as failure:
        sys.exit(f"evaluation: {failure}")


if __name__ == "__main__":
    main(sys.argv)
