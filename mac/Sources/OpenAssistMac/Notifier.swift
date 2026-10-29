import Foundation
import UserNotifications

/// Sistem bildirimleri — Ayarlar › Bildirimler anahtarlarının karşılığı.
///
/// Üç tetik var: koşu bitti (bot yanıtı), onay bekliyor, rutin bitti.
/// Tetikleyen LiveStore'dur (sunucu turu), çünkü Telegram'dan ya da
/// rutinden doğan koşular uygulamanın akışından geçmez — yalnız sunucu
/// durumundan görülür.
///
/// Paketsiz koşuda `UNUserNotificationCenter.current()` istisna atar; bu
/// yüzden her yol `available` ile korunur ve arayüz nedeni yazar.
@MainActor
@Observable
final class Notifier {
    /// Ayarlar'dan senkronlanır.
    var replies = true
    var approvals = true
    var routines = false
    var sound = true

    /// Sistem izni: nil = henüz sorulmadı.
    private(set) var authorized: Bool?
    private(set) var lastError: String?

    var available: Bool { AppEnvironment.isBundled }

    /// Kullanıcıya gösterilecek tek satır durum.
    var statusLine: String {
        guard available else { return AppEnvironment.unbundledNote }
        switch authorized {
        case true: return "Sistem bildirimleri açık"
        case false: return lastError ?? "macOS bildirim izni verilmedi — Sistem Ayarları › Bildirimler"
        default: return "İzin henüz sorulmadı"
        }
    }

    private var center: UNUserNotificationCenter? {
        available ? .current() : nil
    }

    /// Açılışta ve anahtar açıldığında çağrılır. İzin bir kez sorulur;
    /// reddedilirse tekrar sorulmaz (macOS zaten göstermez).
    func ensureAuthorization() async {
        guard let center, authorized == nil else { return }
        do {
            authorized = try await center.requestAuthorization(options: [.alert, .sound])
        } catch {
            authorized = false
            lastError = error.localizedDescription
        }
    }

    /// Hangi anahtar hangi olayı yönetir — karar mantığı testlenebilir
    /// olsun diye ayrı: rutin koşusu "bot yanıtları"na takılmaz.
    func shouldNotify(routine: Bool) -> Bool { routine ? routines : replies }
    var shouldNotifyApproval: Bool { approvals }

    /// Koşu onay bekliyor (K17 kartı düştü).
    func approvalPending(bot: String, what: String) {
        guard shouldNotifyApproval else { return }
        post(title: "\(bot) onay bekliyor", body: what)
    }

    /// Koşu bitti. `routine` ise "rutin" anahtarı, değilse "bot yanıtları".
    func runFinished(bot: String, ok: Bool, text: String, routine: Bool) {
        guard shouldNotify(routine: routine) else { return }
        post(title: routine ? "\(bot) · rutin" : bot,
             body: ok ? text : "Koşu başarısız — \(text)")
    }

    private func post(title: String, body: String) {
        guard let center else { return }
        let c = UNMutableNotificationContent()
        c.title = title
        c.body = String(body.prefix(180))
        if sound { c.sound = .default }
        // trigger nil = hemen göster.
        center.add(UNNotificationRequest(identifier: UUID().uuidString, content: c, trigger: nil))
    }
}
