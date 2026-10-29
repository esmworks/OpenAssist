import XCTest

@testable import OpenAssistMac

/// Kapı — testin iki isteği bilinçli sırayla döndürmesi için.
private actor Kapi {
    private var devam: CheckedContinuation<Void, Never>?
    private var acik = false

    func bekle() async {
        if acik { return }
        await withCheckedContinuation { c in devam = c }
    }

    func ac() {
        acik = true
        devam?.resume()
        devam = nil
    }
}

/// Mesaj listesi beş ayrı yerden tazeleniyor (3 sn'lik tur, focus, send,
/// edit, akış) ve cevapların dönüş sırası garanti değil.
@MainActor
final class MesajYarisiTests: XCTestCase {
    private func msg(_ text: String) -> Message {
        Message(id: UUID(), role: "user", text: text, createdAt: Date(), runID: nil)
    }

    /// Regresyon: geç dönen ESKİ cevap yeni listeyi eziyordu — kullanıcı
    /// mesajını yazıyor, mesaj görünüyor, sonra bir turdan kalan cevap gelip
    /// listeyi geri sarıyor; mesaj kayboluyor, üç saniye sonra geri geliyordu.
    func testGecDonenEskiCevapYeniListeyiEzmez() async {
        let store = LiveStore()
        let bot = UUID()
        let eski = [msg("merhaba")]
        let yeni = [msg("merhaba"), msg("cevap")]
        let kapi = Kapi()

        // A: önce başlar, geç döner — ESKİ liste.
        let a = Task { await store.mesajlariUygula(bot) { await kapi.bekle(); return eski } }
        await Task.yield() // A sıra numarasını alsın

        // B: sonra başlar, hemen döner — YENİ liste.
        let bYazdi = await store.mesajlariUygula(bot) { yeni }
        await kapi.ac()
        let aYazdi = await a.value

        XCTAssertTrue(bYazdi, "en güncel istek yazmalı")
        XCTAssertFalse(aYazdi, "geç dönen eski cevap yazmamalı")
        XCTAssertEqual(store.messages[bot]?.count, 2, "liste geri sarılmamalı")
    }

    /// Başarısız getirme mevcut listeyi SİLMEZ (ağ titremesinde sohbet
    /// boşalmamalı).
    func testBasarisizGetirmeListeyiSilmez() async {
        let store = LiveStore()
        let bot = UUID()
        _ = await store.mesajlariUygula(bot) { [self.msg("merhaba")] }
        let sonuc = await store.mesajlariUygula(bot) { nil }

        XCTAssertFalse(sonuc)
        XCTAssertEqual(store.messages[bot]?.count, 1)
    }

    /// Tek başına gelen istek elbette yazar (sıra numarası kilitlenmesin).
    func testTekIstekYazar() async {
        let store = LiveStore()
        let bot = UUID()
        let ilk = await store.mesajlariUygula(bot) { [self.msg("a")] }
        let ikinci = await store.mesajlariUygula(bot) { [self.msg("a"), self.msg("b")] }
        XCTAssertTrue(ilk)
        XCTAssertTrue(ikinci)
        XCTAssertEqual(store.messages[bot]?.count, 2)
    }
}
