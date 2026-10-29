import SwiftUI

/// Ayarlar — sol gezinme (240px) + içerik; 8 sekme.
struct SettingsScreen: View {
    @Bindable var store: DesignStore
    @Environment(\.theme) private var th

    private let navItems: [(id: String, label: String, section: String?)] = [
        ("general", "Genel", "Tercihler"),
        ("appearance", "Görünüm", nil),
        ("notifications", "Bildirimler", nil),
        // Modeller sunucu yapılandırmasıdır (hangi sağlayıcı, hangi model) —
        // tercih değil, yönetim; botun modelini seçtiğimiz yerin yanında.
        ("connections", "Bağlantılar", "Yönetim"),
        ("models", "Modeller", nil),
        ("bots", "Botlar", nil),
        // Hafıza botun kendi öğrendikleridir; burada kullanıcının YAZDIĞI
        // kurallar durur — botun değiştiremediği tek hafıza köşesi.
        ("memory", "Hafıza", nil),
        ("stats", "İstatistikler", nil),
        ("about", "Hakkında", nil),
    ]

    var body: some View {
        HStack(spacing: 0) {
            nav
            content
        }
        .background(th.winBg)
    }

    // ── Gezinme sütunu ──

    private var nav: some View {
        VStack(alignment: .leading, spacing: 0) {
            // Gerçek trafik ışıkları bu bölgenin üzerine biner.
            WindowDragArea().frame(height: 52)
            HStack(spacing: 8) {
                Image(systemName: "chevron.left")
                    .font(.system(size: 12, weight: .medium))
                Text("Sohbete dön")
                    .font(.system(size: 13.5, weight: .medium))
            }
            .foregroundStyle(th.text2)
            .padding(EdgeInsets(top: 7, leading: 10, bottom: 7, trailing: 10))
            .frame(maxWidth: .infinity, alignment: .leading)
            .hoverBg(th.hover, radius: 8)
            .tap { store.screen = .chat }
            .padding(.horizontal, 8)
            .padding(.bottom, 12)
            VStack(alignment: .leading, spacing: 2) {
                ForEach(navItems, id: \.id) { n in
                    if let section = n.section {
                        Text(section)
                            .font(.system(size: 12, weight: .medium))
                            .foregroundStyle(th.text3)
                            .padding(EdgeInsets(top: 14, leading: 10, bottom: 5, trailing: 10))
                    }
                    navRow(n.id, n.label)
                }
            }
            .padding(.horizontal, 8)
            Spacer(minLength: 0)
        }
        .frame(width: 240)
        .frame(maxHeight: .infinity)
        .background(th.sidebarBg)
        .overlay(alignment: .trailing) { th.border.frame(width: 1) }
    }

    private func navRow(_ id: String, _ label: String) -> some View {
        HStack(spacing: 9) {
            navIcon(id)
                .foregroundStyle(th.text2)
                .frame(width: 22, height: 22)
            Text(label)
                .font(.system(size: 13.5))
                .foregroundStyle(th.text)
        }
        .padding(EdgeInsets(top: 6, leading: 8, bottom: 6, trailing: 8))
        .frame(maxWidth: .infinity, alignment: .leading)
        .hoverBg(th.hover, active: th.active, isActive: store.settingsTab == id, radius: 8)
        .tap { store.settingsTab = id }
    }

    @ViewBuilder
    private func navIcon(_ id: String) -> some View {
        switch id {
        case "general": Image(systemName: "gearshape").font(.system(size: 14))
        case "models": Image(systemName: "square.stack.3d.up").font(.system(size: 14))
        case "appearance": Image(systemName: "moon").font(.system(size: 13))
        case "notifications": Image(systemName: "bell").font(.system(size: 14))
        case "connections": Image(systemName: "powerplug").font(.system(size: 14))
        case "bots": botsNavIcon
        case "memory": Image(systemName: "brain").font(.system(size: 14))
        case "stats": Image(systemName: "chart.bar").font(.system(size: 14))
        default: Image(systemName: "info.circle").font(.system(size: 14))
        }
    }

    /// Tasarımdaki "iki gözlü daire" bot simgesi — SF karşılığı yok.
    private var botsNavIcon: some View {
        ZStack {
            Circle().stroke(th.text2, lineWidth: 1.3)
            HStack(spacing: 2.2) {
                Capsule().frame(width: 1.4, height: 2.8)
                Capsule().frame(width: 1.4, height: 2.8)
            }
            .offset(y: -0.4)
        }
        .frame(width: 11.5, height: 11.5)
    }

    // ── İçerik ──

    private var title: String {
        navItems.first { $0.id == store.settingsTab }?.label ?? ""
    }

    private var content: some View {
        VStack(spacing: 0) {
            HStack(spacing: 0) {
                Text(title)
                    .font(.system(size: 15, weight: .semibold))
                    .foregroundStyle(th.text)
                    .padding(.horizontal, 28)
                WindowDragArea()
            }
            .frame(height: 52)
            ScrollView {
                tabContent
                    .padding(EdgeInsets(top: 18, leading: 48, bottom: 44, trailing: 48))
            }
        }
    }

    @ViewBuilder
    private var tabContent: some View {
        // Her sekme aynı kolonda durur: 860 genişlik, yatayda ortalı.
        // (Modeller/İstatistikler/Hakkında eskiden sola yapışıktı — diğer
        // ekranlarla hizasız görünüyordu.)
        Group {
            switch store.settingsTab {
            case "models": ModelsView(store: store)
            case "stats": StatsView(store: store)
            case "about": AboutView(store: store)
            default: groupsView
            }
        }
        .frame(maxWidth: 860, alignment: .leading)
        .frame(maxWidth: .infinity)
    }

    @ViewBuilder
    private var groupsView: some View {
        switch store.settingsTab {
        case "appearance": appearanceTab
        case "notifications": notificationsTab
        case "connections": connectionsTab
        case "bots": botsTab
        case "memory": memoryTab
        default: generalTab
        }
    }

    // ── Hafıza: sabit talimatlar ──
    //
    // Burada yazılan her satır, seçili kapsamdaki botların sistem promptuna
    // HER koşuda girer. Ekran bunu açıkça söylüyor: kullanıcı bir kural
    // yazarken onun maliyetini (bağlam) ve gücünü (her koşu) bilmeli.

    private var memoryTab: some View {
        VStack(alignment: .leading, spacing: 18) {
            card("Sabit talimatlar") {
                rowShell("Kapsam",
                         "Kural seçili kapsamdaki her koşuda geçerlidir",
                         first: true) {
                    memScopeChip
                }
                ForEach(store.instructions) { m in
                    rowShell(m.content, m.scopeLabel) {
                        Image(systemName: "trash")
                            .font(.system(size: 12))
                            .foregroundStyle(th.text3)
                            .frame(width: 26, height: 26)
                            .hoverBg(th.hover, radius: 7)
                            .tap { store.removeInstruction(m) }
                            .disabled(store.live.instructionBusy)
                    }
                }
                if let hint = store.memEmptyHint {
                    rowShell(hint, nil) { EmptyView() }
                }
                rowShell("Yeni kural", "Örnek: “Onaysız e-posta gönderme”") {
                    HStack(spacing: 8) {
                        TextField("Kuralı yaz", text: $store.memDraft)
                            .textFieldStyle(.plain)
                            .font(.system(size: 13))
                            .frame(width: 300)
                            .padding(EdgeInsets(top: 6, leading: 9, bottom: 6, trailing: 9))
                            .background(th.inputBg, in: RoundedRectangle(cornerRadius: 8))
                            .overlay(RoundedRectangle(cornerRadius: 8).stroke(th.border, lineWidth: 1))
                            .onSubmit { store.addInstruction() }
                        Text("Ekle")
                            .font(.system(size: 12.5, weight: .medium))
                            .padding(EdgeInsets(top: 6, leading: 12, bottom: 6, trailing: 12))
                            .background(th.inputBg, in: RoundedRectangle(cornerRadius: 8))
                            .overlay(RoundedRectangle(cornerRadius: 8).stroke(th.border, lineWidth: 1))
                            .hoverBg(th.hover, radius: 8)
                            .tap { store.addInstruction() }
                    }
                }
            }
            .zIndex(MenuKat.kat(store.openDD, "mem:scope"))
            Text("""
            Sabit talimatlar hafızanın geri kalanından ayrı tutulur: gece \
            konsolidasyonu onları birleştiremez ya da eleyemez, botun kendisi \
            de yazamaz. Buraya yalnız sen yazarsın. En fazla 20 kural \
            gösterilir — fazlası modele hiç ulaşmaz.
            """)
            .font(.system(size: 12))
            .foregroundStyle(th.text3)
            .fixedSize(horizontal: false, vertical: true)
        }
        .onAppear { store.loadInstructions() }
    }

    private var memScopeChip: some View {
        let open = store.openDD == "mem:scope"
        let ad = store.memBot.flatMap { id in store.allAgents.first { $0.id == id }?.name } ?? "Tüm botlar"
        return CycleChip(value: ad, open: open) {
            store.openDD = open ? nil : "mem:scope"
        }
        .overlay(alignment: .topTrailing) {
            if open {
                DropMenu(minWidth: 200) {
                    MenuRow(label: "Tüm botlar", checked: store.memBot == nil) {
                        store.selectInstructionBot(nil)
                        store.openDD = nil
                    }
                    ForEach(store.allAgents) { a in
                        MenuRow(label: a.name, checked: store.memBot == a.id) {
                            store.selectInstructionBot(a.id)
                            store.openDD = nil
                        }
                    }
                }
                .offset(y: 33)
                .fixedSize()
            }
        }
    }

    // ── Sekmeler ──

    private var generalTab: some View {
        // "Otomatik güncelleme" ve "Dil" satırları kaldırıldı: güncelleyici
        // yok, arayüz tek dilli — hiçbir şey yapmayan anahtar, olmayan
        // anahtardan kötüdür.
        card("Uygulama") {
            // Giriş öğesi sisteme yazılır; paketsiz koşuda kaydolamaz.
            rowShell("Başlangıçta çalıştır",
                     LoginItem.available
                        ? "Oturum açıldığında OpenAssist otomatik başlar"
                        : AppEnvironment.unbundledNote,
                     first: true) {
                Toggle("", isOn: Binding(get: { store.prefs.launchAtLogin },
                                         set: { store.setLaunchAtLogin($0) }))
                    .labelsHidden()
                    .toggleStyle(.switch)
                    .disabled(!LoginItem.available)
            }
            toggleRow("Menü çubuğunda göster", "Uygulama simgesi menü çubuğunda görünür",
                      $store.prefs.menubar)
        }
    }

    private var appearanceTab: some View {
        card("Uygulama teması") {
            rowShell("Tema", "Seçim kalıcıdır; \"Sistem\" macOS görünümünü izler",
                     first: true) {
                segControl([
                    ("Sistem", store.themeChoice == "system", { store.themeChoice = "system" }),
                    ("Açık", store.themeChoice == "light", { store.themeChoice = "light" }),
                    ("Koyu", store.themeChoice == "dark", { store.themeChoice = "dark" }),
                ])
            }
        }
    }

    private var notificationsTab: some View {
        VStack(spacing: 30) {
            card("Uyarılar") {
                toggleRow("Bot yanıtları", "Bot bir işi bitirdiğinde bildir",
                          $store.prefs.notifReplies, first: true)
                toggleRow("Onay bekleyen taslaklar", "Bir istek onayını beklediğinde bildir",
                          $store.prefs.notifApprovals)
                toggleRow("Rutin tamamlandığında", "Zamanlanmış iş bittiğinde bildir",
                          $store.prefs.notifRoutines)
                toggleRow("Bildirim sesi", "Bildirimler sesle gelsin", $store.prefs.sound)
            }
            // İzin durumu gizlenmez: macOS reddettiyse anahtarlar açık
            // görünüp hiçbir bildirim gelmezdi.
            card("Sistem izni") {
                rowShell("Durum", store.live.notifier.statusLine,
                         dot: Color(hex: store.notifDotHex), first: true) {
                    EmptyView()
                }
            }
        }
    }

    private var connectionsTab: some View {
        VStack(spacing: 30) {
            card("Sunucu") {
                rowShell("Adres", "Botların yaşadığı core sunucusu (özel API)",
                         dot: Color(hex: store.live.online ? "#28c840" : "#ff5f57"), first: true) {
                    DTextField(placeholder: "http://127.0.0.1:8080",
                               text: $store.serverURL, size: 13)
                        .frame(width: 240)
                        .padding(.horizontal, 10)
                        .frame(height: 30)
                        .background(RoundedRectangle(cornerRadius: 8).fill(th.inputBg))
                }
                rowShell("API belirteci",
                         store.apiToken.isEmpty
                             ? "Sunucunun API_TOKEN değeri — kurulumda üretilir"
                             : "Kaydedildi (keychain)",
                         dot: Color(hex: store.apiToken.isEmpty ? "#f0a020" : "#28c840")) {
                    // SecureField: belirteç ekranda düz durmasın (omuz
                    // üstünden okuma, ekran paylaşımı, ekran görüntüsü).
                    SecureField("", text: $store.apiToken,
                                prompt: Text("openssl rand -hex 32").foregroundStyle(th.text3))
                        .textFieldStyle(.plain)
                        .font(.system(size: 13, design: .monospaced))
                        .foregroundStyle(th.text)
                        .frame(width: 240)
                        .padding(.horizontal, 10)
                        .frame(height: 30)
                        .background(RoundedRectangle(cornerRadius: 8).fill(th.inputBg))
                }
                rowShell("Durum", store.serverStateLabel, first: false) {
                    Text(store.live.online ? "Bağlı" : "Kapalı")
                        .font(.system(size: 13, weight: .medium))
                        .foregroundStyle(th.text2)
                }
                rowShell("Bu Mac (yerel araçlar)",
                         store.live.runner.connected
                             ? "Botlar dosya okuma/yazma için bu Mac'i kullanabilir"
                             : (store.live.runner.lastError ?? "Bağlı değil — sunucuya erişilemiyor"),
                         dot: Color(hex: store.live.runner.connected ? "#28c840" : "#f0a020")) {
                    Text(store.live.runner.connected ? "Aktif" : "Kapalı")
                        .font(.system(size: 13, weight: .medium))
                        .foregroundStyle(th.text2)
                }
            }
            card("Servisler") {
                let conns = store.conns(for: store.active)
                ForEach(Array(conns.enumerated()), id: \.offset) { i, c in
                    rowShell(c.name, c.on ? "Bağlı" : "Bağlı değil",
                             dot: Color(hex: c.on ? "#28c840" : "#ff5f57"), first: i == 0) {
                        Text(c.on ? "Bağlı" : "Sohbetten bağla")
                            .font(.system(size: 13, weight: .medium))
                            .foregroundStyle(th.text2)
                    }
                }
                if conns.isEmpty {
                    rowShell("Konektör yok", "Bota \"Gmail'i bağla\" yazarak kurulumu başlat (K13)",
                             first: true) { EmptyView() }
                }
            }
        }
    }

    private var botsTab: some View {
        VStack(spacing: 30) {
            // Bu kart sunucudaki onay politikasını yazar (K17): kota ve
            // sessiz saatler orkestratörün veto sırasında işler.
            card("Bot davranışı") {
                cycleRow("Günlük eylem kotası", "Aşılınca otomatik izinler durur, her eylem sorulur",
                         key: "set:dailyLimit", value: store.prefs.dailyLimit.isEmpty ? "sınırsız" : store.prefs.dailyLimit,
                         options: ["sınırsız", "10", "25", "50", "100"], first: true) {
                    store.prefs.dailyLimit = $0 == "sınırsız" ? "" : $0
                    store.savePolicyFromPrefs()
                }
                toggleRow("Sessiz saatlerde beklet", "22:00–07:00 arası dış eylemler sabaha onaya düşer",
                          Binding(get: { store.prefs.quietHours },
                                  set: { store.prefs.quietHours = $0; store.savePolicyFromPrefs() }))
            }
            .zIndex(MenuKat.kat(store.openDD, "set:dailyLimit"))
            .onAppear { store.loadPolicyIntoPrefs() }
            // Motor ve model bot başınadır (K18): her bot ayrı CLI ve ayrı
            // model kullanabilir. Seçenekler sunucudan (`/api/engines`)
            // gelir — model listesi seçili motorun yeteneğiyle sınırlıdır.
            // Kart da yükselir (bkz. MenuKat): bugün en alttaki kart olduğu
            // için eksikliği görünmüyordu, altına bir kart eklendiği gün
            // model menüsünün alt yarısı onun altında kalırdı.
            card("Bot motoru ve modeli") {
                ForEach(store.allAgents) { a in
                    botEngineModelRow(a, first: a.id == store.allAgents.first?.id)
                }
            }
            .zIndex(MenuKat.onEk(store.openDD, "be:", "bm:"))
        }
    }

    private func botEngineModelRow(_ a: DAgent, first: Bool) -> some View {
        let eKey = "be:\(a.id)", mKey = "bm:\(a.id)"
        let eOpen = store.openDD == eKey, mOpen = store.openDD == mKey
        let curE = store.engineID(for: a.id)
        let curM = store.live.bot(id: a.id)?.model ?? ""
        return rowShell(a.name, "motor + model", first: first) {
            HStack(spacing: 10) {
                CycleChip(value: store.engineName(for: a.id), open: eOpen) {
                    store.openDD = eOpen ? nil : eKey
                }
                .overlay(alignment: .topTrailing) {
                    if eOpen {
                        DropMenu(minWidth: 180) {
                            ForEach(store.allEngines) { e in
                                MenuRow(label: e.name, checked: curE == e.id) {
                                    store.setEngine(a.id, e.id)
                                    store.openDD = nil
                                }
                            }
                        }
                        .offset(y: 33)
                        .fixedSize()
                    }
                }
                CycleChip(value: store.modelName(for: a.id), open: mOpen) {
                    store.openDD = mOpen ? nil : mKey
                }
                .overlay(alignment: .topTrailing) {
                    if mOpen {
                        SearchableMenu(
                            items: store.models(for: a.id).map { .init(id: $0.id, label: $0.label) },
                            selected: curM, minWidth: 260,
                            placeholder: "Model ara") { id in
                                store.setModel(a.id, id)
                                store.openDD = nil
                            }
                            // fixedSize YOK: ideal boyuta genişlemek
                            // SearchableMenu'nün 260pt tavanını ezerdi ve
                            // menü yine pencereden taşardı.
                            .offset(y: 33)
                    }
                }
            }
        }
        .zIndex(MenuKat.kat(store.openDD, eKey, mKey))
    }

    // ── Kart / satır kalıpları ──

    // Kart ve satır kalıpları UIHelpers'ta yaşar (Modeller ekranı da aynısını
    // kullanır); buradakiler yalnız çağrı yerlerini kısaltan sarmallar.

    private func card(_ label: String? = nil, @ViewBuilder rows: () -> some View) -> some View {
        SettingsCard(title: label, rows: rows)
    }

    private func rowShell(_ label: String, _ sub: String?, dot: Color? = nil, first: Bool = false,
                          @ViewBuilder control: () -> some View) -> some View {
        SettingsRow(label: label, sub: sub, dot: dot, first: first, control: control)
    }

    private func toggleRow(_ label: String, _ sub: String?, _ on: Binding<Bool>, first: Bool = false) -> some View {
        rowShell(label, sub, first: first) { DToggle(on: on) }
    }

    private func cycleRow(_ label: String, _ sub: String?, key: String, value: String,
                          options: [String], first: Bool = false,
                          set: @escaping (String) -> Void) -> some View {
        let open = store.openDD == key
        return rowShell(label, sub, first: first) {
            CycleChip(value: value, open: open) {
                store.openDD = open ? nil : key
            }
            .overlay(alignment: .topTrailing) {
                if open {
                    DropMenu(minWidth: 180) {
                        ForEach(options, id: \.self) { o in
                            MenuRow(label: o, checked: value == o) {
                                set(o)
                                store.openDD = nil
                            }
                        }
                    }
                    .offset(y: 33)
                    .fixedSize()
                }
            }
        }
        .zIndex(MenuKat.kat(store.openDD, key))
    }

    private func segControl(_ segs: [(String, Bool, () -> Void)]) -> some View {
        HStack(spacing: 2) {
            ForEach(Array(segs.enumerated()), id: \.offset) { _, s in
                Text(s.0)
                    .font(.system(size: 13))
                    .foregroundStyle(s.1 ? th.text : th.text2)
                    .padding(EdgeInsets(top: 4, leading: 14, bottom: 4, trailing: 14))
                    .background(
                        RoundedRectangle(cornerRadius: 6)
                            .fill(s.1 ? th.winBg : .clear)
                            .shadow(color: .black.opacity(s.1 ? 0.18 : 0), radius: 1, y: 1))
                    .tap(s.2)
            }
        }
        .padding(2)
        .background(RoundedRectangle(cornerRadius: 8).fill(th.inputBg))
    }
}

// ── İstatistikler ──

struct StatsView: View {
    @Bindable var store: DesignStore
    @Environment(\.theme) private var th

    var body: some View {
        VStack(spacing: 16) {
            topRow
                .zIndex(MenuKat.kat(store.openDD, "statBot"))
            cards
            dailyCard
            usageCard
        }
        .frame(maxWidth: 640, alignment: .leading)
    }

    private var topRow: some View {
        HStack {
            botDropdown
            Spacer()
            HStack(spacing: 2) {
                seg("Son 7 gün", "7")
                seg("Son 30 gün", "30")
            }
            .padding(2)
            .background(RoundedRectangle(cornerRadius: 8).fill(th.inputBg))
        }
    }

    private var botDropdown: some View {
        let open = store.openDD == "statBot"
        return HStack(spacing: 7) {
            Text(store.statBotLabel)
                .font(.system(size: 13))
                .foregroundStyle(th.text)
            Image(systemName: "chevron.down")
                .font(.system(size: 9, weight: .semibold))
                .foregroundStyle(th.text2)
        }
        .padding(EdgeInsets(top: 5, leading: 11, bottom: 5, trailing: 11))
        .background(RoundedRectangle(cornerRadius: 7).fill(th.inputBg))
        .tap { store.openDD = open ? nil : "statBot" }
        .overlay(alignment: .topLeading) {
            if open {
                DropMenu(minWidth: 200) {
                    MenuRow(label: "Tüm botlar", checked: store.statBot == nil) {
                        store.statBot = nil
                        store.openDD = nil
                    }
                    ForEach(store.botUsage, id: \.0) { u in
                        MenuRow(label: u.0, checked: store.statBot == u.0) {
                            store.statBot = u.0
                            store.openDD = nil
                        }
                    }
                }
                .offset(y: 33)
                .fixedSize()
            }
        }
    }

    private func seg(_ label: String, _ val: String) -> some View {
        let on = store.range == val
        return Text(label)
            .font(.system(size: 13))
            .foregroundStyle(on ? th.text : th.text2)
            .padding(EdgeInsets(top: 4, leading: 14, bottom: 4, trailing: 14))
            .background(
                RoundedRectangle(cornerRadius: 6)
                    .fill(on ? th.winBg : .clear)
                    .shadow(color: .black.opacity(on ? 0.18 : 0), radius: 1, y: 1))
            .tap { store.range = val }
    }

    private var cards: some View {
        LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: 12), count: 3),
                  alignment: .leading, spacing: 12) {
            ForEach(store.statCards) { c in
                VStack(alignment: .leading, spacing: 0) {
                    Text(c.label)
                        .font(.system(size: 12.5))
                        .foregroundStyle(th.text2)
                    Text(c.value)
                        .font(.system(size: c.big ? 24 : 15, weight: .bold))
                        .foregroundStyle(th.text)
                        .lineLimit(1)
                        .padding(.top, 4)
                    if let sub = c.sub {
                        Text(sub)
                            .font(.system(size: 11.5))
                            .foregroundStyle(th.text3)
                            .padding(.top, 2)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(EdgeInsets(top: 13, leading: 15, bottom: 13, trailing: 15))
                .background(RoundedRectangle(cornerRadius: 11).fill(th.cardBg))
                .overlay(RoundedRectangle(cornerRadius: 11).stroke(th.border, lineWidth: 1))
            }
        }
    }

    private var dailyCard: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("Günlük mesaj")
                .font(.system(size: 13.5, weight: .semibold))
                .foregroundStyle(th.text)
                .padding(.bottom, 14)
            HStack(alignment: .bottom, spacing: 4) {
                ForEach(store.statBars) { b in
                    UnevenRoundedRectangle(topLeadingRadius: 3, topTrailingRadius: 3)
                        .fill(Theme.primary)
                        .frame(height: max(3, b.h))
                        .frame(maxWidth: .infinity)
                }
            }
            .frame(height: 120, alignment: .bottom)
            HStack(spacing: 4) {
                ForEach(store.statBars) { b in
                    Color.clear
                        .frame(maxWidth: .infinity)
                        .frame(height: 12)
                        .overlay {
                            Text(b.label)
                                .font(.system(size: 9.5))
                                .foregroundStyle(th.text3)
                                .fixedSize()
                        }
                }
            }
            .padding(.top, 6)
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 11).fill(th.cardBg))
        .overlay(RoundedRectangle(cornerRadius: 11).stroke(th.border, lineWidth: 1))
    }

    private var usageCard: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("Bot kullanımı")
                .font(.system(size: 13.5, weight: .semibold))
                .foregroundStyle(th.text)
                .padding(.bottom, 14)
            VStack(spacing: 11) {
                ForEach(store.botUsage, id: \.0) { u in
                    HStack(spacing: 12) {
                        Text(u.0)
                            .font(.system(size: 13))
                            .foregroundStyle(th.text)
                            .lineLimit(1)
                            .frame(width: 150, alignment: .leading)
                        GeometryReader { geo in
                            ZStack(alignment: .leading) {
                                RoundedRectangle(cornerRadius: 3).fill(th.inputBg)
                                RoundedRectangle(cornerRadius: 3)
                                    .fill(Theme.primary)
                                    .frame(width: geo.size.width * CGFloat(u.1) / 100)
                            }
                        }
                        .frame(height: 6)
                        Text("%\(u.1)")
                            .font(.system(size: 12))
                            .foregroundStyle(th.text2)
                            .frame(width: 38, alignment: .trailing)
                    }
                }
            }
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 11).fill(th.cardBg))
        .overlay(RoundedRectangle(cornerRadius: 11).stroke(th.border, lineWidth: 1))
    }
}

// ── Hakkında ──

struct AboutView: View {
    @Bindable var store: DesignStore
    @Environment(\.theme) private var th

    var body: some View {
        VStack(spacing: 20) {
            VStack(spacing: 0) {
                BotIconView(color: Color(hex: "#7b4fd6"), size: 84)
                Text("OpenAssist")
                    .font(.system(size: 22, weight: .heavy))
                    .foregroundStyle(th.text)
                    .padding(.top, 14)
                // Sürüm pakete sorulur; "Güncellemeleri denetle" düğmesi
                // kaldırıldı — güncelleyici yok, tıklayınca "güncel" demek
                // kontrol etmek değil, öyle yazmaktı.
                Text(AppEnvironment.versionLine)
                    .font(.system(size: 13))
                    .foregroundStyle(th.text2)
                    .padding(.top, 3)
            }
            .frame(maxWidth: .infinity)
            .padding(.top, 14)
            VStack(spacing: 0) {
                ForEach(Array(store.aboutRows.enumerated()), id: \.offset) { i, r in
                    HStack {
                        Text(r.0)
                            .font(.system(size: 13.5))
                            .foregroundStyle(th.text)
                            .frame(maxWidth: .infinity, alignment: .leading)
                        Text(r.1)
                            .font(.system(size: 13.5))
                            .foregroundStyle(th.text2)
                    }
                    .padding(EdgeInsets(top: 10, leading: 14, bottom: 10, trailing: 14))
                    .overlay(alignment: .top) {
                        if i > 0 { th.border.frame(height: 1) }
                    }
                }
            }
            .background(RoundedRectangle(cornerRadius: 11).fill(th.cardBg))
            .overlay(RoundedRectangle(cornerRadius: 11).stroke(th.border, lineWidth: 1))
            // Boş `tap {}` yerine gerçek hedefler: depo ve dokümanlar.
            HStack(spacing: 18) {
                ForEach(DesignStore.aboutLinks, id: \.0) { label, url in
                    Text(label)
                        .font(.system(size: 12.5))
                        .foregroundStyle(th.text2)
                        .tap { NSWorkspace.shared.open(url) }
                }
            }
            .frame(maxWidth: .infinity)
            Text("OpenAssist — kendi sunucunda çalışan kişisel asistan.")
                .font(.system(size: 12))
                .foregroundStyle(th.text3)
                .frame(maxWidth: .infinity)
                .padding(.bottom, 8)
        }
        .frame(maxWidth: 560, alignment: .leading)
    }
}
