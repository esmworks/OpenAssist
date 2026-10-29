import SwiftUI

/// Sağ üst köşe bildirimi — bot simgesi + başlık + metin.
struct ToastView: View {
    let toast: DToast
    @Environment(\.theme) private var th

    var body: some View {
        HStack(spacing: 11) {
            BotIconView(color: Color(hex: "#7b4fd6"), size: 34)
            VStack(alignment: .leading, spacing: 0) {
                Text(toast.title)
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(th.text)
                Text(toast.text)
                    .font(.system(size: 12.5))
                    .foregroundStyle(th.text2)
            }
        }
        .padding(EdgeInsets(top: 11, leading: 12, bottom: 11, trailing: 16))
        .frame(minWidth: 280, alignment: .leading)
        .background(RoundedRectangle(cornerRadius: 14).fill(th.winBg))
        .overlay(RoundedRectangle(cornerRadius: 14).stroke(th.border, lineWidth: 1))
        .shadow(color: .black.opacity(0.22), radius: 18, y: 12)
    }
}
