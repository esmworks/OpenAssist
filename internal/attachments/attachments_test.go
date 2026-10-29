package attachments

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKind(t *testing.T) {
	cases := map[string]string{
		"fatura.PDF": "pdf",
		"foto.JPEG":  "image",
		"ekran.png":  "image",
		"rapor.docx": "doc",
		"sayfa.odt":  "doc",
		"kod.go":     "file",
		"isimsiz":    "file",
		"resim.heic": "image",
		"sunum.pptx": "doc",
		"metin.txt":  "file",
		"tablo.xlsx": "doc",
	}
	for ad, beklenen := range cases {
		if k := Kind(ad); k != beklenen {
			t.Errorf("Kind(%q) = %q, beklenen %q", ad, k, beklenen)
		}
	}
}

func TestTemizAd(t *testing.T) {
	cases := map[string]string{
		"fatura.pdf":                       "fatura.pdf",
		"../../etc/passwd":                 "passwd",
		"/mutlak/yol/rapor.docx":           "rapor.docx",
		"   başında sonunda boşluk.pdf   ": "başında sonunda boşluk.pdf",
		"":                                 "ek",
		":tamam:":                          ":tamam:",
		"ad\x00denetim.pdf":                "addenetim.pdf",
		strings.Repeat("a", 300):           strings.Repeat("a", 200),
		".":                                "ek",
	}
	for girilen, beklenen := range cases {
		if out := TemizAd(girilen); out != beklenen {
			t.Errorf("TemizAd(%q) = %q, beklenen %q", girilen, out, beklenen)
		}
	}
}

func TestSaveVeRemove(t *testing.T) {
	dir := t.TempDir()
	rel, n, err := Save(dir, bytes.NewReader([]byte("merhaba dünya")))
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if n != 14 {
		t.Fatalf("yazılan bayt = %d, 14 beklenirdi", n)
	}
	ap := Abs(dir, rel)
	if got, err := os.ReadFile(ap); err != nil || string(got) != "merhaba dünya" {
		t.Fatalf("dosya okunamadı/bozuk: %q, %v", got, err)
	}
	if err := Remove(dir, rel); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(ap); !os.IsNotExist(err) {
		t.Fatal("Remove sonrası dosya hâlâ duruyor")
	}
}

// ADIM 13: tür artık İÇERİKTEN saptanıyor — Telegram'ın "belge" diye
// yolladığı adsız PDF de, kullanıcının "rapor" diye kaydettiği PNG de doğru
// rozeti alsın.
func TestTurSapta(t *testing.T) {
	cases := []struct {
		ad       string
		icerik   []byte
		beklenen string
	}{
		{"belge", []byte("%PDF-1.7\n%…"), "pdf"},
		{"rapor", append([]byte{0xff, 0xd8, 0xff, 0xe0}, make([]byte, 32)...), "image"},
		{"ekran.txt", pngBaslik(false), "image"},
		// Tanınan imza yoksa uzantı konuşur: tür bir İPUCUDUR, tek bilgi
		// kaynağı değil. Yalan söyleyen uzantının bedelini motor tarafında
		// MotorGorur kapatıyor (uyarı prompta düşer).
		{"foto.jpg", []byte("düz metin, uzantı yalan söylüyor"), "image"},
		{"iphone.heic", heicBaslik(), "image"},
		{"animasyon.gif", []byte("GIF89a...."), "image"},
		{"cizim.webp", webpBaslik(), "image"},
		{"eski.bmp", []byte("BM\x00\x00\x00\x00"), "image"},
		{"sunum.pptx", []byte("PK\x03\x04zip"), "doc"}, // imza yok → uzantı
	}
	dir := t.TempDir()
	for _, c := range cases {
		yol := filepath.Join(dir, "x-"+c.ad)
		if err := os.WriteFile(yol, c.icerik, 0o600); err != nil {
			t.Fatal(err)
		}
		if tur := TurSapta(yol, c.ad); tur != c.beklenen {
			t.Errorf("TurSapta(%q) = %q, beklenen %q", c.ad, tur, c.beklenen)
		}
	}

	// Okunamayan dosya uzantıya düşer; yükleme tür yüzünden düşmez.
	if tur := TurSapta(filepath.Join(dir, "yok"), "fatura.pdf"); tur != "pdf" {
		t.Errorf("okunamayan dosya uzantıya düşmeli, geldi %q", tur)
	}
}

// MotorGorur pi'nin read aracıyla AYNI kümeyi tanımalı: yanlış "evet" demek,
// motorun çöp okuyup uydurması demek.
func TestMotorGorur(t *testing.T) {
	cases := []struct {
		ad     string
		icerik []byte
		gorur  bool
	}{
		{"jpeg", []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10}, true},
		{"jpeg-ls", []byte{0xff, 0xd8, 0xff, 0xf7, 0x00}, false}, // pi eliyor
		{"png", pngBaslik(false), true},
		{"apng", pngBaslik(true), false}, // animasyonlu PNG: pi eliyor
		{"gif", []byte("GIF89a"), true},
		{"webp", webpBaslik(), true},
		{"bmp", []byte("BM123456"), true},
		{"heic", heicBaslik(), false}, // iPhone fotoğrafı: türü image, motor göremez
		{"pdf", []byte("%PDF-1.4"), false},
		{"metin", []byte("merhaba"), false},
	}
	dir := t.TempDir()
	for _, c := range cases {
		yol := filepath.Join(dir, c.ad)
		if err := os.WriteFile(yol, c.icerik, 0o600); err != nil {
			t.Fatal(err)
		}
		if got := MotorGorur(yol); got != c.gorur {
			t.Errorf("MotorGorur(%s) = %v, beklenen %v", c.ad, got, c.gorur)
		}
	}
	if MotorGorur(filepath.Join(dir, "olmayan")) {
		t.Error("olmayan dosya için false beklenir")
	}
}

func pngBaslik(animasyon bool) []byte {
	b := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	b = append(b, []byte("\x00\x00\x00\x0dIHDR")...)
	if animasyon {
		b = append(b, []byte("\x00\x00\x00\x08acTL")...)
	}
	return append(b, make([]byte, 16)...)
}

func webpBaslik() []byte {
	b := []byte("RIFF")
	b = append(b, 0x24, 0x00, 0x00, 0x00)
	return append(b, []byte("WEBPVP8 ")...)
}

func heicBaslik() []byte {
	b := []byte{0x00, 0x00, 0x00, 0x18}
	b = append(b, []byte("ftypheic")...)
	return append(b, make([]byte, 16)...)
}

func TestSaveSinir(t *testing.T) {
	dir := t.TempDir()
	// EkAzami+1 bayt: ErrTooBig dönmeli, hiçbir dosya kalmamalı.
	büyük := bytes.NewReader(make([]byte, EkAzami+1))
	_, _, err := Save(dir, büyük)
	if !errors.Is(err, ErrTooBig) {
		t.Fatalf("büyük ek hatası beklenirdi, geldi: %v", err)
	}
	eslesen, _ := filepath.Glob(filepath.Join(Dir(dir), "*"))
	if len(eslesen) != 0 {
		t.Fatalf("sınır aşımında artık dosya kaldı: %v", eslesen)
	}
}
