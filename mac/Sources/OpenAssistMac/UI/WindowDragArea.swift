import SwiftUI
import AppKit

/// Başlık şeridi davranışı: sürükleyerek pencereyi taşı, çift tıklayınca
/// büyüt/küçült. `.hiddenTitleBar` + `.ignoresSafeArea()` ile içerik başlık
/// bölgesini kapladığından bu olayları pencereye kendimiz iletiyoruz.
/// Yalnızca 52px'lik başlıkların boş alanına (eski `Spacer()` yerine) konur —
/// düğmeler kardeş görünüm olduğu için tıklamaları etkilenmez.
struct WindowDragArea: View {
    var body: some View {
        DragRepresentable()
            .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

private struct DragRepresentable: NSViewRepresentable {
    func makeNSView(context: Context) -> NSView { DragNSView() }
    func updateNSView(_ nsView: NSView, context: Context) {}
}

private final class DragNSView: NSView {
    override func mouseDown(with event: NSEvent) {
        guard let window else { return }
        if event.clickCount == 2 {
            // Sistem Ayarları → Masaüstü ve Dock → "Başlık çubuğuna çift tıklama"
            switch UserDefaults.standard.string(forKey: "AppleActionOnDoubleClick") {
            case "Minimize": window.performMiniaturize(nil)
            case "None": break
            default: window.performZoom(nil) // "Maximize" ve varsayılan
            }
        } else {
            window.performDrag(with: event)
        }
    }
}
