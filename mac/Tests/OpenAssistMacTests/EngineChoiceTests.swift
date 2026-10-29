import SwiftUI
import XCTest

@testable import OpenAssistMac

/// Testlerde motor kurmanın kısa yolu — durum alanları varsayılan "sağlıklı".
func engineFixture(id: String, name: String, models: [EngineInfo.Model],
                   available: Bool = true, loggedIn: Bool = true,
                   probed: Bool = true, detail: String = "hazır") -> EngineInfo {
    EngineInfo(id: id, name: name, available: available, version: "1.0",
               loggedIn: loggedIn, account: nil, plan: nil, detail: detail,
               probed: probed, checkedAt: Date(), models: models)
}

/// Bot başına motor + model seçimi (K18). Bu eşleme yanlışsa kullanıcı
/// ayarda başarı görür ama motorun tanımadığı bir ad bota yazılır ve her
/// koşu sessizce düşer — testin konusu tam olarak budur.
final class EngineChoiceTests: XCTestCase {
    // Sunucuda tek motor var (pi); ikinci motor kurgusaldır — eşlemenin
    // motora göre süzdüğünü kanıtlamak için gerekir.
    private let pi = engineFixture(id: "pi", name: "Pi", models: [
        EngineInfo.Model(id: "", label: "Varsayılan — motorun kendi seçimi"),
        EngineInfo.Model(id: "minimax/MiniMax-M2.7", label: "MiniMax · MiniMax-M2.7"),
    ])
    private let ikinci = engineFixture(id: "ikinci", name: "İkinci", models: [
        EngineInfo.Model(id: "deepseek-v4-flash", label: "DeepSeek V4 Flash"),
    ])

    func testMotorKimlikleSecilir() {
        let e = EngineInfo.resolve("ikinci", in: [pi, ikinci])
        XCTAssertEqual(e?.id, "ikinci")
        XCTAssertEqual(e?.models.count, 1)
    }

    /// Model listesi motora göre filtrelidir: başka motorun modeli sızmaz.
    func testModelListesiMotoraGore() {
        let secili = EngineInfo.resolve("pi", in: [pi, ikinci])
        XCTAssertFalse(secili?.models.contains { $0.id == "deepseek-v4-flash" } ?? true,
                       "başka motorun modeli listeye sızdı — koşu düşer")
    }

    /// Eski kayıtta motor boş kalabilir; ilk motora düşülür.
    func testBosMotorIlkMotoraDuser() {
        XCTAssertEqual(EngineInfo.resolve("", in: [pi, ikinci])?.id, "pi")
    }

    /// Sunucuda artık kayıtlı olmayan motor: uydurma yapılmaz, nil döner —
    /// UI ham kimliği gösterir, model listesi boş kalır.
    func testTaninmayanMotorNilDoner() {
        XCTAssertNil(EngineInfo.resolve("kimi", in: [pi]))
        XCTAssertNil(EngineInfo.resolve("kimi", in: []))
    }

    func testModelEtiketi() {
        XCTAssertEqual(EngineInfo.modelLabel("minimax/MiniMax-M2.7", in: pi),
                       "MiniMax · MiniMax-M2.7")
        XCTAssertEqual(EngineInfo.modelLabel("", in: pi), "varsayılan")
        // Elle sabitlenmiş tam ad listede yok — ham hâliyle gösterilir.
        XCTAssertEqual(EngineInfo.modelLabel("minimax/yeni-model", in: pi), "minimax/yeni-model")
        XCTAssertEqual(EngineInfo.modelLabel("minimax/MiniMax-M2.7", in: nil),
                       "minimax/MiniMax-M2.7")
    }

    /// Sunucu kontratı: /api/engines gövdesi olduğu gibi çözülmeli —
    /// durum alanları dahil (Modeller ekranı bunları gösterir).
    func testSunucuGovdesiCozulur() throws {
        let json = """
        [{"id":"pi","name":"Pi","available":true,"version":"0.84.2",
          "logged_in":true,"account":"MiniMax","plan":null,
          "detail":"kurulu — 6 model seçilebilir","probed":true,
          "checked_at":"2026-08-14T15:27:12.453702+03:00",
          "models":[{"id":"","label":"Varsayılan"},{"id":"minimax/M2.7","label":"M2.7"}]}]
        """
        // API.decoder ile: gerçek yol bu. Tarih çözülemezse istek `try?`
        // içinde sessizce düşer ve Modeller ekranı boş kalır — mikrosaniyeli
        // ve saat dilimli Go formatı burada sabitlenir.
        let got = try API.decoder.decode([EngineInfo].self, from: Data(json.utf8))
        let e = try XCTUnwrap(got.first)
        XCTAssertEqual(e.name, "Pi")
        XCTAssertEqual(e.version, "0.84.2")
        XCTAssertTrue(e.available && e.loggedIn && e.probed)
        XCTAssertEqual(e.models.map(\.id), ["", "minimax/M2.7"])
        XCTAssertEqual(e.health, .ready)
    }

    // ── Modeller ekranı: motor durum ışığı ──
    //
    // Işık yanlışsa ekran yalan söyler: kurulu olmayan motoru yeşil
    // göstermek, koşu düşene kadar hatayı gizlemek demektir.

    func testIsikHazirMotorYesil() {
        XCTAssertEqual(pi.health, .ready)
        XCTAssertEqual(ModelsView.dotColor(.ready), Color(hex: "#28c840"))
    }

    func testIsikKimliksizMotorSari() {
        let e = engineFixture(id: "x", name: "X", models: [], loggedIn: false,
                              detail: "kimlik yok — `claude login` gerekir")
        XCTAssertEqual(e.health, .warning)
    }

    func testIsikKurulmamisMotorKirmizi() {
        let e = engineFixture(id: "x", name: "X", models: [], available: false,
                              loggedIn: false, detail: "bulunamadı")
        XCTAssertEqual(e.health, .missing)
    }

    /// Yoklanamayan motor "hazır" sayılmaz: durumu bilinmiyor, sarı yanar.
    func testYoklanmayanMotorSari() {
        let e = engineFixture(id: "x", name: "X", models: [], probed: false,
                              detail: "durum yoklaması desteklenmiyor")
        XCTAssertEqual(e.health, .warning)
    }

    /// Motor değişince model sıfırlanmalı — gövdede model alanı "" gitmeli
    /// (nil gitseydi sunucu eski modeli korurdu, yeni motorda geçersiz).
    func testMotorDegisinceModelSifirlanir() throws {
        let body = BotUpdate(engine: "ikinci", model: "")
        let obj = try JSONSerialization.jsonObject(
            with: JSONEncoder().encode(body)) as? [String: Any]
        XCTAssertEqual(obj?["engine"] as? String, "ikinci")
        XCTAssertEqual(obj?["model"] as? String, "")
    }
}

/// Bot düzenleme modalındaki motor + model seçimi.
///
/// Regresyon: model listesi sabit tek bir motor kimliğinden geliyordu. Başka
/// motorun botu düzenlenirken bile o motorun modelleri listeleniyor, seçilen
/// ad geçersiz olduğu için her koşu düşüyordu. Üstelik modalda motor seçimi
/// hiç yoktu — motoru değiştirmek için Ayarlar'a gitmek gerekiyordu.
@MainActor
final class YeniBotMotorSecimiTests: XCTestCase {
    private func store() -> DesignStore {
        let s = DesignStore()
        s.live.setEnginesForTesting([
            engineFixture(id: "pi", name: "Pi", models: [
                EngineInfo.Model(id: "", label: "Varsayılan"),
                EngineInfo.Model(id: "minimax/M2.7", label: "MiniMax · M2.7"),
            ]),
            engineFixture(id: "ikinci", name: "İkinci", models: [
                EngineInfo.Model(id: "", label: "Varsayılan"),
                EngineInfo.Model(id: "baska/kimi-k3", label: "kimi-k3 — Başka"),
            ]),
        ])
        return s
    }

    func testModelListesiSeciliMotoruIzler() {
        let s = store()
        s.nbEngine = "ikinci"
        XCTAssertEqual(s.newBotModels.map(\.id), ["", "baska/kimi-k3"])
        XCTAssertFalse(s.newBotModels.contains { $0.id == "minimax/M2.7" },
                       "başka motorun modeli listeye sızdı — koşu düşerdi")

        s.nbEngine = "pi"
        XCTAssertTrue(s.newBotModels.contains { $0.id == "minimax/M2.7" })
    }

    /// Motor değişince model sıfırlanmalı: bir motorun adı ötekinde geçersizdir.
    func testMotorDegisinceModelSifirlanir() {
        let s = store()
        s.nbEngine = "pi"
        s.nbModel = "minimax/M2.7"
        s.setNBEngine("ikinci")
        XCTAssertNil(s.nbModel, "eski motorun modeli taşındı — her koşu düşerdi")
        XCTAssertEqual(s.nbEngineID, "ikinci")
    }

    /// Aynı motor yeniden seçilirse model korunur (kazara sıfırlama yok).
    func testAyniMotorModeliKorur() {
        let s = store()
        s.nbEngine = "ikinci"
        s.nbModel = "baska/kimi-k3"
        s.setNBEngine("ikinci")
        XCTAssertEqual(s.nbModel, "baska/kimi-k3")
    }

    /// Motor seçilmemişse (yeni bot) sunucu varsayılanına düşülür.
    func testSecilmemisMotorVarsayilanaDuser() {
        let s = store()
        XCTAssertNil(s.nbEngine)
        XCTAssertEqual(s.nbEngineID, "pi")
        XCTAssertEqual(s.nbEngineLabel, "Pi")
    }
}

/// Aramalı menü süzmesi. Katalogda bir sağlayıcının 80'den çok modeli olabilir;
/// kaydırmasız düz bir menüde alttaki girdilere hiç ulaşılamıyordu — model
/// seçilemiyordu.
final class SearchableMenuTests: XCTestCase {
    private let items: [SearchableMenu.Item] = [
        .init(id: "", label: "Varsayılan — motorun kendi seçimi"),
        .init(id: "openrouter/claude-opus-4-6", label: "claude-opus-4-6 — OpenRouter"),
        .init(id: "moonshot/kimi-k3", label: "kimi-k3 — Moonshot"),
        .init(id: "moonshot/grok-4.5", label: "grok-4.5 — Moonshot"),
    ]

    func testBosSorguHepsiniVerir() {
        XCTAssertEqual(SearchableMenu.filtre(items, "").count, 4)
        XCTAssertEqual(SearchableMenu.filtre(items, "   ").count, 4)
    }

    func testEtiketeGoreSuzer() {
        let r = SearchableMenu.filtre(items, "kimi")
        XCTAssertEqual(r.map(\.id), ["moonshot/kimi-k3"])
    }

    /// Kullanıcı sağlayıcıya göre de arayabilmeli — kimlik alanı da taranır.
    func testKimligeGoreSuzer() {
        let r = SearchableMenu.filtre(items, "moonshot")
        XCTAssertEqual(r.count, 2)
    }

    func testBuyukKucukHarfDuyarsiz() {
        XCTAssertEqual(SearchableMenu.filtre(items, "GROK").count, 1)
    }

    func testEslesmeyenSorguBosDoner() {
        XCTAssertTrue(SearchableMenu.filtre(items, "gemini").isEmpty)
    }
}
