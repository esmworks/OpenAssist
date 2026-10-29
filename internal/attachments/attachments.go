// Package attachments — ek dosyalarının disk tarafı (ADIM 5).
//
// Yolun TEK tanımı burada yaşar (internal/workspace ile aynı patern):
// herkes Dir(dataDir) üzerinden gider. DataDir hâlihazırda deploy'da kalıcı
// mount'tur (ekler dahil, §6). Dosya adları id tabanlıdır — istemcinin
// gönderdiği ad ASLA yola girmez (path traversal imkânsız); gerçek ad DB'de
// yaşar.
package attachments

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/google/uuid"
)

// EkAzami — tek ekin üst sınırı (20 MB). Telegram sendDocument üst sınırının
// (50 MB) altında, sendPhoto'nun (10 MB) üstünde bilinçli seçim.
const EkAzami = 20 << 20

// ErrTooBig — ekin EkAzami'yi aştığını söyler (HTTP katmanı 413'e çevirir).
var ErrTooBig = errors.New("ek çok büyük")

// Dir — eklerin kök dizini (DataDir'e göre).
func Dir(dataDir string) string { return filepath.Join(dataDir, "attachments") }

// Save — r'yi ekler dizinine yazar; dönen yol Dir'e GÖRELİdir. n, gerçek
// yazılan bayt sayısıdır. EkAzami aşılırsa dosya silinir ve ErrTooBig döner.
func Save(dataDir string, r io.Reader) (relPath string, n int64, err error) {
	dir := Dir(dataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, err
	}
	rel := uuid.New().String()
	dst := filepath.Join(dir, rel)
	f, err := os.Create(dst)
	if err != nil {
		return "", 0, err
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(dst)
		}
	}()
	n, err = io.Copy(f, io.LimitReader(r, EkAzami+1))
	if err != nil {
		return "", 0, err
	}
	if n > EkAzami {
		return "", 0, ErrTooBig
	}
	if err := f.Close(); err != nil {
		return "", 0, err
	}
	return rel, n, nil
}

// Remove — başarısız satır yazımının ardından dosyayı temizler.
func Remove(dataDir, relPath string) error {
	if relPath == "" {
		return nil
	}
	return os.Remove(filepath.Join(Dir(dataDir), relPath))
}

// Abs — relPath'i mutlak yola çevirir (indirme/motor ipucu için).
func Abs(dataDir, relPath string) string {
	return filepath.Join(Dir(dataDir), relPath)
}

// Kind — uzantıya göre ek türü (önizleme/akış ipucu).
func Kind(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".pdf":
		return "pdf"
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".heic", ".heif", ".bmp", ".tif", ".tiff", ".avif":
		return "image"
	case ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".odt", ".ods":
		return "doc"
	default:
		return "file"
	}
}

// basBaytlari — tür tespiti için dosyanın başı. 4100 bayt pi'nin kendi
// eşiğidir (utils/mime.js): PNG'nin animasyon bilgisi ilk chunk'larda yaşar.
const basBaytlari = 4100

// TurSapta — ek türünü İÇERİKTEN saptar, uzantı yalnız yedektir.
//
// Neden içerik: Telegram belgeleri adsız gelebiliyor ("belge"), fotoğraflar
// "foto.jpg" diye damgalanıyor, kullanıcı da PDF'i "rapor" diye kaydedebiliyor
// — uzantıya bakan tür, dosya kartında yanlış rozet ve motora yanlış ipucu
// demekti. Başı okunamayan dosya (izin/yarış) uzantıya düşer: tür bir ipucudur,
// yükleme onun yüzünden başarısız olmamalı.
func TurSapta(absPath, name string) string {
	bas, err := dosyaBasi(absPath)
	if err != nil {
		return Kind(name)
	}
	if GoruntuMime(bas) != "" {
		return "image"
	}
	switch {
	case bytes.HasPrefix(bas, []byte("%PDF-")):
		return "pdf"
	case bytes.HasPrefix(bas, []byte("\x00\x00\x00\x0cJP")), // JPEG 2000
		heicMi(bas):
		return "image"
	}
	return Kind(name)
}

// MotorGorur — motor bu dosyayı GÖRÜNTÜ olarak okuyabilir mi.
//
// pi'nin read aracı görüntüyü modele gerçek bir image bloğu olarak yollar ama
// yalnız beş imzayı tanır; tanımadığını METİN sanıp UTF-8 çözer. Yani HEIC bir
// iPhone fotoğrafı sessizce çöpe dönüşür ve model "dosyayı okudum" der. Bunu
// koşudan önce bilip prompta yazıyoruz (bkz. orchestrator.ekBlok).
func MotorGorur(absPath string) bool {
	bas, err := dosyaBasi(absPath)
	if err != nil {
		return false
	}
	return GoruntuMime(bas) != ""
}

// GoruntuMime — pi'nin desteklediği görüntü imzaları (utils/mime.js 0.84.2 ile
// birebir): kayıpsız JPEG (0xf7) ve ANİMASYONLU PNG dışarıda kalır, çünkü pi de
// onları eliyor. Desteklenmeyen tür için boş dize.
func GoruntuMime(bas []byte) string {
	switch {
	case bytes.HasPrefix(bas, []byte{0xff, 0xd8, 0xff}):
		if len(bas) > 3 && bas[3] == 0xf7 {
			return "" // JPEG-LS: pi kabul etmiyor
		}
		return "image/jpeg"
	case bytes.HasPrefix(bas, []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}):
		if bytes.Contains(bas, []byte("acTL")) {
			return "" // APNG: pi kabul etmiyor
		}
		return "image/png"
	case bytes.HasPrefix(bas, []byte("GIF")):
		return "image/gif"
	case bytes.HasPrefix(bas, []byte("RIFF")) && len(bas) >= 12 && bytes.Equal(bas[8:12], []byte("WEBP")):
		return "image/webp"
	case bytes.HasPrefix(bas, []byte("BM")):
		return "image/bmp"
	}
	return ""
}

// heicMi — ISO-BMFF kutusunun markası HEIC/HEIF ailesinden mi (iPhone
// fotoğrafı). Tür olarak "image"dır ama MotorGorur false döner.
func heicMi(bas []byte) bool {
	if len(bas) < 12 || !bytes.Equal(bas[4:8], []byte("ftyp")) {
		return false
	}
	switch string(bas[8:12]) {
	case "heic", "heix", "hevc", "heim", "heis", "hevm", "hevs", "mif1", "msf1", "avif":
		return true
	}
	return false
}

func dosyaBasi(absPath string) ([]byte, error) {
	f, err := os.Open(absPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	bas := make([]byte, basBaytlari)
	n, err := io.ReadFull(f, bas)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return bas[:n], nil
}

// TemizAd — görüntüleme/sunum için güvenli dosya adı: yol bileşenleri atılır,
// denetim karakterleri temizlenir, boş kalırsa "ek" verilir.
func TemizAd(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	name = strings.TrimSpace(b.String())
	if name == "" || name == "." {
		return "ek"
	}
	if len([]rune(name)) > 200 {
		return string([]rune(name)[:200])
	}
	return name
}
