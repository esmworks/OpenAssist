import AppKit
import Foundation
import Observation

// ── tel protokolü (Go internal/runner ile birebir) ─────────────────────

struct ToolDecl: Codable, Sendable {
    let name: String
    let description: String
    let inputSchema: JSONValue
    let cls: String // read | write | external | execute

    enum CodingKeys: String, CodingKey {
        case name, description
        case inputSchema = "input_schema"
        case cls = "class"
    }
}

struct HelloFrame: Encodable, Sendable {
    var type = "hello"
    let name: String
    let version: String
    let tools: [ToolDecl]
}

struct CallFrame: Decodable, Sendable {
    let type: String
    let id: String
    let tool: String
    let args: JSONValue?
}

struct ResultFrame: Codable, Sendable {
    var type = "result"
    let id: String
    let ok: Bool
    var text: String?
    var error: String?
}

/// v1 araç seti ilanı. Şemalar bizim kontratımız: additionalProperties false,
/// motor argümanları buna göre üretir. Sınıflar Go tarafındaki
/// runner.StaticClasses ile aynı olmalıdır.
enum MacTools {
    static func pathSchema(extra: [String: JSONValue] = [:]) -> JSONValue {
        var props: [String: JSONValue] = [
            "path": .object(["type": .string("string"), "description": .string("mutlak yol ya da ~/ ile başlayan yol")]),
        ]
        for (k, v) in extra { props[k] = v }
        return .object([
            "type": .string("object"),
            "properties": .object(props),
            "required": .array([.string("path")] + (extra.isEmpty ? [] : extra.keys.sorted().map { .string($0) })),
            "additionalProperties": .bool(false),
        ])
    }

    static let declarations: [ToolDecl] = [
        ToolDecl(name: "read_file",
                 description: "Mac'teki bir metin dosyasını okur (Belgeler/Masaüstü/İndirilenler)",
                 inputSchema: pathSchema(), cls: "read"),
        ToolDecl(name: "list_dir",
                 description: "Mac'teki bir dizinin içeriğini listeler",
                 inputSchema: pathSchema(), cls: "read"),
        ToolDecl(name: "write_file",
                 description: "Mac'te metin dosyası yazar (yerel onay diyaloğu açılır)",
                 inputSchema: pathSchema(extra: [
                     "content": .object(["type": .string("string"), "description": .string("dosya içeriği (UTF-8)")]),
                 ]),
                 cls: "write"),
        ToolDecl(name: "trash_item",
                 description: "Dosya ya da klasörü Çöp Sepeti'ne taşır (kalıcı silme yok, geri alınabilir)",
                 inputSchema: pathSchema(), cls: "write"),
    ]
}

// ── servis ─────────────────────────────────────────────────────────────

/// Canlı tutma periyodu — sunucudaki `pingAralik` ile aynı
/// (internal/runner/hub.go). Boşta duran bağlantıda hiç çerçeve akmaz ve
/// aradaki ters vekil (Cloudflare) WebSocket'i ~100 saniyede kapatır.
private let pingAraligi: Duration = .seconds(30)

/// RunnerService — core'a outbound WebSocket (K7: Mac'te port açılmaz).
/// Bağlantı koptukça artan aralıkla yeniden dener. Araç çağrıları sıralı
/// işlenir (tek koşunun çağrıları zaten sıralıdır); yazma sınıfı işlemler
/// yerel onay diyaloğundan geçer — sunucu onayı yerel politikayı GENİŞLETEMEZ.
@MainActor @Observable
final class RunnerService {
    private(set) var connected = false
    private(set) var lastError: String?
    var policy = LocalPolicy.standard

    private var loop: Task<Void, Never>?

    func start(baseURL: URL, token: String) {
        stop()
        loop = Task { [weak self] in
            var backoff: Duration = .seconds(1)
            while !Task.isCancelled {
                guard let self else { return }
                do {
                    try await self.serve(baseURL: baseURL, token: token)
                    backoff = .seconds(1) // temiz kapanış: hızlı yeniden dene
                } catch {
                    self.lastError = error.localizedDescription
                }
                self.connected = false
                try? await Task.sleep(for: backoff)
                backoff = min(backoff * 2, .seconds(30))
            }
        }
    }

    func stop() {
        loop?.cancel()
        loop = nil
        connected = false
    }

    private func wsURL(from base: URL) -> URL? {
        guard var comps = URLComponents(url: base, resolvingAgainstBaseURL: false) else {
            return nil
        }
        comps.scheme = (comps.scheme == "https") ? "wss" : "ws"
        comps.path = "/api/runner/ws"
        return comps.url
    }

    private func serve(baseURL: URL, token: String) async throws {
        guard let url = wsURL(from: baseURL) else {
            throw FileTools.ToolError(message: "geçersiz sunucu adresi")
        }
        // /api/runner/ws de özel API'dedir, yani bearer ister. URLSession'ın
        // WebSocket görevi başlık taşıyabildiği için belirteci sorgu
        // parametresine koymuyoruz — URL'ler erişim loglarına düşer.
        var req = URLRequest(url: url)
        if !token.isEmpty {
            req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }
        let ws = URLSession.shared.webSocketTask(with: req)
        ws.resume()
        defer { ws.cancel(with: .normalClosure, reason: nil) }

        let hello = HelloFrame(name: Host.current().localizedName ?? "Mac",
                               version: appVersion,
                               tools: MacTools.declarations)
        let helloData = try API.encoder.encode(hello)
        try await ws.send(.string(String(decoding: helloData, as: UTF8.self)))
        connected = true
        lastError = nil

        // Sunucu da ping atıyor; buradaki ping sunucu ya da ağ sessizce
        // ölürse receive()'in sonsuza kadar asılı kalmasını engeller —
        // görevi iptal edince aşağıdaki receive() hata atar ve dış döngü
        // yeniden bağlanır. (defer'ler ters sırada koşar: önce bu iptal
        // edilir, sonra ws kapanır.)
        let pinger = Task {
            while !Task.isCancelled {
                try? await Task.sleep(for: pingAraligi)
                if Task.isCancelled { return }
                // Geri çağrı sürümü bilinçli: async sarmalayıcı, pong hiç
                // gelmezse (bağlantı sessizce ölürse) continuation'ı sonsuza
                // kadar askıda bırakır ve görev iptali de onu uyandıramaz.
                ws.sendPing { error in
                    if error != nil {
                        ws.cancel(with: .goingAway, reason: nil)
                    }
                }
            }
        }
        defer { pinger.cancel() }

        while !Task.isCancelled {
            let msg = try await ws.receive()
            guard case .string(let text) = msg else { continue }
            guard let call = try? API.decoder.decode(CallFrame.self, from: Data(text.utf8)),
                  call.type == "call"
            else {
                continue // bilinmeyen çerçeve: yok say (yabancı kontrat toleransı)
            }
            let result = await execute(call)
            let data = try API.encoder.encode(result)
            try await ws.send(.string(String(decoding: data, as: UTF8.self)))
        }
    }

    private var appVersion: String {
        Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "0.1.0"
    }

    // ── araç yürütme ───────────────────────────────────────────────────

    private func execute(_ call: CallFrame) async -> ResultFrame {
        do {
            let text = try await run(call)
            return ResultFrame(id: call.id, ok: true, text: text)
        } catch {
            return ResultFrame(id: call.id, ok: false, error: error.localizedDescription)
        }
    }

    private func run(_ call: CallFrame) async throws -> String {
        let args = call.args?.objectValue ?? [:]
        guard let path = args["path"]?.stringValue else {
            throw FileTools.ToolError(message: "path argümanı gerekli")
        }
        let url = try policy.resolve(path)

        switch call.tool {
        case "read_file":
            return try FileTools.readFile(at: url, maxBytes: policy.maxReadBytes)
        case "list_dir":
            return try FileTools.listDir(at: url)
        case "write_file":
            guard let content = args["content"]?.stringValue else {
                throw FileTools.ToolError(message: "content argümanı gerekli")
            }
            try await approveLocally(action: "Dosya yaz", detail: "\(url.path)\n(\(content.utf8.count) bayt)")
            return try FileTools.writeFile(at: url, content: content)
        case "trash_item":
            try await approveLocally(action: "Çöp Sepeti'ne taşı", detail: url.path)
            return try FileTools.trashItem(at: url)
        default:
            throw FileTools.ToolError(message: "bilinmeyen araç: \(call.tool)")
        }
    }

    /// Yerel onay diyaloğu (K güvenlik): yazma sınıfı işlem, sunucu onayından
    /// BAĞIMSIZ olarak Mac'te de sorulur. Reddedilirse araç hatası modele döner.
    private func approveLocally(action: String, detail: String) async throws {
        guard policy.requireApprovalForWrites else { return }
        let alert = NSAlert()
        alert.messageText = "OpenAssist: \(action)"
        alert.informativeText = "Bot şu işlemi istiyor:\n\n\(detail)"
        alert.addButton(withTitle: "İzin Ver")
        alert.addButton(withTitle: "Reddet")
        alert.alertStyle = .warning
        NSApp.activate(ignoringOtherApps: true)
        guard alert.runModal() == .alertFirstButtonReturn else {
            throw FileTools.ToolError(message: "kullanıcı yerel onayı reddetti: \(action)")
        }
    }
}
