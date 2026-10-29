import SwiftUI

/// Sol kenar çubuğu — 272px, sidebarBg.
struct SidebarView: View {
    @Bindable var store: DesignStore
    @Environment(\.theme) private var th

    var body: some View {
        VStack(spacing: 0) {
            header
            searchField
            navRows
            agentList
            footer
        }
        .frame(width: 272)
        .frame(maxHeight: .infinity)
        .background(th.sidebarBg)
        .overlay(alignment: .trailing) { th.border.frame(width: 1) }
    }

    private var header: some View {
        HStack(spacing: 8) {
            // Gerçek pencere trafik ışıkları bu boş şeridin üstüne düşer;
            // şerit başlık çubuğu gibi davranır (sürükle / çift tıkla büyüt).
            WindowDragArea()
            IconButton(icon: "menubar.rectangle", size: 28, iconSize: 14, help: "Menü çubuğu görünümü") {
                store.menubarOpen = true
            }
            IconButton(icon: "magnifyingglass", size: 28, iconSize: 14, help: "Komut paleti (⌘K)") {
                store.openPalette()
            }
            IconButton(icon: "plus", size: 28, iconSize: 14, help: "Yeni bot") {
                store.openNewBot()
            }
        }
        .padding(.horizontal, 16)
        .frame(height: 52)
    }

    private var searchField: some View {
        HStack(spacing: 7) {
            Image(systemName: "magnifyingglass")
                .font(.system(size: 12, weight: .medium))
                .foregroundStyle(th.text3)
            DTextField(placeholder: "Ara", text: $store.query, size: 13)
        }
        .padding(.horizontal, 10)
        .frame(height: 30)
        .background(RoundedRectangle(cornerRadius: 8).fill(th.inputBg))
        .padding(EdgeInsets(top: 2, leading: 12, bottom: 12, trailing: 12))
    }

    private var navRows: some View {
        VStack(spacing: 1) {
            navRow(icon: "tray", label: "Onay Merkezi",
                   badge: store.pendingCount > 0 ? "\(store.pendingCount)" : nil,
                   active: store.screen == .inbox) { store.screen = .inbox }
            navRow(icon: "waveform.path.ecg", label: "Aktivite",
                   badge: nil, active: store.screen == .activity) { store.screen = .activity }
        }
        .padding(EdgeInsets(top: 0, leading: 8, bottom: 8, trailing: 8))
    }

    private func navRow(icon: String, label: String, badge: String?, active: Bool,
                        action: @escaping () -> Void) -> some View {
        HStack(spacing: 10) {
            Image(systemName: icon)
                .font(.system(size: 14, weight: .medium))
                .foregroundStyle(th.text2)
                .frame(width: 16)
            Text(label)
                .font(.system(size: 13.5))
                .foregroundStyle(th.text)
            Spacer()
            if let badge {
                Text(badge)
                    .font(.system(size: 11, weight: .bold))
                    .foregroundStyle(.white)
                    .padding(EdgeInsets(top: 1, leading: 7, bottom: 1, trailing: 7))
                    .background(Capsule().fill(Theme.primary))
            }
        }
        .padding(EdgeInsets(top: 6, leading: 10, bottom: 6, trailing: 10))
        .hoverBg(th.hover, active: th.active, isActive: active, radius: 9)
        .tap(action)
    }

    private var agentList: some View {
        ScrollView {
            VStack(spacing: 2) {
                ForEach(store.sidebarRows) { row in
                    agentRow(row.agent, tur: row.tur)
                }
                if store.searchEmpty {
                    Text("Sonuç bulunamadı")
                        .font(.system(size: 13))
                        .foregroundStyle(th.text3)
                        .padding(.vertical, 24)
                }
                if store.noBots {
                    Text("Henüz bot yok")
                        .font(.system(size: 13))
                        .foregroundStyle(th.text3)
                        .padding(.vertical, 24)
                        .padding(.horizontal, 12)
                }
            }
            .padding(.horizontal, 8)
        }
        .frame(maxHeight: .infinity)
    }

    /// Bir satır: ekip (yığın avatar), ekip üyesi (girintili, küçük avatar)
    /// ya da ekipsiz bot. Üye satırı ekibin ALTINDA yaşadığı için daha küçük
    /// çizilir — kenar çubuğu "3E Satış → içindekiler" olarak okunmalı.
    private func agentRow(_ a: DAgent, tur: SidebarRow.Tur) -> some View {
        let uye = tur == .uye
        let avatar: CGFloat = uye ? 28 : 40
        return HStack(spacing: 10) {
            if tur == .ekip {
                TeamAvatarView(colors: teamColors(a), layout: TeamAvatarView.stack40, size: avatar)
            } else {
                BotIconView(color: Color(hex: a.c2), size: avatar)
            }
            VStack(spacing: 0) {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Text(a.name)
                        .font(.system(size: uye ? 13 : 14, weight: .semibold))
                        .foregroundStyle(th.text)
                        .lineLimit(1)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    Text(a.time)
                        .font(.system(size: 11.5))
                        .foregroundStyle(th.text3)
                }
                HStack(spacing: 6) {
                    Text(a.preview)
                        .font(.system(size: 12.5))
                        .foregroundStyle(th.text2)
                        .lineLimit(1)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    if store.isUnread(a) {
                        Circle().fill(Theme.primary).frame(width: 8, height: 8)
                    }
                }
            }
        }
        .padding(EdgeInsets(top: uye ? 6 : 9, leading: 10, bottom: uye ? 6 : 9, trailing: 10))
        .hoverBg(th.hover, active: th.active,
                 isActive: store.active == a.id && store.screen == .chat, radius: 10)
        .tap { store.pick(a.id) }
        // Girinti + ince dikey çizgi: üyenin ekibe bağlı olduğu tek bakışta
        // görünsün (ayrı bir başlık satırı eklemeden).
        .padding(.leading, uye ? 14 : 0)
        .overlay(alignment: .leading) {
            if uye { th.border.frame(width: 1).padding(.leading, 5) }
        }
    }

    /// Ekip avatarının renkleri — üyelerinden. Üyesiz ekipte satır boş
    /// kalmasın diye ekibin kendi rengine düşer.
    private func teamColors(_ ekip: DAgent) -> [Color] {
        let uyeler = (ekip.team ?? []).prefix(3)
            .map { Color(hex: store.agent($0)?.c2 ?? "#8e8e93") }
        return uyeler.isEmpty ? [Color(hex: ekip.c2)] : uyeler
    }

    private var footer: some View {
        VStack(spacing: 0) {
            HStack(spacing: 10) {
                Image(systemName: "powerplug")
                    .font(.system(size: 15, weight: .medium))
                    .frame(width: 17)
                Text("Eklentiler")
                    .font(.system(size: 14))
                Spacer()
            }
            .foregroundStyle(th.text)
            .padding(EdgeInsets(top: 8, leading: 10, bottom: 8, trailing: 10))
            .hoverBg(th.hover, radius: 10)
            .tap { store.screen = .plugins }

            HStack(spacing: 10) {
                Text(store.userInitials)
                    .font(.system(size: 11, weight: .bold))
                    .foregroundStyle(.white)
                    .frame(width: 28, height: 28)
                    .background(Circle().fill(gradient145(Color(hex: "#9bb0c9"), Color(hex: "#63788f"))))
                Text(store.userName)
                    .font(.system(size: 14, weight: .medium))
                    .foregroundStyle(th.text)
                Spacer()
                IconButton(icon: "gearshape", size: 28, iconSize: 13, help: "Ayarlar") {
                    store.screen = .settings
                }
                IconButton(icon: store.dark ? "sun.max" : "moon", size: 28, iconSize: 13, help: "Tema değiştir") {
                    store.toggleTheme()
                }
            }
            .padding(EdgeInsets(top: 8, leading: 10, bottom: 8, trailing: 10))
        }
        .padding(EdgeInsets(top: 8, leading: 12, bottom: 12, trailing: 12))
        .overlay(alignment: .top) { th.border.frame(height: 1) }
    }
}
