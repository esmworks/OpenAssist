import SwiftUI

// Ortak küçük parçalar — tasarımdaki hover/menü/anahtar/giriş kalıpları.

/// Hover'da arka plan; aktifken kalıcı arka plan.
struct HoverBg: ViewModifier {
    var hover: Color
    var active: Color?
    var isActive: Bool
    var radius: CGFloat
    @State private var over = false

    func body(content: Content) -> some View {
        content
            .background(
                RoundedRectangle(cornerRadius: radius)
                    .fill(isActive ? (active ?? hover) : (over ? hover : .clear)))
            .onHover { over = $0 }
    }
}

extension View {
    func hoverBg(_ hover: Color, active: Color? = nil, isActive: Bool = false, radius: CGFloat) -> some View {
        modifier(HoverBg(hover: hover, active: active, isActive: isActive, radius: radius))
    }

    /// Tıklanabilir alan + el imleci.
    func tap(_ action: @escaping () -> Void) -> some View {
        contentShape(Rectangle())
            .onTapGesture(perform: action)
    }
}

/// Kare simge düğmesi (28/30 px).
struct IconButton: View {
    var icon: String
    var size: CGFloat
    var iconSize: CGFloat
    var radius: CGFloat = 8
    var color: Color?
    var help: String = ""
    var action: () -> Void
    @Environment(\.theme) private var th

    var body: some View {
        Image(systemName: icon)
            .font(.system(size: iconSize, weight: .medium))
            .foregroundStyle(color ?? th.text2)
            .frame(width: size, height: size)
            .hoverBg(th.hover, radius: radius)
            .tap(action)
            .help(help)
    }
}

/// Tasarımdaki 38×23 anahtar (toggle).
struct DToggle: View {
    @Binding var on: Bool

    var body: some View {
        ZStack(alignment: .topLeading) {
            RoundedRectangle(cornerRadius: 12)
                .fill(on ? Theme.primary : Color(red: 120 / 255, green: 120 / 255, blue: 128 / 255).opacity(0.32))
            Circle()
                .fill(.white)
                .frame(width: 19, height: 19)
                .shadow(color: .black.opacity(0.25), radius: 1, y: 1)
                .offset(x: on ? 17 : 2, y: 2)
        }
        .frame(width: 38, height: 23)
        .animation(.easeInOut(duration: 0.18), value: on)
        .tap { on.toggle() }
    }
}

/// Sade metin girişi — placeholder rengi text3.
struct DTextField: View {
    var placeholder: String
    @Binding var text: String
    var size: CGFloat = 13
    var mono = false
    var onSubmit: () -> Void = {}
    @Environment(\.theme) private var th

    var body: some View {
        TextField("", text: $text, prompt: Text(placeholder).foregroundStyle(th.text3))
            .textFieldStyle(.plain)
            .font(mono ? .system(size: size, design: .monospaced) : .system(size: size))
            .foregroundStyle(th.text)
            .onSubmit(onSubmit)
    }
}

/// Placeholder'lı çok satırlı giriş (textarea karşılığı).
struct DTextArea: View {
    var placeholder: String
    @Binding var text: String
    var size: CGFloat = 13.5
    var lineSpacing: CGFloat = 5
    @Environment(\.theme) private var th

    var body: some View {
        ZStack(alignment: .topLeading) {
            if text.isEmpty {
                Text(placeholder)
                    .font(.system(size: size))
                    .lineSpacing(lineSpacing)
                    .foregroundStyle(th.text3)
                    .padding(.top, 8)
                    .padding(.leading, 12)
                    .allowsHitTesting(false)
            }
            TextEditor(text: $text)
                .font(.system(size: size))
                .lineSpacing(lineSpacing)
                .foregroundStyle(th.text)
                .scrollContentBackground(.hidden)
                .padding(.top, 4)
                .padding(.horizontal, 8)
        }
    }
}

/// Açılır menü kapsayıcısı (winBg + border + gölge, pad 4).
struct DropMenu<Content: View>: View {
    var minWidth: CGFloat = 160
    @ViewBuilder var content: Content
    @Environment(\.theme) private var th

    var body: some View {
        VStack(alignment: .leading, spacing: 0) { content }
            .padding(4)
            .frame(minWidth: minWidth, alignment: .leading)
            .background(RoundedRectangle(cornerRadius: 10).fill(th.winBg))
            .overlay(RoundedRectangle(cornerRadius: 10).stroke(th.border, lineWidth: 1))
            .shadow(color: .black.opacity(0.18), radius: 15, y: 10)
    }
}

/// Açılır menülerin çizim katı.
///
/// SwiftUI kardeşleri SIRAYLA çizer ve `zIndex` yalnız KARDEŞLER arasında sıra
/// belirler. Menüler kendi kutularının sınırlarını taşan overlay'lerdir, yani
/// menüyü taşıyan satır yükseltilse bile ONU İÇEREN KART yükseltilmezse
/// menünün taşan kısmı sonraki kartların altında kalır — bu hata iki kez
/// yaşandı (yeni bot modalında "Yapay zeka modeli" kutusu, Modeller'de Motor
/// kartı menünün alt yarısını yutuyordu).
///
/// Kural: menü hangi kutunun içindeyse, o kutudan KÖKE kadar her katman aynı
/// anahtarla yükseltilir. Ternary'ler yerine burada toplanıyor ki kural
/// sınanabilsin ve bir katman unutulduğunda gözden kaçmasın.
enum MenuKat {
    static let ustte: Double = 10
    static let normal: Double = 0

    /// Verilen anahtarlardan biri açıksa üst kat, değilse normal.
    static func kat(_ openDD: String?, _ anahtarlar: String...) -> Double {
        katalog(openDD, anahtarlar)
    }

    /// Dizi alan hâli — testler için.
    static func katalog(_ openDD: String?, _ anahtarlar: [String]) -> Double {
        guard let openDD, anahtarlar.contains(openDD) else { return normal }
        return ustte
    }

    /// Anahtarı çalışma anında üretilen menüler (bot başına "be:<id>",
    /// "bm:<id>") için önek eşlemesi.
    static func onEk(_ openDD: String?, _ onEkler: String...) -> Double {
        guard let openDD, onEkler.contains(where: openDD.hasPrefix) else { return normal }
        return ustte
    }
}

/// Menü satırı: fs13, pad 7×11, r7; isteğe bağlı onay imi.
struct MenuRow: View {
    var label: String
    var icon: String?
    var color: Color?
    var checked = false
    var action: () -> Void
    @Environment(\.theme) private var th

    var body: some View {
        HStack(spacing: 9) {
            if let icon {
                Image(systemName: icon)
                    .font(.system(size: 13, weight: .medium))
            }
            Text(label)
                .font(.system(size: 13))
            if checked {
                Spacer(minLength: 12)
                Image(systemName: "checkmark")
                    .font(.system(size: 10, weight: .semibold))
            }
        }
        .foregroundStyle(color ?? th.text)
        .padding(EdgeInsets(top: 7, leading: 11, bottom: 7, trailing: 11))
        .frame(maxWidth: .infinity, alignment: .leading)
        .hoverBg(th.hover, radius: 7)
        .tap(action)
    }
}

/// Ayarlar kartı — başlık + çerçeveli satır yığını.
///
/// Kart çerçevesi ARKA planda: overlay olsaydı satırların üstüne çizilirdi ve
/// son satırdan aşağı açılan menü, kartın alt kenar çizgisi menünün ortasından
/// geçtiği için saydam görünürdü. Satır içeriği kenarları boyamaz — çerçeve
/// arkada da tam görünür.
struct SettingsCard<Content: View>: View {
    var title: String? = nil
    @ViewBuilder var rows: Content
    @Environment(\.theme) private var th

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            if let title {
                Text(title)
                    .font(.system(size: 15, weight: .semibold))
                    .foregroundStyle(th.text)
                    .padding(EdgeInsets(top: 0, leading: 2, bottom: 12, trailing: 2))
            }
            VStack(spacing: 0) { rows }
                .background {
                    RoundedRectangle(cornerRadius: 11).fill(th.cardBg)
                    RoundedRectangle(cornerRadius: 11).strokeBorder(th.border, lineWidth: 1)
                }
        }
    }
}

/// Ayarlar satırı — etiket + alt açıklama, sağda denetim.
struct SettingsRow<Control: View>: View {
    var label: String
    var sub: String? = nil
    var dot: Color? = nil
    var first = false
    var mono = false
    @ViewBuilder var control: Control
    @Environment(\.theme) private var th

    var body: some View {
        HStack(spacing: 16) {
            if let dot {
                Circle().fill(dot).frame(width: 8, height: 8)
            }
            VStack(alignment: .leading, spacing: 0) {
                Text(label)
                    .font(mono
                          ? .system(size: 13.5, design: .monospaced)
                          : .system(size: 14.5, weight: .semibold))
                    .foregroundStyle(th.text)
                if let sub {
                    Text(sub)
                        .font(.system(size: 13))
                        .lineSpacing(3)
                        .foregroundStyle(th.text2)
                        .padding(.top, 3)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            control
        }
        .padding(EdgeInsets(top: 15, leading: 18, bottom: 15, trailing: 18))
        .overlay(alignment: .top) {
            if !first { th.border.frame(height: 1) }
        }
    }
}

/// Kutulu metin girişi (ayarlardaki 30pt yükseklikli alan).
struct DFieldBox: View {
    var placeholder: String
    @Binding var text: String
    var width: CGFloat = 260
    var mono = false
    @Environment(\.theme) private var th

    var body: some View {
        DTextField(placeholder: placeholder, text: $text, mono: mono)
            .frame(width: width)
            .padding(.horizontal, 10)
            .frame(height: 30)
            .background(RoundedRectangle(cornerRadius: 8).fill(th.inputBg))
    }
}

/// Küçük ikincil düğme (Kaydet / Vazgeç / Ekle…).
struct DButton: View {
    var label: String
    var icon: String? = nil
    var kind: Kind = .normal
    var disabled = false
    var action: () -> Void
    @Environment(\.theme) private var th

    enum Kind { case normal, primary, danger }

    var body: some View {
        HStack(spacing: 6) {
            if let icon {
                Image(systemName: icon).font(.system(size: 11, weight: .semibold))
            }
            Text(label).font(.system(size: 12.5, weight: .medium))
        }
        .foregroundStyle(renk)
        .padding(EdgeInsets(top: 6, leading: 12, bottom: 6, trailing: 12))
        .background(RoundedRectangle(cornerRadius: 8).fill(zemin))
        .opacity(disabled ? 0.45 : 1)
        .tap { if !disabled { action() } }
    }

    private var renk: Color {
        switch kind {
        case .normal: th.text2
        case .primary: .white
        case .danger: Theme.error
        }
    }

    private var zemin: Color {
        switch kind {
        case .normal, .danger: th.inputBg
        case .primary: Theme.primary
        }
    }
}

/// Satır dolunca alta kayan sarmal düzen (CSS flex-wrap karşılığı).
struct FlowLayout: Layout {
    var spacing: CGFloat = 7
    var lineSpacing: CGFloat = 7

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let maxW = proposal.width ?? .infinity
        var x: CGFloat = 0, y: CGFloat = 0, rowH: CGFloat = 0, width: CGFloat = 0
        for sv in subviews {
            let s = sv.sizeThatFits(.unspecified)
            if x > 0, x + s.width > maxW {
                x = 0
                y += rowH + lineSpacing
                rowH = 0
            }
            x += s.width + spacing
            rowH = max(rowH, s.height)
            width = max(width, x - spacing)
        }
        return CGSize(width: proposal.width ?? width, height: y + rowH)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var x: CGFloat = 0, y: CGFloat = 0, rowH: CGFloat = 0
        for sv in subviews {
            let s = sv.sizeThatFits(.unspecified)
            if x > 0, x + s.width > bounds.width {
                x = 0
                y += rowH + lineSpacing
                rowH = 0
            }
            sv.place(at: CGPoint(x: bounds.minX + x, y: bounds.minY + y),
                     proposal: ProposedViewSize(s))
            x += s.width + spacing
            rowH = max(rowH, s.height)
        }
    }
}

/// Chip biçimli açılır seçim (Cycle) — inputBg r7 fs13 + şef ok.
struct CycleChip: View {
    var value: String
    var mono = false
    var open: Bool
    var toggle: () -> Void
    @Environment(\.theme) private var th

    var body: some View {
        HStack(spacing: 7) {
            Text(value)
                .font(mono ? .system(size: 13, design: .monospaced) : .system(size: 13))
                .foregroundStyle(th.text)
            Image(systemName: "chevron.down")
                .font(.system(size: 8, weight: .semibold))
                .foregroundStyle(th.text3)
        }
        .padding(EdgeInsets(top: 5, leading: 11, bottom: 5, trailing: 11))
        .background(RoundedRectangle(cornerRadius: 7).fill(th.inputBg))
        .tap(toggle)
    }
}

/// Menü listesinin gerçek içerik boyu (ScrollView'a açık yükseklik vermek
/// için ölçülür).
private struct IcerikYuksekligi: PreferenceKey {
    static let defaultValue: CGFloat = 0
    static func reduce(value: inout CGFloat, nextValue: () -> CGFloat) {
        value = max(value, nextValue())
    }
}

/// Uzun listeler için aramalı + kaydırmalı menü.
///
/// DropMenu düz bir VStack: 80 model geldiğinde (opencode kataloğu) menü
/// pencerenin altından taşıyor, alttaki girdilere hiç ulaşılamıyordu —
/// kullanıcı modelini seçemiyordu. Burada liste kaydırılır ve eşik üstünde
/// arama kutusu çıkar.
struct SearchableMenu: View {
    struct Item: Identifiable, Hashable {
        let id: String
        let label: String
    }

    var items: [Item]
    var selected: String
    var minWidth: CGFloat = 260
    /// Bu sayının altında arama kutusu gösterilmez — dört satır için arama
    /// kutusu koymak gürültü.
    var searchThreshold = 8
    var placeholder = "Ara…"
    var onPick: (String) -> Void

    @State private var q = ""
    @State private var olculen: CGFloat = 0
    @FocusState private var focused: Bool
    @Environment(\.theme) private var th

    /// Süzme — görünümden ayrı tutulur ki sınanabilsin.
    /// Hem etiket hem kimlik aranır: kullanıcı "kimi" de yazabilir,
    /// "opencode-go" da.
    static func filtre(_ items: [Item], _ query: String) -> [Item] {
        let q = query.trimmingCharacters(in: .whitespaces)
        guard !q.isEmpty else { return items }
        return items.filter {
            $0.label.localizedCaseInsensitiveContains(q)
                || $0.id.localizedCaseInsensitiveContains(q)
        }
    }

    private var shown: [Item] { Self.filtre(items, q) }

    /// Satır yüksekliği tahmini (fs13 + 7/7 dolgu) — ölçüm gelene kadar
    /// kullanılır, ilk karede menü çökük görünmesin.
    static let satirTahmini: CGFloat = 31
    static let listeTavani: CGFloat = 260

    /// Liste yüksekliği AÇIKÇA verilir; hesabı sınanabilsin diye burada.
    ///
    /// ScrollView esnektir: kendisine ÖNERİLEN yüksekliği kabul eder. Bu menü
    /// bir overlay olduğu için öneri, altındaki kutunun yüksekliğidir (~38pt)
    /// — liste bir dilim halinde çiziliyordu, ekranda yalnız arama kutusu
    /// görünüyor ve model seçilemiyordu. `maxHeight` bunu ÇÖZMEZ: tavan
    /// koyar, taban koymaz. Açık `frame(height:)` öneriyi ezer.
    static func listeYuksekligi(olculen: CGFloat, satirSayisi: Int) -> CGFloat {
        let ham = olculen > 0 ? olculen : CGFloat(satirSayisi) * satirTahmini
        return min(listeTavani, max(satirTahmini, ham))
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            if items.count >= searchThreshold {
                TextField("", text: $q, prompt: Text(placeholder).foregroundStyle(th.text3))
                    .textFieldStyle(.plain)
                    .font(.system(size: 13))
                    .foregroundStyle(th.text)
                    .focused($focused)
                    .padding(EdgeInsets(top: 7, leading: 11, bottom: 7, trailing: 11))
                    .background(RoundedRectangle(cornerRadius: 7).fill(th.inputBg))
                    .padding(.bottom, 4)
                    .onAppear { focused = true }
            }
            if shown.isEmpty {
                Text("eşleşme yok")
                    .font(.system(size: 13))
                    .foregroundStyle(th.text3)
                    .padding(EdgeInsets(top: 7, leading: 11, bottom: 7, trailing: 11))
            } else {
                ScrollView {
                    VStack(alignment: .leading, spacing: 0) {
                        ForEach(shown) { it in
                            MenuRow(label: it.label, checked: selected == it.id) {
                                onPick(it.id)
                            }
                        }
                    }
                    .background {
                        GeometryReader { g in
                            Color.clear.preference(key: IcerikYuksekligi.self, value: g.size.height)
                        }
                    }
                }
                // Ölçülen içerik boyu, tavanla sınırlı (bkz. listeYuksekligi).
                .frame(height: Self.listeYuksekligi(olculen: olculen, satirSayisi: shown.count))
                .onPreferenceChange(IcerikYuksekligi.self) { h in
                    Task { @MainActor in olculen = h }
                }
                // Süzme listeyi kısaltınca eski ölçüm bayatlar (hiç eşleşme
                // olmayan sorguda ScrollView hiç çizilmediği için ölçüm de
                // gelmez): menü bir kare kocaman boşlukla açılırdı.
                .onChange(of: q) { olculen = 0 }
            }
        }
        .padding(4)
        .frame(minWidth: minWidth, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 10).fill(th.winBg))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(th.border, lineWidth: 1))
        .shadow(color: .black.opacity(0.18), radius: 15, y: 10)
    }
}
