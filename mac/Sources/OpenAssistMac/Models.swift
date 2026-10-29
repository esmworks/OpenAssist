import Foundation

/// Go httpapi DTO'larının Swift karşılıkları. Alan adları sunucu kontratıyla
/// birebirdir (snake_case anahtarlar CodingKeys ile eşlenir); sunucu bizim
/// kontratımız olsa da istemci tarafında bilinmeyen alan HATA DEĞİLDİR —
/// eski istemci yeni sunucuyla çalışabilmelidir.
struct Bot: Codable, Identifiable, Hashable, Sendable {
    let id: UUID
    let kind: String // "bot" | "team"
    let name: String
    let description: String
    let colorPrimary: String
    let colorSecondary: String
    let section: String
    let engine: String
    let model: String
    let pinned: Bool
    let members: [UUID]?
    /// Botun pi eklenti allowlist'i (`bots.extensions`): koşuda motora YALNIZ
    /// bunlar yüklenir. Boş liste geçerli bir seçimdir ("çıplak pi"), o yüzden
    /// nil ile boşu ayırıyoruz — alan hiç gelmediyse (eski sunucu) bilinmiyor
    /// demektir ve ekran "hiçbiri" diye YAZMAZ.
    let extensions: [String]?

    enum CodingKeys: String, CodingKey {
        case id, kind, name, description, section, engine, model, pinned, members, extensions
        case colorPrimary = "color_primary"
        case colorSecondary = "color_secondary"
    }
}

/// ExtensionInfo — sunucuda KURULU bir motor eklentisi (`GET /api/extensions`).
/// Eklentiler ekranı ve bot formundaki seçim listesi buradan gelir; istemci
/// liste GÖMMEZ — kurulu olmayan bir ad allowlist'e yazılırsa koşu düşer.
struct ExtensionInfo: Codable, Identifiable, Hashable, Sendable {
    /// Allowlist adı (ör. "oa-gateway.ts"). Kimlik budur, görünen ad değil.
    let id: String
    let engine: String
    let name: String
    let description: String
    /// "builtin" (ikiliye gömülü, kaldırılamaz) | "file" | "npm" | "skill"
    let kind: String
    let version: String?
    /// Bu girdi motora NE taşıyor: extension | skill | prompt | theme.
    /// Bir paket birkaçını birden taşıyabilir; kullanıcı neyi açtığını
    /// görmeli çünkü beceri de modele iş yaptırır.
    let resources: [String]?
    /// Bu eklentiyi kullanan botların adları — kaldırmadan önce kimin
    /// bozulacağı görünsün.
    let usedBy: [String]

    var builtin: Bool { kind == "builtin" }

    enum CodingKeys: String, CodingKey {
        case id, engine, name, description, kind, version, resources
        case usedBy = "used_by"
    }
}

/// MemoryRecord — kalıcı hafıza kaydı (`/api/memories…`).
///
/// Ekranda şimdilik yalnız `kind == "instruction"` olanlar görünüyor (sabit
/// talimatlar). Tip yine de tam kayıt: hafızanın kalan yüzeyi (olgular,
/// düzeltmeler, arşiv) aynı gövdeden okunacak.
struct MemoryRecord: Codable, Identifiable, Hashable, Sendable {
    let id: String
    /// "user" = tüm botlar, "bot" = tek bot.
    let scope: String
    let botID: String?
    /// fact | preference | instruction | correction
    let kind: String
    let content: String
    let revision: Int
    let createdAt: Date

    /// Kapsam rozeti: kullanıcı hangi botları bağladığını görmeli.
    var scopeLabel: String { scope == "user" ? "Tüm botlar" : "Bu bot" }

    enum CodingKeys: String, CodingKey {
        case id, scope, kind, content, revision
        case botID = "bot_id"
        case createdAt = "created_at"
    }
}

/// CatalogPackage — pi paket galerisinden bir kayıt (`GET /api/extensions/catalog`).
/// Galeri npm'in `pi-package` etiketiyle beslenir; listede olmak paketin
/// GÜVENLİ olduğu anlamına gelmez — kurmak sunucuda kod çalıştırır.
struct CatalogPackage: Codable, Identifiable, Hashable, Sendable {
    let name: String
    let version: String
    let description: String
    let publisher: String?
    /// Kurulum gövdesine aynen konur — sürüm sabitlenmiş kaynak dizesi.
    let source: String
    let monthlyDownloads: Int
    let installed: Bool
    /// OpenAssist'te karşılığı varsa sebebi (çakışma uyarısı).
    let conflictNote: String?
    let npmURL: String?

    var id: String { name }

    enum CodingKeys: String, CodingKey {
        case name, version, description, publisher, source, installed
        case monthlyDownloads = "monthly_downloads"
        case conflictNote = "conflict_note"
        case npmURL = "npm_url"
    }
}

/// EngineInfo — sunucuda kayıtlı bir motor ve kabul ettiği modeller
/// (`GET /api/engines`). Model listesi BURADAN gelir, istemcide gömülü
/// değildir: motora geçmeyen bir ad seçilirse koşu düşer (K18).
struct EngineInfo: Codable, Identifiable, Hashable, Sendable {
    struct Model: Codable, Identifiable, Hashable, Sendable {
        let id: String // motora aynen geçer; boş = motorun varsayılanı
        let label: String
    }

    let id: String
    let name: String
    /// Motorun sunucudaki hali — CLI kurulu mu, hangi sürüm, hangi kimlik.
    /// Modeller ekranı bunu gösterir: hata koşu denenmeden görülsün.
    let available: Bool
    let version: String?
    let loggedIn: Bool
    let account: String?
    let plan: String?
    let detail: String
    let probed: Bool
    let checkedAt: Date
    let models: [Model]

    enum CodingKeys: String, CodingKey {
        case id, name, available, version, account, plan, detail, probed, models
        case loggedIn = "logged_in"
        case checkedAt = "checked_at"
    }

    /// Durum ışığı: yeşil = koşmaya hazır · sarı = kurulu ama kimlik yok
    /// (ya da yoklanamıyor) · kırmızı = CLI yok.
    enum Health: Sendable { case ready, warning, missing }

    var health: Health {
        guard available else { return .missing }
        guard probed else { return .warning } // durumu bilinmiyor
        return loggedIn ? .ready : .warning
    }

    /// Bot kaydındaki motor kimliğini listeye çöz. Boşsa (eski kayıt) ilk
    /// motor; tanınmıyorsa nil — o zaman model listesi de boş kalır, çünkü
    /// bilmediğimiz motorun yeteneğini uydurmayız (K18).
    static func resolve(_ botEngine: String, in engines: [EngineInfo]) -> EngineInfo? {
        if let e = engines.first(where: { $0.id == botEngine }) { return e }
        return botEngine.isEmpty ? engines.first : nil
    }

    /// Model adının kullanıcıya görünen hali. Boş = motorun varsayılanı;
    /// listede yoksa ham ad kalır (kullanıcı elle sabitlemiş olabilir).
    static func modelLabel(_ model: String, in engine: EngineInfo?) -> String {
        guard !model.isEmpty else { return "varsayılan" }
        return engine?.models.first { $0.id == model }?.label ?? model
    }
}

// ── Sağlayıcılar + models.dev kataloğu ──
//
// Sağlayıcı tarifleri sunucunun veritabanında yaşar (`/api/providers`), ortam
// değişkeninde değil: yeni model eklemek ya da dönen anahtarı değiştirmek için
// sunucuya girip kabı yeniden başlatmak gerekmesin.
//
// ANAHTAR TEK YÖNLÜDÜR: sunucu değeri hiçbir yanıtta göndermez, yalnız
// "tanımlı mı" (`has_key`) bilgisini verir. Bu yüzden düzenleme sırasında
// anahtar alanı BOŞ başlar ve boş bırakılırsa kayıtlı anahtara dokunulmaz.

/// ProviderInfo — sunucuda kayıtlı bir model sağlayıcısı.
struct ProviderInfo: Codable, Identifiable, Hashable, Sendable {
    struct Model: Codable, Identifiable, Hashable, Sendable {
        var id: String // sağlayıcıya giden ham ad; eğik çizgi içeremez
        var label: String
        var contextWindow: Int
        var maxTokens: Int
        var reasoning: Bool
        var input: [String]

        init(id: String, label: String = "", contextWindow: Int = 0,
             maxTokens: Int = 0, reasoning: Bool = false, input: [String] = []) {
            self.id = id
            self.label = label
            self.contextWindow = contextWindow
            self.maxTokens = maxTokens
            self.reasoning = reasoning
            self.input = input
        }

        enum CodingKeys: String, CodingKey {
            case id, label, reasoning, input
            case contextWindow = "context_window"
            case maxTokens = "max_tokens"
        }
    }

    var id: String
    var name: String
    var baseURL: String
    var api: String // openai-completions | openai-responses | anthropic-messages | google-generative-ai
    var enabled: Bool
    var hasKey: Bool
    var position: Int
    var models: [Model]

    enum CodingKeys: String, CodingKey {
        case id, name, api, enabled, position, models
        case baseURL = "base_url"
        case hasKey = "has_key"
    }
}

/// ProviderSave — PUT /api/providers/{id} gövdesi.
///
/// Model listesi TAM DEĞİŞTİRİLİR (sunucu PUT semantiği): ekranda ne varsa
/// kayıtta o olur.
struct ProviderSave: Encodable, Sendable {
    var id: String
    var name: String
    var baseURL: String
    var api: String
    var enabled: Bool
    /// nil = kayıtlı anahtara dokunma · "" = sil · dolu = değiştir.
    /// Optional olması şart: JSONEncoder nil alanı hiç yazmaz, sunucu da
    /// "gönderilmedi" ile "boşalt" arasını böyle ayırır.
    var apiKey: String?
    var position: Int
    var models: [ProviderInfo.Model]

    enum CodingKeys: String, CodingKey {
        case id, name, api, enabled, position, models
        case baseURL = "base_url"
        case apiKey = "api_key"
    }
}

/// ProviderDraft — Modeller ekranındaki düzenleme taslağı.
///
/// Sunucuya yalnız "Kaydet" ile gidilir: her tuşta PUT atmak, yarım yazılmış
/// bir adresi kaydedip aradaki koşuları düşürürdü.
struct ProviderDraft: Identifiable, Hashable, Sendable {
    var id: String
    /// Yeni kayıt mı — kimlik YALNIZ burada düzenlenebilir. Var olan kaydın
    /// kimliğini değiştirmek onu taşımaz, ikinci bir kayıt yaratırdı (PUT
    /// upsert'tir); eskisi anahtarıyla birlikte ortada kalırdı.
    var yeni: Bool
    var name: String
    var baseURL: String
    var api: String
    var enabled: Bool
    /// Sunucuda kayıtlı anahtar var mı — değeri asla gelmez.
    var hasKey: Bool
    var position: Int
    var models: [ProviderInfo.Model]
    /// Kullanıcının yazdığı yeni anahtar; boş = kayıtlıya dokunma.
    var key = ""
    /// Kayıtlı anahtarı sil.
    var keySilinsin = false

    /// pi'nin konuştuğu tel biçimleri (sunucudaki gecerliAPIler ile aynı küme).
    static let apiSecenekleri: [(id: String, label: String)] = [
        ("openai-completions", "OpenAI · /chat/completions"),
        ("openai-responses", "OpenAI · /responses"),
        ("anthropic-messages", "Anthropic · /messages"),
        ("google-generative-ai", "Google · Generative AI"),
    ]

    init(_ p: ProviderInfo) {
        id = p.id
        yeni = false
        name = p.name
        baseURL = p.baseURL
        api = p.api
        enabled = p.enabled
        hasKey = p.hasKey
        position = p.position
        models = p.models
    }

    /// Katalogdan yeni sağlayıcı — adres ve tel biçimi ön-dolar, modeller
    /// boş başlar (hangilerinin isteneceğini kullanıcı seçer).
    init(catalog c: CatalogProvider, position: Int) {
        id = c.id
        yeni = true
        name = c.name
        baseURL = c.baseURL ?? ""
        api = c.api ?? "openai-completions"
        enabled = true
        hasKey = false
        self.position = position
        models = []
    }

    /// Boş taslak — katalogda olmayan bir uç (yerel sunucu, şirket içi vekil).
    init(position: Int) {
        id = ""
        yeni = true
        name = ""
        baseURL = ""
        api = "openai-completions"
        enabled = true
        hasKey = false
        self.position = position
        models = []
    }

    /// Sunucunun reddedeceği hâller — kurallar sunucudakiyle aynı, burada
    /// tekrarlanıyor ki kullanıcı gidiş-dönüş beklemeden görsün.
    var hata: String? {
        let kimlik = id.trimmingCharacters(in: .whitespaces).lowercased()
        if kimlik.isEmpty { return "Kimlik boş olamaz" }
        if kimlik.range(of: "^[a-z0-9][a-z0-9._-]*$", options: .regularExpression) == nil {
            return "Kimlik küçük harf, rakam ve . _ - içerebilir; harf ya da rakamla başlamalı"
        }
        let adres = baseURL.trimmingCharacters(in: .whitespaces)
        if adres.isEmpty { return "Taban adres boş olamaz" }
        if !adres.hasPrefix("http://"), !adres.hasPrefix("https://") {
            return "Taban adres http:// ya da https:// ile başlamalı"
        }
        if models.contains(where: { $0.id.contains("/") }) {
            return "Model kimliği eğik çizgi içeremez — önek sağlayıcının kendisidir"
        }
        return nil
    }

    /// PUT gövdesi. Anahtar alanı üç durumu ayırır: boş bırakıldıysa hiç
    /// gönderilmez (kayıtlı anahtar korunur), "sil" işaretliyse boş dize
    /// gider (sunucu siler), doluysa değişir.
    var kayit: ProviderSave {
        ProviderSave(
            id: id.trimmingCharacters(in: .whitespaces).lowercased(),
            name: name.trimmingCharacters(in: .whitespaces),
            baseURL: baseURL.trimmingCharacters(in: .whitespaces),
            api: api, enabled: enabled,
            apiKey: keySilinsin ? "" : (key.isEmpty ? nil : key),
            position: position, models: models)
    }
}

/// models.dev kataloğu (`GET /api/models/catalog`) — sağlayıcı ve model
/// seçmeyi kolaylaştıran liste. Otorite değildir: kullanıcı elle de ekler.
struct ModelCatalog: Codable, Sendable {
    let fetchedAt: Date
    let providers: [CatalogProvider]

    enum CodingKeys: String, CodingKey {
        case providers
        case fetchedAt = "fetched_at"
    }
}

struct CatalogProvider: Codable, Identifiable, Hashable, Sendable {
    let id: String
    let name: String
    /// Katalogdaki taban adres; kayıtların bir kısmında yok (omitempty).
    let baseURL: String?
    let api: String?
    let doc: String?
    let models: [CatalogModel]

    enum CodingKeys: String, CodingKey {
        case id, name, api, doc, models
        case baseURL = "base_url"
    }
}

struct CatalogModel: Codable, Identifiable, Hashable, Sendable {
    let id: String
    let name: String
    let reasoning: Bool
    let toolCall: Bool
    let contextWindow: Int
    let maxTokens: Int
    let input: [String]?
    let releaseDate: String?

    enum CodingKeys: String, CodingKey {
        case id, name, reasoning, input
        case toolCall = "tool_call"
        case contextWindow = "context_window"
        case maxTokens = "max_tokens"
        case releaseDate = "release_date"
    }

    /// Katalog kaydından kaydedilebilir model — sınırlar da taşınır ki pi
    /// bağlam penceresini bilsin (models.json'a yazılır).
    var kayit: ProviderInfo.Model {
        ProviderInfo.Model(id: id, label: name, contextWindow: contextWindow,
                           maxTokens: maxTokens, reasoning: reasoning,
                           input: input ?? [])
    }
}

/// Ek — sunucuya yüklenen dosya metadata'sı (ADIM 5). İçerik
/// GET /api/attachments/{id}/file ile indirilir.
struct Attachment: Codable, Identifiable, Hashable, Sendable {
    let id: UUID
    let name: String
    let kind: String // pdf | image | doc | file
    let size: Int64
}

struct Message: Codable, Identifiable, Hashable, Sendable {
    let id: UUID
    let role: String // "user" | "assistant" | "system"
    let text: String
    let createdAt: Date
    let runID: UUID?
    // DİKKAT — bu üç alan `= nil` VARSAYILANIYLA YAZILMAMALI. Swift'in
    // sentezlediği init(from:) başlangıç değeri olan `let` alanlarını hiç
    // çözmez (sabit sayılır, atama yapılamaz): alanlar JSON'da gelse bile
    // sessizce nil kalırdı. Ölçüldü — ek kartları ve grup gönderen adı bu
    // yüzden hiç görünmüyordu (ADIM 13). Opsiyonel oldukları için decodeIfPresent
    // kullanılır; alan yoksa yine nil olur, varsayılana gerek yok.
    /// Ekler (ADIM 5). Opsiyonel: sunucu eklemeden önceki sürümlerde yoktur.
    let attachments: [Attachment]?
    /// Mesajı yazan bot (ADIM 10: grupta kanal sahibinden farklı olabilir).
    let botID: UUID?
    /// Bot mesajının görünür göndereni (ADIM 10: grup üyesi adı).
    let sender: String?

    // Varsayılanlar üye üye init'te yaşar: alanların kendisine yazılırsa
    // (yukarıdaki not) çözme bozulur, burada yazılınca yalnız çağrı yerlerini
    // kısaltır.
    init(id: UUID, role: String, text: String, createdAt: Date, runID: UUID?,
         attachments: [Attachment]? = nil, botID: UUID? = nil, sender: String? = nil) {
        self.id = id
        self.role = role
        self.text = text
        self.createdAt = createdAt
        self.runID = runID
        self.attachments = attachments
        self.botID = botID
        self.sender = sender
    }

    enum CodingKeys: String, CodingKey {
        case id, role, text
        case createdAt = "created_at"
        case runID = "run_id"
        case attachments
        case botID = "bot_id"
        case sender
    }
}

/// Ask — koşunun beklettiği istek (runs.pending_ask / SSE request.opened).
struct Ask: Codable, Hashable, Sendable {
    let kind: String // "permission" | "question"
    let tool: String?
    let input: JSONValue?
    let question: String?
    let choices: [String]?
    /// Bu istek için "hep izin ver" sunulabilir mi (sunucunun kararı —
    /// policy.Decide). Kendimiz hesaplamıyoruz: kural mantığı iki yerde
    /// yaşarsa sessizce ayrışır. Eski sunucu göndermezse false.
    var allowAlways: Bool?
}

struct Run: Codable, Identifiable, Hashable, Sendable {
    let id: UUID
    let botID: UUID
    let status: String
    let prompt: String
    let tainted: Bool
    let pendingAsk: Ask?
    let error: String?
    let costUSD: Double
    let finalText: String?
    let queuedAt: Date
    let startedAt: Date?
    let finishedAt: Date?
    /// Koşuyu doğuran kanal (api|mac|telegram|routine). Opsiyonel: eski
    /// sunucu göndermezse istemci kırılmasın.
    var origin: String?

    enum CodingKeys: String, CodingKey {
        case id, status, prompt, tainted, error, origin
        case botID = "bot_id"
        case pendingAsk = "pending_ask"
        case costUSD = "cost_usd"
        case finalText = "final_text"
        case queuedAt = "queued_at"
        case startedAt = "started_at"
        case finishedAt = "finished_at"
    }
}

struct SendReceipt: Codable, Sendable {
    let runID: UUID
    let status: String

    enum CodingKeys: String, CodingKey {
        case runID = "run_id"
        case status
    }
}

struct ApprovalDecision: Encodable, Sendable {
    var decision: String = "" // allow | deny
    var answer: String = "" // soru cevabı
    var always: Bool = false
}

struct Routine: Codable, Identifiable, Hashable, Sendable {
    let id: UUID
    let botID: UUID
    let name: String
    let cronExpr: String
    let timezone: String
    let enabled: Bool
    let nextRunAt: Date?
    let lastRunAt: Date?

    enum CodingKeys: String, CodingKey {
        case id, name, timezone, enabled
        case botID = "bot_id"
        case cronExpr = "cron_expr"
        case nextRunAt = "next_run_at"
        case lastRunAt = "last_run_at"
    }
}

/// Rutin yaratma gövdesi. K11: girdi kaynağı ve beklenen çıktı zorunludur —
/// panelden eklenen rutinde bunlar rutin adından türetilir, Ayarlar/API'den
/// detaylandırılabilir.
struct RoutineCreate: Encodable, Sendable {
    var botID: UUID
    var name: String
    var cronExpr: String
    var timezone: String
    var inputSource = "Botun bağlı araçları ve sohbet geçmişi"
    var expectedOutput: String { name }
    var stalePolicy = "fail"

    enum CodingKeys: String, CodingKey {
        case name, timezone
        case botID = "bot_id"
        case cronExpr = "cron_expr"
        case inputSource = "input_source"
        case expectedOutput = "expected_output"
        case stalePolicy = "stale_policy"
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(botID, forKey: .botID)
        try c.encode(name, forKey: .name)
        try c.encode(cronExpr, forKey: .cronExpr)
        try c.encode(timezone, forKey: .timezone)
        try c.encode(inputSource, forKey: .inputSource)
        try c.encode(expectedOutput, forKey: .expectedOutput)
        try c.encode(stalePolicy, forKey: .stalePolicy)
    }
}

/// Aktivite akışı satırı (koşu ya da yerel Mac işlemi, K19).
struct ActivityItem: Codable, Identifiable, Hashable, Sendable {
    let type: String // run | local
    let at: Date
    let botID: UUID?
    let runID: UUID?
    let status: String?
    let text: String
    let origin: String?
    let ok: Bool?

    var id: String { "\(type)-\(runID?.uuidString ?? "")-\(at.timeIntervalSince1970)" }

    enum CodingKeys: String, CodingKey {
        case type, at, status, text, origin, ok
        case botID = "bot_id"
        case runID = "run_id"
    }
}

/// Görevler panosu (K19).
struct TaskBoard: Codable, Sendable {
    let running: [Run]
    let queued: [Run]
    let approvalPending: [Run]
    let routines: [Routine]

    enum CodingKeys: String, CodingKey {
        case running, queued, routines
        case approvalPending = "approval_pending"
    }
}

/// Onay politikası (`policy.Settings`). PUT katı çözer: alan adları birebir.
struct PolicySettings: Codable, Sendable, Equatable {
    var dailyActionQuota: Int = 0
    var quietHoursStart: String = ""
    var quietHoursEnd: String = ""
    var timezone: String = "Europe/Istanbul"
    var approvalTTLHours: Int = 72

    enum CodingKeys: String, CodingKey {
        case timezone
        case dailyActionQuota = "daily_action_quota"
        case quietHoursStart = "quiet_hours_start"
        case quietHoursEnd = "quiet_hours_end"
        case approvalTTLHours = "approval_ttl_hours"
    }
}

/// Maliyet/koşu panosu (GET /api/stats?days=N).
struct Stats: Codable, Sendable {
    struct ByBot: Codable, Sendable {
        let botID: UUID
        let runs: Int
        let costUSD: Double

        enum CodingKeys: String, CodingKey {
            case runs
            case botID = "bot_id"
            case costUSD = "cost_usd"
        }
    }

    let days: Int
    let totalRuns: Int
    let completed: Int
    let failed: Int
    let expired: Int
    let pending: Int
    let totalCostUSD: Double
    let inputTokens: Int
    let outputTokens: Int
    let byBot: [ByBot]?

    enum CodingKeys: String, CodingKey {
        case days, completed, failed, expired, pending
        case totalRuns = "total_runs"
        case totalCostUSD = "total_cost_usd"
        case inputTokens = "input_tokens"
        case outputTokens = "output_tokens"
        case byBot = "by_bot"
    }
}

/// Bağlı konektör (Google vb.).
struct Connector: Codable, Identifiable, Sendable {
    let id: UUID
    let kind: String
    let account: String
    let status: String
    let error: String?
}

/// Bot yaratma gövdesi — sunucu katı çözer, yalnız bilinen alanlar gider.
struct BotCreate: Encodable, Sendable {
    var kind = "bot"
    var name: String
    var description: String
    var colorPrimary: String
    var colorSecondary: String
    var engine: String?
    var model: String?
    var temperature: Double?
    var members: [UUID]?
    /// Eklenti allowlist'i. nil GÖNDERİLMEZ ve sunucu varsayılanı (onay
    /// kapısını taşıyan oa-gateway) devreye girer; boş dizi ise "çıplak pi"
    /// demektir — ikisi aynı şey değil.
    var extensions: [String]?

    enum CodingKeys: String, CodingKey {
        case kind, name, description, engine, model, temperature, members, extensions
        case colorPrimary = "color_primary"
        case colorSecondary = "color_secondary"
    }
}

/// Bot güncelleme gövdesi — nil alanlar gönderilmez (sunucu dokunmaz).
struct BotUpdate: Encodable, Sendable {
    var name: String?
    var description: String?
    var colorPrimary: String?
    var colorSecondary: String?
    var engine: String?
    var model: String?
    var temperature: Double?
    var members: [UUID]?
    var extensions: [String]?

    enum CodingKeys: String, CodingKey {
        case name, description, engine, model, temperature, members, extensions
        case colorPrimary = "color_primary"
        case colorSecondary = "color_secondary"
    }
}

struct BulkApproveResult: Codable, Sendable {
    struct Item: Codable, Sendable {
        let runID: UUID
        let status: String
        let reason: String?

        enum CodingKeys: String, CodingKey {
            case status, reason
            case runID = "run_id"
        }
    }

    let results: [Item]
}

// ── SSE olayları ───────────────────────────────────────────────────────

/// Sunucudan akan bir SSE çerçevesi; `kind` engine olay sözlüğüdür
/// (content.delta, request.opened, turn.completed, stream.end …).
struct StreamEvent: Sendable {
    let id: Int64?
    let kind: String
    let data: Data
}

/// content.delta / assistant.text yükü.
struct TextPayload: Codable, Sendable {
    let text: String?
}

// ── JSON kodlayıcılar ──────────────────────────────────────────────────

/// Go `time.Time` RFC3339'u kesirli saniyeli ya da saniyesiz gelebilir;
/// iki biçimi de kabul eden çözücü.
enum API {
    static let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .custom { decoder in
            let s = try decoder.singleValueContainer().decode(String.self)
            // Formatter Sendable değil — çözücü başına kurulur (listeler küçük).
            let fractional = ISO8601DateFormatter()
            fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            if let date = fractional.date(from: s) {
                return date
            }
            let plain = ISO8601DateFormatter()
            plain.formatOptions = [.withInternetDateTime]
            if let date = plain.date(from: s) {
                return date
            }
            throw DecodingError.dataCorrupted(.init(
                codingPath: decoder.codingPath,
                debugDescription: "tarih çözülemedi: \(s)"
            ))
        }
        return d
    }()

    static let encoder = JSONEncoder()
}
