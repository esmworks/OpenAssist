import XCTest

@testable import OpenAssistMac

/// Sunucu verisinin tasarım kartlarına çevrimi — panelin kullanılabilir
/// olması bu eşlemenin doğruluğuna bağlı (yanlış eşleme = yanlış onay).
@MainActor
final class LiveMappingTests: XCTestCase {
    private let botID = UUID()

    private func msg(_ role: String, _ text: String, _ date: Date, runID: UUID? = nil) -> Message {
        Message(id: UUID(), role: role, text: text, createdAt: date, runID: runID)
    }

    private func run(kind: String, tool: String?, question: String? = nil,
                     input: JSONValue? = nil, tainted: Bool = false) -> Run {
        Run(id: UUID(), botID: botID, status: "approval_pending", prompt: "iş",
            tainted: tainted,
            pendingAsk: Ask(kind: kind, tool: tool, input: input,
                            question: question, choices: nil),
            error: nil, costUSD: 0, finalText: "taslak hazır",
            queuedAt: Date(), startedAt: nil, finishedAt: nil)
    }

    // ── sohbet akışı ───────────────────────────────────────────────────

    func testGunAyraciYalnizGunDegisincedeGirer() {
        let today = Date()
        let yesterday = today.addingTimeInterval(-86400)
        let out = LiveStore.chatMessages(
            messages: [msg("user", "dün", yesterday),
                       msg("bot", "dün cevap", yesterday),
                       msg("user", "bugün", today)],
            streaming: nil, pending: [], streamKey: "k")

        let dividers = out.filter { $0.k == .divider }.map(\.text)
        XCTAssertEqual(dividers, ["Dün", "Bugün"], "gün başına tek ayraç olmalı")
        XCTAssertEqual(out.filter { $0.k == .user }.count, 2)
        XCTAssertEqual(out.filter { $0.k == .bot }.count, 1)
    }

    /// Ek kartı hem kullanıcı hem BOT mesajında çizilmeli (ADIM 13): giden
    /// dosya yalnız kullanıcı dalında çizilseydi send_file ile gelen rapor
    /// Mac'te hiç görünmezdi.
    func testEklerHemGelenHemGidenMesajdaKartOlur() {
        let now = Date()
        let gelen = Message(id: UUID(), role: "user", text: "şu faturaya bak",
                            createdAt: now, runID: nil,
                            attachments: [Attachment(id: UUID(), name: "fatura.pdf", kind: "pdf", size: 1024)])
        let giden = Message(id: UUID(), role: "bot", text: "özet hazır",
                            createdAt: now, runID: nil,
                            attachments: [Attachment(id: UUID(), name: "ozet.md", kind: "file", size: 300)])

        let out = LiveStore.chatMessages(messages: [gelen, giden], streaming: nil,
                                         pending: [], streamKey: "k")
        let dosyalar = out.filter { $0.k == .file }
        XCTAssertEqual(dosyalar.map(\.name), ["fatura.pdf", "ozet.md"])
        // Kart mesajının HEMEN ardında durmalı (sıra: user, ek, bot, ek).
        XCTAssertEqual(out.filter { $0.k != .divider }.map(\.k), [.user, .file, .bot, .file])
        XCTAssertEqual(dosyalar.first?.ekKind, "pdf")
    }

    func testAkanYanitBaloncugaDuser() {
        let out = LiveStore.chatMessages(messages: [], streaming: "yazıyor…",
                                         pending: [], streamKey: "bot1")
        XCTAssertEqual(out.last?.k, .bot)
        XCTAssertEqual(out.last?.text, "yazıyor…")
        XCTAssertEqual(out.last?.id, "stream-bot1", "akış kartının kimliği sabit olmalı")
    }

    func testIzinIstegiOnayKartiOlur() {
        let r = run(kind: "permission", tool: "mcp__mac__write_file",
                    input: .object(["path": .string("~/Desktop/not.txt"),
                                    "content": .string("deneme")]))
        let out = LiveStore.chatMessages(messages: [], streaming: nil,
                                         pending: [r], streamKey: "k")

        let card = try! XCTUnwrap(out.last)
        XCTAssertEqual(card.k, .approve)
        XCTAssertEqual(card.id, r.id.uuidString, "kart kimliği koşu kimliği olmalı (onay bunu gönderir)")
        XCTAssertTrue(card.title.contains("Write File"), "araç adı okunur olmalı: \(card.title)")
        XCTAssertTrue(card.text.contains("~/Desktop/not.txt"), "argümanlar görünmeli: \(card.text)")
    }

    func testTaintedKosuKarttaUyariGosterir() {
        let r = run(kind: "permission", tool: "mcp__gw__gmail_send_draft", tainted: true)
        let card = LiveStore.askMessage(r)
        XCTAssertTrue(card.to.contains("dış içerik"), "§5.1 uyarısı kartta olmalı: \(card.to)")
    }

    func testSoruKartDegilBaloncukOlur() {
        let r = run(kind: "question", tool: nil, question: "Hangi dosyaya yazayım?")
        let out = LiveStore.chatMessages(messages: [], streaming: nil,
                                         pending: [r], streamKey: "k")
        XCTAssertEqual(out.last?.k, .bot, "sorular onay kartı DEĞİL — sohbete yazılarak cevaplanır")
        XCTAssertTrue(out.last?.text.contains("Hangi dosyaya yazayım?") == true)
    }

    /// Regresyon: onay kartı akışın SONUNA çakılıyordu — kullanıcı yeni mesaj
    /// yazınca eski istek en altta kalıyor, hangi isteğin hangi mesaja ait
    /// olduğu kayboluyordu. Kart, koşuyu doğuran mesajın hemen ardında kalır.
    func testOnayKartiKosuyuDoguranMesajinArdindaKalir() {
        let runID = UUID()
        // Günün ORTASINA sabitlenir: `Date()` ile iki mesaj arasındaki 120
        // saniye gece yarısına denk geldiğinde araya ikinci bir gün ayracı
        // giriyor ve test yalnız 00:00–00:02 arasında düşüyordu.
        let now = Calendar.current.startOfDay(for: Date()).addingTimeInterval(12 * 3600)
        let r = Run(id: runID, botID: botID, status: "approval_pending", prompt: "skoru çek",
                    tainted: false,
                    pendingAsk: Ask(kind: "permission", tool: "Bash", input: nil,
                                    question: nil, choices: nil),
                    error: nil, costUSD: 0, finalText: nil,
                    queuedAt: now.addingTimeInterval(-120), startedAt: nil, finishedAt: nil)
        let out = LiveStore.chatMessages(
            messages: [msg("user", "skoru çek", now.addingTimeInterval(-120), runID: runID),
                       msg("user", "bir de hava durumuna bak", now)],
            streaming: nil, pending: [r], streamKey: "k")

        // ayraç → ilk mesaj → onay kartı → YENİ mesaj (kart alta kaymaz)
        XCTAssertEqual(out.map(\.k), [.divider, .user, .approve, .user])
        XCTAssertEqual(out[2].id, runID.uuidString, "kartın kimliği koşu kimliği olmalı")
    }

    /// "Hep izin ver" düğmesi SUNUCUNUN kararına bağlı olmalı.
    ///
    /// Sahada ölçülen yorgunluk: on mükerrer hafıza kaydını silen bot her
    /// silme için ayrı karta düşüyordu ve her kart ayrı bir park/resume turu
    /// demekti (resume izni tek kullanımlık, K15). Çıkış kapısı kural
    /// yazmaktı; sunucu destekliyordu ama kartta düğmesi yoktu.
    func testHepIzinDugmesiSunucununKararindanGelir() {
        func kart(_ allowAlways: Bool?) -> DMsg {
            let r = Run(id: UUID(), botID: botID, status: "approval_pending", prompt: "sil",
                        tainted: false,
                        pendingAsk: Ask(kind: "permission", tool: "mcp__gw__memory_delete",
                                        input: nil, question: nil, choices: nil,
                                        allowAlways: allowAlways),
                        error: nil, costUSD: 0, finalText: nil,
                        queuedAt: Date(), startedAt: nil, finishedAt: nil)
            return LiveStore.askMessage(r)
        }
        XCTAssertTrue(kart(true).hepIzin, "sunucu izin verdi ama düğme çıkmıyor")
        XCTAssertFalse(kart(false).hepIzin, "sunucu hayır dedi ama düğme çıkıyor")
        // Eski sunucu alanı hiç göndermez: düğme çıkmamalı, tutulamayacak
        // bir söz vermektense hiç sunmamak doğru.
        XCTAssertFalse(kart(nil).hepIzin, "alan yokken düğme çıktı")
    }

    /// Bağı olmayan koşu (rutin/telegram) eskisi gibi sona eklenir.
    func testBagiOlmayanOnayKartiSonaEklenir() {
        let r = run(kind: "permission", tool: "Bash")
        let now = Date()
        let out = LiveStore.chatMessages(
            messages: [msg("user", "merhaba", now)],
            streaming: nil, pending: [r], streamKey: "k")
        XCTAssertEqual(out.last?.k, .approve)
        XCTAssertEqual(out.last?.id, r.id.uuidString)
    }

    // ── Onay Merkezi ───────────────────────────────────────────────────

    func testOnayMerkeziSorulariVeKararVerilmisleriDusurur() {
        let izin = run(kind: "permission", tool: "mcp__mac__write_file")
        let soru = run(kind: "question", tool: nil, question: "hangisi?")
        let kararli = run(kind: "permission", tool: "mcp__mac__trash_item")
        let bot = Bot(id: botID, kind: "bot", name: "Asistan", description: "",
                      colorPrimary: "#b18cf0", colorSecondary: "#7b4fd6",
                      section: "", engine: "claudecode", model: "", pinned: false, members: nil,
                      extensions: nil)

        let items = LiveStore.inboxItems(
            pending: [izin, soru, kararli], bots: [bot],
            decided: [kararli.id.uuidString: "ok"])

        XCTAssertEqual(items.count, 1, "yalnız cevaplanmamış izin isteği kalmalı")
        XCTAssertEqual(items.first?.id, izin.id.uuidString)
        XCTAssertEqual(items.first?.botName, "Asistan")
    }

    // ── biçimlendirme ──────────────────────────────────────────────────

    func testAracAdiOkunurHaleGelir() {
        XCTAssertEqual(LiveStore.toolLabel("mcp__gw__gmail_send_draft"), "Gmail Send Draft")
        XCTAssertEqual(LiveStore.toolLabel("mcp__mac__list_dir"), "List Dir")
        XCTAssertEqual(LiveStore.toolLabel("Bash"), "Bash")
    }

    func testUzunMetinTekSatiraKirpilir() {
        let uzun = String(repeating: "a", count: 200)
        let out = LiveStore.oneLine("satır1\nsatır2 " + uzun)
        XCTAssertFalse(out.contains("\n"), "önizleme tek satır olmalı")
        XCTAssertTrue(out.hasSuffix("…"))
        XCTAssertLessThanOrEqual(out.count, 141)
    }

    func testGunEtiketleri() {
        XCTAssertEqual(LiveStore.dayLabel(Date()), "Bugün")
        XCTAssertEqual(LiveStore.dayLabel(Date().addingTimeInterval(-86400)), "Dün")
    }

    // ── başarısız koşu ─────────────────────────────────────────────────

    /// Regresyon: koşu düşünce ekranda HİÇBİR ŞEY olmuyordu — kullanıcı
    /// mesajını yazıyor, cevap gelmiyor, sebep yalnız sunucu logunda
    /// kalıyordu. Sunucu artık sohbete çip yazıyor, burada hata KARTI olur.
    func testBasarisizKosuHataKartiOlur() {
        let text = LiveStore.hataCipiOneki + "opencode: APIError: Insufficient balance."
        let out = LiveStore.chatMessages(messages: [msg("event", text, Date())],
                                         streaming: nil, pending: [], streamKey: "k")
        guard let kart = out.first(where: { $0.k == .error }) else {
            return XCTFail("hata kartı bekleniyordu: \(out.map(\.k))")
        }
        XCTAssertEqual(kart.title, "Koşu başarısız")
        XCTAssertEqual(kart.text, "opencode: APIError: Insufficient balance.",
                       "önek kartın gövdesinde tekrar etmemeli")
    }

    /// Devir çipleri çip kalmalı: her event mesajı hata değildir.
    func testDevirCipiAyracKalir() {
        let out = LiveStore.chatMessages(
            messages: [msg("event", "← Gelen Kutusu cevabını verdi: 3 okunmamış var", Date())],
            streaming: nil, pending: [], streamKey: "k")
        XCTAssertTrue(out.contains { $0.k == .divider && $0.text.hasPrefix("←") })
        XCTAssertFalse(out.contains { $0.k == .error })
    }

    // ── aramalı menü ───────────────────────────────────────────────────

    /// Regresyon: ScrollView overlay içinde ALTTAKİ kutunun yüksekliğini
    /// öneri olarak alıyor ve bir dilime çöküyordu; ekranda yalnız arama
    /// kutusu görünüyor, model seçilemiyordu. Yükseklik AÇIKÇA verilir.
    func testMenuListesiYuksekligiTavanVeTabanlaSinirli() {
        // Ölçüm gelmeden: satır sayısından tahmin.
        let iki = SearchableMenu.listeYuksekligi(olculen: 0, satirSayisi: 2)
        XCTAssertEqual(iki, 2 * SearchableMenu.satirTahmini, accuracy: 0.1)
        // Uzun liste tavanı aşamaz (menü pencereden taşmasın).
        XCTAssertEqual(SearchableMenu.listeYuksekligi(olculen: 0, satirSayisi: 79),
                       SearchableMenu.listeTavani, accuracy: 0.1)
        XCTAssertEqual(SearchableMenu.listeYuksekligi(olculen: 4000, satirSayisi: 79),
                       SearchableMenu.listeTavani, accuracy: 0.1)
        // Ölçüm varsa o kullanılır.
        XCTAssertEqual(SearchableMenu.listeYuksekligi(olculen: 95, satirSayisi: 3), 95, accuracy: 0.1)
        // Hiç satır yokken bile çökmez (tek satırlık taban).
        XCTAssertEqual(SearchableMenu.listeYuksekligi(olculen: 0, satirSayisi: 0),
                       SearchableMenu.satirTahmini, accuracy: 0.1)
    }

    // ── "yazıyor" göstergesi ───────────────────────────────────────────

    private func pano(running: [Run] = [], queued: [Run] = []) -> TaskBoard {
        TaskBoard(running: running, queued: queued, approvalPending: [], routines: [])
    }

    private func kosu(_ status: String) -> Run {
        Run(id: UUID(), botID: botID, status: status, prompt: "iş", tainted: false,
            pendingAsk: nil, error: nil, costUSD: 0, finalText: nil,
            queuedAt: Date(), startedAt: Date(), finishedAt: nil)
    }

    /// REGRESYON: sunucu koşunun ortasında yeniden başlatılınca cevap ekranda
    /// kalıyor, akış kopuyor ama pano son "running" satırıyla donuyordu —
    /// üç nokta sonsuza dek açık kalıyordu.
    func testSunucuyaErisilemezkenYaziyorGostergesiSoner() {
        let bayatPano = pano(running: [kosu("running")])
        XCTAssertFalse(
            LiveStore.busy(online: false, streaming: nil, board: bayatPano, botID: botID),
            "çevrimdışıyken pano bayattır: 'yazıyor' denemez")
        XCTAssertFalse(
            LiveStore.busy(online: false, streaming: "yarım cevap", board: bayatPano, botID: botID),
            "akış da koptuğu için canlı metin varlığı da meşgul saymaz")
    }

    func testCevrimiciykenPanoVeAkisMesgulSayar() {
        XCTAssertTrue(
            LiveStore.busy(online: true, streaming: nil, board: pano(running: [kosu("running")]),
                           botID: botID),
            "çalışan koşu = yazıyor")
        XCTAssertTrue(
            LiveStore.busy(online: true, streaming: nil, board: pano(queued: [kosu("queued")]),
                           botID: botID),
            "sıradaki koşu = yazıyor")
        XCTAssertTrue(
            LiveStore.busy(online: true, streaming: "", board: pano(), botID: botID),
            "akış açık: pano daha güncellenmemişken bile gösterge yanar")
    }

    func testBosPanoVeBaskaBotunKosusuMesgulSaymaz() {
        XCTAssertFalse(LiveStore.busy(online: true, streaming: nil, board: pano(), botID: botID))
        XCTAssertFalse(LiveStore.busy(online: true, streaming: nil, board: nil, botID: botID))

        var baskaBot = kosu("running")
        baskaBot = Run(id: baskaBot.id, botID: UUID(), status: "running", prompt: "iş",
                       tainted: false, pendingAsk: nil, error: nil, costUSD: 0,
                       finalText: nil, queuedAt: Date(), startedAt: Date(), finishedAt: nil)
        XCTAssertFalse(
            LiveStore.busy(online: true, streaming: nil, board: pano(running: [baskaBot]),
                           botID: botID),
            "başka botun koşusu bu sohbeti meşgul göstermemeli")
    }
}
