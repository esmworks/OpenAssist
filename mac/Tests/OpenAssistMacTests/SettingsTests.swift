import XCTest

@testable import OpenAssistMac

/// Ayar ekranlarının "gerçekten çalışması" iki şeye bağlı: tercihler
/// diskte kalmalı ve bildirim tetikleyicileri doğru olayda ateşlemeli.
/// İkisi de sessizce bozulur — bu yüzden testleri var.
final class SettingsTests: XCTestCase {
    // ── Tercih kalıcılığı ──

    /// Diske yalnız YEREL tercihler yazılır. Sistem (girişte başlat) ve
    /// sunucu (kota, sessiz saat) alanları kaydedilirse ekran, kaynağı
    /// değiştiğinde bayat değeri gösterir.
    func testYalnizYerelTercihlerKaydedilir() throws {
        var s = DSettings()
        s.notifRoutines = true
        s.sound = false
        s.launchAtLogin = true // sistemden gelir
        s.dailyLimit = "50"    // sunucudan gelir

        let obj = try JSONSerialization.jsonObject(
            with: JSONEncoder().encode(s)) as? [String: Any]
        XCTAssertEqual(obj?["notifRoutines"] as? Bool, true)
        XCTAssertEqual(obj?["sound"] as? Bool, false)
        XCTAssertNil(obj?["launchAtLogin"], "sistem gerçeği diske yazılmamalı")
        XCTAssertNil(obj?["dailyLimit"], "sunucu politikası diske yazılmamalı")
    }

    /// Eski/eksik kayıt tüm ayarları sıfırlamamalı: olmayan alan varsayılana düşer.
    func testEksikKayitVarsayilanaDuser() throws {
        let json = #"{"sound":false}"#
        let s = try JSONDecoder().decode(DSettings.self, from: Data(json.utf8))
        XCTAssertFalse(s.sound)
        XCTAssertTrue(s.notifReplies, "verilmeyen alan varsayılanını korumalı")
        XCTAssertTrue(s.menubar)
    }

    // ── Bildirim anahtarları ──

    @MainActor
    func testAnahtarKapaliykenBildirimYok() {
        let n = Notifier()
        n.replies = false
        n.approvals = true
        // Paketsiz testte zaten sistem çağrısı yapılmaz; burada kontrol
        // edilen karar mantığı: kapalı anahtar susmalı.
        XCTAssertFalse(n.shouldNotify(routine: false))
        XCTAssertTrue(n.shouldNotifyApproval)
    }

    @MainActor
    func testRutinAyriAnahtar() {
        let n = Notifier()
        n.replies = true
        n.routines = false
        // Rutin koşusu "bot yanıtı" anahtarına takılmaz; kendi anahtarı var.
        XCTAssertFalse(n.shouldNotify(routine: true))
        n.routines = true
        XCTAssertTrue(n.shouldNotify(routine: true))
    }

    /// Paketsiz koşuda bildirim yolu kapalı ve nedeni yazılı olmalı —
    /// UNUserNotificationCenter paketsiz süreçte istisna atar.
    @MainActor
    func testPaketsizKosudaKapali() {
        let n = Notifier()
        XCTAssertEqual(n.available, AppEnvironment.isBundled)
        if !AppEnvironment.isBundled {
            XCTAssertEqual(n.statusLine, AppEnvironment.unbundledNote)
        }
    }

    // ── Onay kartı metni ──

    @MainActor
    func testOnayBildirimMetni() {
        let izin = Ask(kind: "permission", tool: "mcp__mac__write_file",
                       input: nil, question: nil, choices: nil)
        XCTAssertEqual(LiveStore.askLabel(izin), "Write File çalıştırmak istiyor")

        let soru = Ask(kind: "question", tool: nil, input: nil,
                       question: "Hangi klasöre yazayım?", choices: nil)
        XCTAssertEqual(LiveStore.askLabel(soru), "Hangi klasöre yazayım?")

        XCTAssertEqual(LiveStore.askLabel(nil), "Bir istek onay bekliyor")
    }

    // ── Koşu kökeni ──

    /// Rutin bildirimini ayırmak koşunun `origin` alanına bağlı; sunucu
    /// göndermezse (eski sürüm) istemci kırılmamalı.
    func testKosuKokeniOpsiyonel() throws {
        let json = """
        {"id":"\(UUID().uuidString)","bot_id":"\(UUID().uuidString)","status":"completed",
         "prompt":"iş","tainted":false,"cost_usd":0.1,"queued_at":"2026-08-14T12:00:00Z"}
        """
        let run = try API.decoder.decode(Run.self, from: Data(json.utf8))
        XCTAssertNil(run.origin)

        let withOrigin = """
        {"id":"\(UUID().uuidString)","bot_id":"\(UUID().uuidString)","status":"completed",
         "prompt":"iş","tainted":false,"cost_usd":0.1,"origin":"routine",
         "queued_at":"2026-08-14T12:00:00Z"}
        """
        XCTAssertEqual(try API.decoder.decode(Run.self, from: Data(withOrigin.utf8)).origin,
                       "routine")
    }
}
