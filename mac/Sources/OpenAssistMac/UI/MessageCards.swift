import SwiftUI

/// Tek mesaj satırı — türe göre kartı seçer ve hizalar.
struct MessageView: View {
    var m: DMsg
    @Bindable var store: DesignStore
    /// Mesaj listesinin iç genişliği (yüzde tabanlı maks. genişlikler için).
    var listW: CGFloat
    @Environment(\.theme) private var th

    var body: some View {
        switch m.k {
        case .divider:
            Text(m.text)
                .font(.system(size: 11.5))
                .foregroundStyle(th.text3)
                .padding(.vertical, 6)
                .frame(maxWidth: .infinity)
        case .user:
            row(.trailing) {
                bubble(m.text, bg: th.bubbleUser, fg: th.bubbleUserText)
                    .frame(maxWidth: listW * 0.62, alignment: .trailing)
                    .contextMenu {
                        Button("Kopyala") { store.copyMessage(m.text) }
                        Button("Düzenle ve Yeniden Üret") {
                            store.beginEdit(messageID: m.id)
                        }
                    }
            }
        case .bot:
            row(.leading) {
                bubble(m.text, bg: th.bubbleBot, fg: th.text)
                    .frame(maxWidth: listW * 0.62, alignment: .leading)
                    .contextMenu {
                        Button("Kopyala") { store.copyMessage(m.text) }
                    }
            }
        case .from:
            row(.leading) {
                fromMsg
                    .frame(maxWidth: listW * 0.72, alignment: .leading)
                    .contextMenu {
                        Button("Kopyala") { store.copyMessage(m.text) }
                    }
            }
        case .check:
            row(.leading) { checkCard.frame(maxWidth: listW * 0.70, alignment: .leading) }
        case .tool:
            row(.leading) {
                toolCard
                    .frame(minWidth: min(380, listW))
                    .frame(maxWidth: listW * 0.74, alignment: .leading)
            }
        case .error:
            row(.leading) {
                errorCard
                    .frame(minWidth: min(340, listW))
                    .frame(maxWidth: listW * 0.70, alignment: .leading)
                    .contextMenu {
                        Button("Kopyala") { store.copyMessage(m.text) }
                    }
            }
        case .file:
            row(.leading) {
                fileCard
                    .frame(minWidth: min(260, listW))
                    .frame(maxWidth: listW * 0.74, alignment: .leading)
            }
        case .work:
            row(.leading) {
                workCard
                    .frame(maxWidth: listW * 0.62, alignment: .leading)
                    .contextMenu {
                        Button("Kopyala") { store.copyMessage(m.text) }
                    }
            }
        case .approve:
            row(.leading) {
                approveCard
                    .frame(minWidth: min(340, listW))
                    .frame(maxWidth: listW * 0.72, alignment: .leading)
            }
        }
    }

    private func row(_ align: Alignment, @ViewBuilder _ content: () -> some View) -> some View {
        content().frame(maxWidth: .infinity, alignment: align)
    }

    private func bubble(_ text: String, bg: Color, fg: Color) -> some View {
        Text(text)
            .font(.system(size: 14))
            .lineSpacing(3)
            .foregroundStyle(fg)
            .padding(EdgeInsets(top: 10, leading: 14, bottom: 10, trailing: 14))
            .background(RoundedRectangle(cornerRadius: 18).fill(bg))
    }

    // ── Ekip mesajı: mini avatar + gönderen adı + balon ──

    private var fromMsg: some View {
        let sender = store.agent(m.bot ?? "")
        return HStack(alignment: .top, spacing: 9) {
            BotIconView(color: Color(hex: sender?.c2 ?? "#8e8e93"), size: 24)
                .padding(.top, 2)
            VStack(alignment: .leading, spacing: 0) {
                Text(sender?.name ?? "")
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundStyle(th.text2)
                    .padding(EdgeInsets(top: 0, leading: 2, bottom: 3, trailing: 0))
                Text(m.text)
                    .font(.system(size: 14))
                    .lineSpacing(3)
                    .foregroundStyle(th.text)
                    .padding(EdgeInsets(top: 10, leading: 14, bottom: 10, trailing: 14))
                    .background(
                        UnevenRoundedRectangle(topLeadingRadius: 6, bottomLeadingRadius: 18,
                                               bottomTrailingRadius: 18, topTrailingRadius: 18)
                            .fill(th.bubbleBot))
            }
        }
    }

    // ── Kontrol listesi kartı ──

    private var checkCard: some View {
        VStack(alignment: .leading, spacing: 7) {
            ForEach(Array(m.items.enumerated()), id: \.offset) { _, it in
                HStack(alignment: .top, spacing: 8) {
                    Text("✓")
                        .font(.system(size: 13.5))
                        .foregroundStyle(Color(hex: "#28a745"))
                    (Text(it.label).fontWeight(.semibold) + Text(" → \(it.text)"))
                        .font(.system(size: 13.5))
                        .lineSpacing(3)
                        .foregroundStyle(th.text)
                }
            }
        }
        .padding(EdgeInsets(top: 12, leading: 16, bottom: 12, trailing: 16))
        .background(RoundedRectangle(cornerRadius: 14).fill(th.bubbleBot))
    }

    // ── Araç çağrısı kartı (açılır/kapanır) ──

    private var toolCard: some View {
        let open = store.toolOpen.contains(m.id)
        return VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 9) {
                Image(systemName: "chevron.right")
                    .font(.system(size: 10, weight: .semibold))
                    .foregroundStyle(th.text3)
                    .rotationEffect(.degrees(open ? 90 : 0))
                Text(m.title)
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(th.text)
                Text(m.summary)
                    .font(.system(size: 12))
                    .foregroundStyle(th.text3)
                    .lineLimit(1)
                    .frame(maxWidth: .infinity, alignment: .leading)
                Text(m.dur)
                    .font(.system(size: 11.5))
                    .monospacedDigit()
                    .foregroundStyle(th.text3)
            }
            .padding(EdgeInsets(top: 10, leading: 14, bottom: 10, trailing: 14))
            .contentShape(Rectangle())
            .hoverBg(th.hover, radius: 0)
            .tap {
                if open { store.toolOpen.remove(m.id) } else { store.toolOpen.insert(m.id) }
            }

            if open {
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(Array(m.calls.enumerated()), id: \.offset) { _, c in
                        HStack(alignment: .top, spacing: 10) {
                            Text(c.err ? "✕" : "✓")
                                .font(.system(size: 12))
                                .foregroundStyle(c.err ? Theme.error : Theme.successText)
                                .frame(width: 15)
                                .padding(.top, 1)
                            VStack(alignment: .leading, spacing: 0) {
                                Text(c.call)
                                    .font(.system(size: 12, design: .monospaced))
                                    .foregroundStyle(th.text)
                                Text(c.result)
                                    .font(.system(size: 12.5))
                                    .foregroundStyle(c.err ? Theme.error : th.text2)
                                    .padding(.top, 3)
                            }
                            .frame(maxWidth: .infinity, alignment: .leading)
                            Text(c.msLabel)
                                .font(.system(size: 11.5))
                                .monospacedDigit()
                                .foregroundStyle(th.text3)
                        }
                        .padding(EdgeInsets(top: 9, leading: 14, bottom: 9, trailing: 14))
                    }
                }
                .padding(.vertical, 4)
                .frame(maxWidth: .infinity, alignment: .leading)
                .overlay(alignment: .top) { th.border.frame(height: 1) }
            }
        }
        .background(RoundedRectangle(cornerRadius: 14).fill(th.cardBg))
        .overlay(RoundedRectangle(cornerRadius: 14).stroke(th.border, lineWidth: 1))
        .clipShape(RoundedRectangle(cornerRadius: 14))
    }

    // ── Hata kartı ──

    private var errorCard: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 8) {
                Image(systemName: "exclamationmark.circle")
                    .font(.system(size: 13, weight: .medium))
                    .foregroundStyle(Theme.error)
                Text(m.title)
                    .font(.system(size: 13.5, weight: .semibold))
                    .foregroundStyle(th.text)
            }
            Text(m.text)
                .font(.system(size: 13.5))
                .lineSpacing(3)
                .foregroundStyle(th.text2)
                .textSelection(.enabled)
                .padding(.top, 6)
            HStack(spacing: 8) {
                Text("Tekrar dene")
                    .font(.system(size: 12.5, weight: .semibold))
                    .foregroundStyle(th.winBg)
                    .padding(EdgeInsets(top: 5, leading: 13, bottom: 5, trailing: 13))
                    .background(RoundedRectangle(cornerRadius: 8).fill(th.text))
                    .tap { store.retry() }
                if !m.fixLabel.isEmpty {
                    Text(m.fixLabel)
                        .font(.system(size: 12.5, weight: .medium))
                        .foregroundStyle(th.text)
                        .padding(EdgeInsets(top: 5, leading: 13, bottom: 5, trailing: 13))
                        .background(RoundedRectangle(cornerRadius: 8).fill(th.inputBg))
                        .tap { store.goFix() }
                }
            }
            .padding(.top, 11)
        }
        .padding(EdgeInsets(top: 12, leading: 15, bottom: 12, trailing: 15))
        .background(RoundedRectangle(cornerRadius: 14).fill(Theme.error.opacity(0.07)))
        .overlay(RoundedRectangle(cornerRadius: 14).stroke(Theme.error.opacity(0.28), lineWidth: 1))
    }

    // ── Dosya kartı ──

    // ── Dosya kartı ──

    /// Ek türüne göre rozet (kısa etiket + renk).
    private var ekRozet: (etiket: String, renk: String) {
        switch m.ekKind {
        case "pdf": return ("PDF", "#d33c3c")
        case "image": return ("IMG", "#2f9e6e")
        case "doc": return ("DOC", "#3b82f6")
        default: return ("DOSYA", "#9a8fb8")
        }
    }

    private var fileCard: some View {
        HStack(spacing: 12) {
            Text(ekRozet.etiket)
                .font(.system(size: 9.5, weight: .heavy))
                .foregroundStyle(Color(hex: ekRozet.renk))
                .frame(width: 36, height: 36)
                .background(RoundedRectangle(cornerRadius: 9).fill(Color(hex: ekRozet.renk).opacity(0.12)))
            VStack(alignment: .leading, spacing: 0) {
                Text(m.name)
                    .font(.system(size: 14, weight: .semibold))
                    .foregroundStyle(th.text)
                    .lineLimit(1)
                Text(m.meta)
                    .font(.system(size: 12))
                    .foregroundStyle(th.text2)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            if let fid = m.fid {
                IconButton(icon: "arrow.down.to.line", size: 28, iconSize: 13, radius: 14, help: "Aç") {
                    store.openAttachment(fid)
                }
            }
        }
        .padding(EdgeInsets(top: 10, leading: 14, bottom: 10, trailing: 14))
        .background(RoundedRectangle(cornerRadius: 14).fill(th.cardBg))
        .overlay(RoundedRectangle(cornerRadius: 14).stroke(th.border, lineWidth: 1))
    }

    // ── Çalışıyor kartı ──

    private var workCard: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 8) {
                Text(m.title)
                    .font(.system(size: 14, weight: .semibold))
                    .foregroundStyle(th.text)
                Text("✦ Çalışıyor")
                    .font(.system(size: 11.5, weight: .semibold))
                    .foregroundStyle(Theme.warningText)
                    .padding(EdgeInsets(top: 2, leading: 9, bottom: 2, trailing: 9))
                    .background(Capsule().fill(Color(hex: "#f59e0b").opacity(0.16)))
            }
            Text(m.text)
                .font(.system(size: 13.5))
                .lineSpacing(3)
                .foregroundStyle(th.text2)
                .textSelection(.enabled)
                .padding(.top, 5)
        }
        .padding(EdgeInsets(top: 12, leading: 16, bottom: 12, trailing: 16))
        .background(RoundedRectangle(cornerRadius: 14).fill(th.cardBg))
        .overlay(RoundedRectangle(cornerRadius: 14).stroke(th.border, lineWidth: 1))
    }

    // ── Onay kartı ──

    private var approveCard: some View {
        let decision = store.approvals[m.id]
        return VStack(alignment: .leading, spacing: 0) {
            VStack(alignment: .leading, spacing: 0) {
                HStack(spacing: 9) {
                    Text(m.title)
                        .font(.system(size: 13.5, weight: .semibold))
                        .foregroundStyle(th.text)
                    if decision == nil {
                        Text("Onay bekliyor")
                            .font(.system(size: 11, weight: .semibold))
                            .foregroundStyle(Theme.warningText)
                            .padding(EdgeInsets(top: 2, leading: 8, bottom: 2, trailing: 8))
                            .background(Capsule().fill(Color(hex: "#f59e0b").opacity(0.16)))
                    }
                }
                if !m.to.isEmpty {
                    Text(m.to)
                        .font(.system(size: 12))
                        .foregroundStyle(th.text3)
                        .padding(.top, 3)
                }
                Text(m.text)
                    .font(.system(size: 13.5))
                    .lineSpacing(3.5)
                    .foregroundStyle(th.text2)
                    .padding(.top, 8)
            }
            .padding(EdgeInsets(top: 13, leading: 16, bottom: 0, trailing: 16))

            if let decision {
                Text(decision == "ok"
                     ? "✓ Onaylandı · gönderim sıraya alındı"
                     : "✕ Reddedildi · bot bilgilendirildi")
                    .font(.system(size: 13, weight: .medium))
                    .foregroundStyle(decision == "ok" ? Theme.successText : Theme.error)
                    .padding(EdgeInsets(top: 10, leading: 16, bottom: 13, trailing: 16))
            } else {
                HStack(spacing: 8) {
                    Text("Onayla")
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundStyle(.white)
                        .padding(EdgeInsets(top: 6, leading: 16, bottom: 6, trailing: 16))
                        .background(RoundedRectangle(cornerRadius: 8).fill(Theme.primary))
                        .tap { store.decide(store.cur.name, m.id, true) }
                    // Aynı aracı arka arkaya çağıran bot (on mükerrer kaydı
                    // silmek gibi) yoksa her adımda ayrı karta düşüyor ve her
                    // kart ayrı bir park/resume turu oluyor — resume izni tek
                    // kullanımlık. Kural yazmak bunun çıkış kapısı; sunucu
                    // zaten destekliyordu, yüzeyde düğmesi yoktu.
                    if m.hepIzin {
                        Text("Hep izin ver")
                            .font(.system(size: 13, weight: .medium))
                            .foregroundStyle(Theme.primary)
                            .padding(EdgeInsets(top: 6, leading: 14, bottom: 6, trailing: 14))
                            .hoverBg(th.hover, radius: 8)
                            .tap { store.decide(store.cur.name, m.id, true, always: true) }
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
                        .tap { store.decide(store.cur.name, m.id, false) }
                }
                .padding(EdgeInsets(top: 12, leading: 16, bottom: 14, trailing: 16))
            }
        }
        .background(RoundedRectangle(cornerRadius: 14).fill(th.cardBg))
        .overlay(RoundedRectangle(cornerRadius: 14).stroke(th.border, lineWidth: 1))
    }
}

/// "Yazıyor" balonu — üç yanıp sönen nokta.
struct TypingBubble: View {
    @Environment(\.theme) private var th
    @State private var on = false

    var body: some View {
        HStack(spacing: 4) {
            ForEach(0..<3, id: \.self) { i in
                Circle()
                    .fill(th.text3)
                    .frame(width: 6, height: 6)
                    .opacity(on ? 1 : 0.25)
                    .animation(.easeInOut(duration: 0.6).repeatForever(autoreverses: true)
                        .delay(Double(i) * 0.2), value: on)
            }
        }
        .padding(EdgeInsets(top: 13, leading: 16, bottom: 13, trailing: 16))
        .background(RoundedRectangle(cornerRadius: 18).fill(th.bubbleBot))
        .frame(maxWidth: .infinity, alignment: .leading)
        .onAppear { on = true }
    }
}
