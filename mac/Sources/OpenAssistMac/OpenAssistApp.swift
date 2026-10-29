import SwiftUI

/// Çıplak SPM executable (.app paketi yok) macOS'ta arka plan süreci sayılır:
/// Dock ikonu ve Cmd-Tab görünmez. Politika açılışta "regular"a çekilir —
/// paketli/notarize edilmiş sürümde de zararsız (zaten regular).
final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.regular)
        NSApp.activate(ignoringOtherApps: true)
    }
}

@main
struct OpenAssistApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    @State private var store = DesignStore()
    // isInserted için kararlı binding — store'u App gövdesinde okumak
    // (inline Binding ya da Bindable) sonsuz sahne-güncelleme döngüsü yaratıyor.
    @AppStorage("menubarExtra") private var menubarExtra = true

    /// Menü çubuğu simgesi — bot glyph'i template imaj olarak (açık/koyu menü çubuğuna uyar).
    private static let menuBarIcon: NSImage = {
        let img = NSImage(size: NSSize(width: 18, height: 18), flipped: false) { rect in
            NSColor.black.setFill()
            NSBezierPath(cgPath: BotShape().path(in: rect).cgPath).fill()
            return true
        }
        img.isTemplate = true
        return img
    }()

    var body: some Scene {
        WindowGroup(id: "main") {
            RootShell(store: store)
                .frame(minWidth: 1280, minHeight: 780)
                // hiddenTitleBar başlık bölgesini güvenli alan olarak bırakıyor;
                // tasarımda içerik pencerenin tepesinden başlar, trafik ışıkları
                // kenar çubuğu başlığının üstüne biner.
                .ignoresSafeArea()
        }
        .windowStyle(.hiddenTitleBar)
        .defaultSize(width: 1280, height: 780)
        .commands { AppCommands(store: store) }

        MenuBarExtra(isInserted: $menubarExtra) {
            MenuBarPanelView(store: store)
        } label: {
            Image(nsImage: Self.menuBarIcon)
        }
        .menuBarExtraStyle(.window)
    }
}

/// Ana menü kısayolları — ⌘N yeni bot, ⌘K palet, ⌘O pencere, ⌘, ayarlar.
struct AppCommands: Commands {
    var store: DesignStore
    @Environment(\.openWindow) private var openWindow

    var body: some Commands {
        CommandGroup(replacing: .appSettings) {
            Button("Ayarlar…") {
                openWindow(id: "main")
                store.screen = .settings
            }
            .keyboardShortcut(",")
        }
        CommandGroup(replacing: .newItem) {
            Button("Yeni Bot") {
                openWindow(id: "main")
                store.resetNB()
                store.screen = .newbot
            }
            .keyboardShortcut("n")
        }
        CommandGroup(after: .windowArrangement) {
            Button("Hızlı Komut") { store.togglePalette() }
                .keyboardShortcut("k")
            Button("Pencereyi Aç") { openWindow(id: "main") }
                .keyboardShortcut("o")
        }
    }
}
