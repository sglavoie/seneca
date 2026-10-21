# Provenance

This package is the recording and on-device transcription part of an earlier
app by the same author, copied into Seneca and adapted. Seneca does not depend
on that app. Its three modules are three targets of one package here:
`CaptureCore`, the platform-neutral engine and its seams; `CaptureApple`, the
AVFoundation recorder and the Speech adapters; and `CaptureTestSupport`, the
simulated recorder and transcriber the tests stand on.

What changed in the copy:

- `AudioFileStore` names a partial `<name>.partial.m4a` after its final file,
  and can finalize a file outside an entry directory, so Seneca keeps
  `audio/<date>/<uuid>.m4a`.
- `withDeadline` lives in `Sources/CaptureApple/Deadline.swift`, internal to
  `CaptureApple`.
- The tests drive `AudioCaptureEngine` through `InMemoryCaptureRecords`, the
  smallest `CaptureRecordStore` the engine accepts. The host supplies its own
  store.
- Playback, capability probing, and background continuation were left out:
  nothing here plays audio or schedules work for when the app is in the
  background.

## Seneca's own

| Here | Why |
| --- | --- |
| `Tests/CaptureCoreTests/HostLayoutTests.swift` | Holds the host-named layout `AudioFileStore` gained here. |

`Package.swift` is new: one package, iOS 26 and macOS 26, Swift 6 language
mode, with no dependency outside Apple's SDKs.

## Network

Nothing here opens a network connection. `SpeechTranscriptionAdapters.swift`
asks iOS, through `AssetInventory`, to download the on-device speech model a
locale needs; iOS performs that download, as it does for keyboard dictation.
Audio and transcripts never leave the device.

## Tests

```sh
swift test --package-path ios/Packages/Capture   # macOS: everything but the iOS-only adapter tests
ios/scripts/capture-test.sh OUTPUT_DIR           # every test, on an iOS simulator
```
