import SwiftUI

/// Yeni Bot / Yeni Ekip / Düzenle formu.
/// Düzen: solda kimlik kartı, sağda davranış alanları, altta sabit eylem çubuğu —
/// tek sütunlu uzun form yerine ekrana sığan iki sütun.
struct NewBotScreen: View {
    @Bindable var store: DesignStore
    @Environment(\.theme) private var th

    private var isTeam: Bool { store.nbKind == "team" }

    private var title: String {
        if store.nbEdit != nil { return isTeam ? "Ekibi Düzenle" : "Botu Düzenle" }
        return isTeam ? "Yeni Ekip" : "Yeni Bot"
    }

    private var btnLabel: String {
        if store.nbEdit != nil { return "Kaydet" }
        return isTeam ? "Ekibi Oluştur" : "Botu Oluştur"
    }

    private var valid: Bool { !store.nbName.trimmingCharacters(in: .whitespaces).isEmpty }

    private var showTemplates: Bool { store.nbEdit == nil && !isTeam }

    /// Kimlik kartı genişliği ve iç genişliği (menü genişliği buna bağlı).
    private let cardW: CGFloat = 300
    private var cardInnerW: CGFloat { cardW - 40 }

    var body: some View {
        VStack(spacing: 0) {
            header
            // Kaydırma yalnızca gerçekten taşınca devreye girer (dar pencere,
            // açık "Gelişmiş" bölümü); sığdığında içerik dikey ortalanır.
            GeometryReader { proxy in
                ScrollView {
                    VStack(alignment: .leading, spacing: 0) {
                        HStack(alignment: .top, spacing: 28) {
                            identity
                                .zIndex(MenuKat.kat(store.openDD, "nbModel"))
                            behavior
                        }
                        if showTemplates {
                            templates
                        }
                    }
                    .frame(maxWidth: 840, alignment: .leading)
                    .padding(EdgeInsets(top: 28, leading: 32, bottom: 28, trailing: 32))
                    .frame(maxWidth: .infinity, minHeight: proxy.size.height)
                }
            }
            footer
        }
        .background(th.winBg)
    }

    private var header: some View {
        HStack {
            Text(title)
                .font(.system(size: 15, weight: .semibold))
                .foregroundStyle(th.text)
            WindowDragArea()
            IconButton(icon: "xmark", size: 30, iconSize: 12, help: "Kapat") {
                store.closeNewBot()
            }
        }
        .padding(.horizontal, 16)
        .frame(height: 52)
        .overlay(alignment: .bottom) { th.border.frame(height: 1) }
    }

    // ── Sol sütun: kimlik ──

    private var identity: some View {
        VStack(alignment: .leading, spacing: 0) {
            BotIconView(color: Color(hex: DesignData.nbc[store.nbC].1), size: 64)
                .frame(maxWidth: .infinity)
            swatches
                .padding(.top, 18)
            th.border
                .frame(height: 1)
                .padding(.vertical, 20)
            fieldLabel("Ad", top: 0)
            DTextField(placeholder: "örn. Takip Botu", text: $store.nbName, size: 14)
                .padding(EdgeInsets(top: 10, leading: 12, bottom: 10, trailing: 12))
                .background(RoundedRectangle(cornerRadius: 9).fill(th.inputBg))
            if store.nbEdit == nil {
                kindSegment
            }
            // ÖNCE motor, SONRA model: model adları motora özgüdür
            // (claudecode "opus", opencode "opencode-go/kimi-k3"), yani
            // motor seçilmeden model listesi anlamlı değil.
            fieldLabel("Motor")
            engineDropdown
            fieldLabel("Yapay zeka modeli")
            modelDropdown
        }
        .frame(width: cardInnerW)
        .padding(20)
        .background(RoundedRectangle(cornerRadius: 14).fill(th.cardBg))
        .overlay(RoundedRectangle(cornerRadius: 14).stroke(th.border, lineWidth: 1))
    }

    private var swatches: some View {
        LazyVGrid(columns: Array(repeating: GridItem(.fixed(22), spacing: 14), count: 5),
                  spacing: 14) {
            ForEach(Array(DesignData.nbc.enumerated()), id: \.offset) { i, c in
                Circle()
                    .fill(gradient145(Color(hex: c.0), Color(hex: c.1)))
                    .frame(width: 22, height: 22)
                    .overlay {
                        if i == store.nbC {
                            Circle().stroke(th.cardBg, lineWidth: 2).padding(-2)
                            Circle().stroke(Theme.primary, lineWidth: 2).padding(-4)
                        }
                    }
                    .tap { store.nbC = i }
            }
        }
    }

    private var kindSegment: some View {
        HStack(spacing: 2) {
            ForEach([("bot", "Tek bot"), ("team", "Ekip")], id: \.0) { key, label in
                let on = store.nbKind == key
                Text(label)
                    .font(.system(size: 13))
                    .foregroundStyle(on ? th.text : th.text2)
                    .padding(EdgeInsets(top: 6, leading: 14, bottom: 6, trailing: 14))
                    .frame(maxWidth: .infinity)
                    .background(
                        RoundedRectangle(cornerRadius: 7)
                            .fill(on ? th.winBg : .clear)
                            .shadow(color: .black.opacity(on ? 0.18 : 0), radius: 1, y: 1))
                    .tap { store.nbKind = key }
            }
        }
        .padding(2)
        .background(RoundedRectangle(cornerRadius: 9).fill(th.inputBg))
        .padding(.top, 10)
    }

    private var engineDropdown: some View {
        HStack(spacing: 8) {
            Text(store.nbEngineLabel)
                .font(.system(size: 13))
                .foregroundStyle(th.text)
                .lineLimit(1)
                .frame(maxWidth: .infinity, alignment: .leading)
            Image(systemName: "chevron.down")
                .font(.system(size: 9, weight: .semibold))
                .foregroundStyle(th.text2)
        }
        .padding(EdgeInsets(top: 9, leading: 12, bottom: 9, trailing: 12))
        .background(RoundedRectangle(cornerRadius: 9).fill(th.inputBg))
        .tap { store.openDD = store.openDD == "nbEngine" ? nil : "nbEngine" }
        .overlay(alignment: .topLeading) {
            if store.openDD == "nbEngine" {
                DropMenu(minWidth: cardInnerW) {
                    ForEach(store.live.engines) { e in
                        // Kurulu olmayan motor da listelenir ama durumu
                        // yazılır: gizlemek "neden yok?" sorusunu doğurur,
                        // Modeller ekranına bakmayı gerektirirdi.
                        MenuRow(label: e.available ? e.name : "\(e.name) — kurulu değil",
                                checked: store.nbEngineID == e.id) {
                            store.setNBEngine(e.id)
                            store.openDD = nil
                        }
                    }
                }
                .offset(y: 41)
            }
        }
        // Menü SONRAKİ kardeşlerin ALTINDA kalıyordu: SwiftUI kardeşleri
        // sırayla çizer, "Yapay zeka modeli" etiketi ve model kutusu menünün
        // ÜSTÜNE düşüyordu — menü saydammış gibi görünüyordu (arkası değil,
        // önü sızıyordu). zIndex açıkken menüyü öne alır.
        .zIndex(MenuKat.kat(store.openDD, "nbEngine"))
    }

    private var modelDropdown: some View {
        let curM = store.nbModel ?? ""
        return HStack(spacing: 8) {
            Text(store.nbModelLabel)
                .font(.system(size: 13, design: .monospaced))
                .foregroundStyle(th.text)
                .lineLimit(1)
                .frame(maxWidth: .infinity, alignment: .leading)
            Image(systemName: "chevron.down")
                .font(.system(size: 9, weight: .semibold))
                .foregroundStyle(th.text2)
        }
        .padding(EdgeInsets(top: 9, leading: 12, bottom: 9, trailing: 12))
        .background(RoundedRectangle(cornerRadius: 9).fill(th.inputBg))
        .tap { store.openDD = store.openDD == "nbModel" ? nil : "nbModel" }
        .overlay(alignment: .topLeading) {
            if store.openDD == "nbModel" {
                SearchableMenu(
                    items: store.newBotModels.map { .init(id: $0.id, label: $0.label) },
                    selected: curM, minWidth: cardInnerW,
                    placeholder: "Model ara — ör. kimi, opus, go") { id in
                        store.nbModel = id
                        store.openDD = nil
                    }
                    .offset(y: 41)
            }
        }
        .zIndex(MenuKat.kat(store.openDD, "nbModel"))
    }

    // ── Sağ sütun: davranış ──

    private var behavior: some View {
        VStack(alignment: .leading, spacing: 0) {
            fieldLabel(isTeam
                ? "Ekibin amacı — birlikte neyi başaracaklar"
                : "Görevi — kim olduğunu ve ne yapması gerektiğini anlat", top: 0)
            DTextArea(
                placeholder: "örn. Cevapsız kalan teklifleri izle, 3 gün sonra takip e-postası hazırla, göndermeden önce bana sor.",
                text: $store.nbDesc, size: 13.5, lineSpacing: 3)
                .frame(height: 104)
                .background(RoundedRectangle(cornerRadius: 9).fill(th.inputBg))
            if isTeam {
                fieldLabel("Ekipteki botlar", bottom: 8)
                teamPicks
            } else {
                fieldLabel("Kullanabileceği eklentiler", bottom: 8)
                plugPicks
            }
            advanced
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func fieldLabel(_ text: String, top: CGFloat = 16, bottom: CGFloat = 6) -> some View {
        Text(text)
            .font(.system(size: 12.5))
            .foregroundStyle(th.text2)
            .padding(EdgeInsets(top: top, leading: 0, bottom: bottom, trailing: 0))
    }

    private var teamPicks: some View {
        FlowLayout(spacing: 8, lineSpacing: 8) {
            ForEach(store.allAgents.filter { store.teamOf($0.id) == nil }) { a in
                let on = store.nbMembers.contains(a.id)
                HStack(spacing: 8) {
                    BotIconView(color: Color(hex: a.c2), size: 18)
                    Text(a.name)
                        .font(.system(size: 13))
                        .foregroundStyle(on ? th.text : th.text2)
                }
                .padding(EdgeInsets(top: 6, leading: 9, bottom: 6, trailing: 13))
                .background(Capsule().fill(on ? th.active : .clear))
                .overlay(Capsule().stroke(on ? .clear : th.border, lineWidth: 1))
                .tap {
                    if on { store.nbMembers.remove(a.id) } else { store.nbMembers.insert(a.id) }
                }
            }
        }
    }

    /// Eklenti seçimi = botun allowlist'i (bots.extensions). Liste sunucudaki
    /// KURULU eklentilerden gelir; gömülü olanı kapatmak "çıplak pi" demektir
    /// (onay kapısı oa-gateway'in içinde), o yüzden ipucunda uyarı var.
    private var plugPicks: some View {
        VStack(alignment: .leading, spacing: 8) {
            if store.installedTiles.isEmpty {
                Text(store.live.online ? "Sunucuda kurulu eklenti yok" : "Sunucuya bağlı değil")
                    .font(.system(size: 12.5))
                    .foregroundStyle(th.text3)
            }
            FlowLayout(spacing: 8, lineSpacing: 8) {
                ForEach(store.installedTiles) { p in
                    let on = store.nbPlugs.contains(p.id)
                    Text(p.name)
                        .font(.system(size: 12.5, weight: .medium))
                        .foregroundStyle(on ? .white : th.text2)
                        .padding(EdgeInsets(top: 5, leading: 13, bottom: 5, trailing: 13))
                        .background(RoundedRectangle(cornerRadius: 16).fill(on ? Theme.primary : .clear))
                        .overlay(RoundedRectangle(cornerRadius: 16).stroke(on ? .clear : th.border, lineWidth: 1))
                        .help(p.builtin
                            ? "\(p.desc) — kapatılırsa bot onay kapısı olmadan koşar."
                            : (p.resourceLabel.isEmpty ? p.desc : "\(p.desc) [\(p.resourceLabel)]"))
                        .tap {
                            if on { store.nbPlugs.remove(p.id) } else { store.nbPlugs.insert(p.id) }
                        }
                }
            }
        }
    }

    private var advanced: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 7) {
                Image(systemName: "chevron.right")
                    .font(.system(size: 10, weight: .semibold))
                    .rotationEffect(.degrees(store.nbAdv ? 90 : 0))
                Text("Gelişmiş — talimat metni")
                    .font(.system(size: 12.5))
            }
            .foregroundStyle(th.text2)
            .padding(.top, 20)
            .tap { store.nbAdv.toggle() }
            if store.nbAdv {
                VStack(alignment: .leading, spacing: 0) {
                    Text("Sistem talimatı — botun kalıcı kuralları")
                        .font(.system(size: 12.5))
                        .foregroundStyle(th.text2)
                        .padding(.bottom, 6)
                    DTextArea(
                        placeholder: "örn. Türkçe ve resmi bir dille yaz. Fiyat bilgisi verirken her zaman güncel listeyi doğrula. Müşteriye söz verme, teklif tarihini bana sor.",
                        text: $store.nbPrompt, size: 13.5, lineSpacing: 3.5)
                        .frame(height: 100)
                        .background(RoundedRectangle(cornerRadius: 9).fill(th.inputBg))
                    HStack(spacing: 12) {
                        Text("Yaratıcılık")
                            .font(.system(size: 12.5))
                            .foregroundStyle(th.text2)
                            .frame(width: 110, alignment: .leading)
                        Slider(value: $store.nbTemp, in: 0...100)
                            .tint(Theme.primary)
                        Text(store.nbTempLabel)
                            .font(.system(size: 12.5))
                            .monospacedDigit()
                            .foregroundStyle(th.text3)
                            .frame(width: 32, alignment: .trailing)
                    }
                    .padding(.top, 12)
                }
                .padding(.top, 10)
            }
        }
    }

    // ── Öneriler — tek satırlık şablon rozetleri ──

    private var templates: some View {
        HStack(alignment: .top, spacing: 14) {
            Text("Öneriler")
                .font(.system(size: 12.5))
                .foregroundStyle(th.text2)
                .padding(.top, 7)
            FlowLayout(spacing: 8, lineSpacing: 8) {
                ForEach(DesignData.templates) { t in
                    HStack(spacing: 8) {
                        BotIconView(color: Color(hex: DesignData.nbc[t.c].1), size: 17)
                        Text(t.name)
                            .font(.system(size: 12.5, weight: .medium))
                            .foregroundStyle(th.text2)
                    }
                    .padding(EdgeInsets(top: 6, leading: 9, bottom: 6, trailing: 13))
                    .overlay(Capsule().stroke(th.border, lineWidth: 1))
                    .hoverBg(th.hover, radius: 14)
                    .help(t.desc)
                    .tap { store.applyTemplate(t) }
                }
            }
        }
        .padding(.top, 22)
    }

    // ── Alt eylem çubuğu ──

    private var footer: some View {
        HStack(spacing: 10) {
            Spacer()
            Text("Vazgeç")
                .font(.system(size: 13))
                .foregroundStyle(th.text2)
                .padding(EdgeInsets(top: 8, leading: 16, bottom: 8, trailing: 16))
                .hoverBg(th.hover, radius: 9)
                .tap { store.closeNewBot() }
            Text(btnLabel)
                .font(.system(size: 13.5, weight: .semibold))
                .foregroundStyle(valid ? th.winBg : th.text3)
                .padding(EdgeInsets(top: 9, leading: 20, bottom: 9, trailing: 20))
                .background(RoundedRectangle(cornerRadius: 10).fill(valid ? th.text : th.inputBg))
                .tap { store.createBot() }
        }
        .padding(.horizontal, 24)
        .frame(height: 64)
        .background(th.panelBg)
        .overlay(alignment: .top) { th.border.frame(height: 1) }
    }
}
