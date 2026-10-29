import XCTest

@testable import OpenAssistMac

/// Kenar çubuğu gruplaması (ADIM 10 ikinci aşama): ekipler üstte, üyeleri
/// altlarında girintili, ekipsiz botlar sonda. Sıra ve kimlik burada kırılırsa
/// kullanıcı yanlış kanala yazar — grup sohbeti ile birebir sohbet aynı
/// listede yan yana duruyor.
final class EkipSatirlariTests: XCTestCase {
    private func bot(_ id: String, _ ad: String) -> DAgent {
        DAgent(id: id, name: ad, c1: "#fff", c2: "#000", time: "", preview: "")
    }

    private func ekip(_ id: String, _ ad: String, _ uyeler: [String]) -> DAgent {
        DAgent(id: id, name: ad, c1: "#fff", c2: "#000", time: "", preview: "",
               team: uyeler, isTeam: true)
    }

    func testEkipUsttUyeleriAltindaEkipsizBotlarSonda() {
        let ajanlar = [
            bot("a", "Kişisel Asistan"),
            ekip("t", "3E Satış", ["k", "l"]),
            bot("k", "Satış Koordinatörü"),
            bot("l", "Lead Araştırmacısı"),
        ]

        let satirlar = SidebarRow.build(ajanlar, query: "")

        XCTAssertEqual(satirlar.map(\.agent.name),
                       ["3E Satış", "Satış Koordinatörü", "Lead Araştırmacısı", "Kişisel Asistan"],
                       "ekip önce, üyeleri sırayla, ekipsiz bot en sonda")
        XCTAssertEqual(satirlar.map(\.tur), [.ekip, .uye, .uye, .bot])
        XCTAssertEqual(satirlar[1].ekipID, "t", "üye satırı ekibine bağlı olmalı")
    }

    func testUyeEkipsizListeyeIkinciKezGirmez() {
        let ajanlar = [ekip("t", "3E Satış", ["k"]), bot("k", "Satış Koordinatörü")]

        let satirlar = SidebarRow.build(ajanlar, query: "")

        XCTAssertEqual(satirlar.filter { $0.agent.id == "k" }.count, 1,
                       "ekibin üyesi listede bir kez görünür")
    }

    func testUyesizEkipYineEkipSatiridir() {
        // Üyesiz ekip `team` listesinden ayırt edilemez: kind taşınmasaydı
        // tekil bot gibi çizilir, kullanıcı grup kanalına yazdığını bilmezdi.
        let satirlar = SidebarRow.build([ekip("t", "Boş Ekip", [])], query: "")

        XCTAssertEqual(satirlar.count, 1)
        XCTAssertEqual(satirlar[0].tur, .ekip)
    }

    func testAyniBotIkiEkipteAyriKimlikAlir() {
        let ajanlar = [
            ekip("t1", "Satış", ["k"]),
            ekip("t2", "Pazarlama", ["k"]),
            bot("k", "Ortak Bot"),
        ]

        let satirlar = SidebarRow.build(ajanlar, query: "")

        XCTAssertEqual(Set(satirlar.map(\.id)).count, satirlar.count,
                       "ForEach kimlikleri benzersiz olmalı (aynı bot iki ekipte)")
        XCTAssertEqual(satirlar.filter { $0.agent.id == "k" }.map(\.ekipID), ["t1", "t2"])
    }

    func testAramaGruplamayiDuzlestirir() {
        let ajanlar = [
            ekip("t", "3E Satış", ["k"]),
            bot("k", "Satış Koordinatörü"),
            bot("a", "Kişisel Asistan"),
        ]

        let satirlar = SidebarRow.build(ajanlar, query: "satış")

        XCTAssertEqual(satirlar.map(\.agent.name), ["3E Satış", "Satış Koordinatörü"])
        XCTAssertEqual(satirlar.map(\.tur), [.ekip, .bot], "aramada girinti yok")
        XCTAssertNil(satirlar[1].ekipID)
    }

    func testEksikUyeAtlanir() {
        // Sunucu üyeyi silmiş ama ekip satırı henüz tazelenmemiş olabilir.
        let satirlar = SidebarRow.build([ekip("t", "3E Satış", ["yok"])], query: "")

        XCTAssertEqual(satirlar.count, 1, "bilinmeyen üye id'si satır üretmez")
    }
}
