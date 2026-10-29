import AppKit
import Foundation
import Observation

/// LiveStore — tasarımın veri kaynağı. Sunucu (özel API :8080) ile konuşur,
/// Mac runner'ı ayakta tutar ve tasarım tiplerine (DAgent/DMsg/DTask…)
/// çeviriyi yapar. İnce istemci: veri sunucudadır, burada yalnız görünüm
/// önbelleği ve akış durumu durur.
@MainActor
@Observable
final class LiveStore {
    // ── yapılandırma ───────────────────────────────────────────────────

    var serverURLString: String {
        didSet {
            guard serverURLString != oldValue else { return }
            UserDefaults.standard.set(serverURLString, forKey: "serverURL")
            // Belirteç sunucu başına saklanır: adres değişince o sunucunun
            // kendi belirteci yüklenir, eskisi yanlışlıkla gönderilmez.
            apiToken = Keychain.token(for: serverURLString)
            restart()
        }
    }

    /// API belirteci — sunucunun API_TOKEN'ı. Keychain'de durur (bkz.
    /// Keychain.swift): sunucunun tamamına yetki veren bir kimlik bilgisi
    /// UserDefaults plist'ine düz metin yazılmaz.
    var apiToken: String {
        didSet {
            guard apiToken != oldValue else { return }
            Keychain.setToken(apiToken, for: serverURLString)
            restart()
        }
    }

    /// Belirteç yanlış/eksik mi — Ayarlar bunu adres hatasından ayrı gösterir.
    private(set) var belirtecGecersiz = false

    // ── sunucu verisi ──────────────────────────────────────────────────

    private(set) var online = false
    private(set) var bots: [Bot] = []
    private(set) var messages: [UUID: [Message]] = [:]
    private(set) var pending: [Run] = []
    private(set) var board: TaskBoard?
    private(set) var routines: [Routine] = []
    private(set) var activity: [ActivityItem] = []
    private(set) var connectors: [Connector] = []
    /// Sunucuda kayıtlı motorlar: durumları ve model listeleri — hem
    /// Modeller ekranı hem bot ayarındaki seçenekler buradan gelir (K18).
    private(set) var engines: [EngineInfo] = []
    /// Motor yoklaması sürüyor (kullanıcı "yenile" dedi).
    private(set) var enginesRefreshing = false
    /// Sunucuda kurulu eklentiler (`GET /api/extensions`) — Eklentiler ekranı
    /// ve bot formundaki seçim listesi. İstemci liste GÖMMEZ: kurulu olmayan
    /// bir ad allowlist'e yazılırsa o botun her koşusu düşer.
    private(set) var extensions: [ExtensionInfo] = []
    /// Model sağlayıcıları (`GET /api/providers`) — Modeller ekranı bunları
    /// düzenler, motor da her turda aynı kayıttan okur.
    private(set) var providers: [ProviderInfo] = []
    /// models.dev kataloğu; yalnız Modeller ekranı açıkken çekilir —
    /// açılışta 2 MB indirmenin karşılığı yok.
    private(set) var catalog: [CatalogProvider] = []
    private(set) var catalogYukleniyor = false
    /// Katalog çekilemedi (ağ kapalı / models.dev düştü). Ekran kilitlenmez:
    /// elle model eklemek her zaman mümkün.
    private(set) var catalogHata: String?
    private(set) var stats: Stats?
    private(set) var policy = PolicySettings()
    /// Akan yanıtın canlı metni (bot başına; koşu bitince mesajlara düşer).
    private(set) var streaming: [UUID: String] = [:]
    /// Gönderilmeyi bekleyen ekler (bot başına, ADIM 5): kullanıcı dosya
    /// seçer → upload → burada bekler → gönderimde mesaja bağlanır.
    private(set) var ekler: [UUID: [Attachment]] = [:]
    private(set) var lastError: String?
    /// Başarısız son gönderimin kimliği (ADIM 3): kullanıcı aynı metin+ek
    /// kümesini yeniden göndermeye çalışırsa AYNI key gider — sunucu çift
    /// koşu üretmez.
    private var sonDeneme: (bot: UUID, metin: String, ekler: [UUID], key: UUID)?
    /// Kullanıcının kararını verdiği ama sunucudan henüz düşmemiş koşular
    /// (kart anında kapansın diye).
    var decided: [String: String] = [:]

    let runner = RunnerService()
    /// Sistem bildirimleri; anahtarları Ayarlar'dan senkronlanır.
    let notifier = Notifier()

    // Bildirim tetikleyicileri sunucu turundan türer: Telegram'dan ya da
    // rutinden doğan koşular uygulamanın akışından geçmez, yalnız burada
    // görünür. İlk tur sessizdir — açılışta birikmiş işler bildirilmez.
    @ObservationIgnored private var notifyArmed = false
    @ObservationIgnored private var seenApprovals: Set<UUID> = []
    @ObservationIgnored private var activeRuns: [UUID: UUID] = [:] // koşu → bot

    @ObservationIgnored private var fastLoop: Task<Void, Never>?
    @ObservationIgnored private var slowLoop: Task<Void, Never>?
    @ObservationIgnored private var streamTasks: [UUID: Task<Void, Never>] = [:]
    /// Mesaj tazeleme sıra numarası (bot → son istek); bkz. tazeleMesajlar.
    @ObservationIgnored private var mesajTur: [UUID: Int] = [:]
    /// Sohbeti açık olan bot — mesajları her turda tazelenir.
    @ObservationIgnored var focusedBot: UUID?
    @ObservationIgnored private var statsDays = 30

    init() {
        let adres = UserDefaults.standard.string(forKey: "serverURL")
            ?? "http://127.0.0.1:8080"
        serverURLString = adres
        apiToken = Keychain.token(for: adres)
    }

    /// Testler için motor listesini doğrudan kurar — ağ turu beklemeden
    /// motor/model eşlemesi sınanabilsin.
    func setEnginesForTesting(_ list: [EngineInfo]) { engines = list }

    /// Testler için sağlayıcı listesi — Modeller ekranının taslak mantığı
    /// sunucusuz sınanabilsin.
    func setProvidersForTesting(_ list: [ProviderInfo]) { providers = list }

    func setCatalogForTesting(_ list: [CatalogProvider]) { catalog = list }

    /// Testler için eklenti listesi — ekran ve bot formu eşlemesi sunucusuz
    /// sınanabilsin.
    func setExtensionsForTesting(_ list: [ExtensionInfo]) { extensions = list }

    /// Testler için bağlantı durumu: "bağlı değil" ile "liste boş" ayrı
    /// mesajlar üretiyor, ikisi de sınanabilmeli.
    func setOnlineForTesting(_ v: Bool) { online = v }

    /// Eklenti listesini tazele (kurulum/kaldırma sonrası: ekran anında
    /// doğruyu göstersin, 15 sn'lik yavaş turu beklemesin).
    func refreshExtensions() async {
        guard let api, online else { return }
        extensions = (try? await api.extensions()) ?? extensions
    }

    // ── paket galerisi (kurulum) ───────────────────────────────────────

    /// Galeri sonuçları — yalnız Eklentiler ekranı "Ekle" derken çekilir.
    /// Açılışta çekilmez: kimsenin bakmadığı bir liste için npm'e gitmenin
    /// karşılığı yok.
    private(set) var packageCatalog: [CatalogPackage] = []
    private(set) var packageCatalogLoading = false
    /// Galeri çekilemedi (ağ kapalı / npm düştü). Ekran kilitlenmez.
    private(set) var packageCatalogError: String?
    /// Kurulumu/kaldırması süren paket kaynağı — düğme iki kez basılmasın.
    private(set) var packageBusy: String?

    func setPackageCatalogForTesting(_ list: [CatalogPackage]) { packageCatalog = list }

    func loadPackageCatalog(q: String) async {
        guard let api, online else {
            packageCatalogError = "Sunucuya bağlı değil"
            return
        }
        packageCatalogLoading = true
        defer { packageCatalogLoading = false }
        do {
            packageCatalog = try await api.extensionCatalog(q: q)
            packageCatalogError = nil
        } catch {
            packageCatalogError = error.localizedDescription
        }
    }

    /// Paket kur. Dönen liste sunucunun SON hâlidir — kendi tahminimizi
    /// yazmıyoruz, kurulum yarıda kalırsa ekran yalan söylemesin.
    func installPackage(source: String) async -> String? {
        guard let api else { return "Sunucuya bağlı değil" }
        packageBusy = source
        defer { packageBusy = nil }
        do {
            extensions = try await api.installExtension(source: source)
            return nil
        } catch {
            return error.localizedDescription
        }
    }

    func removePackage(id: String) async -> String? {
        guard let api else { return "Sunucuya bağlı değil" }
        packageBusy = id
        defer { packageBusy = nil }
        do {
            extensions = try await api.removeExtension(id: id)
            return nil
        } catch {
            return error.localizedDescription
        }
    }

    // ── sabit talimatlar ───────────────────────────────────────────────

    /// Sistem promptuna her koşuda giren kurallar. Turlarda çekilmez: yalnız
    /// Ayarlar › Hafıza açıkken okunur — nadiren değişen, kullanıcının kendi
    /// yazdığı küçük bir liste.
    private(set) var standingInstructions: [MemoryRecord] = []
    private(set) var instructionsLoading = false
    private(set) var instructionsError: String?
    /// Ekleme/silme sürerken düğmeler kilitlenir.
    private(set) var instructionBusy = false

    func setInstructionsForTesting(_ list: [MemoryRecord]) { standingInstructions = list }
    func setInstructionsErrorForTesting(_ err: String?) { instructionsError = err }

    func loadStandingInstructions(bot: String?) async {
        guard let api, online else {
            instructionsError = "Sunucuya bağlı değil"
            return
        }
        instructionsLoading = true
        defer { instructionsLoading = false }
        do {
            standingInstructions = try await api.standingInstructions(bot: bot)
            instructionsError = nil
        } catch {
            instructionsError = error.localizedDescription
        }
    }

    /// Yeni kural. Hata dizesi döner (nil = başarılı) — çağıran ekranda
    /// gösterir; sır kapısı gibi sunucu vetolarının sebebi kullanıcıya ulaşsın.
    func addStandingInstruction(_ text: String, bot: String?) async -> String? {
        guard let api else { return "Sunucuya bağlı değil" }
        instructionBusy = true
        defer { instructionBusy = false }
        do {
            _ = try await api.createMemory(
                scope: bot == nil ? "user" : "bot", botID: bot,
                kind: "instruction", content: text)
            standingInstructions = try await api.standingInstructions(bot: bot)
            return nil
        } catch {
            return error.localizedDescription
        }
    }

    func removeStandingInstruction(id: String, bot: String?) async -> String? {
        guard let api else { return "Sunucuya bağlı değil" }
        instructionBusy = true
        defer { instructionBusy = false }
        do {
            try await api.deleteMemory(id: id)
            standingInstructions = try await api.standingInstructions(bot: bot)
            return nil
        } catch {
            return error.localizedDescription
        }
    }

    var api: APIClient? {
        guard let url = URL(string: serverURLString), url.host != nil else { return nil }
        return APIClient(baseURL: url, token: apiToken)
    }

    // ── yaşam döngüsü ──────────────────────────────────────────────────

    func start() {
        guard let api else { return }
        runner.start(baseURL: api.baseURL, token: api.token)
        fastLoop?.cancel()
        fastLoop = Task { [weak self] in
            while !Task.isCancelled {
                await self?.refresh()
                try? await Task.sleep(for: .seconds(3))
            }
        }
        slowLoop?.cancel()
        slowLoop = Task { [weak self] in
            while !Task.isCancelled {
                await self?.refreshSlow()
                try? await Task.sleep(for: .seconds(15))
            }
        }
    }

    func restart() {
        runner.stop()
        bots = []
        messages = [:]
        pending = []
        board = nil
        start()
    }

    /// Hızlı tur: durum, botlar, bekleyen onaylar, pano, açık sohbet.
    func refresh() async {
        guard let api else {
            online = false
            return
        }
        switch await api.health() {
        case .iyi:
            online = true
            belirtecGecersiz = false
        case .belirtecGecersiz:
            // Sunucu ayakta ama bizi tanımıyor: adres doğru, belirteç değil.
            online = false
            belirtecGecersiz = true
            lastError = "API belirteci geçersiz — Ayarlar › Bağlantılar"
        case .erisilemiyor:
            online = false
            belirtecGecersiz = false
        }
        guard online else { return }
        do {
            bots = try await api.bots()
            pending = try await api.runs(status: "approval_pending")
            board = try await api.tasks()
            if let focused = focusedBot {
                await tazeleMesajlar(focused)
            }
            lastError = nil
            notifyFromDiff()
        } catch {
            lastError = error.localizedDescription
        }
    }

    /// Bu turda ne değişti → hangi bildirim. Onay: listeye YENİ düşen koşu.
    /// Bitiş: çalışan/kuyruktaki bir koşunun listeden düşmesi (sonucu
    /// koşunun kendisinden okunur — durum ve köken orada).
    private func notifyFromDiff() {
        let pendingIDs = Set(pending.map(\.id))
        let active = Dictionary(uniqueKeysWithValues:
            ((board?.running ?? []) + (board?.queued ?? [])).map { ($0.id, $0.botID) })

        let newApprovals = pendingIDs.subtracting(seenApprovals)
        // Artık beklemeyen koşu yeniden sorarsa tekrar bildirilsin.
        seenApprovals = pendingIDs

        let finished = activeRuns.filter { !active.keys.contains($0.key) && !pendingIDs.contains($0.key) }
        activeRuns = active

        guard notifyArmed else {
            notifyArmed = true // açılıştaki birikmiş iş bildirim değildir
            return
        }
        for run in pending where newApprovals.contains(run.id) {
            notifier.approvalPending(bot: botName(run.botID), what: Self.askLabel(run.pendingAsk))
        }
        for (runID, botID) in finished {
            Task { await notifyFinished(runID: runID, botID: botID) }
        }
    }

    private func notifyFinished(runID: UUID, botID: UUID) async {
        guard let api, let run = try? await api.run(id: runID) else { return }
        // Park eden koşu bitmiş sayılmaz: onay kartı zaten bildirildi.
        guard run.status == "completed" || run.status == "failed" else { return }
        let final = run.finalText ?? ""
        let text = run.status == "completed"
            ? (final.isEmpty ? "İş tamamlandı" : Self.oneLine(final))
            : (run.error ?? "bilinmeyen hata")
        notifier.runFinished(bot: botName(botID), ok: run.status == "completed",
                             text: text, routine: run.origin == "routine")
    }

    private func botName(_ id: UUID) -> String {
        bots.first { $0.id == id }?.name ?? "Bot"
    }

    static func askLabel(_ ask: Ask?) -> String {
        guard let ask else { return "Bir istek onay bekliyor" }
        if ask.kind == "question" { return ask.question ?? "Bot bir soru sordu" }
        return toolLabel(ask.tool ?? "") + " çalıştırmak istiyor"
    }

    /// Yavaş tur: aktivite, rutinler, konektörler, sayaçlar, politika.
    func refreshSlow() async {
        guard let api, online else { return }
        activity = (try? await api.activity(limit: 200)) ?? activity
        routines = (try? await api.routines()) ?? routines
        connectors = (try? await api.connectors()) ?? connectors
        engines = (try? await api.engines()) ?? engines
        // Eklenti listesi yavaş turda: sunucuda bir dizin okuması, ama bot
        // formundaki seçim listesi de bundan besleniyor — ekran açılmadan
        // hazır olmalı.
        extensions = (try? await api.extensions()) ?? extensions
        providers = (try? await api.providers()) ?? providers
        stats = (try? await api.stats(days: statsDays)) ?? stats
        policy = (try? await api.policy()) ?? policy
    }

    /// Motorları sunucuda yeniden yoklat (CLI kuruldu / sağlayıcı değişti →
    /// ekran anında doğruyu göstersin).
    func refreshEngines() {
        guard let api, !enginesRefreshing else { return }
        enginesRefreshing = true
        Task {
            defer { enginesRefreshing = false }
            engines = (try? await api.engines(refresh: true)) ?? engines
            providers = (try? await api.providers()) ?? providers
        }
    }

    // ── sağlayıcılar ───────────────────────────────────────────────────

    func refreshProviders() async {
        guard let api else { return }
        providers = (try? await api.providers()) ?? providers
    }

    /// Kaydeder ve LİSTEYİ SUNUCUDAN tazeler: kayıt sırasında sunucu
    /// normalleştirme yapıyor (kimlik küçültme, boş biçim varsayılanı) —
    /// taslağı ekranda bırakmak, kaydedilenden farklı bir şey göstermek olurdu.
    func saveProvider(_ body: ProviderSave) async -> Bool {
        guard let api else { return false }
        do {
            _ = try await api.saveProvider(body)
            await refreshProviders()
            // Model listesi değişti; motorun bildirdiği liste (bot ayarındaki
            // seçenekler) bayat kalmasın.
            refreshEngines()
            return true
        } catch {
            lastError = error.localizedDescription
            return false
        }
    }

    func deleteProvider(_ id: String) async -> Bool {
        guard let api else { return false }
        do {
            try await api.deleteProvider(id: id)
            await refreshProviders()
            refreshEngines()
            return true
        } catch {
            lastError = error.localizedDescription
            return false
        }
    }

    /// Katalog — bir kez çekilir, sonra bellekte durur. `refresh` kullanıcının
    /// açık isteğidir (sunucuda da 24 saatlik önbelleği atlar).
    func loadCatalog(refresh: Bool = false) {
        guard let api, !catalogYukleniyor else { return }
        if !refresh, !catalog.isEmpty { return }
        catalogYukleniyor = true
        Task {
            defer { catalogYukleniyor = false }
            do {
                catalog = try await api.modelCatalog(refresh: refresh).providers
                catalogHata = nil
            } catch {
                catalogHata = error.localizedDescription
            }
        }
    }

    func focus(bot: UUID) {
        focusedBot = bot
        Task { await tazeleMesajlar(bot) }
    }

    /// Mesaj listesini tazeler — TEK yazma noktası.
    ///
    /// NEDEN sıra numarası: listeyi beş ayrı yerden (3 sn'lik tur, focus,
    /// send, edit, akış) tazeliyoruz ve isteklerin dönüş sırası garanti
    /// değil. Geç dönen ESKİ cevap yeniyi eziyordu: kullanıcı mesajını
    /// yazıyor, mesaj görünüyor, sonra bir turdan kalan cevap gelip listeyi
    /// geri sarıyor — mesaj kayboluyor, üç saniye sonra geri geliyordu.
    /// Cevabın kendisi de aynı şekilde kaybolup geri geliyordu. Artık her
    /// istek bir numara alır ve dönüşte hâlâ en güncel istek değilse
    /// sonucunu ATAR.
    @discardableResult
    func tazeleMesajlar(_ bot: UUID) async -> Bool {
        guard let api else { return false }
        return await mesajlariUygula(bot) { try? await api.messages(botID: bot) }
    }

    /// Sıra numarası mantığı — getirme işi dışarıdan verilir ki yarış
    /// sınanabilsin (bkz. MesajYarisiTests).
    @discardableResult
    func mesajlariUygula(_ bot: UUID, _ getir: () async -> [Message]?) async -> Bool {
        let tur = (mesajTur[bot] ?? 0) + 1
        mesajTur[bot] = tur
        guard let liste = await getir() else { return false }
        guard mesajTur[bot] == tur else { return false } // daha yeni istek var
        messages[bot] = liste
        return true
    }

    // ── sohbet ─────────────────────────────────────────────────────────

    /// Bot bir SORU sorup park ettiyse yazılan metin yeni koşu değil, o
    /// sorunun cevabıdır (Telegram gateway'iyle aynı davranış).
    func parkedQuestion(for botID: UUID) -> Run? {
        pending.first { $0.botID == botID && $0.pendingAsk?.kind == "question" }
    }

    /// Gönderim: metin + bekleyen ekler (ADIM 3+5). Metin boş, ek dolu olabilir
    /// (yalnız dosya gönderimi). Aynı gönderimin retry'i aynı key'i taşır.
    func send(bot: UUID, text: String) {
        guard let api else { return }
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        let ids = (ekler[bot] ?? []).map(\.id)
        guard !trimmed.isEmpty || !ids.isEmpty else { return }

        if let question = parkedQuestion(for: bot), ids.isEmpty {
            resolve(run: question, answer: trimmed)
            return
        }

        // Retry anahtarı: başarısız son denemeyle AYNI içerikse anahtar korunur.
        let key: UUID
        if let d = sonDeneme, d.bot == bot, d.metin == trimmed, d.ekler == ids {
            key = d.key
        } else {
            key = UUID()
            sonDeneme = (bot, trimmed, ids, key)
        }

        streaming[bot] = ""
        Task {
            do {
                let receipt = try await api.send(botID: bot, text: trimmed,
                                                key: key, attachmentIDs: ids)
                if sonDeneme?.key == key { sonDeneme = nil }
                ekler[bot] = nil
                await tazeleMesajlar(bot)
                stream(runID: receipt.runID, bot: bot)
            } catch {
                streaming[bot] = nil
                lastError = error.localizedDescription
                // sonDeneme korunur: kullanıcı tekrar "Gönder" derse aynı key
                // gider, sunucu çift koşu üretmez (ADIM 3).
            }
        }
    }

    // ── düzenleme (ADIM 9) ────────────────────────────────────────────

    /// Kullanıcı mesajını düzenler: sunucu eski sürümü superseded yapar,
    /// bot yeni dalı taze oturumla yanıtlar.
    func edit(bot: UUID, mesajID: UUID, text: String) {
        guard let api else { return }
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return }
        streaming[bot] = ""
        Task {
            do {
                let receipt = try await api.editMessage(id: mesajID, text: trimmed, key: UUID())
                await tazeleMesajlar(bot)
                stream(runID: receipt.runID, bot: bot)
            } catch {
                streaming[bot] = nil
                lastError = error.localizedDescription
            }
        }
    }

    // ── ekler (ADIM 5) ─────────────────────────────────────────────────

    /// Dosyayı sunucuya yükler, gönderim kuyruğuna ekler. Hata kullanıcıya
    /// lastError üzerinden gider; kuyruk bozulmaz.
    func addAttachment(bot: UUID, url: URL) {
        guard let api else { return }
        Task {
            do {
                let att = try await api.uploadAttachment(botID: bot, fileURL: url)
                ekler[bot, default: []].append(att)
            } catch {
                lastError = error.localizedDescription
            }
        }
    }

    func removeAttachment(bot: UUID, id: UUID) {
        ekler[bot]?.removeAll { $0.id == id }
        if ekler[bot]?.isEmpty == true { ekler[bot] = nil }
    }

    /// Eki indirip varsayılan uygulamada açar (macOS: NSWorkspace).
    func openAttachment(_ id: UUID) {
        guard let api else { return }
        Task {
            do {
                let url = try await api.downloadAttachment(id: id)
                if !NSWorkspace.shared.open(url) {
                    lastError = "Dosya açılamadı: \(url.lastPathComponent)"
                }
            } catch {
                lastError = error.localizedDescription
            }
        }
    }

    /// Ek boyutunun insan-okur biçimi (ADIM 5).
    static func boyut(_ n: Int64) -> String {
        ByteCountFormatter.string(fromByteCount: n, countStyle: .file)
    }

    /// Koşunun SSE akışı: delta'lar canlı yazılır, akış bitince kalıcı
    /// mesajlar yeniden çekilir.
    private func stream(runID: UUID, bot: UUID) {
        streamTasks[bot]?.cancel()
        streamTasks[bot] = Task { [weak self] in
            guard let self, let api = self.api else { return }
            do {
                for try await ev in api.events(runID: runID) {
                    switch ev.kind {
                    case "content.delta", "assistant.text":
                        if let p = try? API.decoder.decode(TextPayload.self, from: ev.data),
                           let t = p.text {
                            let onceki = self.streaming[bot] ?? ""
                            // Tamamlanmış blok kendi paragrafıdır: kalıcı
                            // mesajda bloklar "\n\n" ile birleşiyor, canlı
                            // metin ayraçsız eklenirse koşu bitince metin
                            // gözle görülür biçimde SIÇRIYORDU.
                            self.streaming[bot] = (ev.kind == "assistant.text" && !onceki.isEmpty)
                                ? onceki + "\n\n" + t
                                : onceki + t
                        }
                    case "request.opened":
                        await self.refresh() // onay kartı hemen düşsün
                    case "run.settled":
                        // Koşu park edildi (onay/soru bekliyor): akış açık
                        // kalır — resume aynı koşuda sürer — ama bot artık
                        // yazmıyor. Göstergeyi düşür, metni kalıcı mesajdan
                        // al. Koşu bittiyse zaten hemen ardından akış kapanır.
                        //
                        // SIRA ÖNEMLİ: önce kalıcı mesaj, sonra gösterge.
                        // Tersi bir gidiş-dönüş boyunca ekranda ne akan metin
                        // ne kaydedilmiş mesaj bırakıyordu — cevap bir anlığına
                        // siliniyor, sonra geri geliyordu. Sunucu bot mesajını
                        // run.settled'dan ÖNCE yazar, yani burada hazırdır.
                        await self.tazeleMesajlar(bot)
                        self.streaming[bot] = nil
                        await self.refresh()
                    default:
                        continue
                    }
                }
            } catch {
                // kopan akış: kalıcı veriden toparlanır
            }
            // İPTAL EDİLDİYSE DOKUNMA: stream() yeni koşu için eskisini iptal
            // ediyor; buradaki temizlik o durumda YENİ koşunun "yazıyor"
            // göstergesini siliyor ve iptal edilmiş bağlamda koşan refresh()
            // sağlık yoklamasını düşürüp uygulamayı bir tur "Bağlanılamıyor"
            // gösteriyordu.
            if Task.isCancelled { return }
            await self.tazeleMesajlar(bot)
            self.streaming[bot] = nil
            await self.refresh()
        }
    }

    /// Koşu sürüyor mu (yazıyor göstergesi).
    func isBusy(_ botID: UUID) -> Bool {
        Self.busy(online: online, streaming: streaming[botID], board: board, botID: botID)
    }

    /// Saf karar (test edilebilir): "yazıyor" göstergesi yansın mı?
    ///
    /// SUNUCUYA ERİŞİLEMEZKEN PANO BAYATTIR: refresh() sağlık yoklaması
    /// düşünce erken dönüyor ve `board` son gördüğü anlık görüntüyle donuyor.
    /// Sunucu koşunun ORTASINDA yeniden başlatılınca (geliştirmede sürekli
    /// oluyor) cevap ekranda kalıyor, akış kopuyor ama pano hâlâ "running"
    /// diyordu — üç nokta sonsuza dek açık kalıyordu. Bayat panoya bakarak
    /// "yazıyor" demek, bilmediğimiz bir şeyi biliyormuş gibi göstermektir.
    static func busy(online: Bool, streaming: String?, board: TaskBoard?, botID: UUID) -> Bool {
        guard online else { return false }
        if streaming != nil { return true }
        guard let board else { return false }
        return (board.running + board.queued).contains { $0.botID == botID }
    }

    // ── onaylar ────────────────────────────────────────────────────────

    func resolve(run: Run, decision: String = "", answer: String = "", always: Bool = false) {
        guard let api else { return }
        decided[run.id.uuidString] = decision == "deny" ? "no" : "ok"
        Task {
            do {
                try await api.resolve(runID: run.id, ApprovalDecision(
                    decision: decision, answer: answer, always: always))
                // Karar sonrası koşu devam eder — akışı izle.
                stream(runID: run.id, bot: run.botID)
            } catch {
                decided[run.id.uuidString] = nil
                lastError = error.localizedDescription
            }
            await refresh()
        }
    }

    func resolve(runID: String, decision: String, always: Bool = false) {
        guard let uuid = UUID(uuidString: runID), let run = pending.first(where: { $0.id == uuid })
        else { return }
        resolve(run: run, decision: decision, always: always)
    }

    /// Sınırlı toplu onay (K19) — sunucu soruları, tainted koşuları ve
    /// yüksek riskli sınıfları atlar; özet döner.
    func approveAll() async -> String {
        guard let api else { return "" }
        do {
            let res = try await api.bulkApprove()
            let approved = res.results.filter { $0.status == "approved" }
            for item in approved { decided[item.runID.uuidString] = "ok" }
            let skipped = res.results.count - approved.count
            await refresh()
            if res.results.isEmpty { return "Bekleyen onay yok" }
            return "\(approved.count) onaylandı"
                + (skipped > 0 ? " · \(skipped) atlandı (tek tek incele)" : "")
        } catch {
            lastError = error.localizedDescription
            return error.localizedDescription
        }
    }

    // ── bot yönetimi ───────────────────────────────────────────────────

    func createBot(_ body: BotCreate) async -> Bot? {
        guard let api else { return nil }
        do {
            let bot = try await api.createBot(body)
            await refresh()
            return bot
        } catch {
            lastError = error.localizedDescription
            return nil
        }
    }

    func updateBot(id: UUID, _ body: BotUpdate) async -> Bool {
        guard let api else { return false }
        do {
            _ = try await api.updateBot(id: id, body)
            await refresh()
            return true
        } catch {
            lastError = error.localizedDescription
            return false
        }
    }

    func deleteBot(id: UUID) async -> Bool {
        guard let api else { return false }
        do {
            try await api.deleteBot(id: id)
            messages[id] = nil
            await refresh()
            return true
        } catch {
            lastError = error.localizedDescription
            return false
        }
    }

    /// Sohbeti Temizle: mesajlar + motor imleçleri sunucuda silinir, yerel
    /// önbellek boşalır. Bot bağlamını yitirir — sonraki koşu taze zincirle.
    func clearChat(bot: UUID) async -> Bool {
        guard let api else { return false }
        do {
            try await api.clearMessages(botID: bot)
            messages[bot] = []
            streaming[bot] = nil
            return true
        } catch {
            lastError = error.localizedDescription
            return false
        }
    }

    // ── rutinler / politika / sayaçlar ─────────────────────────────────

    /// Rutin ekle (K14: cron + dilim sunucuda saklanır).
    func createRoutine(botID: UUID, name: String, cron: String) async -> Bool {
        guard let api else { return false }
        do {
            _ = try await api.createRoutine(RoutineCreate(
                botID: botID, name: name, cronExpr: cron,
                timezone: TimeZone.current.identifier))
            routines = (try? await api.routines()) ?? routines
            return true
        } catch {
            lastError = error.localizedDescription
            return false
        }
    }

    func setRoutine(id: UUID, enabled: Bool) {
        guard let api else { return }
        Task {
            do {
                try await api.setRoutine(id: id, enabled: enabled)
                routines = (try? await api.routines()) ?? routines
            } catch {
                lastError = error.localizedDescription
            }
        }
    }

    func savePolicy(_ p: PolicySettings) {
        policy = p
        guard let api else { return }
        Task {
            do {
                try await api.setPolicy(p)
            } catch {
                lastError = error.localizedDescription
                policy = (try? await api.policy()) ?? policy
            }
        }
    }

    func setStatsRange(days: Int) {
        statsDays = days
        guard let api else { return }
        Task { stats = (try? await api.stats(days: days)) ?? stats }
    }

    // ── tasarım tiplerine çeviri ───────────────────────────────────────

    func bot(id: String) -> Bot? {
        guard let uuid = UUID(uuidString: id) else { return nil }
        return bots.first { $0.id == uuid }
    }

    func botName(_ id: UUID?) -> String {
        guard let id else { return "" }
        return bots.first { $0.id == id }?.name ?? "—"
    }

    /// Kenar çubuğu satırları: son mesaj önizlemesi + bekleyen onay rozeti.
    var agents: [DAgent] {
        bots.map { b in
            let msgs = messages[b.id] ?? []
            let last = msgs.last
            let hasPending = pending.contains { $0.botID == b.id }
            let preview = last?.text ?? b.description
            return DAgent(
                id: b.id.uuidString, name: b.name,
                c1: b.colorPrimary, c2: b.colorSecondary,
                time: last.map { Self.shortTime($0.createdAt) } ?? "",
                preview: Self.oneLine(preview),
                team: b.members?.map(\.uuidString),
                unread: hasPending,
                desc: b.description,
                isTeam: b.kind == "team")
        }
    }

    /// Sohbet akışı: gün ayraçları + mesajlar + canlı yanıt + onay kartı.
    func chatMessages(for botID: UUID) -> [DMsg] {
        Self.chatMessages(messages: messages[botID] ?? [],
                          streaming: streaming[botID],
                          pending: pending.filter { $0.botID == botID },
                          streamKey: botID.uuidString,
                          kanal: botID)
    }

    /// Başarısızlık çipinin öneki — sunucudaki `orchestrator.HataCipiOneki`
    /// ile BİREBİR aynı olmalı (tel protokolündeki gibi: iki taraf tek metin).
    static let hataCipiOneki = "⚠️ Koşu başarısız: "

    /// Saf çeviri (test edilebilir): sunucu mesajları → tasarım kartları.
    /// kanal: mesajların ait olduğu bot/ekip — grupta başka bottan gelen
    /// (sender'lı) mesajlar .from kartına düşer (ADIM 10).
    static func chatMessages(messages: [Message], streaming: String?,
                             pending: [Run], streamKey: String,
                             kanal: UUID? = nil) -> [DMsg] {
        // Onay kartı koşuyu doğuran mesajın hemen ardına yerleşir: kullanıcı
        // yeni mesaj yazsa bile eski istek akıştaki yerinde kalır. Bağ,
        // mesajın run_id'sidir (SaveChat ikisini aynı transaction'da yazar).
        // Bağı olmayan koşular (rutin/telegram) eskisi gibi sona eklenir.
        var kartlar: [UUID: [DMsg]] = [:]
        for run in pending {
            kartlar[run.id, default: []].append(askMessage(run))
        }
        var yerlesen: Set<UUID> = []
        var out: [DMsg] = []
        var lastDay = ""
        for m in messages {
            let day = Self.dayLabel(m.createdAt)
            if day != lastDay {
                out.append(DMsg(id: "day-\(m.id.uuidString)", k: .divider, text: day))
                lastDay = day
            }
            switch m.role {
            case "user":
                out.append(DMsg(id: m.id.uuidString, k: .user, text: m.text))
                Self.ekKartlari(m, into: &out)
            case "system", "event":
                // Başarısız koşu HATA KARTI olur: kullanıcı eskiden hiçbir
                // şey görmüyordu (mesajını yazıyor, cevap gelmiyor, sebep
                // yalnız sunucu logunda). Sebebi görmek cevapsız kalmaktan
                // iyidir — kartta "Tekrar dene" de var.
                if m.text.hasPrefix(Self.hataCipiOneki) {
                    out.append(DMsg(id: m.id.uuidString, k: .error,
                                    text: String(m.text.dropFirst(Self.hataCipiOneki.count)),
                                    title: "Koşu başarısız"))
                } else {
                    // Devir çipleri (ADIM 6): "→ … iletildi / ← … cevabı".
                    out.append(DMsg(id: m.id.uuidString, k: .divider, text: m.text))
                }
            default:
                // Grupta başka botun cevabı gönderen adıyla görünür (ADIM 10).
                if let s = m.sender, let bid = m.botID, kanal != bid {
                    out.append(DMsg(id: m.id.uuidString, k: .from, text: m.text, bot: s))
                } else {
                    out.append(DMsg(id: m.id.uuidString, k: .bot, text: m.text))
                }
                // Botun GÖNDERDİĞİ dosyalar da kart olur (ADIM 13): ek yalnız
                // kullanıcı mesajlarında çizilseydi send_file ile gelen rapor
                // Mac'te hiç görünmezdi.
                Self.ekKartlari(m, into: &out)
            }
            if let rid = m.runID, let kart = kartlar[rid] {
                out.append(contentsOf: kart)
                yerlesen.insert(rid)
            }
        }
        if let live = streaming, !live.isEmpty {
            out.append(DMsg(id: "stream-\(streamKey)", k: .bot, text: live))
        }
        for run in pending where !yerlesen.contains(run.id) {
            out.append(askMessage(run))
        }
        return out
    }

    /// Mesajın ekleri → dosya kartları (ADIM 5 gelen, ADIM 13 giden). Mesajın
    /// hemen altında akar; "Aç" düğmesi dosyayı indirip varsayılan uygulamada
    /// açar.
    static func ekKartlari(_ m: Message, into out: inout [DMsg]) {
        for att in m.attachments ?? [] {
            out.append(DMsg(id: "ek-\(att.id.uuidString)", k: .file,
                            name: att.name, meta: boyut(att.size),
                            fid: att.id, ekKind: att.kind))
        }
    }

    /// Park edilmiş istek → kart. İzinler onay kartı, sorular baloncuk
    /// (cevap sohbete yazılır — Telegram'daki davranışın aynısı).
    static func askMessage(_ run: Run) -> DMsg {
        guard let ask = run.pendingAsk else {
            return DMsg(id: run.id.uuidString, k: .approve,
                        text: run.finalText ?? run.prompt, title: "Onay bekliyor")
        }
        if ask.kind == "question" {
            let q = ask.question ?? "Bot bir soru sordu."
            let choices = (ask.choices ?? []).isEmpty
                ? "" : "\n\nSeçenekler: " + (ask.choices ?? []).joined(separator: " · ")
            return DMsg(id: run.id.uuidString, k: .bot,
                        text: "❓ " + q + choices + "\n\nCevabını buraya yazabilirsin.")
        }
        let tool = ask.tool ?? "araç"
        return DMsg(
            id: run.id.uuidString, k: .approve,
            text: Self.askDetail(ask, fallback: run.finalText ?? run.prompt),
            title: Self.toolLabel(tool) + " izni gerekiyor",
            to: run.tainted
                ? "⚠ Bu koşu dış içerik okudu — otomatik izinler geçersiz"
                : "Araç: " + tool,
            hepIzin: ask.allowAlways ?? false)
    }

    /// Onay Merkezi satırları — izin bekleyen koşular (sorular hariç:
    /// onlar sohbette cevaplanır).
    var inbox: [InboxItem] { Self.inboxItems(pending: pending, bots: bots, decided: decided) }

    /// Saf çeviri (test edilebilir): sorular ve kararı verilmişler düşer.
    static func inboxItems(pending: [Run], bots: [Bot], decided: [String: String]) -> [InboxItem] {
        pending.compactMap { run -> InboxItem? in
            guard run.pendingAsk?.kind != "question" else { return nil }
            guard decided[run.id.uuidString] == nil else { return nil }
            let bot = bots.first { $0.id == run.botID }
            return InboxItem(
                id: run.id.uuidString, msg: askMessage(run),
                botId: run.botID.uuidString,
                botName: bot?.name ?? "—",
                c2: bot?.colorSecondary ?? "#7b4fd6",
                time: shortTime(run.queuedAt))
        }
    }

    /// Görevler paneli: botun çalışan / sırada / onay bekleyen koşuları.
    func tasks(for botID: UUID) -> [DTask] {
        guard let board else { return [] }
        var out: [DTask] = []
        for r in board.running where r.botID == botID {
            out.append(DTask(Self.oneLine(r.prompt), .running, Self.since(r.startedAt ?? r.queuedAt), 0))
        }
        for r in board.queued where r.botID == botID {
            out.append(DTask(Self.oneLine(r.prompt), .queued, "sırada", 0))
        }
        for r in board.approvalPending where r.botID == botID {
            out.append(DTask(Self.oneLine(r.prompt), .waiting, Self.since(r.queuedAt), 0))
        }
        return out
    }

    /// Menü çubuğu: tüm botların aktif işleri.
    var allTasks: [DTask] {
        guard let board else { return [] }
        return board.running.map { DTask(Self.oneLine($0.prompt), .running, botName($0.botID), 0) }
            + board.approvalPending.map { DTask(Self.oneLine($0.prompt), .waiting, botName($0.botID), 0) }
            + board.queued.map { DTask(Self.oneLine($0.prompt), .queued, botName($0.botID), 0) }
    }

    func designRoutines(for botID: UUID) -> [DRoutine] {
        routines.filter { $0.botID == botID }.map { r in
            var sub = r.cronExpr
            if let next = r.nextRunAt, r.enabled {
                sub += " · sonraki " + Self.shortTime(next)
            }
            return DRoutine(r.name, sub, !r.enabled)
        }
    }

    func routineID(botID: UUID, index: Int) -> UUID? {
        let list = routines.filter { $0.botID == botID }
        return list.indices.contains(index) ? list[index].id : nil
    }

    /// Bağlantılar: sunucudaki konektörler (Google…) + Mac runner.
    var designConns: [DConn] {
        var out = connectors.map { DConn(Self.connLabel($0.kind), $0.status == "connected") }
        out.append(DConn("Mac (yerel araçlar)", runner.connected))
        return out
    }

    // ── aktivite ───────────────────────────────────────────────────────

    func activityDays(filter: String) -> [ActivityDay] {
        let items = activity.filter { item in
            switch filter {
            case "all": return true
            case "err": return item.ok == false || item.status == "failed"
            default: return botName(item.botID) == filter
            }
        }
        var order: [String] = []
        var grouped: [String: [DActivityItem]] = [:]
        for item in items {
            let day = Self.dayLabel(item.at)
            if grouped[day] == nil { order.append(day) }
            grouped[day, default: []].append(DActivityItem(
                Self.clockTime(item.at),
                Self.oneLine(item.text),
                Self.activityMeta(item),
                item.type == "local" ? "Mac" : botName(item.botID),
                item.ok == false || item.status == "failed"))
        }
        return order.map { ActivityDay(id: $0, label: $0, items: grouped[$0] ?? []) }
    }

    private static func activityMeta(_ item: ActivityItem) -> String {
        var parts: [String] = []
        if let s = item.status { parts.append(statusLabel(s)) }
        if let o = item.origin, !o.isEmpty { parts.append(originLabel(o)) }
        if item.type == "local" { parts.append(item.ok == true ? "yerel işlem" : "yerel işlem · hata") }
        return parts.joined(separator: " · ")
    }

    private static func statusLabel(_ s: String) -> String {
        switch s {
        case "completed": return "tamamlandı"
        case "failed": return "başarısız"
        case "approval_pending": return "onay bekliyor"
        case "running": return "çalışıyor"
        case "queued": return "sırada"
        case "expired": return "süresi doldu"
        default: return s
        }
    }

    private static func originLabel(_ o: String) -> String {
        switch o {
        case "telegram": return "Telegram"
        case "routine": return "rutin"
        case "mac": return "Mac"
        case "api": return "API"
        default: return o
        }
    }

    private static func connLabel(_ kind: String) -> String {
        switch kind {
        case "google": return "Google (Gmail · Takvim)"
        default: return kind.capitalized
        }
    }

    /// `mcp__gw__gmail_send_draft` → "Gmail send draft"
    static func toolLabel(_ tool: String) -> String {
        var name = tool
        if let r = name.range(of: "__", options: .backwards) {
            name = String(name[r.upperBound...])
        }
        return name.replacingOccurrences(of: "_", with: " ").capitalized
    }

    private static func askDetail(_ ask: Ask, fallback: String) -> String {
        if let input = ask.input, case .object(let fields) = input {
            let lines = fields
                .sorted { $0.key < $1.key }
                .prefix(6)
                .map { "\($0.key): \(oneLine(describe($0.value)))" }
            if !lines.isEmpty { return lines.joined(separator: "\n") }
        }
        return oneLine(fallback)
    }

    private static func describe(_ v: JSONValue) -> String {
        switch v {
        case .string(let s): return s
        case .number(let n): return n == n.rounded() ? String(Int(n)) : String(n)
        case .bool(let b): return b ? "evet" : "hayır"
        case .null: return "—"
        case .array(let a): return a.map(describe).joined(separator: ", ")
        case .object(let o): return o.keys.sorted().joined(separator: ", ")
        }
    }

    // ── biçimlendirme ──────────────────────────────────────────────────

    static func oneLine(_ s: String, limit: Int = 140) -> String {
        let flat = s.replacingOccurrences(of: "\n", with: " ")
            .trimmingCharacters(in: .whitespacesAndNewlines)
        return flat.count <= limit ? flat : String(flat.prefix(limit)) + "…"
    }

    private static let clock: DateFormatter = {
        let f = DateFormatter()
        f.locale = Locale(identifier: "tr_TR")
        f.dateFormat = "HH:mm"
        return f
    }()

    private static let dayMonth: DateFormatter = {
        let f = DateFormatter()
        f.locale = Locale(identifier: "tr_TR")
        f.dateFormat = "d MMMM"
        return f
    }()

    static func clockTime(_ d: Date) -> String { clock.string(from: d) }

    /// Bugünse saat, dünse "Dün", değilse gün-ay.
    static func shortTime(_ d: Date) -> String {
        let cal = Calendar.current
        if cal.isDateInToday(d) { return clock.string(from: d) }
        if cal.isDateInYesterday(d) { return "Dün" }
        return dayMonth.string(from: d)
    }

    static func dayLabel(_ d: Date) -> String {
        let cal = Calendar.current
        if cal.isDateInToday(d) { return "Bugün" }
        if cal.isDateInYesterday(d) { return "Dün" }
        return dayMonth.string(from: d)
    }

    static func since(_ d: Date) -> String {
        let secs = Int(Date().timeIntervalSince(d))
        if secs < 60 { return "\(max(1, secs)) sn" }
        if secs < 3600 { return "\(secs / 60) dk" }
        if secs < 86400 { return "\(secs / 3600) sa" }
        return "\(secs / 86400) gün"
    }
}
