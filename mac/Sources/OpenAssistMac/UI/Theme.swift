import SwiftUI

/// Tasarım belirteçleri — panel.dc.html :root / [data-theme=dark] birebir.
struct Theme: Equatable {
    let dark: Bool
    let winBg: Color
    let sidebarBg: Color
    let panelBg: Color
    let text: Color
    let text2: Color
    let text3: Color
    let border: Color
    let hover: Color
    let active: Color
    let bubbleBot: Color
    let bubbleUser: Color
    let bubbleUserText: Color
    let inputBg: Color
    let cardBg: Color
    let popBg: Color
    let popBorder: Color

    static let light = Theme(
        dark: false,
        winBg: Color(hex: "#ffffff"),
        sidebarBg: Color(hex: "#f5f5f7"),
        panelBg: Color(hex: "#fafafa"),
        text: Color(hex: "#1d1d1f"),
        text2: Color(hex: "#6e6e73"),
        text3: Color(hex: "#a1a1a6"),
        border: Color.black.opacity(0.08),
        hover: Color.black.opacity(0.05),
        active: Color(hex: "#e6e6eb"),
        bubbleBot: Color(hex: "#f0f0f2"),
        bubbleUser: Color(hex: "#1d1d1f"),
        bubbleUserText: Color(hex: "#ffffff"),
        inputBg: Color(hex: "#eeeef0"),
        cardBg: Color(hex: "#f7f7f9"),
        popBg: Color(hex: "#f8f8fa").opacity(0.94),
        popBorder: Color.black.opacity(0.14)
    )

    static let darkTheme = Theme(
        dark: true,
        winBg: Color(hex: "#1e1e20"),
        sidebarBg: Color(hex: "#151517"),
        panelBg: Color(hex: "#19191b"),
        text: Color(hex: "#f5f5f7"),
        text2: Color(hex: "#98989d"),
        text3: Color(hex: "#68686d"),
        border: Color.white.opacity(0.09),
        hover: Color.white.opacity(0.06),
        active: Color(hex: "#2c2c2e"),
        bubbleBot: Color(hex: "#2c2c2e"),
        bubbleUser: Color(hex: "#4a4a4d"),
        bubbleUserText: Color(hex: "#ffffff"),
        inputBg: Color(hex: "#2a2a2c"),
        cardBg: Color(hex: "#252527"),
        popBg: Color(hex: "#2a2a2d").opacity(0.95),
        popBorder: Color.white.opacity(0.14)
    )

    // Anlamsal sabitler (tema bağımsız)
    static let primary = Color(hex: "#3478f6")
    static let success = Color(hex: "#28c840")
    static let successText = Color(hex: "#1e9e38")
    static let warning = Color(hex: "#f0a020")
    static let warningText = Color(hex: "#c77e0a")
    static let error = Color(hex: "#ff453a")
}

private struct ThemeKey: EnvironmentKey {
    static let defaultValue = Theme.light
}

extension EnvironmentValues {
    var theme: Theme {
        get { self[ThemeKey.self] }
        set { self[ThemeKey.self] = newValue }
    }
}

extension Color {
    /// "#rrggbb" → Color; bozuk değerde gri.
    init(hex: String) {
        var s = hex.trimmingCharacters(in: .whitespaces)
        if s.hasPrefix("#") { s.removeFirst() }
        guard s.count == 6, let v = UInt32(s, radix: 16) else {
            self = .gray
            return
        }
        self.init(red: Double((v >> 16) & 0xFF) / 255,
                  green: Double((v >> 8) & 0xFF) / 255,
                  blue: Double(v & 0xFF) / 255)
    }
}

/// CSS `linear-gradient(145deg, c1, c2)` karşılığı.
func gradient145(_ c1: Color, _ c2: Color) -> LinearGradient {
    // CSS: 0deg yukarı, saat yönü. 145deg → yön (sin145°, -cos145°) ≈ (0.574, 0.819) (y aşağı).
    LinearGradient(colors: [c1, c2],
                   startPoint: UnitPoint(x: 0.5 - 0.2868, y: 0.5 - 0.4096),
                   endPoint: UnitPoint(x: 0.5 + 0.2868, y: 0.5 + 0.4096))
}
