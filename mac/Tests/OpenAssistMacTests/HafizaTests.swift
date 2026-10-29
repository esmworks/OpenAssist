import XCTest

@testable import OpenAssistMac

/// Sabit talimatlar: sistem promptuna her koşuda giren, YALNIZ kullanıcının
/// yazabildiği kurallar. Ekranın işi bu ayrımı görünür kılmak.
final class SabitTalimatKontratiTests: XCTestCase {
    func testGovdeCozulur() throws {
        let json = """
        [{"id":"6f0f5a3c-3b7d-4a1e-9a2b-000000000001","scope":"user","kind":"instruction",
          "content":"Onaysız e-posta gönderme","revision":0,
          "created_at":"2026-08-16T20:11:58Z"},
         {"id":"6f0f5a3c-3b7d-4a1e-9a2b-000000000002","scope":"bot",
          "bot_id":"6f0f5a3c-3b7d-4a1e-9a2b-000000000003","kind":"instruction",
          "content":"Taslakları resmi dille yaz","revision":2,
          "created_at":"2026-08-16T20:12:00.123Z"}]
        """
        let got = try API.decoder.decode([MemoryRecord].self, from: Data(json.utf8))
        XCTAssertEqual(got.count, 2)

        // user kapsamı: bot_id ALANI HİÇ GELMEZ (omitempty) — nil kalmalı,
        // boş dize olmamalı; ekran rozeti buna bakıyor.
        XCTAssertNil(got[0].botID)
        XCTAssertEqual(got[0].scopeLabel, "Tüm botlar")
        XCTAssertEqual(got[1].scopeLabel, "Bu bot")
        XCTAssertEqual(got[1].revision, 2)
    }
}

@MainActor
final class SabitTalimatEkraniTests: XCTestCase {
    private func kural(_ metin: String, scope: String = "user") -> MemoryRecord {
        MemoryRecord(id: UUID().uuidString, scope: scope, botID: scope == "bot" ? "b1" : nil,
                     kind: "instruction", content: metin, revision: 0, createdAt: Date())
    }

    private func store(_ list: [MemoryRecord], online: Bool = true) -> DesignStore {
        let s = DesignStore()
        s.live.setOnlineForTesting(online)
        s.live.setInstructionsForTesting(list)
        return s
    }

    /// Boş ekranın SEBEBİ ayrı yazılır. "Bağlı değiliz" ile "kural yok" aynı
    /// boşluk olsaydı kullanıcı kural yazdığını sanıp bağlantıyı kurcalardı —
    /// üstelik burada boş liste "hiçbir yasak yok" anlamına geliyor.
    func testBosEkranSebebiniSoyler() {
        XCTAssertEqual(store([], online: false).memEmptyHint,
                       "Sunucuya bağlı değil — Ayarlar › Bağlantılar")

        let bos = store([])
        XCTAssertEqual(bos.memEmptyHint, "Henüz kural yok — tüm botlar için bir kural yaz")

        // Bot kapsamında metin değişir: kullanıcı "tüm botlar" kurallarının da
        // burada göründüğünü bilmeli, yoksa aynı kuralı ikinci kez yazar.
        bos.memBot = "b1"
        XCTAssertEqual(bos.memEmptyHint,
                       "Bu bot için kural yok (tüm botlara yazılanlar da burada görünür)")

        let dolu = store([kural("Onaysız e-posta gönderme")])
        XCTAssertNil(dolu.memEmptyHint, "satır varken ipucu gösterilmemeli")
        XCTAssertEqual(dolu.instructions.count, 1)
    }

    /// Sunucu hatası (ör. sır kapısı ya da düşmüş bağlantı) boş listeyle
    /// KARIŞMAZ: "kural yok" ile "kuralları okuyamadım" farklı şeyler.
    func testSunucuHatasiIpucuOlarakGosterilir() {
        let s = store([])
        s.live.setInstructionsErrorForTesting("HTTP 500")
        XCTAssertEqual(s.memEmptyHint, "HTTP 500")
    }

    /// Boş/boşluk taslağı istek üretmemeli: yanlışlıkla basılan Enter
    /// hafızaya boş kural yazmasın.
    func testBosTaslakGonderilmez() {
        let s = store([])
        s.memDraft = "   "
        s.addInstruction()
        XCTAssertEqual(s.memDraft, "   ", "boş taslak temizlenmemeli, istek de gitmemeli")
    }
}
