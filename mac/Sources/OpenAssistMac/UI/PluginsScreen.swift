import SwiftUI

/// Eklentiler kataloğu — arama, yüklü kutucuklar, kategoriler.
struct PluginsScreen: View {
    @Bindable var store: DesignStore
    @Environment(\.theme) private var th

    var body: some View {
        VStack(spacing: 0) {
            header
            ScrollView {
                VStack(spacing: 0) {
                    hero
                    content
                }
                .frame(maxWidth: 820)
                .frame(maxWidth: .infinity)
                .padding(EdgeInsets(top: 28, leading: 36, bottom: 28, trailing: 36))
            }
        }
        .background(th.winBg)
    }

    private var header: some View {
        HStack {
            Text("Eklentiler")
                .font(.system(size: 15, weight: .semibold))
                .foregroundStyle(th.text)
            WindowDragArea()
            IconButton(icon: "xmark", size: 30, iconSize: 12, help: "Kapat") {
                store.screen = .chat
            }
        }
        .padding(.horizontal, 16)
        .frame(height: 52)
        .overlay(alignment: .bottom) { th.border.frame(height: 1) }
    }

    private var katalogKipi: Bool { store.plugMode == "catalog" }

    private var hero: some View {
        HStack(alignment: .top, spacing: 10) {
            VStack(alignment: .leading, spacing: 6) {
                Text(katalogKipi ? "Paket galerisi" : "Eklentiler")
                    .font(.system(size: 26, weight: .heavy))
                    .foregroundStyle(th.text)
                Text(katalogKipi
                    ? "Kurmak sunucuda üçüncü parti kod çalıştırır. Kurulan paket, botun eklenti listesine eklenene kadar hiçbir bota takılmaz."
                    : "Sunucuda kurulu olanlar. Hangi botun hangisini kullanacağı bot ayarında seçilir.")
                    .font(.system(size: 13.5))
                    .foregroundStyle(th.text2)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            IconButton(icon: "arrow.clockwise", size: 30, iconSize: 13, help: "Yenile") {
                Task {
                    if katalogKipi {
                        await store.live.loadPackageCatalog(q: store.plugQuery)
                    } else {
                        await store.live.refreshExtensions()
                    }
                }
            }
            Text(katalogKipi ? "Kapat" : "Ekle")
                .font(.system(size: 13.5, weight: .semibold))
                .foregroundStyle(th.text)
                .padding(EdgeInsets(top: 6, leading: 14, bottom: 6, trailing: 14))
                .background(RoundedRectangle(cornerRadius: 8).fill(th.inputBg))
                .hoverBg(th.hover, radius: 8)
                .tap { katalogKipi ? store.closePluginCatalog() : store.openPluginCatalog() }
        }
    }

    private var content: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 8) {
                Image(systemName: "magnifyingglass")
                    .font(.system(size: 13))
                    .foregroundStyle(th.text3)
                DTextField(
                    placeholder: katalogKipi ? "Galeride ara — Enter" : "Eklenti ara",
                    text: $store.plugQuery, size: 14,
                    onSubmit: { if katalogKipi { store.searchPluginCatalog() } })
            }
            .padding(.horizontal, 12)
            .frame(height: 38)
            .background(RoundedRectangle(cornerRadius: 10).fill(th.inputBg))

            if katalogKipi {
                katalog
            } else {
                kurulular
            }
        }
        .padding(.top, 22)
    }

    private var kurulular: some View {
        VStack(alignment: .leading, spacing: 0) {
            sectionTitle("Yüklü", size: 17)
                .padding(EdgeInsets(top: 28, leading: 0, bottom: 16, trailing: 0))

            FlowLayout(spacing: 14, lineSpacing: 14) {
                ForEach(store.installedTiles) { t in
                    tile(t.mono).help("\(t.name) — \(t.usageLabel)")
                }
            }
            .padding(.bottom, 20)

            HStack(spacing: 8) {
                ForEach([("all", "Tümü"), ("used", "Kullanımda")], id: \.0) { key, label in
                    let sel = store.plugTab == key
                    Text(label)
                        .font(.system(size: 13.5, weight: .medium))
                        .foregroundStyle(sel ? th.text : th.text2)
                        .padding(EdgeInsets(top: 6, leading: 16, bottom: 6, trailing: 16))
                        .background(RoundedRectangle(cornerRadius: 18).fill(sel ? th.active : th.inputBg))
                        .tap { store.plugTab = key }
                }
            }
            .padding(.bottom, 28)

            if let hint = store.plugEmptyHint {
                Text(hint)
                    .font(.system(size: 13))
                    .foregroundStyle(th.text2)
                    .padding(.vertical, 24)
            }

            ForEach(store.plugCats, id: \.label) { cat in
                VStack(alignment: .leading, spacing: 0) {
                    sectionTitle(cat.label, size: 18)
                        .padding(.bottom, 6)
                    LazyVGrid(
                        columns: [GridItem(.flexible(), spacing: 48), GridItem(.flexible())],
                        alignment: .leading, spacing: 2
                    ) {
                        ForEach(cat.items) { p in
                            pluginRow(p)
                        }
                    }
                }
                .padding(.bottom, 32)
            }
        }
    }

    // ── paket galerisi ─────────────────────────────────────────────────

    private var katalog: some View {
        VStack(alignment: .leading, spacing: 0) {
            sectionTitle("pi paketleri", size: 17)
                .padding(EdgeInsets(top: 28, leading: 0, bottom: 4, trailing: 0))
            Text("Liste npm'deki pi paketlerinden gelir. Burada olmak paketin denetlendiği anlamına GELMEZ — eklentiler sınırsız kod çalıştırır.")
                .font(.system(size: 12))
                .foregroundStyle(th.text3)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.bottom, 14)

            if store.live.packageCatalogLoading {
                Text("Aranıyor…")
                    .font(.system(size: 13))
                    .foregroundStyle(th.text2)
                    .padding(.vertical, 24)
            } else if let hata = store.live.packageCatalogError {
                Text(hata)
                    .font(.system(size: 13))
                    .foregroundStyle(Theme.error)
                    .padding(.vertical, 24)
            } else if store.catalogRows.isEmpty {
                Text("Sonuç yok")
                    .font(.system(size: 13))
                    .foregroundStyle(th.text2)
                    .padding(.vertical, 24)
            }

            ForEach(store.catalogRows) { p in
                catalogRow(p)
            }
        }
    }

    private func catalogRow(_ p: CatalogPackage) -> some View {
        let mesgul = store.live.packageBusy == p.source
        return HStack(alignment: .top, spacing: 12) {
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text(p.name)
                        .font(.system(size: 14.5, weight: .semibold))
                        .foregroundStyle(th.text)
                    Text(p.version)
                        .font(.system(size: 11.5, design: .monospaced))
                        .foregroundStyle(th.text3)
                    if p.monthlyDownloads > 0 {
                        Text("· ayda \(p.monthlyDownloads.formatted(.number.notation(.compactName))) indirme")
                            .font(.system(size: 11.5))
                            .foregroundStyle(th.text3)
                    }
                    if let y = p.publisher, !y.isEmpty {
                        Text("· \(y)")
                            .font(.system(size: 11.5))
                            .foregroundStyle(th.text3)
                    }
                }
                Text(p.description)
                    .font(.system(size: 12.5))
                    .foregroundStyle(th.text2)
                    .lineLimit(2)
                    .fixedSize(horizontal: false, vertical: true)
                // Çakışma notu: paket kötü değil, işi OpenAssist'te zaten
                // yapılıyor. Gizlemek yerine sebebiyle söylüyoruz.
                if let not = p.conflictNote, !not.isEmpty {
                    Text("OpenAssist'te karşılığı var: \(not)")
                        .font(.system(size: 11.5))
                        .foregroundStyle(Theme.warningText)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)

            Text(p.installed ? "kurulu" : (mesgul ? "kuruluyor…" : "Kur"))
                .font(.system(size: 13, weight: .medium))
                .foregroundStyle(p.installed || mesgul ? th.text3 : th.text)
                .padding(EdgeInsets(top: 4, leading: 14, bottom: 4, trailing: 14))
                .overlay(RoundedRectangle(cornerRadius: 14)
                    .stroke(p.installed || mesgul ? .clear : th.border, lineWidth: 1))
                .hoverBg(p.installed || mesgul ? .clear : th.hover, radius: 14)
                .tap {
                    guard !p.installed, !mesgul else { return }
                    store.installPackage(p)
                }
        }
        .padding(.vertical, 11)
    }

    private func sectionTitle(_ label: String, size: CGFloat) -> some View {
        Text(label)
            .font(.system(size: size, weight: .bold))
            .foregroundStyle(th.text)
            .padding(.bottom, 10)
            .frame(maxWidth: .infinity, alignment: .leading)
            .overlay(alignment: .bottom) { th.border.frame(height: 1) }
    }

    private func rozet(_ metin: String) -> some View {
        Text(metin)
            .font(.system(size: 10.5, weight: .semibold))
            .foregroundStyle(th.text3)
            .padding(EdgeInsets(top: 2, leading: 6, bottom: 2, trailing: 6))
            .background(Capsule().fill(th.inputBg))
    }

    private func tile(_ mono: String) -> some View {
        Text(mono)
            .font(.system(size: 14, weight: .bold))
            .foregroundStyle(th.text2)
            .frame(width: 44, height: 44)
            .background(RoundedRectangle(cornerRadius: 11).fill(th.inputBg))
            .overlay(RoundedRectangle(cornerRadius: 11).stroke(th.border, lineWidth: 1))
    }

    /// Satır: eklenti + KİM kullanıyor. Kurulum/kaldırma düğmesi YOK —
    /// eklenti kurmak sunucuda kod çalıştırmak demek ve o akış (paket
    /// kaynağı + sürüm sabitleme) ayrı ele alınıyor; burada olmayan bir
    /// düğmenin sahtesini göstermektense durumu dürüst yazıyoruz.
    private func pluginRow(_ p: DPlugin) -> some View {
        HStack(spacing: 12) {
            tile(p.mono)
            VStack(alignment: .leading, spacing: 0) {
                HStack(spacing: 6) {
                    Text(p.name)
                        .font(.system(size: 14.5, weight: .semibold))
                        .foregroundStyle(th.text)
                        .lineLimit(1)
                    if p.builtin {
                        rozet("gömülü")
                    }
                    // Ne taşıdığı: beceri de en az eklenti kadar iş yaptırır,
                    // satırda görünmesi lazım.
                    if !p.resourceLabel.isEmpty {
                        rozet(p.resourceLabel)
                    }
                }
                Text(p.desc.isEmpty ? p.id : p.desc)
                    .font(.system(size: 12.5))
                    .foregroundStyle(th.text2)
                    .lineLimit(1)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            Text(p.usageLabel)
                .font(.system(size: 12.5))
                .foregroundStyle(th.text3)
                .lineLimit(1)
                // Botların adı ipucunda: kaldırma kararı "kaç bot"a değil
                // "hangi bot"a bakılarak verilir.
                .help(p.usedBy.isEmpty ? "Hiçbir botun eklenti listesinde değil"
                    : p.usedBy.joined(separator: ", "))

            // Kaldırma yalnız KURULAN paketlerde: gömülü eklenti ikilinin
            // parçası, dizine elle konan dosya da bizim yönetimimizde değil.
            if p.kind == "npm" {
                let key = "plug:\(p.id)"
                Text("···")
                    .font(.system(size: 13, weight: .medium))
                    .foregroundStyle(th.text3)
                    .padding(EdgeInsets(top: 4, leading: 10, bottom: 4, trailing: 10))
                    .hoverBg(th.hover, radius: 14)
                    .tap { store.openDD = store.openDD == key ? nil : key }
                    .overlay(alignment: .topTrailing) {
                        if store.openDD == key {
                            DropMenu(minWidth: 130) {
                                MenuRow(label: "Kaldır", color: Theme.error) {
                                    store.openDD = nil
                                    store.removePlugin(p)
                                }
                            }
                            .offset(y: 32)
                            .fixedSize()
                        }
                    }
                    .zIndex(MenuKat.kat(store.openDD, key))
            }
        }
        .padding(.vertical, 11)
        .zIndex(MenuKat.kat(store.openDD, "plug:\(p.id)"))
    }
}
