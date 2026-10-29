import Foundation
import Security

/// Keychain — API belirtecinin saklandığı yer.
///
/// UserDefaults DEĞİL: bu belirteç sunucunun tamamına yetki verir (bot
/// yönetimi, onay kararları, runner WS). UserDefaults düz bir plist'tir ve
/// kullanıcı olarak koşan her süreç okuyabilir; keychain kaydı en azından
/// uygulama sınırında durur ve yedeklere düz metin sızmaz.
enum Keychain {
    /// Servis adı: aynı Mac'te birden çok kurulum olabilir, hesap alanı
    /// sunucu adresidir — her sunucunun kendi belirteci.
    private static let service = "com.esmworks.OpenAssist.apiToken"

    static func token(for server: String) -> String {
        var sorgu = temel(server)
        sorgu[kSecReturnData as String] = true
        sorgu[kSecMatchLimit as String] = kSecMatchLimitOne
        var sonuc: CFTypeRef?
        guard SecItemCopyMatching(sorgu as CFDictionary, &sonuc) == errSecSuccess,
              let data = sonuc as? Data else { return "" }
        return String(decoding: data, as: UTF8.self)
    }

    static func setToken(_ token: String, for server: String) {
        let sorgu = temel(server)
        // Boş değer = kaydı sil; "" saklamak "belirteç var ama boş" gibi
        // görünürdü.
        guard !token.isEmpty else {
            SecItemDelete(sorgu as CFDictionary)
            return
        }
        let data = Data(token.utf8)
        let guncelle = [kSecValueData as String: data] as CFDictionary
        if SecItemUpdate(sorgu as CFDictionary, guncelle) == errSecSuccess { return }
        var ekle = sorgu
        ekle[kSecValueData as String] = data
        // Kilitli keychain'de arka plandan okunamasın diye "unlocked":
        // uygulama zaten kullanıcı oturumundayken koşar.
        ekle[kSecAttrAccessible as String] = kSecAttrAccessibleWhenUnlocked
        SecItemAdd(ekle as CFDictionary, nil)
    }

    private static func temel(_ server: String) -> [String: Any] {
        [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: server,
        ]
    }
}
