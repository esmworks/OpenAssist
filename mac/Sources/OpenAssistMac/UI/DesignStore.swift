import SwiftUI
import Observation
import AppKit

// panel.dc.html Component durumu + davranışları — birebir Swift karşılığı.

enum Screen {
    case chat, inbox, activity, settings, plugins, newbot
}

struct DToast: Equatable {
    var title: String
    var text: String
}

/// Ayarlar durumu. Üç kaynaktan beslenir, hepsi ayrı yerde yaşar:
///   · yerel tercihler (menubar, bildirim anahtarları) → UserDefaults
///   · sistem gerçeği (launchAtLogin) → SMAppService'e sorulur
///   · sunucu politikası (dailyLimit, quietHours) → /api/policy
/// Codable olan yalnız yerel tercihlerdir; ötekiler her açılışta
/// kaynağından tazelenir, yoksa ekran yalan söyler.
struct DSettings: Codable {
    var launchAtLogin = false
    var menubar = true
    var notifReplies = true
    var notifApprovals = true
    var notifRoutines = false
    var sound = true
    var quietHours = true
    var dailyLimit = "25"

    /// Diskte tutulanlar — sistem/sunucu kaynaklı alanlar dışarıda.
    private enum CodingKeys: String, CodingKey {
        case menubar, notifReplies, notifApprovals, notifRoutines, sound
    }

    init() {}

    /// Eksik anahtar varsayılana düşer. Sentezlenen çözücü olmayan alanda
    /// HATA verirdi; kayıt `try?` ile okunduğu için sonuç, yeni bir ayar
    /// eklendiğinde kullanıcının TÜM tercihlerinin sessizce sıfırlanması
    /// olurdu.
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        let d = DSettings()
        menubar = try c.decodeIfPresent(Bool.self, forKey: .menubar) ?? d.menubar
        notifReplies = try c.decodeIfPresent(Bool.self, forKey: .notifReplies) ?? d.notifReplies
        notifApprovals = try c.decodeIfPresent(Bool.self, forKey: .notifApprovals) ?? d.notifApprovals
        notifRoutines = try c.decodeIfPresent(Bool.self, forKey: .notifRoutines) ?? d.notifRoutines
        sound = try c.decodeIfPresent(Bool.self, forKey: .sound) ?? d.sound
    }
}

struct InboxItem: Identifiable {
    let id: String
    let msg: DMsg
    let botId: String
    let botName: String
    let c2: String
    let time: String
}

struct ActivityDay: Identifiable {
    let id: String
    let label: String
    let items: [DActivityItem]
}

struct PaletteItem: Identifiable {
    let id: String
    let group: String
    let label: String
    let hint: String
    let action: @MainActor () -> Void
}

struct MBTask: Identifiable {
    let id = UUID()
    let label: String
    let meta: String
    let dotHex: String
    let hasBar: Bool
    let pct: Int
}

/// DPlugin — Eklentiler ekranının ve bot formundaki seçim listesinin satırı.
/// Sunucudaki KURULU eklentiden türer (mock katalog kaldırıldı): kimliği
/// allowlist adıdır, çünkü koşuda motora verilen şey odur.
struct DPlugin: Identifiable, Hashable {
    let id: String
    let name: String
    let desc: String
    let kind: String // builtin | file | npm | skill
    let version: String
    /// Motora ne taşıdığı: extension | skill | prompt | theme.
    let resources: [String]
    /// Bu eklentiyi allowlist'inde taşıyan botların adları.
    let usedBy: [String]

    /// Gömülü eklenti ikiliyle gelir: kaldırılamaz, güncellenmez.
    var builtin: Bool { kind == "builtin" }

    init(_ e: ExtensionInfo) {
        id = e.id
        name = e.name
        desc = e.description
        kind = e.kind
        version = e.version ?? ""
        resources = e.resources ?? []
        usedBy = e.usedBy
    }

    /// "eklenti + beceri" — kullanıcı neyi açtığını görsün. Beceri de modele
    /// iş yaptırır; "eklenti kurdum" sanıp talimat demeti açmak başka bir şey.
    var resourceLabel: String {
        let adlar = ["extension": "eklenti", "skill": "beceri",
                     "prompt": "şablon", "theme": "tema"]
        return resources.compactMap { adlar[$0] }.joined(separator: " + ")
    }

    /// Baş harfler (mono): "oa-gateway" → "OG". Ad tire/alt çizgi/nokta ile de
    /// bölünür — eklenti adları boşluk taşımıyor, yalnız boşluğa bakan eski
    /// kural hepsini tek harfe düşürürdü.
    var mono: String {
        let parts = name.split(whereSeparator: { " -_.".contains($0) })
        return String(parts.compactMap(\.first).prefix(2)).uppercased()
    }

    /// "3 bot kullanıyor" / "kullanılmıyor" — kaldırmadan önce kimin
    /// bozulacağını söyler.
    var usageLabel: String {
        usedBy.isEmpty ? "kullanılmıyor" : "\(usedBy.count) bot kullanıyor"
    }
}

struct StatCard: Identifiable {
    let id: String
    let label: String
    let value: String
    var sub: String?
    var big = true
}

struct StatBar: Identifiable {
    let id = UUID()
    let h: CGFloat
    let label: String
}

@MainActor
@Observable
final class DesignStore {
    /// Sunucu köprüsü — botlar, mesajlar, onaylar, rutinler buradan gelir.
    let live = LiveStore()

    let userName = NSFullUserName().isEmpty ? "Kullanıcı" : NSFullUserName()

    // ── Durum (initial state birebir) ──
    var active = ""
    var darkOv: Bool?
    var rightOpen = true
    var draft = ""
    var query = ""
    var read: Set<String> = []
    var screen: Screen = .chat
    var settingsTab = "general"
    var openDD: String?
    var plugQuery = ""
    /// "all" = kurulu her eklenti, "used" = bir botun allowlist'inde olanlar.
    var plugTab = "all"
    var range = "30" {
        didSet { live.setStatsRange(days: range == "7" ? 7 : 30) }
    }
    var statBot: String?
    var actFilter = "all"
    var rightTab = "routines"
    var toolOpen: Set<String> = []
    var raOpen = false
    var raLabel = ""
    var raSub = ""
    var paletteOpen = false
    var pq = ""
    var pi = 0
    var menubarOpen = false

    // Yeni bot / ekip formu
    var nbName = ""
    var nbDesc = ""
    var nbC = 7
    var nbPlugs: Set<String> = []
    var nbModel: String?
    /// Botun motoru (nil = sunucu varsayılanı). Model listesi buna bağlı.
    var nbEngine: String?
    var nbKind = "bot"
    var nbMembers: Set<String> = []
    var nbTemp: Double = 30
    var nbPrompt = ""
    var nbAdv = false
    var nbEdit: String?

    // Ayarlar — menubar tercihi UserDefaults'a yansır; MenuBarExtra'nın
    // isInserted'ı @AppStorage'dan okur (App gövdesinde store okumak
    // TextField odak sistemiyle sonsuz sahne-güncelleme döngüsü yaratıyor).
    var prefs: DSettings = {
        var s = (UserDefaults.standard.data(forKey: "prefs")
            .flatMap { try? JSONDecoder().decode(DSettings.self, from: $0) }) ?? DSettings()
        // menubarExtra ayrı anahtar: MenuBarExtra'nın @AppStorage'ı bunu okur.
        if let v = UserDefaults.standard.object(forKey: "menubarExtra") as? Bool {
            s.menubar = v
        }
        s.launchAtLogin = LoginItem.enabled // sistem gerçeği
        return s
    }() {
        didSet { savePrefs() }
    }

    var toast: DToast?
    @ObservationIgnored private var toastTask: Task<Void, Never>?

    // ── Tema ──
    //
    // darkOv nil = sistemi izle. Seçim kalıcıdır (eskiden her açılışta
    // açık temaya dönüyordu) ve "Sistem" gerçekten macOS görünümünü izler.

    var dark: Bool { darkOv ?? systemDark }
    var theme: Theme { dark ? .darkTheme : .light }

    /// "system" | "light" | "dark"
    var themeChoice: String {
        get { darkOv == nil ? "system" : (darkOv! ? "dark" : "light") }
        set {
            darkOv = newValue == "system" ? nil : (newValue == "dark")
            UserDefaults.standard.set(newValue, forKey: "theme")
        }
    }

    func toggleTheme() { themeChoice = dark ? "light" : "dark" }

    /// macOS görünümü — "Sistem" seçiliyken tema bunu izler.
    private(set) var systemDark = DesignStore.readSystemDark()

    static func readSystemDark() -> Bool {
        UserDefaults.standard.string(forKey: "AppleInterfaceStyle")?.lowercased() == "dark"
    }

    private func savePrefs() {
        UserDefaults.standard.set(prefs.menubar, forKey: "menubarExtra")
        if let data = try? JSONEncoder().encode(prefs) {
            UserDefaults.standard.set(data, forKey: "prefs")
        }
        syncNotifier()
    }

    /// Bildirim anahtarları Notifier'a taşınır (tetikleyen LiveStore'dur).
    private func syncNotifier() {
        let n = live.notifier
        n.replies = prefs.notifReplies
        n.approvals = prefs.notifApprovals
        n.routines = prefs.notifRoutines
        n.sound = prefs.sound
        if prefs.notifReplies || prefs.notifApprovals || prefs.notifRoutines {
            Task { await n.ensureAuthorization() }
        }
    }

    /// "Başlangıçta çalıştır" — sisteme yazar; reddedilirse anahtar geri
    /// döner ve gerekçe söylenir (sessizce açık görünmesi yalan olurdu).
    func setLaunchAtLogin(_ on: Bool) {
        if let err = LoginItem.set(on) {
            prefs.launchAtLogin = LoginItem.enabled
            showToast("Başlangıçta çalıştır", err)
            return
        }
        prefs.launchAtLogin = LoginItem.enabled
        if LoginItem.needsApproval {
            showToast("Başlangıçta çalıştır", "macOS onayı bekliyor — Sistem Ayarları › Genel › Giriş Öğeleri")
        }
    }

    // ── Yaşam döngüsü ──

    /// Uygulama açılışında: sunucu senkronu + runner başlar.
    func start() {
        darkOv = {
            switch UserDefaults.standard.string(forKey: "theme") {
            case "dark": return true
            case "light": return false
            default: return nil // sistem
            }
        }()
        observeSystemAppearance()
        syncNotifier()
        live.start()
        Task {
            await live.refresh()
            await live.refreshSlow()
            syncActive()
        }
    }

    /// macOS açık/koyu değişince "Sistem" seçili temayı anında çevir.
    private func observeSystemAppearance() {
        DistributedNotificationCenter.default.addObserver(
            forName: Notification.Name("AppleInterfaceThemeChangedNotification"),
            object: nil, queue: .main
        ) { [weak self] _ in
            MainActor.assumeIsolated { self?.systemDark = DesignStore.readSystemDark() }
        }
    }

    /// Aktif bot sunucudan gelen listede yoksa ilkine düş (ilk açılış,
    /// silinen bot, sunucu değişimi).
    func syncActive() {
        let ids = live.agents.map(\.id)
        if active.isEmpty || !ids.contains(active) {
            active = ids.first ?? ""
            if let uuid = curUUID { live.focus(bot: uuid) }
        }
    }

    // ── Botlar ──
    var allAgents: [DAgent] { live.agents }

    var curUUID: UUID? { UUID(uuidString: active) }

    var filteredAgents: [DAgent] {
        let q = query.lowercased()
        return q.isEmpty ? allAgents : allAgents.filter { $0.name.lowercased().contains(q) }
    }

    /// Kenar çubuğu satırları: ekipler üstte, üyeleri altlarında girintili,
    /// ekipsiz botlar sonda (ADIM 10 ikinci aşama).
    var sidebarRows: [SidebarRow] { SidebarRow.build(allAgents, query: query) }

    var searchEmpty: Bool { !allAgents.isEmpty && filteredAgents.isEmpty && !query.isEmpty }
    var noBots: Bool { allAgents.isEmpty }

    func agent(_ id: String) -> DAgent? { allAgents.first { $0.id == id } }

    var cur: DAgent {
        agent(active) ?? allAgents.first
            ?? DAgent(id: "__none", name: "OpenAssist", c1: "#b18cf0", c2: "#7b4fd6", time: "", preview: "")
    }

    func teamOf(_ id: String) -> [String]? {
        guard let team = live.bot(id: id)?.members, !team.isEmpty else { return nil }
        return team.map(\.uuidString)
    }

    func isUnread(_ a: DAgent) -> Bool { a.unread && !read.contains(a.id) }

    func pick(_ id: String) {
        active = id
        screen = .chat
        read.insert(id)
        if let uuid = UUID(uuidString: id) { live.focus(bot: uuid) }
    }

    // ── Mesajlar ──
    func msgs(for id: String) -> [DMsg] {
        guard let uuid = UUID(uuidString: id) else { return [] }
        return live.chatMessages(for: uuid)
    }

    /// Düzenlenen mesaj (ADIM 9): doluysa Gönder yeni mesaj değil, o mesajın
    /// düzenlenmiş sürümünü üretir (sunucu eski sürümü görünmez yapar).
    var duzenlenen: (bot: UUID, id: UUID)?
    var isEditing: Bool { duzenlenen != nil }

    func beginEdit(messageID: String) {
        guard let bot = curUUID, let mid = UUID(uuidString: messageID) else { return }
        guard let org = live.messages[bot]?.first(where: { $0.id == mid }) else { return }
        draft = org.text
        duzenlenen = (bot, mid)
    }

    func cancelEdit() {
        duzenlenen = nil
        draft = ""
    }

    /// Mesaj metnini panoya kopyalar.
    func copyMessage(_ text: String) {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(text, forType: .string)
        showToast(cur.name, "Metin panoya kopyalandı")
    }

    /// Gönderilebilir mi: metin ya da bekleyen ek var (ADIM 5).
    func canSend() -> Bool {
        guard let uuid = curUUID else { return false }
        let t = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        return !t.isEmpty || !(live.ekler[uuid] ?? []).isEmpty
    }

    func send() {
        let t = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !t.isEmpty else { return }
        // Düzenleme modu: yeni mesaj değil, seçili mesajın yeniden üretimi.
        if let d = duzenlenen {
            draft = ""
            duzenlenen = nil
            live.edit(bot: d.bot, mesajID: d.id, text: t)
            return
        }
        guard canSend(), let uuid = curUUID else { return }
        draft = ""
        live.send(bot: uuid, text: t)
    }

    // ── Ekler (ADIM 5) ──
    func ekler(for id: String) -> [Attachment] {
        guard let uuid = UUID(uuidString: id) else { return [] }
        return live.ekler[uuid] ?? []
    }

    func addEk(_ url: URL) {
        guard let uuid = curUUID else { return }
        live.addAttachment(bot: uuid, url: url)
    }

    func removeEk(_ id: UUID) {
        guard let uuid = curUUID else { return }
        live.removeAttachment(bot: uuid, id: id)
    }

    /// Eki indirip varsayılan uygulamada açar (ADIM 5).
    func openAttachment(_ id: UUID) {
        live.openAttachment(id)
    }

    // ── Toast ──
    func showToast(_ title: String, _ text: String) {
        toastTask?.cancel()
        toast = DToast(title: title, text: text)
        toastTask = Task { [weak self] in
            try? await Task.sleep(for: .seconds(4.2))
            guard !Task.isCancelled else { return }
            self?.toast = nil
        }
    }

    // ── Onaylar ──

    /// Kart kararı — sunucuya gider (K17 vetoları orada da işler).
    ///
    /// always: "hep izin ver" — bu araç için kural yazılır ve aynı araç bir
    /// daha sorulmaz. Aynı işi arka arkaya yapan bot (on mükerrer kaydı
    /// silmek gibi) yoksa her adımda ayrı karta düşüyordu; resume izni tek
    /// kullanımlık olduğu için her kart ayrı bir park/resume turu demekti.
    func decide(_ name: String, _ id: String, _ ok: Bool, always: Bool = false) {
        live.resolve(runID: id, decision: ok ? "allow" : "deny", always: always)
        if always {
            showToast(name, ok ? "Hep izin verildi · bu araç bir daha sorulmayacak"
                              : "Hep reddedildi · bu araç bir daha sorulmayacak")
            return
        }
        showToast(name, ok ? "Onaylandı · koşu devam ediyor" : "Reddedildi · bot bilgilendirildi")
    }

    /// Kararı verilmiş kartın rozet durumu (sunucudan düşene kadar).
    var approvals: [String: String] { live.decided }

    func retry() {
        guard let uuid = curUUID else { return }
        live.send(bot: uuid, text: "Son işi tekrar dene.")
        showToast(cur.name, "Yeniden deneniyor…")
    }

    func goFix() {
        screen = .settings
        settingsTab = "connections"
    }

    // Onay Merkezi: izin bekleyen koşular (sorular sohbette cevaplanır).
    var inboxItems: [InboxItem] { live.inbox }

    var pendingCount: Int { inboxItems.count }
    var inboxSub: String { pendingCount > 0 ? "\(pendingCount) istek onay bekliyor" : "" }

    func approveAll() {
        Task {
            let summary = await live.approveAll()
            showToast("Onay Merkezi", summary)
        }
    }

    // ── Rutinler ──
    func routines(for id: String) -> [DRoutine] {
        guard let uuid = UUID(uuidString: id) else { return [] }
        return live.designRoutines(for: uuid)
    }

    func toggleRoutine(_ id: String, _ i: Int) {
        guard let uuid = UUID(uuidString: id),
              let rid = live.routineID(botID: uuid, index: i) else { return }
        let paused = live.designRoutines(for: uuid)[i].paused
        live.setRoutine(id: rid, enabled: paused) // duraklatılmışsa aç
    }

    /// Rutin ekle — takvim ifadesi cron'dur (boşsa her gün 09:00).
    func raAdd() {
        let l = raLabel.trimmingCharacters(in: .whitespaces)
        guard !l.isEmpty, let uuid = curUUID else { return }
        let cron = raSub.trimmingCharacters(in: .whitespaces)
        raOpen = false
        raLabel = ""
        raSub = ""
        Task {
            if await live.createRoutine(botID: uuid, name: l,
                                        cron: cron.isEmpty ? "0 9 * * *" : cron) {
                showToast(cur.name, "Rutin eklendi")
            } else {
                showToast("Rutin eklenemedi", live.lastError ?? "takvim ifadesi geçersiz (örn. 0 9 * * *)")
            }
        }
    }

    func raCancel() {
        raOpen = false
        raLabel = ""
        raSub = ""
    }

    // ── Bağlantılar ── (sunucudaki konektörler + Mac runner)
    func conns(for id: String) -> [DConn] { live.designConns }

    // ── Görevler ──
    func tasks(for id: String) -> [DTask] {
        guard let uuid = UUID(uuidString: id) else { return [] }
        return live.tasks(for: uuid)
    }

    // ── Ekip ──
    var curTeam: [String]? { teamOf(cur.id) }

    var teamMembers: [DAgent] { (curTeam ?? []).compactMap { agent($0) } }

    var teamCandidates: [DAgent] {
        // Ekip ekibe üye olamaz (sunucu da reddeder); üyesiz ekip de aday
        // listesinde görünmemeli — kind'a bakıyoruz, üye listesine değil.
        allAgents.filter { !$0.isTeam && !(curTeam ?? []).contains($0.id) }
    }

    func role(of id: String) -> String { "Üye" }

    /// Bot kullanım dağılımı — sunucu sayaçlarından (ad, yüzde pay).
    var botUsage: [(String, Int)] {
        guard let rows = live.stats?.byBot, !rows.isEmpty else { return [] }
        let total = max(1, rows.reduce(0) { $0 + $1.runs })
        return rows
            .sorted { $0.runs > $1.runs }
            .map { (live.botName($0.botID), Int((Double($0.runs) / Double(total) * 100).rounded())) }
    }

    func modelShort(of id: String) -> String {
        modelName(for: id).split(separator: "-").first.map(String.init) ?? ""
    }

    /// Ekibe bot ekle — üyeler sunucuda saklanır.
    func addToTeam(_ a: DAgent) {
        guard let teamID = curUUID, let memberID = UUID(uuidString: a.id) else { return }
        let members = (curTeam ?? []).compactMap { UUID(uuidString: $0) } + [memberID]
        Task {
            let ok = await live.updateBot(id: teamID, BotUpdate(members: members))
            showToast(cur.name, ok ? "\(a.name) ekibe eklendi" : (live.lastError ?? "eklenemedi"))
        }
    }

    // ── Bot silme / düzenleme ──
    func deleteBot(_ id: String) {
        openDD = nil
        guard let uuid = UUID(uuidString: id) else { return }
        Task {
            if await live.deleteBot(id: uuid) {
                syncActive()
                showToast("OpenAssist", "Bot silindi")
            } else {
                showToast("Silinemedi", live.lastError ?? "sunucu reddetti")
            }
        }
    }

    /// Sohbeti Temizle (sağ üst menü): tüm konuşma sunucuda silinir, bot
    /// sonraki koşuya taze bağlamla başlar. İki adımlı onay menüde.
    func clearChat() {
        openDD = nil
        guard let uuid = curUUID else { return }
        let ad = cur.name
        Task {
            if await live.clearChat(bot: uuid) {
                showToast(ad, "Sohbet temizlendi")
            } else {
                showToast("Temizlenemedi", live.lastError ?? "sunucu reddetti")
            }
        }
    }

    func openEditBot() {
        let c = cur
        var ci = DesignData.nbc.firstIndex { $0.0 == c.c1 } ?? 7
        if ci < 0 { ci = 7 }
        nbEdit = c.id
        nbName = c.name
        nbDesc = c.desc
        nbC = ci
        // Botun KAYITLI allowlist'i forma yüklenir. Boş bir setle açsaydık
        // kaydete basan kullanıcı farkında olmadan onay kapısını da silerdi.
        nbPlugs = Set(live.bot(id: c.id)?.extensions ?? [])
        nbEngine = engineID(for: c.id)
        nbModel = live.bot(id: c.id)?.model.isEmpty == false ? live.bot(id: c.id)?.model : nil
        nbKind = teamOf(c.id) != nil ? "team" : "bot"
        screen = .newbot
        openDD = nil
    }

    func resetNB() {
        nbName = ""
        nbDesc = ""
        nbC = 7
        // Yeni bot GÖMÜLÜ eklentilerle başlar — sunucunun varsayılanıyla aynı
        // (onay kapısı ve OpenAssist araçları orada). Boş başlasaydı formu
        // hiç açmayan kullanıcıyla açan kullanıcı farklı bot yaratırdı.
        nbPlugs = Set(live.extensions.filter(\.builtin).map(\.id))
        nbModel = nil
        nbEngine = nil
        nbKind = "bot"
        nbMembers = []
        nbTemp = 30
        nbPrompt = ""
        nbEdit = nil
    }

    func openNewBot() {
        resetNB()
        screen = .newbot
    }

    func closeNewBot() {
        screen = .chat
        nbEdit = nil
    }

    func applyTemplate(_ t: DTemplate) {
        nbName = t.name
        nbDesc = t.desc
        nbC = t.c
        // Eklenti seçimine DOKUNULMAZ: şablon bir metin taslağıdır, kurulu
        // olmayan bir eklenti adı yazsaydı o botun her koşusu düşerdi.
    }

    var nbTempLabel: String {
        String(format: "%.1f", nbTemp / 100).replacingOccurrences(of: ".", with: ",")
    }

    /// Kaydedilecek eklenti allowlist'i. Katalog boşken (sunucuya bağlı
    /// değiliz ya da liste henüz gelmedi) nil döner: alan gönderilmez ve
    /// sunucu kayıtlıya DOKUNMAZ. Boş dizi göndermek "hiçbir eklenti yükleme"
    /// demek olurdu — bağlantı yokluğu yüzünden botun onay kapısı silinirdi.
    var nbExtensions: [String]? {
        live.extensions.isEmpty ? nil : Array(nbPlugs).sorted()
    }

    /// Bot/ekip yarat ya da güncelle — sunucuda kalıcılaşır.
    func createBot() {
        let name = nbName.trimmingCharacters(in: .whitespaces)
        guard !name.isEmpty else { return }
        let desc = nbDesc.trimmingCharacters(in: .whitespaces)
        let (c1, c2) = DesignData.nbc[nbC]
        let model = nbModel
        let engine = nbEngine

        let plugs = nbExtensions

        if let eid = nbEdit, let uuid = UUID(uuidString: eid) {
            Task {
                let ok = await live.updateBot(id: uuid, BotUpdate(
                    name: name, description: desc,
                    colorPrimary: c1, colorSecondary: c2,
                    engine: engine, model: model ?? "", extensions: plugs))
                showToast("OpenAssist", ok ? "Bot güncellendi" : (live.lastError ?? "güncellenemedi"))
            }
            screen = .chat
            resetNB()
            return
        }

        let isTeam = nbKind == "team"
        let members = isTeam ? nbMembers.compactMap { UUID(uuidString: $0) } : []
        if isTeam, members.isEmpty {
            showToast("Yeni Ekip", "En az bir bot seç")
            return
        }
        Task {
            let created = await live.createBot(BotCreate(
                kind: isTeam ? "team" : "bot", name: name, description: desc,
                colorPrimary: c1, colorSecondary: c2, engine: engine, model: model,
                members: members.isEmpty ? nil : members, extensions: plugs))
            guard let created else {
                showToast("Oluşturulamadı", live.lastError ?? "sunucu reddetti")
                return
            }
            active = created.id.uuidString
            live.focus(bot: created.id)
            screen = .chat
            if isTeam { rightTab = "members" }
            resetNB()
            showToast("OpenAssist", isTeam ? "Ekip oluşturuldu" : "Bot oluşturuldu")
        }
        screen = .chat
    }

    // ── Eklentiler ──
    //
    // Liste SUNUCUDAN gelir (`GET /api/extensions`); istemcide katalog yoktur.
    // Ekran iki ayrı gerçeği birlikte gösterir: eklenti sunucuda kurulu mu ve
    // hangi botlar kullanıyor. İkisi aynı şey değil — kurulum makine
    // seviyesinde, kullanım bot başına allowlist'te (bots.extensions).

    /// Kurulu eklentilerin tamamı — "Yüklü" kutucukları. Arama kutusundan
    /// ETKİLENMEZ: kutucuklar neyin kurulu olduğunu söyler, arama sonucunu değil.
    var installedTiles: [DPlugin] { live.extensions.map(DPlugin.init(_:)) }

    var visiblePlugins: [DPlugin] {
        let q = plugQuery.trimmingCharacters(in: .whitespaces).lowercased()
        return live.extensions
            .filter { e in
                q.isEmpty || e.name.lowercased().contains(q)
                    || e.description.lowercased().contains(q) || e.id.lowercased().contains(q)
            }
            // "Kullanımda" sekmesi: allowlist'inde bu eklenti olan bir bot var mı.
            .filter { plugTab == "all" || !$0.usedBy.isEmpty }
            .map(DPlugin.init(_:))
    }

    /// Bölümler türe göre: gömülü / kurulan paket ve dosya / beceri. Beceriler
    /// AYRI durur çünkü kod değil talimattır — kullanıcı ikisini karıştırmasın.
    var plugCats: [(label: String, items: [DPlugin])] {
        let rows = visiblePlugins
        return [
            ("Gömülü", rows.filter(\.builtin)),
            ("Kurulu", rows.filter { !$0.builtin && $0.kind != "skill" }),
            ("Beceriler", rows.filter { $0.kind == "skill" }),
        ].filter { !$0.1.isEmpty }
    }

    // ── paket galerisi (kurulum) ──
    //
    // Kurmak SUNUCUDA KOD ÇALIŞTIRIR. Bu yüzden galeri ayrı bir kipte durur
    // (yanlışlıkla tıklanacak bir "Kur" düğmesi kurulu listenin içinde değil)
    // ve satırlar indirme sayısı, yayıncı ve varsa çakışma notuyla gelir.

    /// "installed" = kurulu eklentiler, "catalog" = pi paket galerisi.
    var plugMode = "installed"

    func openPluginCatalog() {
        plugMode = "catalog"
        plugQuery = ""
        Task { await live.loadPackageCatalog(q: "") }
    }

    func closePluginCatalog() {
        plugMode = "installed"
        plugQuery = ""
    }

    /// Galeri araması sunucuya gider (npm kayıt defteri) — her tuşta değil,
    /// Enter'da: her harf için npm'e istek atmanın karşılığı yok.
    func searchPluginCatalog() {
        Task { await live.loadPackageCatalog(q: plugQuery) }
    }

    var catalogRows: [CatalogPackage] { live.packageCatalog }

    func installPackage(_ p: CatalogPackage) {
        Task {
            if let err = await live.installPackage(source: p.source) {
                showToast("Kurulamadı", err)
                return
            }
            // Kurmak KULLANMAK değildir: paket hiçbir bota kendiliğinden
            // takılmaz, kullanıcının bunu bilmesi gerekiyor.
            showToast("Kuruldu", "\(p.name) — kullanmak için botun eklenti listesine ekle")
            await live.loadPackageCatalog(q: plugQuery)
        }
    }

    func removePlugin(_ p: DPlugin) {
        Task {
            if let err = await live.removePackage(id: p.id) {
                showToast("Kaldırılamadı", err)
                return
            }
            showToast("Eklentiler", "\(p.name) kaldırıldı")
        }
    }

    /// Liste boşken ekranın söyleyeceği tek satır. nil = gösterilecek satır var.
    /// Sebebi ayırıyoruz: "sunucuya bağlı değiliz" ile "kurulu eklenti yok"
    /// aynı boş ekran olsaydı kullanıcı ağı değil eklentileri kurcalardı.
    var plugEmptyHint: String? {
        if !live.online { return "Sunucuya bağlı değil — Ayarlar › Bağlantılar" }
        if live.extensions.isEmpty { return "Sunucuda kurulu eklenti yok" }
        if visiblePlugins.isEmpty {
            return plugTab == "all" ? "Aramaya uyan eklenti yok" : "Hiçbir bot eklenti kullanmıyor"
        }
        return nil
    }

    // ── Sabit talimatlar (Ayarlar › Hafıza) ──
    //
    // Hafızanın geri kalanı botun kendi öğrendikleridir; burası kullanıcının
    // yazdığı kurallar. Ayrı bir ekran olmasının sebebi, ayrı bir güvenceye
    // sahip olmaları: her koşuda sistem promptuna girerler ve gece
    // konsolidasyonu onlara dokunamaz.

    /// Kuralın bağlandığı bot; nil = tüm botlar (user kapsamı).
    var memBot: String?
    /// Ekleme kutusundaki taslak metin.
    var memDraft = ""

    var instructions: [MemoryRecord] { live.standingInstructions }

    /// Liste boşken ekranın söyleyeceği tek satır (eklentilerdeki ile aynı
    /// duruş: boşluğun SEBEBİ yazılmalı).
    var memEmptyHint: String? {
        if !live.online { return "Sunucuya bağlı değil — Ayarlar › Bağlantılar" }
        if let err = live.instructionsError { return err }
        if live.standingInstructions.isEmpty {
            return memBot == nil
                ? "Henüz kural yok — tüm botlar için bir kural yaz"
                : "Bu bot için kural yok (tüm botlara yazılanlar da burada görünür)"
        }
        return nil
    }

    func loadInstructions() {
        Task { await live.loadStandingInstructions(bot: memBot) }
    }

    /// Bot seçimi değişince liste yeniden çekilir: kapsam listeyi değiştirir
    /// (tüm botlar → yalnız user kapsamı, bot → user + o botun kuralları).
    func selectInstructionBot(_ id: String?) {
        memBot = id
        loadInstructions()
    }

    func addInstruction() {
        let metin = memDraft.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !metin.isEmpty else { return }
        Task {
            if let err = await live.addStandingInstruction(metin, bot: memBot) {
                showToast("Kural yazılamadı", err)
                return
            }
            memDraft = ""
            showToast("Hafıza", "Kural eklendi — bir sonraki koşudan itibaren geçerli")
        }
    }

    func removeInstruction(_ m: MemoryRecord) {
        Task {
            if let err = await live.removeStandingInstruction(id: m.id, bot: memBot) {
                showToast("Silinemedi", err)
                return
            }
            showToast("Hafıza", "Kural silindi")
        }
    }

    // ── Motor + model (bot başına, K18) ──
    //
    // Seçenekler sunucudan gelir (`GET /api/engines`); istemci liste
    // GÖMMEZ. Gömseydi motorun tanımadığı bir ad (başka sağlayıcının
    // modeli) bota yazılabilir ve her koşu sessizce düşerdi.

    var allEngines: [EngineInfo] { live.engines }

    func engineInfo(for id: String) -> EngineInfo? {
        EngineInfo.resolve(live.bot(id: id)?.engine ?? "", in: live.engines)
    }

    /// Botun motoru; sunucu boş bırakmaz ama eski kayıt için düşüş var.
    func engineID(for id: String) -> String {
        let raw = live.bot(id: id)?.engine ?? ""
        return raw.isEmpty ? (engineInfo(for: id)?.id ?? Self.varsayilanMotor) : raw
    }

    /// Motor listesi henüz gelmediğinde kullanılan ad. Sunucudaki tek motor
    /// budur; liste boşken de bota geçersiz bir ad yazılmasın.
    static let varsayilanMotor = "pi"

    func engineName(for id: String) -> String {
        engineInfo(for: id)?.name ?? engineID(for: id)
    }

    /// Seçili motorun sunduğu modeller — liste motora göre filtrelidir.
    func models(for id: String) -> [EngineInfo.Model] {
        engineInfo(for: id)?.models ?? []
    }

    /// Botun modeli sunucudadır; boşsa motor varsayılanı kullanılır.
    func modelName(for id: String) -> String {
        EngineInfo.modelLabel(live.bot(id: id)?.model ?? "", in: engineInfo(for: id))
    }

    /// Düzenlenen botun motoru; seçilmemişse sunucu varsayılanı ("pi"), o da
    /// kayıtlı değilse ilk motor.
    var nbEngineID: String {
        if let e = nbEngine, live.engines.contains(where: { $0.id == e }) { return e }
        return (live.engines.first { $0.id == Self.varsayilanMotor }
            ?? live.engines.first)?.id ?? Self.varsayilanMotor
    }

    var nbEngineLabel: String {
        live.engines.first { $0.id == nbEngineID }?.name ?? nbEngineID
    }

    /// Model listesi SEÇİLİ motorunkidir. Sabit tek bir motor kimliğine
    /// bakılıyordu: başka motorun botu düzenlenirken bile o motorun modelleri
    /// listeleniyor, seçilen ad geçersiz olduğu için her koşu düşüyordu.
    var newBotModels: [EngineInfo.Model] {
        live.engines.first { $0.id == nbEngineID }?.models ?? []
    }

    /// Motor değişince model SIFIRLANIR — eski ad yeni motorda geçerli
    /// olmayabilir (setEngine ile aynı gerekçe).
    func setNBEngine(_ id: String) {
        guard nbEngine != id else { return }
        nbEngine = id
        nbModel = nil
    }

    var nbModelLabel: String {
        guard let m = nbModel, !m.isEmpty else { return "varsayılan" }
        return newBotModels.first { $0.id == m }?.label ?? m
    }

    func setEngine(_ botID: String, _ engineID: String) {
        guard let uuid = UUID(uuidString: botID) else { return }
        let name = live.engines.first { $0.id == engineID }?.name ?? engineID
        Task {
            // Motor değişince model sıfırlanır: eski ad yeni motorda
            // geçerli olmayabilir, sessiz düşmektense varsayılana dönülür.
            let ok = await live.updateBot(id: uuid, BotUpdate(engine: engineID, model: ""))
            showToast("Motor", ok ? "\(name) atandı" : (live.lastError ?? "atanamadı"))
        }
    }

    func setModel(_ botID: String, _ model: String) {
        guard let uuid = UUID(uuidString: botID) else { return }
        let label = models(for: botID).first { $0.id == model }?.label ?? model
        Task {
            let ok = await live.updateBot(id: uuid, BotUpdate(model: model))
            showToast("Model", ok ? "\(label) atandı" : (live.lastError ?? "atanamadı"))
        }
    }

    // ── Modeller ekranı (sağlayıcılar + models.dev) ──
    //
    // Tek motor (pi) olduğu için ekranın konusu "hangi CLI kurulu" değil,
    // "hangi sağlayıcıdan hangi modeller çalışıyor". Tarifler sunucunun
    // veritabanındadır; motor her turda oradan okur.

    var allProviders: [ProviderInfo] { live.providers }

    /// Düzenlenen sağlayıcı; nil = liste görünümü.
    var provDraft: ProviderDraft?
    /// Silme iki adımlıdır: sağlayıcıyla birlikte anahtarı da gider.
    var provSilOnay = false
    var provKaydediyor = false
    /// Elle model ekleme alanının içeriği.
    var provYeniModel = ""
    /// Model önerilerinin geldiği katalog sağlayıcısı. Genelde kaydın
    /// kimliğiyle aynıdır; kullanıcı kendi kimliğini verdiyse (örn. iki ayrı
    /// hesap için "minimax-2") elle seçilir.
    var provKatalogID = ""

    func provAc(_ p: ProviderInfo) {
        provDraft = ProviderDraft(p)
        provKatalogID = live.catalog.contains { $0.id == p.id } ? p.id : ""
        provSifirla()
    }

    func provYeni(_ c: CatalogProvider?) {
        // Kayıtlı bir kimlik katalogdan yeniden seçilirse VAR OLAN kayıt
        // açılır: PUT upsert'tir, "yeni" diye kaydetmek sağlayıcının
        // anahtarını ve model listesini sessizce ezerdi.
        if let c, let mevcut = allProviders.first(where: { $0.id == c.id }) {
            provAc(mevcut)
            showToast("Modeller", "\(mevcut.name.isEmpty ? mevcut.id : mevcut.name) zaten kayıtlı — düzenleniyor")
            return
        }
        let sira = (allProviders.map(\.position).max() ?? -1) + 1
        if let c {
            provDraft = ProviderDraft(catalog: c, position: sira)
            provKatalogID = c.id
        } else {
            provDraft = ProviderDraft(position: sira)
            provKatalogID = ""
        }
        provSifirla()
    }

    private func provSifirla() {
        provSilOnay = false
        provYeniModel = ""
        openDD = nil
        live.loadCatalog()
    }

    func provKapat() {
        provDraft = nil
        provSilOnay = false
        openDD = nil
    }

    func provKaydet() {
        guard let d = provDraft, !provKaydediyor else { return }
        if let h = d.hata {
            showToast("Kaydedilmedi", h)
            return
        }
        let govde = d.kayit
        // Elle yazılan kimlik kayıtlı bir sağlayıcıyla çakışıyorsa kaydetme:
        // sunucu upsert yapar ve o kaydın anahtarı bu ekrandan gelen (boş)
        // taslakla değil ama model listesiyle ezilirdi.
        if d.yeni, allProviders.contains(where: { $0.id == govde.id }) {
            showToast("Kaydedilmedi", "\(govde.id) zaten kayıtlı — listeden aç ve düzenle")
            return
        }
        provKaydediyor = true
        Task {
            let ok = await live.saveProvider(govde)
            provKaydediyor = false
            if ok {
                provDraft = nil
                showToast("Modeller", "\(govde.name.isEmpty ? govde.id : govde.name) kaydedildi")
            } else {
                showToast("Kaydedilmedi", live.lastError ?? "sunucu reddetti")
            }
        }
    }

    func provSil() {
        guard let d = provDraft, !d.yeni else {
            // Kaydedilmemiş taslağın silinecek bir karşılığı yok.
            provKapat()
            return
        }
        let id = d.id
        Task {
            let ok = await live.deleteProvider(id)
            if ok {
                provDraft = nil
                provSilOnay = false
                showToast("Modeller", "\(id) silindi")
            } else {
                showToast("Silinemedi", live.lastError ?? "sunucu reddetti")
            }
        }
    }

    /// Model ekler; aynı kimlik varsa bilgilerini TAZELER (katalogdan gelen
    /// sınırlar güncellenmiş olabilir) — liste ikizlenmez.
    func provModelEkle(_ m: ProviderInfo.Model) {
        guard var d = provDraft, !m.id.isEmpty else { return }
        if let i = d.models.firstIndex(where: { $0.id == m.id }) {
            d.models[i] = m
        } else {
            d.models.append(m)
        }
        provDraft = d
    }

    func provModelCikar(_ id: String) {
        provDraft?.models.removeAll { $0.id == id }
    }

    /// Elle model ekleme — katalogda olmayan bir ad (yeni çıkmış model,
    /// şirket içi ince ayar) her zaman yazılabilmeli.
    func provElleModelEkle() {
        let ad = provYeniModel.trimmingCharacters(in: .whitespaces)
        guard !ad.isEmpty else { return }
        provModelEkle(ProviderInfo.Model(id: ad))
        provYeniModel = ""
    }

    /// Taslağa bağlı katalog kaydı — model önerileri buradan gelir.
    var provKatalog: CatalogProvider? {
        live.catalog.first { $0.id == provKatalogID }
    }

    /// Katalog sağlayıcı listesi (yeni sağlayıcı menüsü).
    var provKatalogSecenekleri: [SearchableMenu.Item] {
        live.catalog.map { .init(id: $0.id, label: "\($0.name) · \($0.models.count) model") }
    }

    /// Seçili katalog sağlayıcısının modelleri; zaten ekli olanlar da
    /// listede kalır (onay imiyle görünürler).
    var provKatalogModelleri: [SearchableMenu.Item] {
        (provKatalog?.models ?? []).map {
            .init(id: $0.id, label: $0.name.isEmpty ? $0.id : $0.name)
        }
    }

    func provKatalogModeliEkle(_ id: String) {
        guard let m = provKatalog?.models.first(where: { $0.id == id }) else { return }
        provModelEkle(m.kayit)
    }

    /// Bu sağlayıcının modellerini kaç bot kullanıyor — kaydın boşta mı
    /// olduğu, silmeden önce görünsün.
    func provKullanim(_ id: String) -> Int {
        allAgents.filter { (live.bot(id: $0.id)?.model ?? "").hasPrefix(id + "/") }.count
    }

    /// "az önce kontrol edildi" — yoklama zamanı motorlardan gelir.
    var enginesCheckedLabel: String {
        guard let at = live.engines.map(\.checkedAt).min() else { return "" }
        let sn = Int(Date().timeIntervalSince(at))
        if live.enginesRefreshing { return "kontrol ediliyor…" }
        if sn < 60 { return "az önce kontrol edildi" }
        if sn < 3600 { return "\(sn / 60) dk önce kontrol edildi" }
        return "\(sn / 3600) sa önce kontrol edildi"
    }

    // ── Aktivite ──
    var actFilters: [(id: String, label: String)] {
        [("all", "Tümü"), ("err", "Hatalar")]
            + allAgents.prefix(3).map { (id: $0.name, label: $0.name) }
    }

    var activityDays: [ActivityDay] { live.activityDays(filter: actFilter) }

    // ── İstatistikler ──
    private static let trNumber: NumberFormatter = {
        let f = NumberFormatter()
        f.locale = Locale(identifier: "tr_TR")
        f.numberStyle = .decimal
        return f
    }()

    private func num(_ v: Int) -> String {
        Self.trNumber.string(from: NSNumber(value: v)) ?? "\(v)"
    }

    /// Seçili botun sunucu kimliği (istatistik kırılımı için).
    private var statBotID: UUID? {
        guard let name = statBot else { return nil }
        return allAgents.first { $0.name == name }.flatMap { UUID(uuidString: $0.id) }
    }

    var statCards: [StatCard] {
        guard let s = live.stats else {
            return [StatCard(id: "durum", label: "Sayaçlar", value: live.online ? "…" : "Sunucu yok", big: false)]
        }
        if let id = statBotID {
            let row = s.byBot?.first { $0.botID == id }
            return [
                StatCard(id: "Koşu", label: "Koşu", value: num(row?.runs ?? 0)),
                StatCard(id: "Maliyet", label: "Maliyet", value: Self.money(row?.costUSD ?? 0)),
                StatCard(id: "Seçili bot", label: "Seçili bot", value: statBot ?? "", big: false),
            ]
        }
        return [
            StatCard(id: "Koşu", label: "Koşu", value: num(s.totalRuns)),
            StatCard(id: "Tamamlanan", label: "Tamamlanan", value: num(s.completed)),
            StatCard(id: "Başarısız", label: "Başarısız", value: num(s.failed + s.expired)),
            StatCard(id: "Maliyet", label: "Maliyet", value: Self.money(s.totalCostUSD)),
            StatCard(id: "Token", label: "Token", value: num(s.inputTokens + s.outputTokens),
                     sub: "\(num(s.inputTokens)) giriş · \(num(s.outputTokens)) çıkış", big: false),
            StatCard(id: "En aktif bot", label: "En aktif bot",
                     value: topBot?.0 ?? "—", sub: topBot.map { "\(num($0.1)) koşu" }, big: false),
        ]
    }

    private var topBot: (String, Int)? {
        guard let rows = live.stats?.byBot, let top = rows.max(by: { $0.runs < $1.runs }) else { return nil }
        return (live.botName(top.botID), top.runs)
    }

    private static func money(_ v: Double) -> String {
        "$" + String(format: "%.2f", v).replacingOccurrences(of: ".", with: ",")
    }

    /// Günlük koşu sütunları — aktivite akışından sayılır (gerçek veri).
    var statBars: [StatBar] {
        let days = range == "7" ? 7 : 30
        let cal = Calendar.current
        let today = cal.startOfDay(for: Date())
        let selected = statBotID
        var counts = [Int](repeating: 0, count: days)
        for item in live.activity where item.type == "run" {
            if let selected, item.botID != selected { continue }
            let day = cal.startOfDay(for: item.at)
            guard let diff = cal.dateComponents([.day], from: day, to: today).day,
                  diff >= 0, diff < days else { continue }
            counts[days - 1 - diff] += 1
        }
        let mx = Double(counts.max() ?? 0)
        return counts.enumerated().map { i, v in
            let h = mx > 0 ? (Double(v) / mx * 112).rounded() : 0
            let date = cal.date(byAdding: .day, value: -(days - 1 - i), to: today) ?? today
            let label = (days == 7 || i % 5 == 0) ? Self.barLabel(date) : ""
            return StatBar(h: max(2, h), label: label)
        }
    }

    private static let barFormatter: DateFormatter = {
        let f = DateFormatter()
        f.locale = Locale(identifier: "tr_TR")
        f.dateFormat = "d MMM"
        return f
    }()

    private static func barLabel(_ d: Date) -> String { barFormatter.string(from: d) }

    var statBotLabel: String { statBot ?? "Tüm botlar" }

    /// Hakkında ekranındaki bağlantılar — gerçek hedefler.
    static let aboutLinks: [(String, URL)] = [
        ("Depo", URL(string: "https://github.com/esmworks/OpenAssist")!),
        ("Kurulum", URL(string: "https://github.com/esmworks/OpenAssist/blob/main/docs/kurulum.md")!),
        ("Sürüm geçmişi", URL(string: "https://github.com/esmworks/OpenAssist/commits/main")!),
    ]

    /// Bildirim izni ışığı: yeşil verildi · sarı sorulmadı · kırmızı yok.
    var notifDotHex: String {
        let n = live.notifier
        guard n.available else { return "#ff5f57" }
        switch n.authorized {
        case true: return "#28c840"
        case false: return "#ff5f57"
        default: return "#f5b544"
        }
    }

    /// Hakkında satırları — sabit yazılmaz, gerçeğinden okunur.
    var aboutRows: [(String, String)] {
        let os = ProcessInfo.processInfo.operatingSystemVersion
        return [
            ("Sürüm", AppEnvironment.version),
            ("Derleme", AppEnvironment.build),
            ("Paket", AppEnvironment.isBundled ? "com.openassist.mac" : "yok (çıplak executable)"),
            ("macOS", "\(os.majorVersion).\(os.minorVersion)"),
            ("Sunucu", live.online ? "bağlı — \(live.serverURLString)" : "bağlı değil"),
            ("Kullanıcı", userName),
        ]
    }

    // ── Menü çubuğu ──
    var mbTasks: [MBTask] {
        live.allTasks.prefix(3).map { t in
            MBTask(label: t.label, meta: t.sub.isEmpty ? t.status.label : "\(t.status.label) · \(t.sub)",
                   dotHex: t.status.dotHex, hasBar: false, pct: 0)
        }
    }

    var mbDotHex: String {
        guard live.online else { return "#ff453a" }
        return live.runner.connected ? "#28c840" : "#f0a020"
    }

    var mbState: String {
        guard live.online else { return "Sunucuya bağlanılamıyor" }
        return live.runner.connected ? "Sunucu bağlı · Mac aktif" : "Sunucu bağlı · Mac kapalı"
    }

    var mbPauseLabel: String { "Onay Merkezi'ni aç" }

    func mbPause() { screen = .inbox }

    // ── Komut paleti ──
    var userInitials: String {
        String(userName.split(separator: " ").compactMap(\.first).prefix(2)).uppercased()
    }

    var paletteItems: [PaletteItem] {
        var items: [PaletteItem] = allAgents.map { a in
            PaletteItem(id: "bot:\(a.id)", group: "Bot", label: a.name, hint: "sohbete git") { [weak self] in
                self?.paletteGo { $0.screen = .chat; $0.active = a.id }
            }
        }
        items += [
            PaletteItem(id: "go:inbox", group: "Git", label: "Onay Merkezi",
                        hint: pendingCount > 0 ? "\(pendingCount) bekliyor" : "") { [weak self] in
                self?.paletteGo { $0.screen = .inbox }
            },
            PaletteItem(id: "go:activity", group: "Git", label: "Aktivite", hint: "") { [weak self] in
                self?.paletteGo { $0.screen = .activity }
            },
            PaletteItem(id: "go:plugins", group: "Git", label: "Eklentiler", hint: "") { [weak self] in
                self?.paletteGo { $0.screen = .plugins }
            },
            PaletteItem(id: "go:settings", group: "Git", label: "Ayarlar", hint: "⌘,") { [weak self] in
                self?.paletteGo { $0.screen = .settings }
            },
            PaletteItem(id: "go:stats", group: "Git", label: "İstatistikler", hint: "") { [weak self] in
                self?.paletteGo { $0.screen = .settings; $0.settingsTab = "stats" }
            },
            PaletteItem(id: "go:engines", group: "Git", label: "Modeller", hint: "sağlayıcılar") { [weak self] in
                self?.paletteGo { $0.screen = .settings; $0.settingsTab = "models" }
            },
            PaletteItem(id: "act:newbot", group: "Eylem", label: "Yeni bot oluştur", hint: "⌘N") { [weak self] in
                self?.paletteGo { $0.resetNB(); $0.screen = .newbot }
            },
            PaletteItem(id: "act:approve", group: "Eylem", label: "Bekleyen taslakları onayla", hint: "") { [weak self] in
                self?.paletteGo { $0.screen = .inbox }
            },
            PaletteItem(id: "act:pause", group: "Eylem", label: "Tüm botları duraklat", hint: "") { [weak self] in
                self?.paletteGo { _ in }
                self?.showToast("OpenAssist", "Tüm botlar duraklatıldı")
            },
            PaletteItem(id: "act:theme", group: "Eylem", label: "Temayı değiştir", hint: "") { [weak self] in
                guard let self else { return }
                self.darkOv = !self.dark
                self.paletteOpen = false
                self.pq = ""
            },
        ]
        return items
    }

    var filteredPaletteItems: [PaletteItem] {
        let q = pq.lowercased().trimmingCharacters(in: .whitespaces)
        guard !q.isEmpty else { return paletteItems }
        return paletteItems.filter { ($0.label + " " + $0.group).lowercased().contains(q) }
    }

    var paletteSel: Int {
        min(pi, max(0, filteredPaletteItems.count - 1))
    }

    private func paletteGo(_ patch: (DesignStore) -> Void) {
        paletteOpen = false
        pq = ""
        patch(self)
    }

    func togglePalette() {
        paletteOpen.toggle()
        pq = ""
        pi = 0
    }

    func openPalette() {
        paletteOpen = true
        pq = ""
        pi = 0
    }

    func paletteMove(_ delta: Int) {
        pi = min(max(paletteSel + delta, 0), max(0, filteredPaletteItems.count - 1))
    }

    func paletteEnter() {
        let items = filteredPaletteItems
        guard items.indices.contains(paletteSel) else { return }
        items[paletteSel].action()
    }

    // ── Ekran durumları ──
    var showSidebar: Bool { screen != .settings }
    var rightVisible: Bool { rightOpen && screen == .chat }
    var rightRoutines: Bool { rightTab == "routines" || (rightTab == "members" && curTeam == nil) }
    var rightMembers: Bool { curTeam != nil && rightTab == "members" }
    var rightTasks: Bool { rightTab == "tasks" }
    var curTyping: Bool { curUUID.map { live.isBusy($0) } ?? false }

    // ── Politika köprüsü (Ayarlar ↔ sunucu) ──

    /// Ayarlar ekranındaki alanlar sunucudaki politikayı yazar (K17).
    func loadPolicyIntoPrefs() {
        let p = live.policy
        prefs.dailyLimit = p.dailyActionQuota == 0 ? "" : String(p.dailyActionQuota)
        prefs.quietHours = !p.quietHoursStart.isEmpty
    }

    func savePolicyFromPrefs() {
        var p = live.policy
        p.dailyActionQuota = Int(prefs.dailyLimit.trimmingCharacters(in: .whitespaces)) ?? 0
        if prefs.quietHours {
            if p.quietHoursStart.isEmpty {
                p.quietHoursStart = "22:00"
                p.quietHoursEnd = "07:00"
            }
        } else {
            p.quietHoursStart = ""
            p.quietHoursEnd = ""
        }
        live.savePolicy(p)
        showToast("Ayarlar", "Politika sunucuya yazıldı")
    }

    /// Sunucu adresi — Ayarlar > Bağlantılar.
    var serverURL: String {
        get { live.serverURLString }
        set { live.serverURLString = newValue }
    }

    var serverStateLabel: String {
        if live.online { return "Bağlı" }
        // Belirteç hatası ayrı söylenir: aksi halde kullanıcı adresi
        // kurcalayıp durur, oysa adres doğrudur.
        if live.belirtecGecersiz { return "Belirteç geçersiz" }
        return live.lastError.map { "Hata: \($0)" } ?? "Bağlanılamıyor"
    }

    var apiToken: String {
        get { live.apiToken }
        set { live.apiToken = newValue }
    }
}
