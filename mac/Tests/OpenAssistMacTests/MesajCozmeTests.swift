import XCTest

@testable import OpenAssistMac

/// Message'ın çözülmesi (ADIM 13 regresyonu).
///
/// Bu test bir Swift tuzağı yüzünden var: `let x: T? = nil` diye yazılan alanı
/// sentezlenen init(from:) HİÇ ÇÖZMEZ — sunucu göndermiş olsa bile alan nil
/// kalır. Ek kartları ve grup gönderen adı tam olarak bu yüzden hiç
/// görünmüyordu; kod doğru, model dilsizdi. Altın örnek burada sabitlenir.
final class MesajCozmeTests: XCTestCase {
    func testEklerVeGondericiCozulur() throws {
        let json = """
            {"id":"6C6D8B7E-0A4B-4F3E-9B7C-1E2D3F4A5B6C",
             "role":"assistant","text":"rapor hazır",
             "created_at":"2026-08-17T09:00:00Z",
             "run_id":"1E2D3F4A-5B6C-4D8E-9F00-112233445566",
             "bot_id":"22222222-3333-4444-5555-666677778888",
             "sender":"Finans Botu",
             "attachments":[{"id":"33333333-4444-5555-6666-777788889999",
                             "name":"rapor.pdf","kind":"pdf","size":2048}]}
            """
        let msg = try API.decoder.decode(Message.self, from: Data(json.utf8))

        let ekler = try XCTUnwrap(msg.attachments, "ekler çözülmedi — alanlara varsayılan yazılmış olabilir")
        XCTAssertEqual(ekler.count, 1)
        XCTAssertEqual(ekler[0].name, "rapor.pdf")
        XCTAssertEqual(ekler[0].kind, "pdf")
        XCTAssertEqual(ekler[0].size, 2048)
        XCTAssertEqual(msg.sender, "Finans Botu")
        XCTAssertNotNil(msg.botID)
    }

    /// Eski sunucu (alanları hiç göndermeyen) hâlâ çözülmeli.
    func testEksikAlanlarNilKalir() throws {
        let json = """
            {"id":"6C6D8B7E-0A4B-4F3E-9B7C-1E2D3F4A5B6C","role":"user",
             "text":"selam","created_at":"2026-08-17T09:00:00Z"}
            """
        let msg = try API.decoder.decode(Message.self, from: Data(json.utf8))
        XCTAssertNil(msg.attachments)
        XCTAssertNil(msg.botID)
        XCTAssertNil(msg.sender)
        XCTAssertNil(msg.runID)
    }
}
