import XCTest

@testable import OpenAssistMac

final class LocalPolicyTests: XCTestCase {
    private var root: URL!
    private var policy: LocalPolicy!

    override func setUpWithError() throws {
        root = FileManager.default.temporaryDirectory
            .appending(path: "oa-policy-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        // temporaryDirectory symlink içerebilir (/var → /private/var) —
        // politika çözülmüş yolla kurulur.
        root = root.resolvingSymlinksInPath()
        policy = LocalPolicy(allowedRoots: [root])
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: root)
    }

    func testInsideRootAllowed() throws {
        let url = try policy.resolve(root.appending(path: "not.txt").path)
        XCTAssertEqual(url.lastPathComponent, "not.txt")
    }

    func testOutsideRootRejected() {
        XCTAssertThrowsError(try policy.resolve("/etc/passwd")) { err in
            guard case LocalPolicy.PolicyError.outsideRoots = err else {
                return XCTFail("outsideRoots beklenirdi: \(err)")
            }
        }
    }

    func testDotDotEscapeRejected() {
        let sneaky = root.appending(path: "alt/../../../../etc/passwd").path
        XCTAssertThrowsError(try policy.resolve(sneaky))
    }

    func testSymlinkEscapeRejected() throws {
        // Kök içinden dışarıya işaret eden symlink: çözülüp reddedilmeli.
        let outside = FileManager.default.temporaryDirectory
            .appending(path: "oa-outside-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: outside, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: outside) }

        let link = root.appending(path: "kacis")
        try FileManager.default.createSymbolicLink(at: link, withDestinationURL: outside)
        XCTAssertThrowsError(try policy.resolve(link.appending(path: "dosya.txt").path))
    }

    func testEmptyPathRejected() {
        XCTAssertThrowsError(try policy.resolve("  "))
    }

    func testTildeExpansion() {
        // ~ genişler; ev dizini köklerde yoksa reddedilir (politika çalışıyor).
        XCTAssertThrowsError(try policy.resolve("~/hicbir-kok-degil.txt"))
    }
}
