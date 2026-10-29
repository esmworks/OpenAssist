import Foundation
import ServiceManagement

/// "Başlangıçta çalıştır" — macOS giriş öğesi (SMAppService).
///
/// Durum bellekte tutulmaz, SİSTEME sorulur: kullanıcı Sistem Ayarları'ndan
/// kapatırsa anahtar da kapalı görünsün. Paket kimliği gerektiği için
/// paketsiz koşuda kaydolamaz — o durumda anahtar geri döner ve neden
/// söylenir (sessizce "açık" görünmesi yalan olurdu).
enum LoginItem {
    static var available: Bool { AppEnvironment.isBundled }

    static var enabled: Bool {
        guard available else { return false }
        return SMAppService.mainApp.status == .enabled
    }

    /// Dönüş: nil = başarılı, doluysa kullanıcıya gösterilecek gerekçe.
    @discardableResult
    static func set(_ on: Bool) -> String? {
        guard available else { return AppEnvironment.unbundledNote }
        do {
            if on {
                try SMAppService.mainApp.register()
            } else {
                try SMAppService.mainApp.unregister()
            }
            return nil
        } catch {
            return error.localizedDescription
        }
    }

    /// Kullanıcı sistemden onay bekliyorsa (macOS "izin ver" akışı).
    static var needsApproval: Bool {
        available && SMAppService.mainApp.status == .requiresApproval
    }
}
