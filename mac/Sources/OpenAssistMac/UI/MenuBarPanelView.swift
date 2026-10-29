import SwiftUI

/// Menü çubuğu paneli — 288px: durum, etkin görevler, hızlı eylemler.
struct MenuBarPanelView: View {
    @Bindable var store: DesignStore
    @Environment(\.theme) private var th
    @Environment(\.openWindow) private var openWindow
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 8) {
                Text("OpenAssist")
                    .font(.system(size: 12.5, weight: .semibold))
                    .foregroundStyle(th.text)
                    .frame(maxWidth: .infinity, alignment: .leading)
                Circle()
                    .fill(Color(hex: store.mbDotHex))
                    .frame(width: 6, height: 6)
                Text(store.mbState)
                    .font(.system(size: 11.5))
                    .foregroundStyle(th.text2)
            }
            .padding(EdgeInsets(top: 6, leading: 13, bottom: 8, trailing: 13))
            sep(bottom: 5)
            Text("ETKİN GÖREVLER")
                .font(.system(size: 10.5, weight: .semibold))
                .kerning(0.5)
                .foregroundStyle(th.text3)
                .padding(EdgeInsets(top: 2, leading: 13, bottom: 5, trailing: 13))
            VStack(alignment: .leading, spacing: 8) {
                ForEach(store.mbTasks) { t in
                    taskRow(t)
                }
                if store.mbTasks.isEmpty {
                    Text("Çalışan görev yok")
                        .font(.system(size: 12.5))
                        .foregroundStyle(th.text3)
                        .padding(EdgeInsets(top: 2, leading: 0, bottom: 4, trailing: 0))
                }
            }
            .padding(EdgeInsets(top: 0, leading: 13, bottom: 7, trailing: 13))
            sep(bottom: 4)
            VStack(spacing: 0) {
                MBRow(label: "Onay Merkezi", badge: store.pendingCount > 0 ? store.pendingCount : nil) {
                    openMain { $0.screen = .inbox }
                }
                MBRow(label: "Pencereyi aç", right: "⌘O") {
                    openMain { _ in }
                }
                MBRow(label: "Hızlı komut", right: "⌘K") {
                    openMain { $0.openPalette() }
                }
            }
            .padding(.horizontal, 5)
            sep(top: 4, bottom: 4)
            VStack(spacing: 0) {
                MBRow(label: store.mbPauseLabel) { store.mbPause() }
                MBRow(label: "Ayarlar", right: "⌘,") {
                    openMain { $0.screen = .settings }
                }
                MBRow(label: "OpenAssist'ten çık", right: "⌘Q") {
                    NSApp.terminate(nil)
                }
            }
            .padding(.horizontal, 5)
        }
        .padding(.vertical, 5)
        .frame(width: 288)
        .background(th.popBg)
        .environment(\.theme, store.theme)
    }

    private func openMain(_ patch: (DesignStore) -> Void) {
        patch(store)
        openWindow(id: "main")
        NSApp.activate(ignoringOtherApps: true)
        dismiss()
    }

    private func sep(top: CGFloat = 0, bottom: CGFloat = 0) -> some View {
        th.popBorder
            .frame(height: 0.5)
            .padding(EdgeInsets(top: top, leading: 0, bottom: bottom, trailing: 0))
    }

    private func taskRow(_ t: MBTask) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 7) {
                Circle()
                    .fill(Color(hex: t.dotHex))
                    .frame(width: 5, height: 5)
                Text(t.label)
                    .font(.system(size: 12.5))
                    .foregroundStyle(th.text)
                    .lineLimit(1)
                    .frame(maxWidth: .infinity, alignment: .leading)
                Text(t.meta)
                    .font(.system(size: 11))
                    .foregroundStyle(th.text3)
            }
            if t.hasBar {
                GeometryReader { geo in
                    ZStack(alignment: .leading) {
                        RoundedRectangle(cornerRadius: 2).fill(th.inputBg)
                        RoundedRectangle(cornerRadius: 2)
                            .fill(Theme.primary)
                            .frame(width: geo.size.width * CGFloat(t.pct) / 100)
                    }
                }
                .frame(height: 3)
                .padding(EdgeInsets(top: 5, leading: 12, bottom: 0, trailing: 0))
            }
        }
    }
}

/// Panel menü satırı — hover'da mavi zemin + beyaz metin.
private struct MBRow: View {
    @Environment(\.theme) private var th
    let label: String
    var right: String?
    var badge: Int?
    let action: () -> Void
    @State private var hov = false

    init(label: String, right: String? = nil, badge: Int? = nil, action: @escaping () -> Void) {
        self.label = label
        self.right = right
        self.badge = badge
        self.action = action
    }

    var body: some View {
        HStack(spacing: 8) {
            Text(label)
                .font(.system(size: 12.5))
                .foregroundStyle(hov ? .white : th.text)
                .frame(maxWidth: .infinity, alignment: .leading)
            if let badge {
                Text("\(badge)")
                    .font(.system(size: 10.5, weight: .bold))
                    .foregroundStyle(.white)
                    .padding(EdgeInsets(top: 1, leading: 6, bottom: 1, trailing: 6))
                    .background(Capsule().fill(Theme.primary))
            }
            if let right {
                Text(right)
                    .font(.system(size: 11.5))
                    .foregroundStyle((hov ? Color.white : th.text).opacity(0.6))
            }
        }
        .padding(EdgeInsets(top: 5, leading: 8, bottom: 5, trailing: 8))
        .background(RoundedRectangle(cornerRadius: 6).fill(hov ? Theme.primary : .clear))
        .contentShape(Rectangle())
        .onHover { hov = $0 }
        .onTapGesture(perform: action)
    }
}
