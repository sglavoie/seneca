[default]
default:
    @just --list

build:
    go build .

install:
    go install .

test:
    go test ./...

vet:
    go vet ./...

cover:
    go test -cover ./...

# Bind the Go engine into the iOS XCFramework, device and simulator slices
ios-bridge:
    ./ios/scripts/build-bridge.sh

# Build the iOS bridge and generate ios/SenecaMobile.xcodeproj
ios-project:
    ./ios/scripts/generate-project.sh

# Build Seneca.app for the simulator
ios-build:
    ./ios/scripts/app.sh build

# Build, install, and launch Seneca.app on a simulator, keeping its journal
ios-run:
    ./ios/scripts/app.sh run

# Run the hosted Swift suite on a simulator, then the round trip across a reinstall
ios-test:
    ./ios/scripts/test.sh "${SENECA_IOS_CACHE:-ios/.cache}/results/suite"
    ./ios/scripts/roundtrip.sh simulator "${SENECA_IOS_CACHE:-ios/.cache}/results/roundtrip-simulator"

# Run the vendored capture package's tests on a simulator: recording, recovery, transcription
ios-capture-test:
    ./ios/scripts/capture-test.sh "${SENECA_IOS_CACHE:-ios/.cache}/results/capture"

# Drive the app's screens on a simulator: drafts across a stop, stale saves, accessibility
ios-uitest:
    ./ios/scripts/uitest.sh "${SENECA_IOS_CACHE:-ios/.cache}/results/uitest"

# Build, sign, install, and launch on your paired iPhone, preserving its data
[positional-arguments]
ios-install team="" device="":
    #!/usr/bin/env bash
    set -euo pipefail
    export SENECA_IOS_DEVELOPMENT_TEAM="${1:-${SENECA_IOS_DEVELOPMENT_TEAM:-}}"
    export SENECA_IOS_DEVICE="${2:-${SENECA_IOS_DEVICE:-}}"
    exec ./ios/scripts/install-device.sh --launch

# Archive for the App Store and export it; --upload sends it to TestFlight
[positional-arguments]
ios-testflight *args:
    ./ios/scripts/testflight.sh "$@"

# Run the round trip on your iPhone across a signed reinstall and record it as evidence
[positional-arguments]
ios-device-evidence team="" device="":
    #!/usr/bin/env bash
    set -euo pipefail
    export SENECA_IOS_DEVELOPMENT_TEAM="${1:-${SENECA_IOS_DEVELOPMENT_TEAM:-}}"
    export SENECA_IOS_DEVICE="${2:-${SENECA_IOS_DEVICE:-}}"
    exec ./ios/scripts/record-device-evidence.sh

# Rehearse the end-to-end exchange on a simulator and this Mac, over a plain folder
ios-sync-rehearsal:
    ./ios/scripts/sync-evidence.sh simulator "${SENECA_IOS_CACHE:-ios/.cache}/results/sync-simulator"

# Run the end-to-end exchange on your iPhone and this Mac through iCloud Drive and record it
[positional-arguments]
ios-sync-evidence team="" device="":
    #!/usr/bin/env bash
    set -euo pipefail
    export SENECA_IOS_DEVELOPMENT_TEAM="${1:-${SENECA_IOS_DEVELOPMENT_TEAM:-}}"
    export SENECA_IOS_DEVICE="${2:-${SENECA_IOS_DEVICE:-}}"
    exec ./ios/scripts/record-sync-evidence.sh

# Have iOS deliver a reminder on your iPhone, and not the next after a save, and record it
[positional-arguments]
ios-notify-evidence team="" device="":
    #!/usr/bin/env bash
    set -euo pipefail
    export SENECA_IOS_DEVELOPMENT_TEAM="${1:-${SENECA_IOS_DEVELOPMENT_TEAM:-}}"
    export SENECA_IOS_DEVICE="${2:-${SENECA_IOS_DEVICE:-}}"
    exec ./ios/scripts/record-notify-evidence.sh

# Write the synthetic three-year journal the release trial runs on into a new directory
mobile-scale-journal dir:
    SENECA_SCALE_JOURNAL="{{ dir }}" go test -count=1 -run '^TestARepresentativeJournalCrossesBetweenTheMacAndThePhoneIntact$' .

# Record the release qualification, e.g. `just mobile-qualify status` (docs/mobile/qualification.md)
[positional-arguments]
mobile-qualify +args:
    python3 -I scripts/qualification.py "$@"

# Record voice and the model evaluation on your iPhone, e.g. `just voice-qualify status` (docs/mobile/qualification.md)
[positional-arguments]
voice-qualify +args:
    python3 -I scripts/voice_qualification.py "$@"

# Run one session's gate of the iPhone plan, e.g. `just mobile-verify 8`
mobile-verify session:
    bash scripts/verify_mobile_session.sh {{ session }}

# Serve the website at http://localhost:8765
website-preview:
    python3 -m http.server 8765 -d website

# Publish the website to Cloudflare Pages (project "seneca", served at seneca.sglavoie.com)
website-deploy:
    npx --yes wrangler@4 pages deploy website --project-name seneca --branch main --commit-dirty=true
