import SwiftUI
import UniformTypeIdentifiers

/// Orta sütun: sohbet başlığı, mesaj listesi ve giriş çubuğu.
struct ChatScreen: View {
    @Bindable var store: DesignStore
    @Environment(\.theme) private var th
    /// Dosya seçici görünürlüğü (ADIM 5).
    @State private var ekSecici = false
    /// "Sohbeti Temizle" iki adımlı onayı: ilk dokunuş kurar, ikinci siler.
    /// Tüm konuşma sunucuda silinir — kazayla tek dokunuşla olmasın.
    @State private var temizOnay = false

    var body: some View {
        if store.noBots {
            emptyState
        } else {
            VStack(spacing: 0) {
                header.zIndex(10)
                messageList
                inputBar
            }
        }
    }

    // ── Bot yokken boş durum ──

    private var emptyState: some View {
        VStack(spacing: 8) {
            BotIconView(color: Color(hex: "#7b4fd6"), size: 84)
            Text("OpenAssist'e hoş geldin")
                .font(.system(size: 18, weight: .bold))
                .foregroundStyle(th.text)
                .padding(.top, 10)
            Text("İlk botunu oluştur, işlerini ona devret.")
                .font(.system(size: 13.5))
                .foregroundStyle(th.text2)
            Text("Yeni Bot Oluştur")
                .font(.system(size: 14, weight: .semibold))
                .foregroundStyle(th.winBg)
                .padding(EdgeInsets(top: 9, leading: 22, bottom: 9, trailing: 22))
                .background(RoundedRectangle(cornerRadius: 10).fill(th.text))
                .padding(.top, 12)
                .tap { store.openNewBot() }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    // ── Başlık ──

    private var header: some View {
        HStack(spacing: 10) {
            if store.cur.isTeam {
                TeamAvatarView(colors: teamColors(store.cur), layout: TeamAvatarView.hstack28, size: 28)
            } else {
                BotIconView(color: Color(hex: store.cur.c2), size: 28)
            }
            Text(store.cur.name)
                .font(.system(size: 15, weight: .semibold))
                .foregroundStyle(th.text)
            if store.cur.isTeam {
                // Üyesiz ekip sessizce "0 bot" demesin: kanal cevap veremez,
                // kullanıcı sebebini burada görsün.
                let sayi = (store.cur.team ?? []).count
                Text(sayi > 0 ? "\(sayi) bot" : "üye yok — yan panelden ekle")
                    .font(.system(size: 12.5))
                    .foregroundStyle(sayi > 0 ? th.text3 : Theme.error)
            }
            WindowDragArea()
            IconButton(icon: "ellipsis", size: 30, iconSize: 14, help: "Bot menüsü") {
                temizOnay = false
                store.openDD = store.openDD == "chatMenu" ? nil : "chatMenu"
            }
            .overlay(alignment: .topTrailing) {
                if store.openDD == "chatMenu" {
                    DropMenu(minWidth: 160) {
                        MenuRow(label: "Botu Düzenle", icon: "pencil") {
                            store.openDD = nil
                            store.openEditBot()
                        }
                        MenuRow(label: temizOnay ? "Emin misin? Sohbet silinsin" : "Sohbeti Temizle",
                                icon: temizOnay ? "exclamationmark.triangle" : "xmark.bubble",
                                color: temizOnay ? Theme.error : nil) {
                            guard temizOnay else { temizOnay = true; return }
                            temizOnay = false
                            store.clearChat()
                        }
                        MenuRow(label: "Botu Sil", icon: "trash", color: Theme.error) {
                            store.openDD = nil
                            store.deleteBot(store.cur.id)
                        }
                    }
                    .offset(y: 36)
                    .fixedSize()
                }
            }
            IconButton(icon: "macwindow", size: 30, iconSize: 15, help: "Yan paneli aç/kapat") {
                store.rightOpen.toggle()
            }
        }
        .padding(.horizontal, 16)
        .frame(height: 52)
    }

    private func teamColors(_ ekip: DAgent) -> [Color] {
        let uyeler = (ekip.team ?? []).prefix(3)
            .map { Color(hex: store.agent($0)?.c2 ?? "#8e8e93") }
        return uyeler.isEmpty ? [Color(hex: ekip.c2)] : uyeler
    }

    // ── Mesaj listesi ──

    private var messageList: some View {
        GeometryReader { geo in
            let listW = max(1, geo.size.width - 56)
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 10) {
                        ForEach(store.msgs(for: store.cur.id)) { m in
                            MessageView(m: m, store: store, listW: listW)
                        }
                        if store.curTyping {
                            TypingBubble()
                        }
                        Color.clear.frame(height: 1).id("bottom")
                    }
                    .padding(EdgeInsets(top: 24, leading: 28, bottom: 16, trailing: 28))
                }
                .onAppear { proxy.scrollTo("bottom", anchor: .bottom) }
                .onChange(of: store.msgs(for: store.cur.id).count) {
                    withAnimation { proxy.scrollTo("bottom", anchor: .bottom) }
                }
                .onChange(of: store.curTyping) {
                    withAnimation { proxy.scrollTo("bottom", anchor: .bottom) }
                }
                .onChange(of: store.active) {
                    proxy.scrollTo("bottom", anchor: .bottom)
                }
            }
        }
    }

    // ── Giriş çubuğu ──

    /// Bekleyen ekler (ADIM 5): gönderim öncesi seçilmiş dosyaların çipleri.
    private var ekCipleri: some View {
        let eks = store.ekler(for: store.cur.id)
        guard !eks.isEmpty else { return AnyView(EmptyView()) }
        return AnyView(
            HStack(spacing: 6) {
                ForEach(eks) { att in
                    HStack(spacing: 6) {
                        Text(att.name)
                            .font(.system(size: 11.5, weight: .medium))
                            .foregroundStyle(th.text)
                            .lineLimit(1)
                        IconButton(icon: "xmark", size: 18, iconSize: 8, radius: 9, help: "Eki kaldır") {
                            store.removeEk(att.id)
                        }
                    }
                    .padding(EdgeInsets(top: 4, leading: 10, bottom: 4, trailing: 6))
                    .background(RoundedRectangle(cornerRadius: 12).fill(th.inputBg))
                    .overlay(RoundedRectangle(cornerRadius: 12).stroke(th.border, lineWidth: 1))
                }
                Spacer(minLength: 0)
            }
            .padding(.horizontal, 20)
        )
    }

    /// Giriş çubuğu ipucu: ekip kanalında mesaj varsayılan olarak
    /// koordinatöre gider, @Ad ile başka üye hedeflenebilir (ADIM 10).
    private var girisIpucu: String {
        if store.isEditing { return "Düzenleniyor — Gönder, yeniden üretsin: \(store.cur.name)" }
        if store.cur.isTeam { return "Mesaj: \(store.cur.name) — @Ad ile üye seçebilirsin" }
        return "Mesaj: \(store.cur.name)"
    }

    private var inputBar: some View {
        VStack(spacing: 0) {
            ekCipleri
            HStack(spacing: 8) {
                IconButton(icon: "plus", size: 30, iconSize: 14, radius: 15, help: "Dosya ekle") {
                    ekSecici = true
                }
                DTextField(placeholder: girisIpucu,
                           text: $store.draft, size: 14) {
                    store.send()
                }
                IconButton(icon: "arrow.up", size: 30, iconSize: 14, radius: 15, help: "Gönder") {
                    store.send()
                }
                .disabled(!store.canSend())
                .opacity(store.canSend() ? 1 : 0.4)
            }
            .padding(.horizontal, 8)
            .frame(height: 44)
            .background(RoundedRectangle(cornerRadius: 22).fill(th.inputBg))
            .padding(EdgeInsets(top: 10, leading: 20, bottom: 16, trailing: 20))
        }
        .fileImporter(isPresented: $ekSecici, allowedContentTypes: [.pdf, .image, .plainText, .item], allowsMultipleSelection: true) { sonuc in
            switch sonuc {
            case .success(let urls):
                for url in urls { store.addEk(url) }
            case .failure(let err):
                store.showToast("Dosya seçilemedi", err.localizedDescription)
            }
        }
    }
}
