import SwiftUI

/// Sağ panel — 300px: Rutinler / Üyeler / Görevler sekmeleri + Bağlantılar.
struct RightPanel: View {
    @Bindable var store: DesignStore
    @Environment(\.theme) private var th

    var body: some View {
        VStack(spacing: 0) {
            header
            tabs
            if store.rightMembers {
                membersSection
            } else if store.rightTasks {
                tasksSection
            } else {
                routinesSection
            }
        }
        .frame(width: 300)
        .frame(maxHeight: .infinity)
        .background(th.panelBg)
        .overlay(alignment: .leading) { th.border.frame(width: 1) }
    }

    private var header: some View {
        HStack(spacing: 4) {
            IconButton(icon: "chevron.left", size: 28, iconSize: 13) {}
            WindowDragArea()
            IconButton(icon: "xmark", size: 28, iconSize: 12, help: "Kapat") {
                store.rightOpen = false
            }
        }
        .padding(.horizontal, 12)
        .frame(height: 52)
    }

    private var tabs: some View {
        // Sekme ekibin KİMLİĞİNE bağlı, üye listesinin doluluğuna değil:
        // üyesiz ekipte "Üyeler" gizlenseydi ilk üyeyi ekleyecek yüzey hiç
        // olmazdı (kanal da cevap veremez).
        let list: [(String, String)] = store.cur.isTeam
            ? [("routines", "Rutinler"), ("members", "Üyeler"), ("tasks", "Görevler")]
            : [("routines", "Rutinler"), ("tasks", "Görevler")]
        return HStack(spacing: 4) {
            ForEach(list, id: \.0) { key, label in
                let sel = store.rightTab == key
                Text(label)
                    .font(.system(size: 13, weight: .medium))
                    .foregroundStyle(sel ? th.text : th.text2)
                    .padding(EdgeInsets(top: 4, leading: 12, bottom: 4, trailing: 12))
                    .hoverBg(th.hover, active: th.active, isActive: sel, radius: 8)
                    .tap { store.rightTab = key }
            }
            Spacer()
        }
        .padding(EdgeInsets(top: 0, leading: 16, bottom: 10, trailing: 16))
    }

    // ── Üyeler ──

    private var membersSection: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                Text("Üyeler")
                    .font(.system(size: 13))
                    .foregroundStyle(th.text2)
                    .padding(.bottom, 12)
                VStack(spacing: 13) {
                    ForEach(store.teamMembers) { mb in
                        HStack(spacing: 11) {
                            BotIconView(color: Color(hex: mb.c2), size: 28)
                            VStack(alignment: .leading, spacing: 0) {
                                Text(mb.name)
                                    .font(.system(size: 13.5, weight: .medium))
                                    .foregroundStyle(th.text)
                                    .lineLimit(1)
                                Text(store.role(of: mb.id))
                                    .font(.system(size: 12))
                                    .foregroundStyle(th.text2)
                            }
                            .frame(maxWidth: .infinity, alignment: .leading)
                            Text(store.modelShort(of: mb.id))
                                .font(.system(size: 11.5))
                                .foregroundStyle(th.text3)
                        }
                        .padding(2)
                        .hoverBg(th.hover, radius: 8)
                        .tap {
                            store.active = mb.id
                            store.rightTab = "routines"
                        }
                    }
                }
                Text("Ekibe ekle")
                    .font(.system(size: 13))
                    .foregroundStyle(th.text2)
                    .padding(EdgeInsets(top: 24, leading: 0, bottom: 10, trailing: 0))
                FlowLayout(spacing: 7, lineSpacing: 7) {
                    ForEach(store.teamCandidates) { c in
                        HStack(spacing: 6) {
                            Image(systemName: "plus")
                                .font(.system(size: 9, weight: .semibold))
                            Text(c.name)
                        }
                        .font(.system(size: 12.5))
                        .foregroundStyle(th.text2)
                        .padding(EdgeInsets(top: 5, leading: 11, bottom: 5, trailing: 11))
                        .overlay(Capsule().stroke(th.border, lineWidth: 1))
                        .tap { store.addToTeam(c) }
                    }
                    if store.teamCandidates.isEmpty {
                        Text("Tüm botlar bu ekipte")
                            .font(.system(size: 12.5))
                            .foregroundStyle(th.text3)
                    }
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(EdgeInsets(top: 6, leading: 18, bottom: 20, trailing: 18))
        }
    }

    // ── Görevler ──

    private var tasksSection: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                ForEach(Array(store.tasks(for: store.cur.id).enumerated()), id: \.offset) { _, t in
                    VStack(alignment: .leading, spacing: 6) {
                        HStack(spacing: 8) {
                            Circle()
                                .fill(Color(hex: t.status.dotHex))
                                .frame(width: 7, height: 7)
                            Text(t.label)
                                .font(.system(size: 13.5, weight: .medium))
                                .foregroundStyle(th.text)
                                .frame(maxWidth: .infinity, alignment: .leading)
                            Text(t.status.label)
                                .font(.system(size: 11.5))
                                .foregroundStyle(t.status.statusHex.map { Color(hex: $0) } ?? th.text3)
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
                            .frame(height: 4)
                            .padding(.leading, 15)
                        }
                        Text(t.sub)
                            .font(.system(size: 12.5))
                            .foregroundStyle(th.text2)
                            .padding(.leading, 15)
                    }
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(EdgeInsets(top: 6, leading: 18, bottom: 20, trailing: 18))
        }
    }

    // ── Rutinler + Bağlantılar ──

    private var routinesSection: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                Text("Rutinler")
                    .font(.system(size: 13))
                    .foregroundStyle(th.text2)
                    .padding(.bottom, 12)
                VStack(alignment: .leading, spacing: 14) {
                    ForEach(Array(store.routines(for: store.cur.id).enumerated()), id: \.offset) { i, r in
                        HStack(alignment: .top, spacing: 12) {
                            Image(systemName: r.paused ? "pause.circle" : "clock")
                                .font(.system(size: 16, weight: .light))
                                .foregroundStyle(r.paused ? th.text3 : th.text2)
                                .padding(.top, 1)
                                .tap { store.toggleRoutine(store.cur.id, i) }
                                .help("Duraklat / Sürdür")
                            VStack(alignment: .leading, spacing: 0) {
                                Text(r.label)
                                    .font(.system(size: 14, weight: .medium))
                                    .foregroundStyle(th.text)
                                Text(r.sub)
                                    .font(.system(size: 13))
                                    .foregroundStyle(th.text2)
                            }
                        }
                    }
                }
                if store.raOpen {
                    VStack(alignment: .leading, spacing: 8) {
                        DTextField(placeholder: "Ne yapılsın? örn. Pipeline özeti", text: $store.raLabel, size: 13)
                            .padding(EdgeInsets(top: 8, leading: 10, bottom: 8, trailing: 10))
                            .background(RoundedRectangle(cornerRadius: 8).fill(th.inputBg))
                        DTextField(placeholder: "Ne zaman? örn. Her gün 09:00", text: $store.raSub, size: 13)
                            .padding(EdgeInsets(top: 8, leading: 10, bottom: 8, trailing: 10))
                            .background(RoundedRectangle(cornerRadius: 8).fill(th.inputBg))
                        HStack(spacing: 8) {
                            Text("Ekle")
                                .font(.system(size: 12.5, weight: .semibold))
                                .foregroundStyle(.white)
                                .padding(EdgeInsets(top: 5, leading: 14, bottom: 5, trailing: 14))
                                .background(RoundedRectangle(cornerRadius: 7).fill(Theme.primary))
                                .tap { store.raAdd() }
                            Text("Vazgeç")
                                .font(.system(size: 12.5))
                                .foregroundStyle(th.text2)
                                .padding(EdgeInsets(top: 5, leading: 12, bottom: 5, trailing: 12))
                                .background(RoundedRectangle(cornerRadius: 7).fill(th.inputBg))
                                .tap { store.raCancel() }
                        }
                    }
                    .padding(.top, 14)
                } else {
                    HStack(spacing: 7) {
                        Image(systemName: "plus")
                            .font(.system(size: 10, weight: .medium))
                        Text("Rutin ekle")
                            .font(.system(size: 13))
                    }
                    .foregroundStyle(th.text2)
                    .padding(.vertical, 5)
                    .padding(.top, 9)
                    .tap { store.raOpen = true }
                }
                Text("Bağlantılar")
                    .font(.system(size: 13))
                    .foregroundStyle(th.text2)
                    .padding(EdgeInsets(top: 24, leading: 0, bottom: 12, trailing: 0))
                VStack(spacing: 12) {
                    ForEach(Array(store.conns(for: store.cur.id).enumerated()), id: \.offset) { _, c in
                        HStack(spacing: 10) {
                            Circle()
                                .fill(Color(hex: c.on ? "#28c840" : "#ff5f57"))
                                .frame(width: 8, height: 8)
                            Text(c.name)
                                .font(.system(size: 14))
                                .foregroundStyle(th.text)
                                .frame(maxWidth: .infinity, alignment: .leading)
                            Text(c.on ? "Bağlı" : "Bağlı değil")
                                .font(.system(size: 12))
                                .foregroundStyle(th.text3)
                        }
                    }
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(EdgeInsets(top: 6, leading: 18, bottom: 20, trailing: 18))
        }
    }
}
