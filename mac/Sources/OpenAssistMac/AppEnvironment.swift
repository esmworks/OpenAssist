import Foundation

/// Uygulamanın kendi koşma biçimi hakkında bildikleri.
///
/// Paketli mi (.app) yoksa çıplak executable mı — bu ayrım kozmetik değil:
/// bildirimler ve girişte-başlat paket kimliği ister. Paketsizken bunları
/// "açık" göstermek yalan olur; arayüz nedenini yazar (bkz. Scripts/make-app.sh).
enum AppEnvironment {
    /// Paket kimliği varsa .app içindeyiz.
    static var isBundled: Bool { Bundle.main.bundleIdentifier != nil }

    /// "1.2.3" — paketsizken "geliştirme".
    static var version: String {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String
            ?? "geliştirme"
    }

    static var build: String {
        Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? "—"
    }

    /// Hakkında ekranındaki sürüm satırı — sabit yazılmaz, pakete sorulur.
    static var versionLine: String {
        isBundled ? "Sürüm \(version) (Derleme \(build))" : "Geliştirme derlemesi (paketsiz)"
    }

    /// Paket gerektiren yetenekler kapalıysa gösterilecek gerekçe.
    static let unbundledNote =
        "Uygulama .app paketi olarak çalışmıyor — `Scripts/make-app.sh` ile paketle."
}
