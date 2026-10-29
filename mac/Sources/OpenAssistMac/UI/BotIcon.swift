import SwiftUI

/// Tasarımdaki bot simgesi: tek SVG path (viewBox 0 0 20 20,
/// transform translate(-25.414562,-111.28127), fill currentColor).
enum BotIcon {
    static let d = "m 35.412312,115.5918 c -1.442046,0 -2.880739,0.49168 -4.052401,1.4748 -0.705182,0.59173 -1.2502,1.31171 -1.628713,2.10012 -0.05253,-0.005 -0.103652,-0.0164 -0.157588,-0.0164 -0.941869,0 -1.70024,0.75836 -1.70024,1.70021 v 2.10134 c 0,0.94184 0.758371,1.70021 1.70024,1.70021 0.05394,0 0.105059,-0.0117 0.157588,-0.0164 0.06777,0.14047 0.137889,0.27991 0.216918,0.41689 0.308938,0.55139 1.134304,0.0793 0.815588,-0.46655 -1.304955,-2.26029 -0.799571,-5.12447 1.199708,-6.80208 1.999326,-1.67765 4.903352,-1.67765 6.902655,0 1.999326,1.67761 2.50471,4.54174 1.199732,6.80208 l -0.01407,0.0234 -0.0094,0.0234 c 0,0 -0.633799,1.40211 -1.878344,1.40211 l -1.575387,-0.0141 c -0.147739,-0.21575 -0.384191,-0.366 -0.666512,-0.366 h -1.015533 c -0.45534,0 -0.821638,0.36752 -0.821638,0.82286 0,0.28087 0.178225,0.49415 0.586056,0.49415 l 3.493009,2.8e-4 c 1.867604,0 2.675336,-1.8335 2.712107,-1.91835 v -0.005 c 0.07832,-0.13578 0.147269,-0.27521 0.214572,-0.41444 0.05488,0.005 0.108342,0.0164 0.164858,0.0164 0.941869,0 1.700237,-0.75836 1.700237,-1.70021 v -2.10134 c 0,-0.94184 -0.75837,-1.70021 -1.700237,-1.70021 -0.05441,0 -0.105762,0.009 -0.158761,0.0164 -0.377627,-0.78846 -0.921023,-1.50837 -1.6263,-2.10014 -1.171614,-0.98312 -2.616355,-1.47481 -4.058451,-1.47481 z m -2.049205,2.98718 c -0.366019,-0.0239 -0.700354,0.0535 -0.992502,0.36114 -0.779049,0.82018 -1.260325,1.9365 -1.260325,3.12533 0,0.98061 0.327722,2.07941 0.879793,2.11951 0.786511,0.0572 2.027657,-0.45928 3.424676,-0.45928 1.480437,0 2.786843,0.57951 3.561606,0.44232 0.469528,-0.083 0.744064,-1.20536 0.744064,-2.10255 0,-1.18883 -0.482452,-2.30515 -1.261524,-3.12533 -0.779054,-0.82021 -1.855342,0 -3.044146,0 -0.743006,0 -1.441646,-0.32109 -2.051642,-0.36114 z m -0.174473,2.02134 c 0.434305,1.7e-4 0.786345,0.35219 0.786486,0.78651 -1.41e-4,0.43431 -0.352181,0.78633 -0.786486,0.78649 -0.434304,-1.6e-4 -0.786345,-0.35218 -0.786485,-0.78649 1.4e-4,-0.43432 0.352181,-0.78634 0.786485,-0.78651 z m 4.452304,0 c 0.434329,1.7e-4 0.786345,0.35219 0.786509,0.78651 -1.66e-4,0.43431 -0.35218,0.78633 -0.786509,0.78649 -0.434304,-1.6e-4 -0.786321,-0.35218 -0.786485,-0.78649 1.64e-4,-0.43432 0.352181,-0.78634 0.786485,-0.78651 z"

    /// 0..20 uzayına çevrilmiş hazır Path (translate uygulanmış).
    static let path: Path = parse(d).applying(
        CGAffineTransform(translationX: -25.414562, y: -111.28127))

    // ── SVG path ayrıştırıcı: m/M c/C h/H l/L v/V z/Z + bilimsel gösterim ──

    private enum Tok {
        case cmd(Character)
        case num(CGFloat)
    }

    private static func tokenize(_ s: String) -> [Tok] {
        var toks: [Tok] = []
        var numStr = ""
        func flush() {
            if !numStr.isEmpty {
                if let v = Double(numStr) { toks.append(.num(CGFloat(v))) }
                numStr = ""
            }
        }
        for ch in s {
            if ch.isLetter && ch != "e" && ch != "E" {
                flush()
                toks.append(.cmd(ch))
            } else if ch == "," || ch.isWhitespace {
                flush()
            } else if ch == "-" || ch == "+",
                      !numStr.isEmpty,
                      !numStr.hasSuffix("e"), !numStr.hasSuffix("E") {
                flush()
                numStr.append(ch)
            } else {
                numStr.append(ch)
            }
        }
        flush()
        return toks
    }

    private static func parse(_ d: String) -> Path {
        let toks = tokenize(d)
        var path = Path()
        var cur = CGPoint.zero
        var subStart = CGPoint.zero
        var i = 0

        func num() -> CGFloat? {
            guard i < toks.count, case .num(let v) = toks[i] else { return nil }
            i += 1
            return v
        }

        while i < toks.count {
            guard case .cmd(let c) = toks[i] else { i += 1; continue }
            i += 1
            let rel = c.isLowercase
            switch Character(c.lowercased()) {
            case "m":
                var first = true
                while let x = num(), let y = num() {
                    let p = rel ? CGPoint(x: cur.x + x, y: cur.y + y) : CGPoint(x: x, y: y)
                    if first {
                        path.move(to: p)
                        subStart = p
                        first = false
                    } else {
                        path.addLine(to: p)
                    }
                    cur = p
                }
            case "l":
                while let x = num(), let y = num() {
                    let p = rel ? CGPoint(x: cur.x + x, y: cur.y + y) : CGPoint(x: x, y: y)
                    path.addLine(to: p)
                    cur = p
                }
            case "h":
                while let x = num() {
                    let p = CGPoint(x: rel ? cur.x + x : x, y: cur.y)
                    path.addLine(to: p)
                    cur = p
                }
            case "v":
                while let y = num() {
                    let p = CGPoint(x: cur.x, y: rel ? cur.y + y : y)
                    path.addLine(to: p)
                    cur = p
                }
            case "c":
                while let x1 = num(), let y1 = num(),
                      let x2 = num(), let y2 = num(),
                      let x = num(), let y = num() {
                    let c1 = rel ? CGPoint(x: cur.x + x1, y: cur.y + y1) : CGPoint(x: x1, y: y1)
                    let c2 = rel ? CGPoint(x: cur.x + x2, y: cur.y + y2) : CGPoint(x: x2, y: y2)
                    let p = rel ? CGPoint(x: cur.x + x, y: cur.y + y) : CGPoint(x: x, y: y)
                    path.addCurve(to: p, control1: c1, control2: c2)
                    cur = p
                }
            case "z":
                path.closeSubpath()
                cur = subStart
            default:
                break
            }
        }
        return path
    }
}

/// Bot simgesi Shape — kutuya ölçekler (viewBox 0 0 20 20).
struct BotShape: Shape {
    func path(in rect: CGRect) -> Path {
        let s = min(rect.width, rect.height) / 20
        let t = CGAffineTransform(scaleX: s, y: s).concatenating(
            CGAffineTransform(translationX: rect.minX + (rect.width - 20 * s) / 2,
                              y: rect.minY + (rect.height - 20 * s) / 2))
        return BotIcon.path.applying(t)
    }
}

/// Tek bot avatarı: c2 rengine boyalı bot simgesi (tasarımda arka plan yok).
struct BotIconView: View {
    var color: Color
    var size: CGFloat

    var body: some View {
        BotShape()
            .fill(color)
            .frame(width: size, height: size)
    }
}

/// Ekip avatarı — 3 mini bot simgesi, tasarımdaki mutlak ofsetlerle.
struct TeamAvatarView: View {
    /// (x, y, boyut) — kapsayıcı sol-üstünden.
    struct Slot {
        let x: CGFloat
        let y: CGFloat
        let s: CGFloat
    }

    /// 40px kenar çubuğu düzeni (STACK).
    static let stack40: [Slot] = [Slot(x: 0, y: 0, s: 22), Slot(x: 13, y: 11, s: 20), Slot(x: 0, y: 17, s: 17)]
    /// 28px başlık düzeni (HSTACK).
    static let hstack28: [Slot] = [Slot(x: 0, y: 0, s: 16), Slot(x: 10, y: 8, s: 14), Slot(x: 0, y: 13, s: 12)]

    var colors: [Color]
    var layout: [Slot]
    var size: CGFloat

    var body: some View {
        ZStack(alignment: .topLeading) {
            ForEach(Array(colors.prefix(3).enumerated()), id: \.offset) { i, color in
                let slot = i < layout.count ? layout[i] : layout[0]
                BotShape()
                    .fill(color)
                    .frame(width: slot.s, height: slot.s)
                    .offset(x: slot.x, y: slot.y)
            }
        }
        .frame(width: size, height: size, alignment: .topLeading)
    }
}
