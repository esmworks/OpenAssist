import SwiftUI
import XCTest

@testable import OpenAssistMac

/// Sunucu kontratı: `/api/providers` gövdesi olduğu gibi çözülmeli.
/// Çözülemezse istek `try?` içinde sessizce düşer ve Modeller ekranı boş
/// kalır — kullanıcı hiçbir hata görmeden sağlayıcısını kaybeder.
final class SaglayiciKontratiTests: XCTestCase {
    private let json = """
    [{"id":"minimax","name":"MiniMax","base_url":"https://api.minimax.io/anthropic/v1",
      "api":"anthropic-messages","enabled":true,"has_key":true,"position":0,
      "models":[{"id":"MiniMax-M2.7","label":"MiniMax-M2.7","context_window":204800,
                 "max_tokens":131072,"reasoning":true,"input":["text"]}]}]
    """

    func testGovdeCozulur() throws {
        let got = try API.decoder.decode([ProviderInfo].self, from: Data(json.utf8))
        let p = try XCTUnwrap(got.first)
        XCTAssertEqual(p.id, "minimax")
        XCTAssertEqual(p.baseURL, "https://api.minimax.io/anthropic/v1")
        XCTAssertEqual(p.api, "anthropic-messages")
        XCTAssertTrue(p.hasKey)
        XCTAssertEqual(p.models.first?.contextWindow, 204_800)
        XCTAssertEqual(p.models.first?.maxTokens, 131_072)
        XCTAssertTrue(p.models.first?.reasoning ?? false)
    }

    /// Katalogda taban adres ve biçim eksik olabilir (omitempty) — çözücü
    /// bunu hata saymamalı, yoksa katalog hiç yüklenmezdi.
    func testKatalogEksikAlanlarlaCozulur() throws {
        let json = """
        {"fetched_at":"2026-08-15T10:00:00Z","providers":[
          {"id":"anthropic","name":"Anthropic","models":[
            {"id":"claude-opus-4-7","name":"Claude Opus 4.7","reasoning":true,
             "tool_call":true,"context_window":200000,"max_tokens":64000,
             "input":["text","image"],"release_date":"2026-05-01"}]}]}
        """
        let cat = try API.decoder.decode(ModelCatalog.self, from: Data(json.utf8))
        let p = try XCTUnwrap(cat.providers.first)
        XCTAssertNil(p.baseURL)
        XCTAssertNil(p.api)
        XCTAssertEqual(p.models.first?.id, "claude-opus-4-7")
    }

    /// Katalog kaydı kaydedilebilir modele çevrilirken SINIRLAR da taşınmalı:
    /// pi bunları models.json'dan okuyor, düşerlerse bağlam penceresi
    /// bilinmez olur.
    func testKatalogModeliKayitOlur() {
        let m = CatalogModel(id: "gpt-5", name: "GPT-5", reasoning: true, toolCall: true,
                             contextWindow: 400_000, maxTokens: 128_000,
                             input: ["text", "image"], releaseDate: "2026-01-01")
        let k = m.kayit
        XCTAssertEqual(k.id, "gpt-5")
        XCTAssertEqual(k.label, "GPT-5")
        XCTAssertEqual(k.contextWindow, 400_000)
        XCTAssertEqual(k.maxTokens, 128_000)
        XCTAssertTrue(k.reasoning)
        XCTAssertEqual(k.input, ["text", "image"])
    }
}

/// Taslak doğrulaması. Kurallar sunucudakiyle aynıdır; burada tekrarlanıyor
/// ki kullanıcı gidiş-dönüş beklemeden görsün — ama asıl mesele, geçersiz
/// bir kaydın sessizce kaydedilmiş GİBİ görünmemesi.
final class SaglayiciTaslagiTests: XCTestCase {
    private func taslak(id: String = "openrouter",
                        adres: String = "https://openrouter.ai/api/v1") -> ProviderDraft {
        var d = ProviderDraft(position: 0)
        d.id = id
        d.baseURL = adres
        d.name = "OpenRouter"
        return d
    }

    func testGecerliTaslakHatasiz() {
        XCTAssertNil(taslak().hata)
    }

    func testKimlikDeseniZorunlu() {
        XCTAssertNotNil(taslak(id: "").hata)
        XCTAssertNotNil(taslak(id: "Open Router").hata, "boşluk kimliği bozar")
        XCTAssertNotNil(taslak(id: "-openrouter").hata, "harf ya da rakamla başlamalı")
        XCTAssertNil(taslak(id: "open-router.2_x").hata)
    }

    func testAdresSemasiZorunlu() {
        XCTAssertNotNil(taslak(adres: "").hata)
        XCTAssertNotNil(taslak(adres: "openrouter.ai/api/v1").hata,
                        "şemasız adres pi'de çözülmez")
        XCTAssertNil(taslak(adres: "http://127.0.0.1:1234/v1").hata, "yerel sunucu geçerli")
    }

    /// Eğik çizgi kimliği ikiye bölerdi: önek sağlayıcının kendisidir.
    func testModelKimligindeEgikCizgiReddedilir() {
        var d = taslak()
        d.models = [ProviderInfo.Model(id: "openai/gpt-5")]
        XCTAssertNotNil(d.hata)
    }

    // ── anahtarın üç durumu ──
    //
    // Sunucu anahtarı hiç geri vermez. Bu yüzden "gönderilmedi" ile
    // "boşalt" ayrımı gövdede yapılır: alan yoksa kayıtlı anahtar korunur.

    func testBosAnahtarAlaniGonderilmez() throws {
        let obj = try govde(taslak())
        XCTAssertNil(obj["api_key"], "boş alan gönderildi — kayıtlı anahtar silinirdi")
    }

    func testYeniAnahtarGonderilir() throws {
        var d = taslak()
        d.key = "sk-yeni"
        XCTAssertEqual(try govde(d)["api_key"] as? String, "sk-yeni")
    }

    func testSilmeIsaretiBosDizeGonderir() throws {
        var d = taslak()
        d.hasKey = true
        d.keySilinsin = true
        XCTAssertEqual(try govde(d)["api_key"] as? String, "")
    }

    /// Silme işaretliyken yazılmış anahtar kazanmamalı: kullanıcının son
    /// eylemi silmedir, gövdede iki niyet birden gidemez.
    func testSilmeYazilanAnahtariEzer() throws {
        var d = taslak()
        d.hasKey = true
        d.key = "sk-yanlislikla"
        d.keySilinsin = true
        XCTAssertEqual(try govde(d)["api_key"] as? String, "")
    }

    /// Kimlik ve adres kırpılıp küçültülür — kopyala-yapıştırdan gelen
    /// boşluk ve büyük harf sunucuda 400'e dönerdi.
    func testKimlikVeAdresNormallesir() throws {
        var d = taslak(id: " OpenRouter ", adres: " https://openrouter.ai/api/v1 ")
        d.name = " OpenRouter "
        let obj = try govde(d)
        XCTAssertEqual(obj["id"] as? String, "openrouter")
        XCTAssertEqual(obj["base_url"] as? String, "https://openrouter.ai/api/v1")
        XCTAssertEqual(obj["name"] as? String, "OpenRouter")
    }

    private func govde(_ d: ProviderDraft) throws -> [String: Any] {
        let data = try API.encoder.encode(d.kayit)
        return try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
    }
}

/// Açılır menü katı. SwiftUI kardeşleri sırayla çizer ve `zIndex` yalnız
/// KARDEŞLER arasında sıra belirler: menüyü taşıyan SATIR yükseltilse bile
/// onu içeren KART yükseltilmezse, menünün kart sınırını taşan yarısı sonraki
/// kartların altında kalır. Modeller ekranında tam bu oldu — Motor kartı
/// katalog menüsünün alt yarısını yutuyordu.
final class MenuKatiTests: XCTestCase {
    func testMenuKapaliykenNormalKat() {
        XCTAssertEqual(MenuKat.kat(nil, "prov:yeni"), MenuKat.normal)
    }

    func testAcikMenuUsteCikar() {
        XCTAssertEqual(MenuKat.kat("prov:yeni", "prov:yeni"), MenuKat.ustte)
    }

    /// Başka bir menü açıkken bu kutu yükselmemeli: her şeyi yükseltmek
    /// sıralamayı yeniden rastgeleye çevirirdi.
    func testBaskaMenuAcikkenYukselmez() {
        XCTAssertEqual(MenuKat.kat("prov:api", "prov:yeni"), MenuKat.normal)
    }

    /// Bir kutuda birden çok menü olabilir (katalog + model seçimi);
    /// hangisi açılırsa kutu yükselir.
    func testCokluAnahtar() {
        XCTAssertEqual(MenuKat.kat("prov:model", "prov:katalog", "prov:model"), MenuKat.ustte)
        XCTAssertEqual(MenuKat.kat("prov:katalog", "prov:katalog", "prov:model"), MenuKat.ustte)
        XCTAssertEqual(MenuKat.kat("baska", "prov:katalog", "prov:model"), MenuKat.normal)
    }

    /// Anahtarı çalışma anında üretilen menüler (bot başına "be:<id>").
    func testOnEkEslemesi() {
        XCTAssertEqual(MenuKat.onEk("be:1234", "be:", "bm:"), MenuKat.ustte)
        XCTAssertEqual(MenuKat.onEk("bm:1234", "be:", "bm:"), MenuKat.ustte)
        XCTAssertEqual(MenuKat.onEk("statBot", "be:", "bm:"), MenuKat.normal)
        XCTAssertEqual(MenuKat.onEk(nil, "be:", "bm:"), MenuKat.normal)
    }
}

/// Modeller ekranının mağaza mantığı: liste düzenleme sunucuya gitmeden
/// taslakta olur.
@MainActor
final class ModellerEkraniTests: XCTestCase {
    private let minimax = ProviderInfo(
        id: "minimax", name: "MiniMax", baseURL: "https://api.minimax.io/anthropic/v1",
        api: "anthropic-messages", enabled: true, hasKey: true, position: 0,
        models: [ProviderInfo.Model(id: "MiniMax-M2.7", label: "MiniMax-M2.7")])

    private func store() -> DesignStore {
        let s = DesignStore()
        s.live.setProvidersForTesting([minimax])
        s.live.setCatalogForTesting([
            CatalogProvider(id: "minimax", name: "MiniMax", baseURL: "https://api.minimax.io/anthropic/v1",
                            api: "anthropic-messages", doc: nil, models: [
                                CatalogModel(id: "MiniMax-M2.7", name: "MiniMax-M2.7",
                                             reasoning: true, toolCall: true,
                                             contextWindow: 204_800, maxTokens: 131_072,
                                             input: ["text"], releaseDate: "2026-03-18"),
                                CatalogModel(id: "MiniMax-M2", name: "MiniMax-M2",
                                             reasoning: false, toolCall: true,
                                             contextWindow: 192_000, maxTokens: 65_536,
                                             input: ["text"], releaseDate: "2025-10-01"),
                            ]),
            // Kayıtlı OLMAYAN sağlayıcı: "katalogdan yeni ekle" yolu bununla sınanır.
            CatalogProvider(id: "openrouter", name: "OpenRouter",
                            baseURL: "https://openrouter.ai/api/v1",
                            api: "openai-completions", doc: nil, models: [
                                CatalogModel(id: "gpt-5", name: "GPT-5", reasoning: true,
                                             toolCall: true, contextWindow: 400_000,
                                             maxTokens: 128_000, input: ["text"],
                                             releaseDate: "2026-01-01"),
                            ]),
        ])
        return s
    }

    func testKayitAcilincaTaslakDolar() {
        let s = store()
        s.provAc(minimax)
        XCTAssertEqual(s.provDraft?.id, "minimax")
        XCTAssertFalse(s.provDraft?.yeni ?? true)
        XCTAssertTrue(s.provDraft?.hasKey ?? false)
        // Katalog eşleşmesi kimlikten kurulur: model önerileri hazır gelsin.
        XCTAssertEqual(s.provKatalogID, "minimax")
        XCTAssertEqual(s.provKatalogModelleri.count, 2)
    }

    /// Katalogdan yeni sağlayıcı: adres ve biçim ön-dolar, modeller boş
    /// başlar (hangilerinin isteneceğini kullanıcı seçer).
    func testKatalogdanYeniSaglayici() throws {
        let s = store()
        let c = try XCTUnwrap(s.live.catalog.first { $0.id == "openrouter" })
        s.provYeni(c)
        let d = try XCTUnwrap(s.provDraft)
        XCTAssertTrue(d.yeni)
        XCTAssertEqual(d.id, "openrouter")
        XCTAssertEqual(d.baseURL, "https://openrouter.ai/api/v1")
        XCTAssertEqual(d.api, "openai-completions")
        XCTAssertTrue(d.models.isEmpty)
        // Sıra numarası son kaydın ardına gelir.
        XCTAssertEqual(d.position, 1)
    }

    /// Kayıtlı bir kimlik katalogdan yeniden seçilirse VAR OLAN kayıt açılır:
    /// "yeni" diye kaydetmek (PUT upsert) anahtarı ve model listesini ezerdi.
    func testKayitliKimlikKatalogdanSecilinceDuzenlenir() throws {
        let s = store()
        let c = try XCTUnwrap(s.live.catalog.first { $0.id == "minimax" })
        s.provYeni(c)
        let d = try XCTUnwrap(s.provDraft)
        XCTAssertFalse(d.yeni, "kayıtlı sağlayıcı yeni kayıt gibi açıldı")
        XCTAssertEqual(d.models.map(\.id), ["MiniMax-M2.7"], "var olan modeller kayboldu")
        XCTAssertTrue(d.hasKey, "anahtar durumu taşınmadı")
    }

    /// Elle yazılan kimlik kayıtlıyla çakışırsa kaydetme reddedilir.
    func testCakisanKimlikKaydedilmez() throws {
        let s = store()
        s.provYeni(nil)
        s.provDraft?.id = "minimax"
        s.provDraft?.baseURL = "https://baska/v1"
        s.provKaydet()
        XCTAssertNotNil(s.provDraft, "çakışan kayıt gönderildi — var olan sağlayıcı ezilirdi")
        XCTAssertEqual(s.toast?.title, "Kaydedilmedi")
    }

    func testElleYeniSaglayiciBosBaslar() throws {
        let s = store()
        s.provYeni(nil)
        let d = try XCTUnwrap(s.provDraft)
        XCTAssertTrue(d.id.isEmpty)
        XCTAssertEqual(d.api, "openai-completions", "en uyumlu biçim varsayılan")
        XCTAssertNotNil(d.hata, "kimlik ve adres girilmeden kaydedilemez")
    }

    /// Aynı model iki kez eklenince liste ikizlenmemeli — kaydetme
    /// sunucuda "model iki kez verildi" hatasına düşerdi.
    func testAyniModelIkizlenmez() {
        let s = store()
        s.provAc(minimax)
        s.provKatalogModeliEkle("MiniMax-M2.7")
        XCTAssertEqual(s.provDraft?.models.count, 1)
        // Katalogdan gelen sınırlar var olan kaydı TAZELER.
        XCTAssertEqual(s.provDraft?.models.first?.contextWindow, 204_800)
    }

    func testKatalogdanModelEklenir() {
        let s = store()
        s.provAc(minimax)
        s.provKatalogModeliEkle("MiniMax-M2")
        XCTAssertEqual(s.provDraft?.models.map(\.id), ["MiniMax-M2.7", "MiniMax-M2"])
    }

    /// Katalogda olmayan ad her zaman yazılabilmeli: yeni çıkan model
    /// katalog güncellenene kadar kullanılamaz olurdu.
    func testElleModelEklenir() {
        let s = store()
        s.provAc(minimax)
        s.provYeniModel = "  MiniMax-M3  "
        s.provElleModelEkle()
        XCTAssertEqual(s.provDraft?.models.last?.id, "MiniMax-M3", "ad kırpılmadı")
        XCTAssertTrue(s.provYeniModel.isEmpty, "alan temizlenmedi")
    }

    func testBosElleModelEklenmez() {
        let s = store()
        s.provAc(minimax)
        s.provYeniModel = "   "
        s.provElleModelEkle()
        XCTAssertEqual(s.provDraft?.models.count, 1)
    }

    func testModelCikarilir() {
        let s = store()
        s.provAc(minimax)
        s.provModelCikar("MiniMax-M2.7")
        XCTAssertTrue(s.provDraft?.models.isEmpty ?? false)
    }

    func testKapatmaTaslagiAtar() {
        let s = store()
        s.provAc(minimax)
        s.provSilOnay = true
        s.provKapat()
        XCTAssertNil(s.provDraft)
        XCTAssertFalse(s.provSilOnay, "silme onayı taslakla birlikte sıfırlanmalı")
    }

    // ── durum ışığı ──
    //
    // Işık yanlışsa ekran yalan söyler: anahtarsız bir sağlayıcıyı yeşil
    // göstermek, hatayı ilk koşu düşene kadar gizlemek demektir.

    func testIsikHazirSaglayiciYesil() {
        XCTAssertEqual(ModelsView.saglayiciNokta(minimax), Color(hex: "#28c840"))
    }

    func testIsikAnahtarsizSari() {
        var p = minimax
        p.hasKey = false
        XCTAssertEqual(ModelsView.saglayiciNokta(p), Color(hex: "#f5b544"))
    }

    func testIsikModelsizSari() {
        var p = minimax
        p.models = []
        XCTAssertEqual(ModelsView.saglayiciNokta(p), Color(hex: "#f5b544"))
    }

    func testIsikKapaliGri() {
        var p = minimax
        p.enabled = false
        XCTAssertEqual(ModelsView.saglayiciNokta(p), Color(hex: "#8a8a8e"))
    }
}
