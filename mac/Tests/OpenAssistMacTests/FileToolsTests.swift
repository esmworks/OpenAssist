import XCTest

@testable import OpenAssistMac

final class FileToolsTests: XCTestCase {
    private var dir: URL!

    override func setUpWithError() throws {
        dir = FileManager.default.temporaryDirectory
            .appending(path: "oa-tools-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: dir)
    }

    func testWriteThenRead() throws {
        let file = dir.appending(path: "not.txt")
        let receipt = try FileTools.writeFile(at: file, content: "merhaba dünya")
        XCTAssertTrue(receipt.contains("not.txt"))
        XCTAssertEqual(try FileTools.readFile(at: file, maxBytes: 1024), "merhaba dünya")
    }

    func testReadTooBigRejected() throws {
        let file = dir.appending(path: "dev.txt")
        try String(repeating: "a", count: 100).write(to: file, atomically: true, encoding: .utf8)
        XCTAssertThrowsError(try FileTools.readFile(at: file, maxBytes: 10))
    }

    func testListDir() throws {
        try Data().write(to: dir.appending(path: "b.txt"))
        try FileManager.default.createDirectory(at: dir.appending(path: "alt"),
                                                withIntermediateDirectories: false)
        let listing = try FileTools.listDir(at: dir)
        XCTAssertEqual(listing, "alt/\nb.txt")
    }

    func testListMissingDirErrors() {
        XCTAssertThrowsError(try FileTools.listDir(at: dir.appending(path: "yok")))
    }

    func testWriteToMissingParentErrors() {
        let orphan = dir.appending(path: "yok/not.txt")
        XCTAssertThrowsError(try FileTools.writeFile(at: orphan, content: "x"))
    }

    // trashItem testi bilinçli yok: sandbox'lı test koşucusunda kullanıcı
    // Çöp Sepeti'ne erişim güvenilir değil; canlı kanıt uygulama üzerinden.
}
