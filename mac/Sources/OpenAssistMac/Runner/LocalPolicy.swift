import Foundation

/// LocalPolicy — runner'ın YEREL politika tabanı (K güvenlik: sunucu yalnız
/// daraltabilir, genişletemez). Sunucudaki onay kapısı ne derse desin, kök
/// dizin allowlist'i dışına çıkan hiçbir yol burada çalışmaz; yazma sınıfı
/// işlemler ayrıca yerel onay diyaloğu ister.
struct LocalPolicy: Sendable {
    /// İzinli kök dizinler. Varsayılan: Belgeler, Masaüstü, İndirilenler.
    var allowedRoots: [URL]
    /// Yazma sınıfı (write_file, trash_item) yerel onay diyaloğu ister.
    var requireApprovalForWrites = true
    /// Tek okumada üst sınır — dev dosya modele akmasın.
    var maxReadBytes = 512 * 1024

    static var standard: LocalPolicy {
        let home = FileManager.default.homeDirectoryForCurrentUser
        return LocalPolicy(allowedRoots: [
            home.appending(path: "Documents"),
            home.appending(path: "Desktop"),
            home.appending(path: "Downloads"),
        ])
    }

    enum PolicyError: Error, LocalizedError, Equatable {
        case outsideRoots(String)
        case emptyPath

        var errorDescription: String? {
            switch self {
            case .outsideRoots(let p):
                return "yol izinli köklerin dışında: \(p) (izinli: Belgeler, Masaüstü, İndirilenler)"
            case .emptyPath:
                return "path boş olamaz"
            }
        }
    }

    /// Yolu mutlaklaştırır, sembolik bağları çözer ve allowlist'e vurur.
    /// Var olmayan dosya için (yeni yazma) ebeveyn zinciri çözülür — kök
    /// dışına symlink ile kaçış bu adımda yakalanır.
    func resolve(_ path: String) throws -> URL {
        let trimmed = path.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { throw PolicyError.emptyPath }
        let expanded = (trimmed as NSString).expandingTildeInPath
        let url = Self.canonicalize(URL(fileURLWithPath: expanded))
        guard isInsideRoots(url) else { throw PolicyError.outsideRoots(trimmed) }
        return url
    }

    /// canonicalize — `resolvingSymlinksInPath` yol MEVCUT DEĞİLSE aradaki
    /// symlink'leri çözmez (canlı doğrulandı); yeni dosya yazımında bu, kök
    /// dışına symlink kaçışı demek olur. Bu yüzden var olan en derin ataya
    /// inilir, o çözülür, var olmayan kuyruk geri eklenir.
    static func canonicalize(_ url: URL) -> URL {
        let fm = FileManager.default
        var existing = url.standardizedFileURL
        var tail: [String] = []
        while existing.pathComponents.count > 1,
              !fm.fileExists(atPath: existing.path) {
            tail.append(existing.lastPathComponent)
            existing = existing.deletingLastPathComponent()
        }
        var resolved = existing.resolvingSymlinksInPath()
        for comp in tail.reversed() {
            resolved.append(path: comp)
        }
        return resolved.standardizedFileURL
    }

    private func isInsideRoots(_ url: URL) -> Bool {
        let target = url.pathComponents
        for root in allowedRoots {
            let base = Self.canonicalize(root).pathComponents
            if target.count >= base.count, Array(target.prefix(base.count)) == base {
                return true
            }
        }
        return false
    }
}
