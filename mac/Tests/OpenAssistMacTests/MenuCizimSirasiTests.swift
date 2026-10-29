import SwiftUI
import XCTest

@testable import OpenAssistMac

/// Açılır menünün GERÇEKTEN üstte çizildiğini ölçen test.
///
/// `MenuKat` eşlemesinin doğru olması yetmez: zIndex'in bir katmanda
/// unutulması derlenir, testten geçer ve yalnız ekranda görünür. Bu yüzden
/// görünüm offscreen render edilip piksel düzeyinde bakılıyor.
///
/// Ölçüt geometriden BAĞIMSIZ: menü kapalı ve açık hâller karşılaştırılır,
/// farklı pikseller hangi satırlarda birikiyorsa o satırlar menünün göründüğü
/// yerdir. Menü doğru çizilirse bu satırlar KESİNTİSİZ bir bant olur; bir kart
/// menünün ortasını yutuyorsa bandın ortasında boşluk kalır (hatanın ekran
/// görüntüsündeki hâli tam buydu: menünün üst ve alt uçları görünüyor, ortası
/// Motor kartının altında kayboluyordu).
@MainActor
final class MenuCizimSirasiTests: XCTestCase {
    private func kurulum() -> DesignStore {
        let s = DesignStore()
        s.live.setEnginesForTesting([
            EngineInfo(id: "pi", name: "Pi", available: true, version: "0.84.2",
                       loggedIn: true, account: nil, plan: nil,
                       detail: "kurulu — 3 model seçilebilir", probed: true,
                       checkedAt: Date(), models: []),
        ])
        // Hem sağlayıcı hem model listesi menünün 260pt tavanına dayanmalı:
        // kısa liste hiçbir şeyin üstüne gelmez ve test hatayı göremezdi.
        let modeller = (0 ..< 40).map { j in
            CatalogModel(id: "m\(j)", name: "Model \(j)", reasoning: false,
                         toolCall: true, contextWindow: 1000, maxTokens: 100,
                         input: ["text"], releaseDate: "2026-01-01")
        }
        s.live.setCatalogForTesting((0 ..< 40).map { i in
            CatalogProvider(id: "s\(i)", name: "Sağlayıcı \(i)", baseURL: "https://ornek/v1",
                            api: "openai-completions", doc: nil, models: modeller)
        })
        return s
    }

    func testKatalogMenusuMotorKartininUstunde() throws {
        let s = kurulum()
        let kapali = try goruntu(s)
        s.openDD = "prov:yeni"
        let acik = try goruntu(s)

        let satirlar = farkliSatirlar(kapali, acik)
        XCTAssertFalse(satirlar.isEmpty, "menü hiç çizilmedi — test kurulumu bozuk")

        let bosluklar = bosluk(satirlar)
        XCTAssertTrue(bosluklar.isEmpty,
                      "menünün ortası örtülü — \(bosluklar) satır aralıklarında "
                          + "başka bir kart menünün üstüne çizilmiş (zIndex bir katmanda eksik)")
    }

    /// Düzenleyicide de aynı risk var: model menüsü kartın ALTINDAKİ
    /// Kaydet/Sil düğmelerinin üstüne açılıyor.
    ///
    /// Ölçüt burada "bant boşluğu" değil (düğmeler dar, bandı kesmiyorlar):
    /// menü sağa hizalı açıldığı için sağdaki "Sağlayıcıyı sil" düğmesinin
    /// üstüne gelir ve o düğmenin kırmızısı (Theme.error) menünün altında
    /// kalmalıdır. Kaydet düğmesi SOLDA olduğu için ölçüte uygun değil —
    /// menüyle hiç çakışmıyor (ölçülerek görüldü).
    func testDuzenleyiciModelMenusuDugmelerinUstunde() throws {
        let s = kurulum()
        s.live.setProvidersForTesting([
            ProviderInfo(id: "s0", name: "Sağlayıcı 0", baseURL: "https://ornek/v1",
                         api: "openai-completions", enabled: true, hasKey: true,
                         position: 0, models: [ProviderInfo.Model(id: "m")]),
        ])
        s.provAc(s.allProviders[0])

        let kapali = try goruntu(s)
        let oncekiKirmizi = renkSayisi(kapali, Theme.error)
        XCTAssertGreaterThan(oncekiKirmizi, 50, "silme düğmesi çizilmedi — kurulum bozuk")

        s.openDD = "prov:model"
        let acik = try goruntu(s)
        XCTAssertFalse(farkliSatirlar(kapali, acik).isEmpty, "menü hiç çizilmedi")

        XCTAssertLessThan(renkSayisi(acik, Theme.error), oncekiKirmizi,
                          "silme düğmesi açık menünün ÜSTÜNE çizildi — "
                              + "model kartına zIndex verilmemiş")
    }

    // ── yardımcılar ──

    private func goruntu(_ store: DesignStore) throws -> CGImage {
        let renderer = ImageRenderer(content:
            ModelsView(store: store)
                .frame(width: 760, alignment: .leading)
                .frame(width: 860, height: 900, alignment: .topLeading)
                .background(Theme.light.winBg))
        renderer.scale = 1
        return try XCTUnwrap(renderer.cgImage, "offscreen render başarısız")
    }

    /// Menünün göründüğü satırlar: iki kare arasında ANLAMLI sayıda piksel
    /// değişen satırlar. Eşik gölge ve kenar yumuşatmasının tek tük
    /// piksellerini eler.
    private func farkliSatirlar(_ a: CGImage, _ b: CGImage) -> [Int] {
        let pa = pikseller(a), pb = pikseller(b)
        guard pa.count == pb.count else { return [] }
        let en = a.width, boy = a.height
        var out: [Int] = []
        for y in 0 ..< boy {
            var sayi = 0
            for x in 0 ..< en {
                let i = (y * en + x) * 4
                if pa[i] != pb[i] || pa[i + 1] != pb[i + 1] || pa[i + 2] != pb[i + 2] {
                    sayi += 1
                }
            }
            if sayi >= 8 { out.append(y) }
        }
        return out
    }

    /// Bantın içindeki kesintiler. Bir satırlık kırıklar (menü içindeki boş
    /// aralıklar) sayılmaz; kart yüksekliği kadar (>= 20 satır) süren
    /// kesinti örtülme demektir.
    private func bosluk(_ satirlar: [Int]) -> [String] {
        var out: [String] = []
        for (a, b) in zip(satirlar, satirlar.dropFirst()) where b - a > 20 {
            out.append("\(a)–\(b)")
        }
        return out
    }

    /// Verilen renge yakın piksel sayısı (kenar yumuşatması için tolerans).
    private func renkSayisi(_ img: CGImage, _ renk: Color) -> Int {
        guard let bilesen = NSColor(renk).usingColorSpace(.deviceRGB) else { return 0 }
        let hedef = (UInt8(bilesen.redComponent * 255),
                     UInt8(bilesen.greenComponent * 255),
                     UInt8(bilesen.blueComponent * 255))
        let px = pikseller(img)
        var sayi = 0
        for i in stride(from: 0, to: px.count, by: 4) {
            if abs(Int(px[i]) - Int(hedef.0)) < 12,
               abs(Int(px[i + 1]) - Int(hedef.1)) < 12,
               abs(Int(px[i + 2]) - Int(hedef.2)) < 12 {
                sayi += 1
            }
        }
        return sayi
    }

    private func pikseller(_ img: CGImage) -> [UInt8] {
        let en = img.width, boy = img.height
        var buf = [UInt8](repeating: 0, count: en * boy * 4)
        let ctx = CGContext(data: &buf, width: en, height: boy, bitsPerComponent: 8,
                            bytesPerRow: en * 4,
                            space: CGColorSpaceCreateDeviceRGB(),
                            bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)
        ctx?.draw(img, in: CGRect(x: 0, y: 0, width: en, height: boy))
        return buf
    }
}
