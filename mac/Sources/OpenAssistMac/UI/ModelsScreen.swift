import SwiftUI

// ── Modeller: sağlayıcılar, model listesi, motor durumu ──
//
// Tek motor (pi) olduğu için ekranın konusu artık "hangi CLI kurulu" değil,
// "hangi sağlayıcıdan hangi modeller çalışıyor". Tarifler sunucunun
// veritabanında yaşar (`/api/providers`) — ortam değişkeninde DEĞİL: yeni bir
// model eklemek ya da dönen bir anahtarı değiştirmek için sunucuya girip kabı
// yeniden başlatmak gerekmesin.
//
// Model adları models.dev kataloğundan seçilir ama katalog OTORİTE DEĞİLDİR:
// yeni çıkmış ya da şirket içi bir model her zaman elle yazılabilir. Katalog
// çekilemezse ekran kilitlenmez, elle giriş yolu açık kalır.

struct ModelsView: View {
    @Bindable var store: DesignStore
    @Environment(\.theme) private var th

    var body: some View {
        VStack(alignment: .leading, spacing: 30) {
            if store.provDraft != nil {
                ProviderEditor(store: store)
            } else {
                // Kart da yükselir, yalnız satır değil (bkz. MenuKat): Motor
                // kartı ve dipnot bu karttan SONRA çizildiği için menünün
                // taşan alt yarısını yutuyorlardı.
                saglayiciKarti
                    .zIndex(MenuKat.kat(store.openDD, "prov:yeni"))
                motorKarti
                dipnot
            }
        }
        .onAppear {
            Task { await store.live.refreshProviders() }
            store.live.loadCatalog()
        }
    }

    // ── sağlayıcı listesi ──

    private var saglayiciKarti: some View {
        SettingsCard(title: "Sağlayıcılar") {
            if store.allProviders.isEmpty {
                SettingsRow(
                    label: "Sağlayıcı yok",
                    sub: store.live.online
                        ? "Model çalıştırmak için en az bir sağlayıcı gerekir."
                        : "Sunucuya bağlı değil — kayıtlar okunamıyor.",
                    first: true) { EmptyView() }
            } else {
                ForEach(Array(store.allProviders.enumerated()), id: \.element.id) { i, p in
                    saglayiciSatiri(p, first: i == 0)
                }
            }
            ekleSatiri
        }
    }

    private func saglayiciSatiri(_ p: ProviderInfo, first: Bool) -> some View {
        SettingsRow(label: p.name.isEmpty ? p.id : p.name,
                    sub: altYazi(p),
                    dot: Self.saglayiciNokta(p),
                    first: first) {
            HStack(spacing: 12) {
                Text("\(p.models.count) model")
                    .font(.system(size: 11, weight: .medium))
                    .foregroundStyle(th.text2)
                    .padding(EdgeInsets(top: 2, leading: 7, bottom: 2, trailing: 7))
                    .background(Capsule().fill(th.inputBg))
                Image(systemName: "chevron.right")
                    .font(.system(size: 10, weight: .semibold))
                    .foregroundStyle(th.text3)
            }
        }
        .tap { store.provAc(p) }
    }

    /// Satırın altında kaydın gerçeği: adres, anahtar durumu, kapalı mı.
    /// Anahtarsız bir sağlayıcı sessizce durmasın — hata koşu denenmeden
    /// görünsün.
    private func altYazi(_ p: ProviderInfo) -> String {
        var parcalar = [p.baseURL]
        if !p.hasKey { parcalar.append("anahtar tanımlı değil") }
        if !p.enabled { parcalar.append("kapalı") }
        if p.models.isEmpty { parcalar.append("model eklenmemiş") }
        return parcalar.joined(separator: " · ")
    }

    /// Durum ışığı: yeşil = koşmaya hazır · sarı = eksik (anahtar/model) ·
    /// gri = kapatılmış. Eşleme sınanabilir olsun diye statik.
    static func saglayiciNokta(_ p: ProviderInfo) -> Color {
        if !p.enabled { return Color(hex: "#8a8a8e") }
        if !p.hasKey || p.models.isEmpty { return Color(hex: "#f5b544") }
        return Color(hex: "#28c840")
    }

    // ── sağlayıcı ekleme ──

    private var ekleSatiri: some View {
        let acik = store.openDD == "prov:yeni"
        // first: false — üstünde her zaman bir satır var (kayıt listesi ya da
        // "sağlayıcı yok" satırı); ayırıcı çizgi kalkmamalı.
        return SettingsRow(label: "Sağlayıcı ekle", sub: katalogDurumu) {
            HStack(spacing: 10) {
                CycleChip(value: store.live.catalogYukleniyor ? "yükleniyor…" : "Katalogdan",
                          open: acik) {
                    store.live.loadCatalog()
                    store.openDD = acik ? nil : "prov:yeni"
                }
                .overlay(alignment: .topTrailing) {
                    if acik {
                        SearchableMenu(items: store.provKatalogSecenekleri, selected: "",
                                       minWidth: 300, placeholder: "Sağlayıcı ara") { id in
                            store.openDD = nil
                            guard let c = store.live.catalog.first(where: { $0.id == id }) else { return }
                            store.provYeni(c)
                        }
                        .offset(y: 33)
                    }
                }
                DButton(label: "Elle", icon: "plus") { store.provYeni(nil) }
            }
        }
        .zIndex(MenuKat.kat(store.openDD, "prov:yeni"))
    }

    private var katalogDurumu: String {
        if let h = store.live.catalogHata {
            return "models.dev kataloğu alınamadı (\(h)) — elle tanımlanabilir"
        }
        if store.live.catalog.isEmpty {
            return "models.dev kataloğundan seç ya da elle tanımla"
        }
        return "models.dev kataloğunda \(store.live.catalog.count) sağlayıcı"
    }

    // ── motor durumu ──
    //
    // Tek motor kaldığı için burası artık ekranın konusu değil, dipnotu:
    // pi kurulu mu ve kaç model seçilebiliyor.

    private var motorKarti: some View {
        SettingsCard(title: "Motor") {
            if store.allEngines.isEmpty {
                SettingsRow(label: "Motor okunamadı",
                            sub: store.live.online
                                ? "Sunucu motor bildirmiyor."
                                : "Sunucuya bağlı değil.",
                            dot: Color(hex: "#ff5f57"), first: true) { yenileDugmesi }
            } else {
                ForEach(Array(store.allEngines.enumerated()), id: \.element.id) { i, e in
                    SettingsRow(label: motorBasligi(e), sub: e.detail,
                                dot: Self.dotColor(e.health), first: i == 0) {
                        yenileDugmesi
                    }
                }
            }
        }
    }

    private func motorBasligi(_ e: EngineInfo) -> String {
        guard let v = e.version, !v.isEmpty else { return e.name }
        return "\(e.name) \(v)"
    }

    private var yenileDugmesi: some View {
        HStack(spacing: 10) {
            Text(store.enginesCheckedLabel)
                .font(.system(size: 12))
                .foregroundStyle(th.text3)
            DButton(label: "Yenile", icon: "arrow.clockwise") { store.live.refreshEngines() }
        }
    }

    private var dipnot: some View {
        Text("Model seçimi bot başınadır — Ayarlar › Botlar. Listede yalnız "
            + "buradaki sağlayıcıların modelleri çıkar; motor da aynı kayıttan okur.")
            .font(.system(size: 12.5))
            .foregroundStyle(th.text3)
            .padding(EdgeInsets(top: 0, leading: 2, bottom: 0, trailing: 2))
    }

    /// Motor durum ışığı: yeşil = koşmaya hazır · sarı = kurulu ama kimlik
    /// yok (ya da yoklanamıyor) · kırmızı = CLI yok.
    static func dotColor(_ h: EngineInfo.Health) -> Color {
        switch h {
        case .ready: Color(hex: "#28c840")
        case .warning: Color(hex: "#f5b544")
        case .missing: Color(hex: "#ff5f57")
        }
    }
}

// ── Sağlayıcı düzenleyici ──
//
// Taslak store'da durur (DesignStore.provDraft): sunucuya yalnız "Kaydet"
// ile gidilir. Her tuşta PUT atmak yarım yazılmış bir adresi kaydeder ve
// aradaki koşuları düşürürdü.

struct ProviderEditor: View {
    @Bindable var store: DesignStore
    @Environment(\.theme) private var th

    var body: some View {
        if let d = store.provDraft {
            VStack(alignment: .leading, spacing: 30) {
                baslik(d)
                // Kart düzeyinde zIndex: satır içindeki zIndex yalnız kartın
                // KENDİ satırları arasında iş görür; aşağıdaki kart ve düğme
                // sırası menünün üstünü kapatırdı (bkz. ModelsView.body).
                kimlikKarti(d)
                    .zIndex(MenuKat.kat(store.openDD, "prov:api"))
                modelKarti(d)
                    .zIndex(MenuKat.kat(store.openDD, "prov:katalog", "prov:model"))
                eylemler(d)
            }
        }
    }


    // ── başlık ──

    private func baslik(_ d: ProviderDraft) -> some View {
        HStack(spacing: 8) {
            Image(systemName: "chevron.left")
                .font(.system(size: 12, weight: .medium))
            Text("Sağlayıcılar")
                .font(.system(size: 13.5, weight: .medium))
        }
        .foregroundStyle(th.text2)
        .padding(EdgeInsets(top: 5, leading: 10, bottom: 5, trailing: 10))
        .hoverBg(th.hover, radius: 8)
        .tap { store.provKapat() }
        .padding(.leading, -10)
    }

    // ── kimlik kartı ──

    private func kimlikKarti(_ d: ProviderDraft) -> some View {
        let acik = store.openDD == "prov:api"
        return SettingsCard(title: d.yeni ? "Yeni sağlayıcı" : (d.name.isEmpty ? d.id : d.name)) {
            SettingsRow(label: "Kimlik",
                        sub: "Modellerin öneki olur (\(onek(d))) — kayıttan sonra değişmez",
                        first: true) {
                if d.yeni {
                    DFieldBox(placeholder: "openrouter", text: metin(\.id), width: 220, mono: true)
                } else {
                    Text(d.id)
                        .font(.system(size: 13, design: .monospaced))
                        .foregroundStyle(th.text2)
                        .textSelection(.enabled)
                }
            }
            SettingsRow(label: "Ad", sub: "Model listelerinde görünen ad") {
                DFieldBox(placeholder: "OpenRouter", text: metin(\.name), width: 220)
            }
            SettingsRow(label: "Taban adres", sub: "Uç noktanın kökü — genelde /v1 ile biter") {
                DFieldBox(placeholder: "https://openrouter.ai/api/v1",
                          text: metin(\.baseURL), width: 300, mono: true)
            }
            SettingsRow(label: "Tel biçimi", sub: "Uç noktanın konuştuğu protokol") {
                CycleChip(value: apiEtiketi(d.api), open: acik) {
                    store.openDD = acik ? nil : "prov:api"
                }
                .overlay(alignment: .topTrailing) {
                    if acik {
                        DropMenu(minWidth: 240) {
                            ForEach(ProviderDraft.apiSecenekleri, id: \.id) { s in
                                MenuRow(label: s.label, checked: d.api == s.id) {
                                    store.provDraft?.api = s.id
                                    store.openDD = nil
                                }
                            }
                        }
                        .offset(y: 33)
                        .fixedSize()
                    }
                }
            }
            .zIndex(MenuKat.kat(store.openDD, "prov:api"))
            anahtarSatiri(d)
            SettingsRow(label: "Etkin",
                        sub: "Kapalıyken bu sağlayıcının modelleri motora hiç verilmez") {
                DToggle(on: bayrak(\.enabled))
            }
        }
    }

    /// Anahtar alanı BOŞ başlar: sunucu değeri hiç göndermez (tek yönlü).
    /// Boş bırakılırsa kayıtlıya dokunulmaz — düzenleme yapmak için anahtarı
    /// yeniden yazmak gerekseydi, kullanıcı her seferinde onu bulmak zorunda
    /// kalırdı.
    private func anahtarSatiri(_ d: ProviderDraft) -> some View {
        SettingsRow(label: "API anahtarı", sub: anahtarAltYazi(d),
                    dot: Color(hex: d.hasKey || !d.key.isEmpty ? "#28c840" : "#f0a020")) {
            HStack(spacing: 10) {
                SecureField("", text: metin(\.key),
                            prompt: Text(d.hasKey ? "değiştirmek için yaz" : "sk-…")
                                .foregroundStyle(th.text3))
                    .textFieldStyle(.plain)
                    .font(.system(size: 13, design: .monospaced))
                    .foregroundStyle(th.text)
                    .disabled(d.keySilinsin)
                    .frame(width: 220)
                    .padding(.horizontal, 10)
                    .frame(height: 30)
                    .background(RoundedRectangle(cornerRadius: 8).fill(th.inputBg))
                if d.hasKey {
                    DButton(label: d.keySilinsin ? "Silinecek" : "Sil",
                            kind: d.keySilinsin ? .danger : .normal) {
                        store.provDraft?.keySilinsin.toggle()
                        if store.provDraft?.keySilinsin == true { store.provDraft?.key = "" }
                    }
                }
            }
        }
    }

    private func anahtarAltYazi(_ d: ProviderDraft) -> String {
        if d.keySilinsin { return "Kayıtlı anahtar kaydedince silinecek" }
        if !d.key.isEmpty { return "Kaydedince değişecek" }
        if d.hasKey { return "Kayıtlı (mühürlü) — boş bırakırsan dokunulmaz" }
        return "Tanımlı değil — anahtar isteyen sağlayıcıda her koşu düşer"
    }

    // ── model kartı ──

    private func modelKarti(_ d: ProviderDraft) -> some View {
        SettingsCard(title: "Modeller (\(d.models.count))") {
            if d.models.isEmpty {
                SettingsRow(label: "Model eklenmedi",
                            sub: "Bu sağlayıcıdan hiçbir model seçilemez.",
                            first: true) { EmptyView() }
            } else {
                ForEach(Array(d.models.enumerated()), id: \.element.id) { i, m in
                    modelSatiri(m, first: i == 0)
                }
            }
            katalogSatiri(d)
            elleSatiri
        }
    }

    private func modelSatiri(_ m: ProviderInfo.Model, first: Bool) -> some View {
        SettingsRow(label: m.id, sub: modelAltYazi(m), first: first, mono: true) {
            IconButton(icon: "xmark", size: 26, iconSize: 10, color: th.text3,
                       help: "Listeden çıkar") {
                store.provModelCikar(m.id)
            }
        }
    }

    private func modelAltYazi(_ m: ProviderInfo.Model) -> String? {
        var parcalar: [String] = []
        if !m.label.isEmpty, m.label != m.id { parcalar.append(m.label) }
        if m.contextWindow > 0 { parcalar.append("\(kisalt(m.contextWindow)) bağlam") }
        if m.maxTokens > 0 { parcalar.append("\(kisalt(m.maxTokens)) çıktı") }
        if m.reasoning { parcalar.append("akıl yürütme") }
        if m.input.contains("image") { parcalar.append("görsel") }
        return parcalar.isEmpty ? nil : parcalar.joined(separator: " · ")
    }

    private func kisalt(_ n: Int) -> String {
        if n >= 1_000_000 { return "\(n / 1_000_000)M" }
        if n >= 1000 { return "\(n / 1000)K" }
        return "\(n)"
    }

    /// Katalogdan model ekleme. Katalog kaydı taslağın kimliğiyle eşleşmezse
    /// (kullanıcı kendi kimliğini vermiş — örn. ikinci hesap için
    /// "minimax-2") hangi katalog kaydından öneri isteneceği elle seçilir.
    private func katalogSatiri(_ d: ProviderDraft) -> some View {
        let kAcik = store.openDD == "prov:katalog"
        let mAcik = store.openDD == "prov:model"
        let katalog = store.provKatalog
        return SettingsRow(label: "Katalogdan ekle",
                           sub: katalog == nil
                               ? "models.dev listesinden bir sağlayıcı seç"
                               : "models.dev · \(katalog?.models.count ?? 0) model") {
            HStack(spacing: 10) {
                CycleChip(value: katalog?.name ?? "Katalog seç", open: kAcik) {
                    store.live.loadCatalog()
                    store.openDD = kAcik ? nil : "prov:katalog"
                }
                .overlay(alignment: .topTrailing) {
                    if kAcik {
                        SearchableMenu(items: store.provKatalogSecenekleri,
                                       selected: store.provKatalogID, minWidth: 300,
                                       placeholder: "Sağlayıcı ara") { id in
                            store.provKatalogID = id
                            store.openDD = nil
                        }
                        .offset(y: 33)
                    }
                }
                DButton(label: "Model seç", icon: "plus", disabled: katalog == nil) {
                    store.openDD = mAcik ? nil : "prov:model"
                }
                .overlay(alignment: .topTrailing) {
                    if mAcik, katalog != nil {
                        SearchableMenu(items: store.provKatalogModelleri, selected: "",
                                       minWidth: 320, placeholder: "Model ara") { id in
                            store.provKatalogModeliEkle(id)
                            store.openDD = nil
                        }
                        .offset(y: 33)
                    }
                }
            }
        }
        .zIndex(MenuKat.kat(store.openDD, "prov:katalog", "prov:model"))
    }

    private var elleSatiri: some View {
        SettingsRow(label: "Elle ekle",
                    sub: "Katalogda olmayan ad (yeni model, şirket içi ince ayar)") {
            HStack(spacing: 10) {
                DTextField(placeholder: "model-adi", text: $store.provYeniModel, mono: true,
                           onSubmit: { store.provElleModelEkle() })
                    .frame(width: 220)
                    .padding(.horizontal, 10)
                    .frame(height: 30)
                    .background(RoundedRectangle(cornerRadius: 8).fill(th.inputBg))
                DButton(label: "Ekle", icon: "plus",
                        disabled: store.provYeniModel.trimmingCharacters(in: .whitespaces).isEmpty) {
                    store.provElleModelEkle()
                }
            }
        }
    }

    // ── eylemler ──

    private func eylemler(_ d: ProviderDraft) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            if let h = d.hata {
                Text(h)
                    .font(.system(size: 12.5))
                    .foregroundStyle(Theme.error)
            }
            HStack(spacing: 10) {
                DButton(label: store.provKaydediyor ? "Kaydediliyor…" : "Kaydet",
                        kind: .primary, disabled: store.provKaydediyor || d.hata != nil) {
                    store.provKaydet()
                }
                DButton(label: "Vazgeç") { store.provKapat() }
                Spacer(minLength: 20)
                silBolumu(d)
            }
        }
        .padding(.horizontal, 2)
    }

    @ViewBuilder
    private func silBolumu(_ d: ProviderDraft) -> some View {
        if !d.yeni {
            if store.provSilOnay {
                HStack(spacing: 10) {
                    Text(silUyarisi(d))
                        .font(.system(size: 12.5))
                        .foregroundStyle(th.text2)
                    DButton(label: "Sil", kind: .danger) { store.provSil() }
                    DButton(label: "Vazgeç") { store.provSilOnay = false }
                }
            } else {
                DButton(label: "Sağlayıcıyı sil", icon: "trash", kind: .danger) {
                    store.provSilOnay = true
                }
            }
        }
    }

    /// Silmeden önce bedeli göster: kullanan bot varsa modelleri geçersiz
    /// kalır ve koşuları düşer.
    private func silUyarisi(_ d: ProviderDraft) -> String {
        let n = store.provKullanim(d.id)
        if n > 0 { return "\(n) bot bu sağlayıcının modelini kullanıyor — koşuları düşer." }
        return "Sağlayıcı, modelleri ve anahtarı silinecek."
    }

    // ── taslak bağları ──
    //
    // Taslak store'da bir struct: alanları doğrudan bağlanamaz, keypath
    // üzerinden sarılır. Nil taslakta yazma işlemsizdir (ekran zaten kapalı).

    private func metin(_ kp: WritableKeyPath<ProviderDraft, String>) -> Binding<String> {
        Binding(get: { store.provDraft?[keyPath: kp] ?? "" },
                set: { store.provDraft?[keyPath: kp] = $0 })
    }

    private func bayrak(_ kp: WritableKeyPath<ProviderDraft, Bool>) -> Binding<Bool> {
        Binding(get: { store.provDraft?[keyPath: kp] ?? false },
                set: { store.provDraft?[keyPath: kp] = $0 })
    }

    private func apiEtiketi(_ id: String) -> String {
        ProviderDraft.apiSecenekleri.first { $0.id == id }?.label ?? id
    }

    private func onek(_ d: ProviderDraft) -> String {
        let k = d.id.trimmingCharacters(in: .whitespaces).lowercased()
        return (k.isEmpty ? "kimlik" : k) + "/model"
    }
}
