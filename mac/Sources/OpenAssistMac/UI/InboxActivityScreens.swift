import SwiftUI

/// Onay Merkezi — bekleyen taslak kartları.
struct InboxScreen: View {
    @Bindable var store: DesignStore
    @Environment(\.theme) private var th

    var body: some View {
        VStack(spacing: 0) {
            header
            ScrollView {
                VStack(spacing: 12) {
                    ForEach(store.inboxItems) { it in
                        card(it)
                    }
                    if store.inboxItems.isEmpty {
                        VStack(spacing: 0) {
                            Text("Bekleyen onay yok")
                                .font(.system(size: 16, weight: .semibold))
                                .foregroundStyle(th.text)
                            Text("Botlar yeni taslak hazırladığında burada görünür.")
                                .font(.system(size: 13.5))
                                .foregroundStyle(th.text2)
                                .padding(.top, 5)
                        }
                        .padding(.vertical, 60)
                    }
                }
                .frame(maxWidth: 760)
                .frame(maxWidth: .infinity)
                .padding(EdgeInsets(top: 20, leading: 28, bottom: 28, trailing: 28))
            }
        }
        .background(th.winBg)
    }

    private var header: some View {
        HStack(spacing: 10) {
            Text("Onay Merkezi")
                .font(.system(size: 15, weight: .semibold))
                .foregroundStyle(th.text)
            Text(store.inboxSub)
                .font(.system(size: 12.5))
                .foregroundStyle(th.text3)
            WindowDragArea()
            Text("Tümünü onayla")
                .font(.system(size: 12.5, weight: .medium))
                .foregroundStyle(th.text)
                .padding(EdgeInsets(top: 5, leading: 13, bottom: 5, trailing: 13))
                .overlay(RoundedRectangle(cornerRadius: 8).stroke(th.border, lineWidth: 1))
                .hoverBg(th.hover, radius: 8)
                .tap { store.approveAll() }
        }
        .padding(.horizontal, 16)
        .frame(height: 52)
        .overlay(alignment: .bottom) { th.border.frame(height: 1) }
    }

    private func card(_ it: InboxItem) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 9) {
                BotIconView(color: Color(hex: it.c2), size: 22)
                Text(it.botName)
                    .font(.system(size: 13.5, weight: .semibold))
                    .foregroundStyle(th.text)
                Text(it.msg.title)
                    .font(.system(size: 12.5))
                    .foregroundStyle(th.text2)
                    .lineLimit(1)
                    .frame(maxWidth: .infinity, alignment: .leading)
                Text(it.time)
                    .font(.system(size: 11.5))
                    .foregroundStyle(th.text3)
            }
            Text(it.msg.to)
                .font(.system(size: 12.5))
                .foregroundStyle(th.text3)
                .padding(EdgeInsets(top: 8, leading: 31, bottom: 0, trailing: 0))
            Text(it.msg.text)
                .font(.system(size: 13.5))
                .lineSpacing(3.5)
                .foregroundStyle(th.text2)
                .padding(EdgeInsets(top: 5, leading: 31, bottom: 0, trailing: 0))
            HStack(spacing: 8) {
                Text("Onayla")
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(.white)
                    .padding(EdgeInsets(top: 6, leading: 16, bottom: 6, trailing: 16))
                    .background(RoundedRectangle(cornerRadius: 8).fill(Theme.primary))
                    .tap { store.decide(it.botName, it.id, true) }
                if it.msg.hepIzin {
                    Text("Hep izin ver")
                        .font(.system(size: 13, weight: .medium))
                        .foregroundStyle(Theme.primary)
                        .padding(EdgeInsets(top: 6, leading: 14, bottom: 6, trailing: 14))
                        .hoverBg(th.hover, radius: 8)
                        .tap { store.decide(it.botName, it.id, true, always: true) }
                }
                Text("Düzenle")
                    .font(.system(size: 13, weight: .medium))
                    .foregroundStyle(th.text)
                    .padding(EdgeInsets(top: 6, leading: 14, bottom: 6, trailing: 14))
                    .background(RoundedRectangle(cornerRadius: 8).fill(th.inputBg))
                Text("Reddet")
                    .font(.system(size: 13, weight: .medium))
                    .foregroundStyle(Theme.error)
                    .padding(EdgeInsets(top: 6, leading: 14, bottom: 6, trailing: 14))
                    .hoverBg(th.hover, radius: 8)
                    .tap { store.decide(it.botName, it.id, false) }
                Spacer()
                Text("Sohbete git")
                    .font(.system(size: 13, weight: .medium))
                    .foregroundStyle(th.text2)
                    .padding(EdgeInsets(top: 6, leading: 14, bottom: 6, trailing: 14))
                    .hoverBg(th.hover, radius: 8)
                    .tap { store.pick(it.botId) }
            }
            .padding(EdgeInsets(top: 12, leading: 31, bottom: 0, trailing: 0))
        }
        .padding(EdgeInsets(top: 14, leading: 16, bottom: 14, trailing: 16))
        .background(RoundedRectangle(cornerRadius: 14).fill(th.cardBg))
        .overlay(RoundedRectangle(cornerRadius: 14).stroke(th.border, lineWidth: 1))
    }
}

/// Aktivite — gün gün olay akışı, filtre çipleriyle.
struct ActivityScreen: View {
    @Bindable var store: DesignStore
    @Environment(\.theme) private var th

    var body: some View {
        VStack(spacing: 0) {
            header
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(store.activityDays) { day in
                        VStack(alignment: .leading, spacing: 0) {
                            Text(day.label.uppercased())
                                .font(.system(size: 12, weight: .semibold))
                                .kerning(0.4)
                                .foregroundStyle(th.text3)
                                .padding(.bottom, 10)
                            ForEach(day.items) { e in
                                eventRow(e)
                            }
                        }
                        .padding(.bottom, 26)
                    }
                }
                .frame(maxWidth: 760, alignment: .leading)
                .frame(maxWidth: .infinity)
                .padding(EdgeInsets(top: 20, leading: 28, bottom: 28, trailing: 28))
            }
        }
        .background(th.winBg)
    }

    private var header: some View {
        HStack(spacing: 8) {
            Text("Aktivite")
                .font(.system(size: 15, weight: .semibold))
                .foregroundStyle(th.text)
            WindowDragArea()
            HStack(spacing: 4) {
                ForEach(store.actFilters, id: \.id) { f in
                    let sel = store.actFilter == f.id
                    Text(f.label)
                        .font(.system(size: 12.5, weight: .medium))
                        .foregroundStyle(sel ? th.text : th.text2)
                        .padding(EdgeInsets(top: 4, leading: 11, bottom: 4, trailing: 11))
                        .hoverBg(th.hover, active: th.active, isActive: sel, radius: 8)
                        .tap { store.actFilter = f.id }
                }
            }
        }
        .padding(.horizontal, 16)
        .frame(height: 52)
        .overlay(alignment: .bottom) { th.border.frame(height: 1) }
    }

    private func eventRow(_ e: DActivityItem) -> some View {
        HStack(alignment: .top, spacing: 12) {
            Text(e.time)
                .font(.system(size: 12))
                .monospacedDigit()
                .foregroundStyle(th.text3)
                .frame(width: 44, alignment: .leading)
                .padding(.top, 1)
            Circle()
                .fill(Color(hex: e.err ? "#ff453a" : "#28c840"))
                .frame(width: 7, height: 7)
                .padding(.top, 6)
            VStack(alignment: .leading, spacing: 2) {
                Text(e.text)
                    .font(.system(size: 13.5))
                    .lineSpacing(3)
                    .foregroundStyle(th.text)
                Text(e.meta)
                    .font(.system(size: 12))
                    .foregroundStyle(th.text3)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            Text(e.bot)
                .font(.system(size: 12))
                .foregroundStyle(th.text3)
                .padding(.top, 1)
        }
        .padding(.vertical, 9)
        .overlay(alignment: .bottom) { th.border.frame(height: 1) }
    }
}
