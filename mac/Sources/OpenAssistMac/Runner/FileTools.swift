import Foundation

/// FileTools — v1 runner araç seti (Faz 6): oku / listele / yaz / çöpe taşı.
/// Kalıcı silme YOKTUR — trash_item `FileManager.trashItem` kullanır, işlem
/// Finder çöpünden geri alınabilir.
enum FileTools {
    struct ToolError: Error, LocalizedError {
        let message: String
        var errorDescription: String? { message }
    }

    static func readFile(at url: URL, maxBytes: Int) throws -> String {
        let data = try Data(contentsOf: url)
        guard data.count <= maxBytes else {
            throw ToolError(message: "dosya çok büyük (\(data.count) bayt, sınır \(maxBytes)) — daha küçük bir aralık iste")
        }
        guard let text = String(data: data, encoding: .utf8) else {
            throw ToolError(message: "dosya UTF-8 metin değil: \(url.path)")
        }
        return text
    }

    static func listDir(at url: URL) throws -> String {
        let fm = FileManager.default
        var isDir: ObjCBool = false
        guard fm.fileExists(atPath: url.path, isDirectory: &isDir), isDir.boolValue else {
            throw ToolError(message: "dizin yok: \(url.path)")
        }
        let entries = try fm.contentsOfDirectory(at: url, includingPropertiesForKeys: [.isDirectoryKey])
        let lines = entries
            .map { entry -> String in
                let isSub = (try? entry.resourceValues(forKeys: [.isDirectoryKey]).isDirectory) ?? false
                return entry.lastPathComponent + (isSub ? "/" : "")
            }
            .sorted()
        return lines.isEmpty ? "(boş dizin)" : lines.joined(separator: "\n")
    }

    static func writeFile(at url: URL, content: String) throws -> String {
        let parent = url.deletingLastPathComponent()
        var isDir: ObjCBool = false
        guard FileManager.default.fileExists(atPath: parent.path, isDirectory: &isDir), isDir.boolValue else {
            throw ToolError(message: "üst dizin yok: \(parent.path)")
        }
        try content.write(to: url, atomically: true, encoding: .utf8)
        return "yazıldı: \(url.path) (\(content.utf8.count) bayt)"
    }

    static func trashItem(at url: URL) throws -> String {
        guard FileManager.default.fileExists(atPath: url.path) else {
            throw ToolError(message: "dosya yok: \(url.path)")
        }
        var trashed: NSURL?
        try FileManager.default.trashItem(at: url, resultingItemURL: &trashed)
        let dest = trashed.map { " → \($0.path ?? "Çöp Sepeti")" } ?? ""
        return "çöpe taşındı: \(url.path)\(dest) (Finder'dan geri alınabilir)"
    }
}
