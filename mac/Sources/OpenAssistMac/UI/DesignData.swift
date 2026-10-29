import Foundation

// Tasarım prototipinin (panel.dc.html) mock verisi — birebir.

struct DAgent: Identifiable {
    let id: String
    var name: String
    var c1: String
    var c2: String
    var time: String
    var preview: String
    var team: [String]?
    var unread: Bool
    var desc: String
    /// Satır bir EKİP mi (`bots.kind == "team"`)? `team` listesinden
    /// türetilemez: üyesi olmayan ekip de bir ekiptir ve kenar çubuğunda
    /// tekil bot gibi görünmemeli (tıklanınca grup kanalı açılır, mesaj
    /// "ekipte üye yok" hatası verir — kullanıcı sebebi görmeli).
    var isTeam: Bool

    init(id: String, name: String, c1: String, c2: String, time: String, preview: String,
         team: [String]? = nil, unread: Bool = false, desc: String = "", isTeam: Bool = false) {
        self.id = id
        self.name = name
        self.c1 = c1
        self.c2 = c2
        self.time = time
        self.preview = preview
        self.team = team
        self.unread = unread
        self.desc = desc
        self.isTeam = isTeam
    }
}

/// Kenar çubuğu satırı (ADIM 10 ikinci aşama): ekipler üstte, üyeleri hemen
/// altlarında GİRİNTİLİ, ekipsiz botlar en sonda düz.
struct SidebarRow: Identifiable {
    enum Tur { case ekip, uye, bot }

    let agent: DAgent
    let tur: Tur
    /// Üye satırının hangi ekibin altında çizildiği. Bir bot BİRDEN ÇOK
    /// ekipte olabilir; kimlik yalnız bot id'si olsaydı ForEach aynı id'yi
    /// iki kez görürdü.
    let ekipID: String?

    var id: String { (ekipID ?? "-") + "/" + agent.id }

    /// Satırları kurar (saf fonksiyon — testte doğrudan çağrılır).
    ///
    /// Arama AKTİFKEN gruplama düşer: kullanıcı adı yazarken sonucu ekibin
    /// altında aramak zorunda kalmasın, eşleşen ne varsa düz listede çıksın.
    static func build(_ agents: [DAgent], query: String) -> [SidebarRow] {
        let q = query.trimmingCharacters(in: .whitespaces).lowercased()
        if !q.isEmpty {
            return agents
                .filter { $0.name.lowercased().contains(q) }
                .map { SidebarRow(agent: $0, tur: $0.isTeam ? .ekip : .bot, ekipID: nil) }
        }
        var rows: [SidebarRow] = []
        var uyeOlan = Set<String>()
        let sozluk = Dictionary(agents.map { ($0.id, $0) }, uniquingKeysWith: { ilk, _ in ilk })
        for ekip in agents where ekip.isTeam {
            rows.append(SidebarRow(agent: ekip, tur: .ekip, ekipID: nil))
            for uyeID in ekip.team ?? [] {
                guard let uye = sozluk[uyeID] else { continue }
                uyeOlan.insert(uyeID)
                rows.append(SidebarRow(agent: uye, tur: .uye, ekipID: ekip.id))
            }
        }
        for a in agents where !a.isTeam && !uyeOlan.contains(a.id) {
            rows.append(SidebarRow(agent: a, tur: .bot, ekipID: nil))
        }
        return rows
    }
}

struct DToolCall: Identifiable {
    let id = UUID()
    let call: String
    let result: String
    let ms: Int
    let err: Bool

    init(_ call: String, _ result: String, _ ms: Int, err: Bool = false) {
        self.call = call
        self.result = result
        self.ms = ms
        self.err = err
    }

    /// c>=1000 → "1,2 sn", değilse "340 ms"
    var msLabel: String {
        if ms >= 1000 {
            return String(format: "%.1f", Double(ms) / 1000).replacingOccurrences(of: ".", with: ",") + " sn"
        }
        return "\(ms) ms"
    }
}

struct DCheckItem: Identifiable {
    let id = UUID()
    let label: String
    let text: String

    init(_ label: String, _ text: String) {
        self.label = label
        self.text = text
    }
}

enum DMsgKind {
    case divider, user, bot, file, work, tool, check, approve, error, from
}

struct DMsg: Identifiable {
    var id: String = UUID().uuidString
    let k: DMsgKind
    var text = ""
    var title = ""
    var name = ""
    var meta = ""
    var summary = ""
    var dur = ""
    var to = ""
    var fix: String?
    var fixLabel = ""
    var calls: [DToolCall] = []
    var items: [DCheckItem] = []
    var bot: String?
    var ap: String?
    /// Onay kartında "Hep izin ver" sunulsun mu (sunucunun kararı).
    var hepIzin = false
    /// Ek kimliği (ADIM 5): dosya kartının indir/aç aksiyonunu taşır.
    var fid: UUID?
    /// Ek türü (pdf|image|doc|file): rozet gösterimi için.
    var ekKind = ""

    static func divider(_ text: String) -> DMsg { DMsg(k: .divider, text: text) }
    static func user(_ text: String) -> DMsg { DMsg(k: .user, text: text) }
    static func bot(_ text: String) -> DMsg { DMsg(k: .bot, text: text) }
    static func file(_ name: String, _ meta: String, fid: UUID? = nil, kind: String = "file") -> DMsg {
        DMsg(k: .file, name: name, meta: meta, fid: fid, ekKind: kind)
    }
    static func work(_ title: String, _ text: String) -> DMsg { DMsg(k: .work, text: text, title: title) }
    static func check(_ items: [DCheckItem]) -> DMsg { DMsg(k: .check, items: items) }
    static func from(_ bot: String, _ text: String, ap: String? = nil) -> DMsg {
        DMsg(k: .from, text: text, bot: bot, ap: ap)
    }
}

struct DRoutine: Identifiable {
    let id = UUID()
    var label: String
    var sub: String
    var paused: Bool

    init(_ label: String, _ sub: String, _ paused: Bool = false) {
        self.label = label
        self.sub = sub
        self.paused = paused
    }
}

enum DTaskStatus: String {
    case running, queued, waiting, done, error

    /// TASK_ST: [etiket, nokta rengi, durum yazı rengi (nil → text3)]
    var label: String {
        switch self {
        case .running: return "Çalışıyor"
        case .queued: return "Sırada"
        case .waiting: return "Onay bekliyor"
        case .done: return "Bitti"
        case .error: return "Hata"
        }
    }

    var dotHex: String {
        switch self {
        case .running: return "#3478f6"
        case .queued: return "#a1a1a6"
        case .waiting: return "#f0a020"
        case .done: return "#28c840"
        case .error: return "#ff453a"
        }
    }

    /// nil → var(--text3)
    var statusHex: String? {
        switch self {
        case .running: return "#3478f6"
        case .waiting: return "#c77e0a"
        case .error: return "#ff453a"
        case .queued, .done: return nil
        }
    }
}

struct DTask: Identifiable {
    let id = UUID()
    let label: String
    let status: DTaskStatus
    let sub: String
    let pct: Int

    init(_ label: String, _ status: DTaskStatus, _ sub: String, _ pct: Int) {
        self.label = label
        self.status = status
        self.sub = sub
        self.pct = pct
    }

    var hasBar: Bool { status == .running && pct > 0 }
}

struct DActivityItem: Identifiable {
    let id = UUID()
    let time: String
    let text: String
    let meta: String
    let bot: String
    let err: Bool

    init(_ time: String, _ text: String, _ meta: String, _ bot: String, _ err: Bool) {
        self.time = time
        self.text = text
        self.meta = meta
        self.bot = bot
        self.err = err
    }
}



struct DConn {
    let name: String
    let on: Bool

    init(_ name: String, _ on: Bool) {
        self.name = name
        self.on = on
    }
}

/// Bot şablonu — formu bir tıkla doldurur. Eklenti listesi TAŞIMAZ: eklentiler
/// artık sunucudaki gerçek kataloğdan geliyor, şablona gömülü bir ad kurulu
/// olmayabilirdi ve o botun her koşusu düşerdi.
struct DTemplate: Identifiable {
    let id = UUID()
    let name: String
    let desc: String
    let c: Int
}

enum DesignData {
    static let nbc: [(String, String)] = [
        ("#a58a6f", "#7d5a3c"), ("#f07470", "#d02f2b"), ("#f6a86b", "#e06b3c"),
        ("#f2ce6b", "#d9a53a"), ("#7ed6a0", "#35a25f"), ("#6fd0c3", "#2f9a8c"),
        ("#6fa8f5", "#3b6fd4"), ("#b18cf0", "#7b4fd6"), ("#f28bb4", "#d14f83"),
        ("#a8a8ad", "#6f6f75"),
    ]

    static let templates: [DTemplate] = [
        DTemplate(name: "Satış Takipçisi", desc: "Cevapsız kalan teklifleri izler, takip e-postaları hazırlar.", c: 2),
        DTemplate(name: "Fatura Toplayıcı", desc: "Tedarikçi portallarından faturaları toplar ve muhasebeye iletir.", c: 4),
        DTemplate(name: "Destek Nöbetçisi", desc: "Gelen destek taleplerini önceliklendirir, yanıt taslağı hazırlar.", c: 6),
        DTemplate(name: "Rakip İzleyici", desc: "Rakip fiyat ve duyurularını izler, haftalık özet çıkarır.", c: 8),
    ]


}
