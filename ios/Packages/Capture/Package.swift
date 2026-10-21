// swift-tools-version: 6.0
//
// Recording and on-device transcription, taken from an earlier app by
// the same author (see PROVENANCE.md). CaptureCore is the platform-neutral engine and its seams,
// CaptureApple the AVFoundation recorder and the Speech adapters, and
// CaptureTestSupport the simulated recorder and transcriber tests stand on.
import PackageDescription

let package = Package(
    name: "Capture",
    platforms: [.iOS("26.0"), .macOS("26.0")],
    products: [
        .library(name: "CaptureCore", targets: ["CaptureCore"]),
        .library(name: "CaptureApple", targets: ["CaptureApple"]),
        .library(name: "CaptureTestSupport", targets: ["CaptureTestSupport"]),
    ],
    targets: [
        .target(name: "CaptureCore"),
        .target(name: "CaptureApple", dependencies: ["CaptureCore"]),
        .target(name: "CaptureTestSupport", dependencies: ["CaptureCore"]),
        .testTarget(name: "CaptureCoreTests", dependencies: ["CaptureCore"]),
        .testTarget(name: "CaptureAppleTests", dependencies: ["CaptureApple", "CaptureCore"]),
        .testTarget(name: "CaptureTestSupportTests", dependencies: ["CaptureTestSupport", "CaptureCore"]),
    ],
    swiftLanguageModes: [.v6]
)
