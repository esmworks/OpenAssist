// Package workspace — bot çalışma dizininin TEK tanımı.
//
// Motoru koşturan her yol (orkestratörün sohbet turu, gece hafıza
// konsolidasyonu) CLI'a bir çalışma dizini vermek zorunda: boş bırakılırsa
// süreç sunucunun kendi dizinini miras alır ve oturum/önbellek dosyalarını
// oraya yazmaya çalışır (konteynerde çoğu zaman yazılamaz). Yol iki yerde
// hesaplanırsa bir gün ayrışır — bu yüzden burada.
package workspace

import (
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// Dir — botun çalışma dizini; yoksa yaratılır. Yaratma hatası yol dönmesini
// engellemez: dizin zaten varsa ya da üst dizin yazılabilir değilse motorun
// kendi hatası daha anlaşılır olur.
func Dir(dataDir string, botID uuid.UUID) (string, error) {
	dir := filepath.Join(dataDir, "workspaces", botID.String())
	return dir, os.MkdirAll(dir, 0o755)
}
