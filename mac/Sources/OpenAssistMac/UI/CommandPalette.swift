import SwiftUI

/// ⌘K komut paleti — karartılmış arka plan üzerinde 560px panel.
struct CommandPalette: View {
    @Bindable var store: DesignStore
    @Environment(\.theme) private var th
    @FocusState private var focused: Bool

    var body: some View {
        ZStack(alignment: .top) {
            Color.black.opacity(0.28)
                .contentShape(Rectangle())
                .onTapGesture { close() }
            panel
                .padding(.top, 140)
        }
        .onAppear { focused = true }
        .onExitCommand { close() }
    }

    private func close() {
        store.paletteOpen = false
        store.pq = ""
    }

    private var panel: some View {
        VStack(spacing: 0) {
            HStack(spacing: 10) {
                Image(systemName: "magnifyingglass")
                    .font(.system(size: 14))
                    .foregroundStyle(th.text3)
                TextField("Komut ara veya bir bota iş ver…", text: $store.pq)
                    .textFieldStyle(.plain)
                    .font(.system(size: 15))
                    .foregroundStyle(th.text)
                    .focused($focused)
                    .onSubmit { store.paletteEnter() }
                    .onKeyPress(.downArrow) { store.paletteMove(1); return .handled }
                    .onKeyPress(.upArrow) { store.paletteMove(-1); return .handled }
                Text("esc")
                    .font(.system(size: 11))
                    .foregroundStyle(th.text3)
                    .padding(EdgeInsets(top: 1, leading: 6, bottom: 1, trailing: 6))
                    .overlay(RoundedRectangle(cornerRadius: 5).stroke(th.border, lineWidth: 1))
            }
            .padding(EdgeInsets(top: 14, leading: 16, bottom: 14, trailing: 16))
            .overlay(alignment: .bottom) { th.border.frame(height: 1) }
            ScrollView {
                VStack(spacing: 0) {
                    ForEach(Array(store.filteredPaletteItems.enumerated()), id: \.element.id) { i, p in
                        row(p, selected: i == store.paletteSel)
                    }
                    if store.filteredPaletteItems.isEmpty {
                        Text("Sonuç yok")
                            .font(.system(size: 13))
                            .foregroundStyle(th.text3)
                            .frame(maxWidth: .infinity)
                            .padding(.vertical, 26)
                    }
                }
                .padding(6)
            }
            .frame(maxHeight: 330)
        }
        .frame(width: 560)
        .background(th.winBg)
        .clipShape(RoundedRectangle(cornerRadius: 14))
        .overlay(RoundedRectangle(cornerRadius: 14).stroke(th.border, lineWidth: 1))
        .shadow(color: .black.opacity(0.4), radius: 35, y: 24)
        .onChange(of: store.pq) { store.pi = 0 }
    }

    private func row(_ p: PaletteItem, selected: Bool) -> some View {
        HStack(spacing: 11) {
            Text(p.group.uppercased())
                .font(.system(size: 11, weight: .semibold))
                .kerning(0.3)
                .foregroundStyle(th.text3)
                .frame(width: 58, alignment: .leading)
            Text(p.label)
                .font(.system(size: 13.5))
                .foregroundStyle(th.text)
                .frame(maxWidth: .infinity, alignment: .leading)
            Text(p.hint)
                .font(.system(size: 11.5))
                .foregroundStyle(th.text3)
        }
        .padding(EdgeInsets(top: 9, leading: 12, bottom: 9, trailing: 12))
        .hoverBg(th.hover, active: th.hover, isActive: selected, radius: 9)
        .tap { p.action() }
    }
}
