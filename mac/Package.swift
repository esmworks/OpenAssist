// swift-tools-version: 6.0
// OpenAssist Mac — ince istemci + runner tek pakette (K7: tek bundle,
// TCC izinleri tek kimliğe bağlanır). `swift build` ile derlenir;
// .app paketleme + Developer ID imza/notarization ayrı adımdır (to-do Faz 6).
import PackageDescription

let package = Package(
    name: "OpenAssistMac",
    platforms: [.macOS(.v14)],
    targets: [
        .executableTarget(
            name: "OpenAssistMac",
            path: "Sources/OpenAssistMac"
        ),
        .testTarget(
            name: "OpenAssistMacTests",
            dependencies: ["OpenAssistMac"],
            path: "Tests/OpenAssistMacTests"
        ),
    ]
)
