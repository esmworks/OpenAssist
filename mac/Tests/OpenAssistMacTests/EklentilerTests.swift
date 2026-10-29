import XCTest

@testable import OpenAssistMac

/// Sunucu kontratı: `/api/extensions` gövdesi olduğu gibi çözülmeli. Çözülemezse
/// istek `try?` içinde sessizce düşer, ekran boş kalır ve kullanıcı onay
/// kapısının kaybolduğunu sanır.
final class EklentiKontratiTests: XCTestCase {
    func testGovdeCozulur() throws {
        let json = """
        [{"id":"oa-gateway.ts","engine":"pi","name":"oa-gateway",
          "description":"OpenAssist'in pi eklentisi.","kind":"builtin",
          "used_by":["Zeynep","Ahmet"]},
         {"id":"notlar.ts","engine":"pi","name":"notlar","description":"",
          "kind":"file","version":"0.2.0","used_by":[]}]
        """
        let got = try API.decoder.decode([ExtensionInfo].self, from: Data(json.utf8))
        XCTAssertEqual(got.count, 2)

        let gateway = try XCTUnwrap(got.first)
        XCTAssertEqual(gateway.id, "oa-gateway.ts", "kimlik allowlist adı olmalı")
        XCTAssertTrue(gateway.builtin)
        XCTAssertEqual(gateway.usedBy, ["Zeynep", "Ahmet"])
        XCTAssertNil(gateway.version, "sürüm yoksa (omitempty) çözüm düşmemeli")

        XCTAssertFalse(got[1].builtin)
        XCTAssertEqual(got[1].version, "0.2.0")
    }

    /// Galeri gövdesi (`/api/extensions/catalog`). Kaynak dizesi SUNUCUDAN
    /// gelir; istemci "npm:" + ad diye kendi dizesini kurmaz.
    func testKatalogGovdesiCozulur() throws {
        let json = """
        [{"name":"pi-web-access","version":"0.23.0","description":"Web erişimi",
          "publisher":"biri","source":"npm:pi-web-access@0.23.0",
          "monthly_downloads":257972,"installed":false,
          "conflict_note":"web okuma OpenAssist'in kendi aracında",
          "npm_url":"https://www.npmjs.com/package/pi-web-access"},
         {"name":"pi-baska","version":"1.0.0","description":"","source":"npm:pi-baska@1.0.0",
          "monthly_downloads":0,"installed":true,"npm_url":""}]
        """
        let got = try API.decoder.decode([CatalogPackage].self, from: Data(json.utf8))
        XCTAssertEqual(got.count, 2)
        XCTAssertEqual(got[0].source, "npm:pi-web-access@0.23.0", "sürüm sabitlenmiş kaynak gelmeli")
        XCTAssertEqual(got[0].monthlyDownloads, 257_972)
        XCTAssertNotNil(got[0].conflictNote)
        XCTAssertTrue(got[1].installed)
        XCTAssertNil(got[1].conflictNote, "çakışma notu yoksa nil kalmalı")
    }

    /// Bot gövdesindeki allowlist: alan hiç gelmezse (eski sunucu) nil kalır,
    /// boş dizi ile KARIŞMAZ — biri "bilinmiyor", diğeri "çıplak pi".
    func testBotAllowlistiCozulur() throws {
        let ortak = """
        "id":"6f0f5a3c-3b7d-4a1e-9a2b-000000000001","kind":"bot","name":"Zeynep",
        "description":"","color_primary":"#fff","color_secondary":"#000",
        "section":"","engine":"pi","model":"","pinned":false
        """
        let dolu = try API.decoder.decode(
            Bot.self, from: Data("{\(ortak),\"extensions\":[\"oa-gateway.ts\"]}".utf8))
        XCTAssertEqual(dolu.extensions, ["oa-gateway.ts"])

        let bos = try API.decoder.decode(Bot.self, from: Data("{\(ortak),\"extensions\":[]}".utf8))
        XCTAssertEqual(bos.extensions, [], "boş liste geçerli bir seçim: çıplak pi")

        let yok = try API.decoder.decode(Bot.self, from: Data("{\(ortak)}".utf8))
        XCTAssertNil(yok.extensions, "alan yoksa nil kalmalı, [] olmamalı")
    }
}

/// Eklentiler ekranının ve bot formunun sunucu verisinden türeyişi.
@MainActor
final class EklentiEkraniTests: XCTestCase {
    private func ext(_ id: String, kind: String = "file", usedBy: [String] = [],
                     desc: String = "", resources: [String] = ["extension"]) -> ExtensionInfo
    {
        ExtensionInfo(id: id, engine: "pi", name: String(id.split(separator: ".").first ?? ""),
                      description: desc, kind: kind, version: nil, resources: resources,
                      usedBy: usedBy)
    }

    private func store(_ list: [ExtensionInfo], online: Bool = true) -> DesignStore {
        let s = DesignStore()
        s.live.setOnlineForTesting(online)
        s.live.setExtensionsForTesting(list)
        return s
    }

    func testGomuluAyriBolumdeVeOnce() {
        let s = store([ext("notlar.ts"), ext("oa-gateway.ts", kind: "builtin")])
        let cats = s.plugCats
        XCTAssertEqual(cats.map(\.label), ["Gömülü", "Kurulu"])
        XCTAssertEqual(cats.first?.items.map(\.id), ["oa-gateway.ts"])
        XCTAssertEqual(cats.last?.items.map(\.id), ["notlar.ts"])
    }

    func testAramaAdiAciklamayiVeAllowlistAdiniTarar() {
        let s = store([ext("oa-gateway.ts", kind: "builtin", desc: "onay kapısı"),
                       ext("notlar.ts", desc: "not tutar")])

        s.plugQuery = "onay"
        XCTAssertEqual(s.visiblePlugins.map(\.id), ["oa-gateway.ts"], "açıklama taranmalı")

        s.plugQuery = "notlar.ts"
        XCTAssertEqual(s.visiblePlugins.map(\.id), ["notlar.ts"], "allowlist adı taranmalı")

        s.plugQuery = "  "
        XCTAssertEqual(s.visiblePlugins.count, 2, "boşluk araması listeyi boşaltmamalı")
    }

    func testKullanimdaSekmesiSadeceBotaTakiliOlanlariGosterir() {
        let s = store([ext("oa-gateway.ts", kind: "builtin", usedBy: ["Zeynep"]), ext("notlar.ts")])
        s.plugTab = "used"
        XCTAssertEqual(s.visiblePlugins.map(\.id), ["oa-gateway.ts"])
        XCTAssertEqual(s.visiblePlugins.first?.usageLabel, "1 bot kullanıyor")
    }

    /// Boş ekranın SEBEBİ ayrı yazılmalı: bağlantı yokluğu ile "kurulu eklenti
    /// yok" aynı boşluk olsaydı kullanıcı yanlış yeri kurcalardı.
    func testBosEkranSebebiniSoyler() {
        XCTAssertEqual(store([], online: false).plugEmptyHint,
                       "Sunucuya bağlı değil — Ayarlar › Bağlantılar")
        XCTAssertEqual(store([]).plugEmptyHint, "Sunucuda kurulu eklenti yok")

        let s = store([ext("notlar.ts")])
        XCTAssertNil(s.plugEmptyHint, "satır varken ipucu gösterilmemeli")
        s.plugQuery = "bulunmaz"
        XCTAssertEqual(s.plugEmptyHint, "Aramaya uyan eklenti yok")
    }

    /// Beceriler AYRI bölümde ve satır ne taşıdığını yazar: beceri kod değil
    /// talimattır ama modele iş yaptırır — kullanıcı ikisini karıştırmasın.
    func testBecerilerAyriBolumdeVeTuruYazili() {
        let s = store([
            ext("oa-gateway.ts", kind: "builtin"),
            ext("npm:demet", kind: "npm", resources: ["extension", "skill"]),
            ext("skill:pdf", kind: "skill", resources: ["skill"]),
        ])
        XCTAssertEqual(s.plugCats.map(\.label), ["Gömülü", "Kurulu", "Beceriler"])
        XCTAssertEqual(s.plugCats[1].items.first?.resourceLabel, "eklenti + beceri")
        XCTAssertEqual(s.plugCats[2].items.first?.resourceLabel, "beceri")
    }

    func testMonoAdiTireliAdlardaDaIkiHarfVerir() {
        let s = store([ext("oa-gateway.ts", kind: "builtin")])
        XCTAssertEqual(s.installedTiles.first?.mono, "OG")
    }

    // ── bot formu (allowlist) ──────────────────────────────────────────

    /// Yeni bot GÖMÜLÜ eklentilerle başlar: sunucunun varsayılanı da bu.
    /// Boş başlasaydı formdan geçen bot onay kapısız yaratılırdı.
    func testYeniBotGomuluEklentilerleBaslar() {
        let s = store([ext("oa-gateway.ts", kind: "builtin"), ext("notlar.ts")])
        s.resetNB()
        XCTAssertEqual(s.nbPlugs, ["oa-gateway.ts"])
        XCTAssertEqual(s.nbExtensions, ["oa-gateway.ts"])
    }

    /// Katalog boşken (sunucuya bağlı değiliz) allowlist GÖNDERİLMEZ: boş dizi
    /// gitseydi bağlantı yokluğu yüzünden botun onay kapısı silinirdi.
    func testKatalogBoskenAllowlistGonderilmez() {
        let s = store([], online: false)
        s.resetNB()
        XCTAssertNil(s.nbExtensions)
    }

    // ── paket galerisi ─────────────────────────────────────────────────

    /// Galeri kipi kurulu listeden AYRI: "Kur" düğmesi kurulu eklentilerin
    /// arasında durmasın, arama kutusu da kip değişince temizlensin (aynı
    /// metin iki farklı listede iki farklı şey arar).
    func testGaleriKipiAramayiTemizler() {
        let s = store([ext("oa-gateway.ts", kind: "builtin")])
        s.plugQuery = "kurulu araması"
        s.openPluginCatalog()
        XCTAssertEqual(s.plugMode, "catalog")
        XCTAssertEqual(s.plugQuery, "")

        s.plugQuery = "galeri araması"
        s.closePluginCatalog()
        XCTAssertEqual(s.plugMode, "installed")
        XCTAssertEqual(s.plugQuery, "")
    }

    func testSecimSiraliGonderilir() {
        let s = store([ext("oa-gateway.ts", kind: "builtin"), ext("b.ts"), ext("a.ts")])
        s.nbPlugs = ["b.ts", "a.ts", "oa-gateway.ts"]
        XCTAssertEqual(s.nbExtensions, ["a.ts", "b.ts", "oa-gateway.ts"],
                       "sıra kararlı olmalı: aynı seçim her kaydette aynı gövdeyi üretsin")
    }
}
