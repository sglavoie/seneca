#!/bin/sh
# Evaluates Apple's Foundation Models against Seneca's real review path.
# Nothing is written: reviews run non-interactively on a copy of the journal.
#
#   scripts/apple-models/run.sh build
#   scripts/apple-models/run.sh weekly  REFLECTIONS.md [ARCHIVE.md] [MODEL_SPEC]
#   scripts/apple-models/run.sh audio
#
# On the iPhone (docs/product/apple-models.md, "Running it on the iPhone"):
#
#   scripts/apple-models/run.sh export  REFLECTIONS.md [ARCHIVE.md] [EXPORT_DIR]
#   scripts/apple-models/run.sh push    [EXPORT_DIR]
#   scripts/apple-models/run.sh pull    [EXPORT_DIR]
#   scripts/apple-models/run.sh replay  [EXPORT_DIR] [RESPONSES.json]
#
# export keeps the prompt and schema Seneca's review hands the harness for
# each week and answers nothing; push and pull copy them onto a paired
# iPhone running a debug build and the responses of its Model Evaluation
# screen back; replay holds each response to `seneca review`'s validator and
# writes EXPORT_DIR/replay.json (default EXPORT_DIR: .cache/apple-models/export).
#
# MODEL_SPEC: ondevice | pcc, plus ",unguided", ",loose" (no date constraints),
# ",light|moderate|deep" (reasoning level). PCC needs the managed
# com.apple.developer.private-cloud-compute entitlement, so it fails from a
# command-line build; run it from the app.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
seneca=$(cd "$here/../.." && pwd)
work=${APPLE_MODELS_WORK:-$seneca/.cache/apple-models}
mkdir -p "$work/bin" "$work/out"
exported=$work/export

build() {
	(cd "$seneca" && go build -o "$work/seneca" .)
	for tool in review_harness transcribe split; do
		xcrun swiftc -parse-as-library -O "$here/$tool.swift" -o "$work/$tool"
	done
	# The review path invokes `claude`; this stand-in answers with Apple's models.
	ln -sf "$work/review_harness" "$work/bin/claude"
}

# split_weeks REFLECTIONS.md DIR: one directory per ISO week of five or more
# entries within one month, holding those entries and the month.
split_weeks() {
	rm -rf "$2" && mkdir -p "$2"
	python3 - "$1" "$2" <<'PY'
import collections, datetime, os, re, sys
text = open(sys.argv[1]).read()
parts = re.split(r'(?m)^(## \d{4}-\d{2}-\d{2})\s*$', text)
weeks = collections.defaultdict(list)
for i in range(1, len(parts), 2):
    day = parts[i][3:]
    weeks[datetime.date.fromisoformat(day).isocalendar()[:2]].append((day, parts[i] + '\n' + parts[i + 1]))
for (year, week), entries in sorted(weeks.items()):
    month = entries[0][0][:7]
    if len(entries) < 5 or any(day[:7] != month for day, _ in entries):
        continue
    d = os.path.join(sys.argv[2], f'{year}-W{week:02d}')
    os.makedirs(d)
    open(os.path.join(d, 'reflections.md'), 'w').write(parts[0] + ''.join(t for _, t in entries))
    open(os.path.join(d, 'month'), 'w').write(month)
PY
}

weekly() {
	journal=$1 archive=${2:-} spec=${3:-ondevice}
	split_weeks "$journal" "$work/weeks"
	log="$work/out/weekly.log"; : > "$log"
	for d in "$work"/weeks/*/; do
		[ -n "$archive" ] && cp "$archive" "$d/seneca-reviews.md"
		name=$(basename "$d")
		if (unset SENECA_EFFORT && cd "$d" && SENECA_FILE="$d/reflections.md" FM_LOG="$log" PATH="$work/bin:$PATH" \
			"$work/seneca" review --month="$(cat "$d/month")" --harness claude --model "$spec" \
			>"$work/out/$name.txt" 2>&1); then
			echo "$name pass"
		else
			echo "$name FAIL: $(grep -m1 -E 'parsing|fm error' "$work/out/$name.txt" | cut -c1-140)"
		fi
	done
	echo "passed $(grep -c ' ok ' "$log") of $(grep -c 'seconds=' "$log"); log: $log"
}

build_seneca() {
	(cd "$seneca" && go build -o "$work/seneca" .)
}

# The `claude` the review path finds first: keeps the prompt, or answers
# with the iPhone's response (evaluation.py).
stub() {
	mkdir -p "$work/bin-$1"
	printf '#!/bin/sh\nexec python3 -I "%s" %s "$@"\n' "$here/evaluation.py" "$1" >"$work/bin-$1/claude"
	chmod +x "$work/bin-$1/claude"
}

export_prompts() {
	journal=$(cd "$(dirname "$1")" && pwd)/$(basename "$1") archive=${2:-} out=${3:-$exported}
	[ -n "$archive" ] && archive=$(cd "$(dirname "$archive")" && pwd)/$(basename "$archive")
	# Absolute, since each review below runs from a scratch directory.
	mkdir -p "$out" && out=$(cd "$out" && pwd)
	build_seneca
	stub capture
	split_weeks "$journal" "$out/windows"
	rm -f "$out/prompts.json" "$out/responses.json" "$out/replay.json"
	for d in "$out"/windows/*/; do
		[ -d "$d" ] || continue
		[ -n "$archive" ] && cp "$archive" "$d/seneca-reviews.md"
		# Each review runs on a copy and stops before writing anything. The
		# stand-in is a CLI harness, which refuses a provider effort.
		scratch=$(mktemp -d)
		cp "$d"/*.md "$scratch/"
		(unset SENECA_EFFORT && cd "$scratch" && SENECA_FILE="$scratch/reflections.md" FM_EXPORT_FILE="$d/captured.json" PATH="$work/bin-capture:$PATH" \
			"$work/seneca" review --month="$(cat "$d/month")" --harness claude --model ondevice \
			</dev/null >"$d/export.log" 2>&1) || true
		rm -rf "$scratch"
	done
	python3 -I "$here/evaluation.py" pack "$out"
}

replay() {
	out=${1:-$exported}
	build_seneca
	python3 -I "$here/evaluation.py" replay "$out" "$work/seneca" ${2:+"$2"}
}

audio() {
	cd "$work/out"
	say -v Samantha -o en.aiff -f "$here/sample-en.txt"
	say -v Amélie -o fr.aiff -f "$here/sample-fr.txt"
	"$work/transcribe" en.aiff en-US >en.transcript
	"$work/transcribe" fr.aiff fr-CA >fr.transcript
	"$work/split" en.transcript "What did I do badly today?" "What did I do well?" "What could I do differently tomorrow?"
	"$work/split" fr.transcript "Qu'est-ce que j'ai mal fait aujourd'hui?" "Qu'est-ce que j'ai bien fait?" "Que pourrais-je faire différemment demain?"
}

cmd=${1:-}; [ $# -gt 0 ] && shift
case $cmd in
build) build ;;
weekly) weekly "$@" ;;
audio) audio ;;
export) export_prompts "$@" ;;
push) python3 -I "$here/evaluation.py" push "${1:-$exported}" ;;
pull) python3 -I "$here/evaluation.py" pull "${1:-$exported}" ;;
replay) replay "$@" ;;
*) sed -n '2,26p' "$0"; exit 2 ;;
esac
