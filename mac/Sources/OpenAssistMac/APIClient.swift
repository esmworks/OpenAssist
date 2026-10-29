import Foundation

/// APIClient — core'un özel HTTP yüzeyine (:8080) ince istemci.
/// Hata gövdesi `{"error": "..."}` kontratıdır; kullanıcıya aynen gösterilir.
///
/// Kimlik: her istek `Authorization: Bearer <token>` taşır. Başlık TEK yerde,
/// istek kurucusunda eklenir — çağrı yerlerine dağıtılsaydı yeni bir uç
/// eklerken unutmak kaçınılmazdı ve hata 401 olarak çok geç görünürdü.
struct APIClient: Sendable {
    var baseURL: URL
    /// Sunucunun API_TOKEN'ı (Ayarlar › Bağlantılar, keychain'de saklanır).
    var token: String = ""
    var session: URLSession = .shared

    struct APIError: Error, LocalizedError {
        let status: Int
        let message: String
        var errorDescription: String? { message }
    }

    // ── botlar / sohbet ────────────────────────────────────────────────

    func bots() async throws -> [Bot] {
        try await get("/api/bots")
    }

    func messages(botID: UUID, limit: Int = 100) async throws -> [Message] {
        try await get("/api/bots/\(botID.uuidString)/messages?limit=\(limit)")
    }

    /// Mesaj gönderimi (ADIM 3+5). key: aynı mantıksal gönderimin retry'inde
    /// tekrar kullanılır (sunucu ikinci konuşcuğu engeller).
    /// attachmentIDs: önce upload ile yüklenen ekler.
    func send(botID: UUID, text: String, key: UUID, attachmentIDs: [UUID]) async throws -> SendReceipt {
        var body: [String: Any] = ["text": text, "origin": "mac", "key": key.uuidString]
        if !attachmentIDs.isEmpty {
            body["attachment_ids"] = attachmentIDs.map(\.uuidString)
        }
        return try await postJSON("/api/bots/\(botID.uuidString)/messages", body)
    }

    /// Ek yükleme (ADIM 5): multipart "file" alanı. Dönen Attachment id'si
    /// mesaj gönderiminde attachment_ids ile verilir.
    func uploadAttachment(botID: UUID, fileURL: URL) async throws -> Attachment {
        var req = try istek("/api/bots/\(botID.uuidString)/attachments", "POST")
        let boundary = "oa-\(UUID().uuidString)"
        var gövde = Data()
        gövde.append(Data("--\(boundary)\r\n".utf8))
        gövde.append(Data("Content-Disposition: form-data; name=\"file\"; filename=\"\(fileURL.lastPathComponent)\"\r\n".utf8))
        gövde.append(Data("Content-Type: application/octet-stream\r\n\r\n".utf8))
        gövde.append(try Data(contentsOf: fileURL))
        gövde.append(Data("\r\n--\(boundary)--\r\n".utf8))

        req.setValue("multipart/form-data; boundary=\(boundary)", forHTTPHeaderField: "Content-Type")
        req.httpBody = gövde
        let (data, resp) = try await session.data(for: req)
        try check(resp, data)
        return try API.decoder.decode(Attachment.self, from: data)
    }

    /// Ek indirme (ADIM 5): içeriği geçici dosyaya yazar, yolunu döndürür.
    func downloadAttachment(id: UUID) async throws -> URL {
        let (data, resp) = try await session.data(for: try istek("/api/attachments/\(id.uuidString)/file"))
        try check(resp, data)
        let tmp = FileManager.default.temporaryDirectory
            .appendingPathComponent("oa-ek-\(id.uuidString)")
        try data.write(to: tmp)
        return tmp
    }

    /// Mesajı düzenle + yeniden üret (ADIM 9): eski sürüm görünmez olur,
    /// yeni dal taze oturumla cevaplanır.
    func editMessage(id: UUID, text: String, key: UUID) async throws -> SendReceipt {
        struct EditBody: Encodable {
            let text: String
            let origin: String
            let key: String
        }
        return try await send("/api/messages/\(id.uuidString)", method: "PATCH",
                              body: EditBody(text: text, origin: "mac", key: key.uuidString))
    }

    // ── koşular / onaylar ──────────────────────────────────────────────

    func runs(status: String) async throws -> [Run] {
        try await get("/api/runs?status=\(status)")
    }

    func botRuns(botID: UUID) async throws -> [Run] {
        try await get("/api/bots/\(botID.uuidString)/runs")
    }

    func run(id: UUID) async throws -> Run {
        try await get("/api/runs/\(id.uuidString)")
    }

    func resolve(runID: UUID, _ decision: ApprovalDecision) async throws {
        var req = try istek("/api/runs/\(runID.uuidString)/approval", "POST")
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try API.encoder.encode(decision)
        let (data, resp) = try await session.data(for: req)
        try check(resp, data)
    }

    func activity(limit: Int = 50) async throws -> [ActivityItem] {
        try await get("/api/activity?limit=\(limit)")
    }

    func tasks() async throws -> TaskBoard {
        try await get("/api/tasks")
    }

    func routines() async throws -> [Routine] {
        try await get("/api/routines")
    }

    func createRoutine(_ body: RoutineCreate) async throws -> Routine {
        try await send("/api/routines", method: "POST", body: body)
    }

    func setRoutine(id: UUID, enabled: Bool) async throws {
        _ = try await send("/api/routines/\(id.uuidString)", method: "PATCH",
                           body: ["enabled": enabled]) as Routine
    }

    // ── bot yönetimi ───────────────────────────────────────────────────

    func createBot(_ body: BotCreate) async throws -> Bot {
        try await send("/api/bots", method: "POST", body: body)
    }

    func updateBot(id: UUID, _ body: BotUpdate) async throws -> Bot {
        try await send("/api/bots/\(id.uuidString)", method: "PATCH", body: body)
    }

    func deleteBot(id: UUID) async throws {
        let req = try istek("/api/bots/\(id.uuidString)", "DELETE")
        let (data, resp) = try await session.data(for: req)
        try check(resp, data)
    }

    /// Sohbeti Temizle: konuşmanın mesajları + motor imleçleri sunucuda silinir.
    func clearMessages(botID: UUID) async throws {
        let req = try istek("/api/bots/\(botID.uuidString)/messages", "DELETE")
        let (data, resp) = try await session.data(for: req)
        try check(resp, data)
    }

    // ── politika / sayaçlar / konektörler ──────────────────────────────

    func policy() async throws -> PolicySettings {
        try await get("/api/policy")
    }

    func setPolicy(_ p: PolicySettings) async throws {
        var req = try istek("/api/policy", "PUT")
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try API.encoder.encode(p)
        let (data, resp) = try await session.data(for: req)
        try check(resp, data)
    }

    func stats(days: Int) async throws -> Stats {
        try await get("/api/stats?days=\(days)")
    }

    func connectors() async throws -> [Connector] {
        try await get("/api/connectors")
    }

    /// refresh: sunucu yoklamayı 5 dk önbellekler; kullanıcı "yenile"
    /// derse bekletmeden yeniden koşturulur.
    func engines(refresh: Bool = false) async throws -> [EngineInfo] {
        try await get("/api/engines" + (refresh ? "?refresh=1" : ""))
    }

    /// Sunucuda kurulu eklentiler + onları kullanan botlar. Eklentiler ekranı
    /// ve bot formundaki seçim listesi buradan beslenir.
    func extensions() async throws -> [ExtensionInfo] {
        try await get("/api/extensions")
    }

    /// Pi paket galerisi (npm `pi-package` etiketi). q boşsa popüler liste.
    func extensionCatalog(q: String) async throws -> [CatalogPackage] {
        let qs = q.isEmpty ? "" : "?q=" + (q.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? "")
        return try await get("/api/extensions/catalog" + qs)
    }

    /// Paket kurar. UZUN SÜRER (npm indirir): çağıran zaman aşımını buna göre
    /// tutmalı, yoksa kurulum sürerken istemci vazgeçmiş görünür.
    func installExtension(source: String) async throws -> [ExtensionInfo] {
        struct Body: Encodable { let source: String }
        var req = try istek("/api/extensions", "POST")
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try API.encoder.encode(Body(source: source))
        req.timeoutInterval = 360
        let (data, resp) = try await session.data(for: req)
        try check(resp, data)
        return try API.decoder.decode([ExtensionInfo].self, from: data)
    }

    /// Kurulu paketi kaldırır. Bir bot kullanıyorsa sunucu 409 döner ve kimin
    /// bozulacağını yazar — istemci bu cümleyi aynen gösterir.
    func removeExtension(id: String) async throws -> [ExtensionInfo] {
        let req = try istek("/api/extensions/\(yolBileseni(id))", "DELETE")
        let (data, resp) = try await session.data(for: req)
        try check(resp, data)
        return try API.decoder.decode([ExtensionInfo].self, from: data)
    }

    // ── hafıza: sabit talimatlar ───────────────────────────────────────

    /// Sabit talimatlar — sistem promptuna HER koşuda giren kurallar.
    /// Sunucu, orkestratörün prompta bastığı listenin aynısını döner: ekranda
    /// görünen şey modelin gördüğü şeydir.
    /// bot boşsa yalnız "tüm botlar" (user) kapsamı gelir.
    func standingInstructions(bot: String?) async throws -> [MemoryRecord] {
        var yol = "/api/memories/instructions"
        if let bot, !bot.isEmpty {
            yol += "?bot=" + (bot.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? "")
        }
        return try await get(yol)
    }

    /// Kullanıcının elle yazdığı kayıt. Sabit talimatı YALNIZ bu yol yazabilir
    /// — motorun `memory_save` aracı kind=instruction'ı reddeder.
    func createMemory(scope: String, botID: String?, kind: String, content: String) async throws -> MemoryRecord {
        struct Body: Encodable {
            let scope: String
            let botID: String
            let kind: String
            let content: String
            enum CodingKeys: String, CodingKey {
                case scope, kind, content
                case botID = "bot_id"
            }
        }
        var req = try istek("/api/memories", "POST")
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try API.encoder.encode(
            Body(scope: scope, botID: botID ?? "", kind: kind, content: content))
        let (data, resp) = try await session.data(for: req)
        try check(resp, data)
        return try API.decoder.decode(MemoryRecord.self, from: data)
    }

    func deleteMemory(id: String) async throws {
        let req = try istek("/api/memories/\(yolBileseni(id))", "DELETE")
        let (data, resp) = try await session.data(for: req)
        try check(resp, data)
    }

    // ── sağlayıcılar / model kataloğu ──────────────────────────────────

    func providers() async throws -> [ProviderInfo] {
        try await get("/api/providers")
    }

    /// Oluşturur ya da tümüyle günceller (PUT). Yanıt kaydın sunucudaki
    /// son hâlidir — ekran onu gösterir, kendi taslağını değil.
    func saveProvider(_ body: ProviderSave) async throws -> ProviderInfo {
        try await send("/api/providers/\(yolBileseni(body.id))", method: "PUT", body: body)
    }

    func deleteProvider(id: String) async throws {
        let req = try istek("/api/providers/\(yolBileseni(id))", "DELETE")
        let (data, resp) = try await session.data(for: req)
        try check(resp, data)
    }

    /// models.dev kataloğu — sunucu 24 saat önbellekler.
    func modelCatalog(refresh: Bool = false) async throws -> ModelCatalog {
        try await get("/api/models/catalog" + (refresh ? "?refresh=1" : ""))
    }

    /// Kimlik yola gömülüyor: sunucu dar bir alfabe dayatsa da kullanıcının
    /// yazdığı ham dize buraya kadar gelebiliyor — kaçırılmadan gönderilirse
    /// yol bambaşka bir uca çözülürdü.
    private func yolBileseni(_ s: String) -> String {
        s.addingPercentEncoding(withAllowedCharacters: .alphanumerics.union(.init(charactersIn: "-._~"))) ?? s
    }

    func bulkApprove() async throws -> BulkApproveResult {
        let req = try istek("/api/approvals/bulk", "POST")
        let (data, resp) = try await session.data(for: req)
        try check(resp, data)
        return try API.decoder.decode(BulkApproveResult.self, from: data)
    }

    /// Sağlık yoklaması. /healthz belirteç istemez (konteyner healthcheck'i
    /// de oradan geçer), ama belirteç yanlışsa "bağlı" göstermek yanıltıcı
    /// olurdu: bu yüzden ayrıca korumalı bir uç yoklanır ve 401 ayrı raporlanır.
    func health() async -> Saglik {
        var req = URLRequest(url: baseURL.appending(path: "/healthz"))
        req.timeoutInterval = 3
        guard let (_, resp) = try? await session.data(for: req),
              let http = resp as? HTTPURLResponse, http.statusCode == 200 else {
            return .erisilemiyor
        }
        guard var yetkili = try? istek("/api/bots") else { return .erisilemiyor }
        yetkili.timeoutInterval = 3
        guard let (_, resp2) = try? await session.data(for: yetkili),
              let http2 = resp2 as? HTTPURLResponse else { return .erisilemiyor }
        return http2.statusCode == 401 ? .belirtecGecersiz : .iyi
    }

    /// Bağlantı durumu — "kapalı" ile "belirteç yanlış" ayrı gösterilir;
    /// ikisi aynı kırmızı nokta olsaydı kullanıcı adresi kurcalayıp dururdu.
    enum Saglik: Sendable {
        case iyi
        case belirtecGecersiz
        case erisilemiyor
    }

    // ── SSE ────────────────────────────────────────────────────────────

    /// Koşu olay akışı. `stream.end` çerçevesi geldiğinde dizi biter;
    /// bağlantı kopmaları hata olarak fırlar (çağıran yeniden bağlanır).
    func events(runID: UUID, after: Int64 = 0) -> AsyncThrowingStream<StreamEvent, Error> {
        AsyncThrowingStream { continuation in
            let task = Task {
                var url = baseURL.appending(path: "/api/runs/\(runID.uuidString)/events")
                if after > 0 {
                    url.append(queryItems: [URLQueryItem(name: "after", value: String(after))])
                }
                var req = URLRequest(url: url)
                req.timeoutInterval = 3600
                req.setValue("text/event-stream", forHTTPHeaderField: "Accept")
                yetkilendir(&req)
                do {
                    let (bytes, resp) = try await session.bytes(for: req)
                    try check(resp, Data())
                    var id: Int64?
                    var kind = "message"
                    var dataLines: [String] = []
                    for try await line in bytes.lines {
                        if line.isEmpty { // çerçeve sonu
                            if !dataLines.isEmpty {
                                let ev = StreamEvent(id: id, kind: kind,
                                                     data: Data(dataLines.joined(separator: "\n").utf8))
                                continuation.yield(ev)
                                if kind == "stream.end" {
                                    continuation.finish()
                                    return
                                }
                            }
                            id = nil
                            kind = "message"
                            dataLines = []
                        } else if line.hasPrefix("id: ") {
                            id = Int64(line.dropFirst(4))
                        } else if line.hasPrefix("event: ") {
                            kind = String(line.dropFirst(7))
                        } else if line.hasPrefix("data: ") {
                            dataLines.append(String(line.dropFirst(6)))
                        } // ": ping" yorum satırları düşer
                    }
                    continuation.finish()
                } catch {
                    continuation.finish(throwing: error)
                }
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    // ── yardımcılar ────────────────────────────────────────────────────

    /// istek — TÜM isteklerin tek kurucusu; belirteç burada takılır.
    private func istek(_ path: String, _ method: String = "GET") throws -> URLRequest {
        guard let url = URL(string: path, relativeTo: baseURL) else {
            throw APIError(status: 0, message: "geçersiz yol: \(path)")
        }
        var req = URLRequest(url: url)
        req.httpMethod = method
        yetkilendir(&req)
        return req
    }

    /// Boş belirteçte başlık hiç konmaz: sunucu 401 döner ve kullanıcı
    /// "belirteç girilmemiş" mesajını görür — "Bearer " gönderip aynı
    /// 401'i almaktan farkı, hatanın nedeninin belli olması.
    private func yetkilendir(_ req: inout URLRequest) {
        guard !token.isEmpty else { return }
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
    }

    private func get<T: Decodable>(_ path: String) async throws -> T {
        let (data, resp) = try await session.data(for: try istek(path))
        try check(resp, data)
        return try API.decoder.decode(T.self, from: data)
    }

    /// Gövdeli istek (POST/PATCH) — kodlanabilir her tipi kabul eder.
    private func send<T: Decodable>(_ path: String, method: String,
                                    body: some Encodable) async throws -> T {
        var req = try istek(path, method)
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try API.encoder.encode(body)
        let (data, resp) = try await session.data(for: req)
        try check(resp, data)
        return try API.decoder.decode(T.self, from: data)
    }

    private func post<T: Decodable>(_ path: String, body: [String: String]) async throws -> T {
        try await postJSON(path, body)
    }

    /// JSON gövdesi her tipi kaldırır ([String: Any] — mesaj gönderimi karma
    /// gövde taşır: text/key ek olarak attachment_ids dizisi, ADIM 3+5).
    private func postJSON<T: Decodable, V>(_ path: String, _ body: [String: V]) async throws -> T {
        var req = try istek(path, "POST")
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONSerialization.data(withJSONObject: body)
        let (data, resp) = try await session.data(for: req)
        try check(resp, data)
        return try API.decoder.decode(T.self, from: data)
    }

    private func check(_ resp: URLResponse, _ data: Data) throws {
        guard let http = resp as? HTTPURLResponse else { return }
        guard (200 ..< 300).contains(http.statusCode) else {
            struct ErrBody: Decodable { let error: String }
            var msg = (try? API.decoder.decode(ErrBody.self, from: data))?.error
                ?? "HTTP \(http.statusCode)"
            if http.statusCode == 401 {
                msg = token.isEmpty
                    ? "API belirteci girilmemiş — Ayarlar › Bağlantılar"
                    : "API belirteci sunucununkiyle eşleşmiyor (\(msg))"
            }
            throw APIError(status: http.statusCode, message: msg)
        }
    }
}
