import SwiftUI

/// Ana pencere kabuğu — kenar çubuğu + ekran + sağ panel + palet/toast katmanları.
struct RootShell: View {
    @Bindable var store: DesignStore

    var body: some View {
        let th = store.theme
        ZStack {
            HStack(spacing: 0) {
                if store.showSidebar {
                    SidebarView(store: store)
                }
                screenView
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                if store.rightVisible {
                    RightPanel(store: store)
                }
            }
            if store.paletteOpen {
                CommandPalette(store: store)
            }
        }
        .overlay(alignment: .topTrailing) {
            if let t = store.toast {
                ToastView(toast: t)
                    .padding(.top, 24)
                    .padding(.trailing, 28)
                    .transition(.offset(y: -12).combined(with: .opacity))
            }
        }
        .animation(.easeOut(duration: 0.25), value: store.toast)
        .background(th.winBg)
        .environment(\.theme, th)
        .preferredColorScheme(store.dark ? .dark : .light)
        .task { store.start() }
        .onChange(of: store.allAgents.count) { store.syncActive() }
    }

    @ViewBuilder
    private var screenView: some View {
        switch store.screen {
        case .chat: ChatScreen(store: store)
        case .inbox: InboxScreen(store: store)
        case .activity: ActivityScreen(store: store)
        case .settings: SettingsScreen(store: store)
        case .plugins: PluginsScreen(store: store)
        case .newbot: NewBotScreen(store: store)
        }
    }
}
