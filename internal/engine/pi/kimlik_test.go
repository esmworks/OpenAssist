package pi

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/esmworks/OpenAssist/internal/engine"
)

// Kullanıcının bildirdiği hata bu dosyanın konusu: koşu
// "No API key found for the selected model. Use /login …" ile düşüyordu.
// pi bu metni model çözülemediğinde basıyor (sağlayıcı "unknown"), yani
// elinde kimlikli tek bir model bile yokken.
//
// İki kaynağı vardı ve ikisi de burada kilitleniyor:
//   1. models.json ile ortamın AYRI okumalardan üretilmesi — dosyada
//      `$OA_PI_KEY_X` referansı, ortamda o değişkenin hiç olmaması.
//   2. Sağlayıcı okuması hata verince boş listeye düşülmesi — bu models.json'ı
//      SİLDİRİYORDU, yani DB'nin anlık bir hıçkırığı yapılandırmayı yok
//      ediyordu.

// ── 1. Tek okuma ──

// Yükleyici turda BİR kez çağrılmalı: iki çağrı arasında değişen bir kayıt
// dosyayla ortamı ayrıştırır.
func TestHazirlaSaglayiciyiBirKezOkur(t *testing.T) {
	sayac := 0
	a := &Adapter{
		ConfigDir: t.TempDir(),
		Providers: func(context.Context) ([]ProviderConfig, error) {
			sayac++
			return ornekSaglayici(), nil
		},
	}

	ps, err := a.hazirla(context.Background())
	if err != nil {
		t.Fatalf("hazirla: %v", err)
	}
	_ = a.cleanEnv("pi", ps)

	if sayac != 1 {
		t.Fatalf("sağlayıcılar %d kez okundu, 1 olmalıydı — dosya ile ortam ayrışabilir", sayac)
	}
}

// models.json'daki `$AD` referansının karşılığı ortamda DURMALI. Bu ikisi
// aynı listeden üretilmezse pi tam olarak kullanıcının gördüğü hatayı verir.
func TestDosyaReferansiOrtamdaKarsilikBulur(t *testing.T) {
	dizin := t.TempDir()
	a := &Adapter{
		ConfigDir: dizin,
		Providers: func(context.Context) ([]ProviderConfig, error) { return ornekSaglayici(), nil },
	}

	ps, err := a.hazirla(context.Background())
	if err != nil {
		t.Fatalf("hazirla: %v", err)
	}
	env := envHarita(a.cleanEnv("pi", ps))

	ham, err := os.ReadFile(filepath.Join(dizin, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	ad := ornekAnahtarEnv()
	if !strings.Contains(string(ham), "$"+ad) {
		t.Fatalf("models.json %q referansını taşımıyor", ad)
	}
	if env[ad] != testAnahtari {
		t.Fatalf("%s ortamda yok/yanlış (%q) — pi anahtarı çözemez", ad, env[ad])
	}
}

// ── 2. Okuma hatası ──

// DB hıçkırığı yapılandırmayı YOK ETMEMELİ. Eski davranışta hata boş listeye
// düşüyor, boş liste de models.json'ı siliyordu.
func TestOkumaHatasiModelsJsonuSilmez(t *testing.T) {
	dizin := t.TempDir()
	if err := saglayicilariYaz(dizin, ornekSaglayici()); err != nil {
		t.Fatal(err)
	}

	patlak := errors.New("db düştü")
	a := &Adapter{
		ConfigDir: dizin,
		Providers: func(context.Context) ([]ProviderConfig, error) { return nil, patlak },
	}

	if _, err := a.hazirla(context.Background()); !errors.Is(err, patlak) {
		t.Fatalf("okuma hatası yukarı çıkmadı: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dizin, "models.json")); err != nil {
		t.Fatalf("models.json silindi — son bilinen iyi yapılandırma kaybedildi: %v", err)
	}
}

// Yükleyici hiç yoksa (yerel kurulum, testler) tarif de yoktur; bu HATA
// değildir, pi kendi kataloğuyla çalışır.
func TestYukleyiciYokHataDegil(t *testing.T) {
	a := &Adapter{ConfigDir: t.TempDir()}
	ps, err := a.hazirla(context.Background())
	if err != nil {
		t.Fatalf("yükleyicisiz hazirla hata verdi: %v", err)
	}
	if len(ps) != 0 {
		t.Fatalf("tarif beklenmiyordu: %v", ps)
	}
}

// ── 3. Ön kontrol ──

func TestModelKontroluSaglayiciYok(t *testing.T) {
	// ConfigDir boş bırakılmaz: boşken pi kendi varsayılan dizinini
	// kullanır ve orada kimlik dosyası olabilir — ön kontrol bilinçli
	// olarak karışmaz. Kapta olan hâl bu değil.
	a := &Adapter{ConfigDir: t.TempDir()}
	err := a.modelKontrol(engine.TurnInput{}, nil)
	if err == nil {
		t.Fatal("sağlayıcısız ve anahtarsız ortamda koşu açılmamalıydı")
	}
	if !strings.Contains(err.Error(), "Modeller") {
		t.Fatalf("mesaj kullanıcıyı doğru ekrana göndermiyor: %v", err)
	}
	// pi'nin yanıltıcı tavsiyesi mesaja SIZMAMALI.
	if strings.Contains(err.Error(), "/login") {
		t.Fatalf("mesajda /login geçiyor: %v", err)
	}
}

// Sağlayıcı var ama modeli yok — pi'nin göreceği şey yine "hiç model yok".
func TestModelKontroluModelsizSaglayici(t *testing.T) {
	ps := []ProviderConfig{{ID: "oa", BaseURL: "http://x"}}
	a := &Adapter{ConfigDir: t.TempDir()}
	if err := a.modelKontrol(engine.TurnInput{}, ps); err == nil {
		t.Fatal("modelsiz sağlayıcı yakalanmadı")
	}
}

// PI_ENV ile pi'nin yerleşik sağlayıcıları kimlikli olabilir: karışma.
func TestModelKontroluYerlesikKimligeKarismaz(t *testing.T) {
	a := &Adapter{ConfigDir: t.TempDir(), Env: map[string]string{"ANTHROPIC_API_KEY": "sk-x"}}
	if err := a.modelKontrol(engine.TurnInput{}, nil); err != nil {
		t.Fatalf("PI_ENV varken koşu durduruldu: %v", err)
	}
}

func TestModelKontroluTanimliModelGecer(t *testing.T) {
	a := &Adapter{ConfigDir: t.TempDir()}
	err := a.modelKontrol(engine.TurnInput{Model: "oa/m-1"}, ornekSaglayici())
	if err != nil {
		t.Fatalf("tanımlı model reddedildi: %v", err)
	}
}

// Bizim sağlayıcımızda OLMAYAN model: sebebi söyle. Bot eski bir modelde
// kalmışsa kullanıcı ekranda ne yapacağını bilsin.
func TestModelKontroluTanimsizModelYakalanir(t *testing.T) {
	a := &Adapter{ConfigDir: t.TempDir()}
	err := a.modelKontrol(engine.TurnInput{Model: "oa/silinmis"}, ornekSaglayici())
	if err == nil {
		t.Fatal("sağlayıcıda olmayan model geçti")
	}
	if !strings.Contains(err.Error(), "silinmis") || !strings.Contains(err.Error(), "oa") {
		t.Fatalf("mesaj hangi model/sağlayıcı olduğunu söylemiyor: %v", err)
	}
}

// Sağlayıcı BİZDE değilse karışmıyoruz: pi'nin yerleşik kataloğundan
// geliyor olabilir, yanlış yere hayır demek koşuyu boşuna düşürürdü.
func TestModelKontroluYabanciSaglayiciyaKarismaz(t *testing.T) {
	a := &Adapter{ConfigDir: t.TempDir()}
	err := a.modelKontrol(engine.TurnInput{Model: "anthropic/claude-x"}, ornekSaglayici())
	if err != nil {
		t.Fatalf("yerleşik sağlayıcı modeli reddedildi: %v", err)
	}
}

// ── 4. pi mesajının çevirisi ──

func TestPiKimlikHatasiCevrilir(t *testing.T) {
	ham := "No API key found for the selected model.\n\nUse /login to log into a provider"
	got := piKimlikHatasi(ham)
	if !strings.Contains(got, "Modeller") {
		t.Fatalf("çeviri kullanıcıyı doğru ekrana göndermiyor: %q", got)
	}
	if !strings.Contains(got, ham) {
		t.Fatal("pi'nin özgün metni düştü — teşhis kaybolur")
	}
}

// Kimlikle ilgisi olmayan hata AYNEN kalmalı; her mesajın önüne "Modeller'e
// git" yazmak gerçek sebebi gizlerdi.
func TestPiKimlikDisiHataDokunulmaz(t *testing.T) {
	ham := "rate limit exceeded"
	if got := piKimlikHatasi(ham); got != ham {
		t.Fatalf("alakasız hata değiştirildi: %q", got)
	}
}

// pi'nin kendi kimlik dosyası varsa (yerel Mac'te `/login`) tarif olmadan da
// model bulunabilir: ön kontrol o kurulumu kilitlememeli.
func TestModelKontroluPiKimlikDosyasinaKarismaz(t *testing.T) {
	dizin := t.TempDir()
	if err := os.WriteFile(filepath.Join(dizin, "auth.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &Adapter{ConfigDir: dizin}
	if err := a.modelKontrol(engine.TurnInput{}, nil); err != nil {
		t.Fatalf("pi kimlik dosyası varken koşu durduruldu: %v", err)
	}
}

// ── 5. Yoklama pi'nin kendi teşhisini göstermeli ──

// pi models.json'ı okuyamazsa sebebi STDERR'e yazıp çıkışa 0 ile döner.
// Eskiden `Output()` yalnız stdout aldığı için o cümle kayboluyor, ekranda
// sebepsiz bir "model yok" kalıyordu.
func TestYoklamaPiStderriniGosterir(t *testing.T) {
	dizin := t.TempDir()
	sahte := sahtePi(t, `#!/bin/sh
case "$1" in
  --version) echo "0.84.2" ;;
  --list-models)
    echo "Warning: errors loading models.json:" >&2
    echo "Unexpected token } in JSON at position 42" >&2
    echo "No models available. Use /login …" ;;
esac
`)
	a := &Adapter{CLI: sahte, ConfigDir: dizin}

	st := a.Probe(context.Background())
	if !st.Available {
		t.Fatalf("sürüm okunamadı: %+v", st)
	}
	if !strings.Contains(st.Detail, "Unexpected token") {
		t.Fatalf("pi'nin sebebi ekrana taşınmadı: %q", st.Detail)
	}
}

// Sebep yoksa genel yönlendirme kalsın: her "model yok" hâli bozuk dosya
// değil, çoğu zaman sağlayıcının hiç eklenmemiş olması.
func TestYoklamaSebepsizGenelMesaj(t *testing.T) {
	sahte := sahtePi(t, `#!/bin/sh
case "$1" in
  --version) echo "0.84.2" ;;
  --list-models) echo "No models available." ;;
esac
`)
	a := &Adapter{CLI: sahte, ConfigDir: t.TempDir()}
	if st := a.Probe(context.Background()); !strings.Contains(st.Detail, "Modeller") {
		t.Fatalf("genel yönlendirme kayboldu: %q", st.Detail)
	}
}

// ── 6. Sır maskesi ──

// pi'nin çıktısı kullanıcıya gösteriliyor VE mesaj olarak DB'ye yazılıyor.
// Motorun bir gün anahtarı hata metnine koyması kalıcı bir sızıntı olurdu.
func TestPiCiktisindakiAnahtarMaskelenir(t *testing.T) {
	ps := ornekSaglayici()
	got := sirlariGizle("upstream reddetti: Bearer "+testAnahtari+" geçersiz", ps)
	if strings.Contains(got, testAnahtari) {
		t.Fatalf("anahtar metinde kaldı: %q", got)
	}
	if !strings.Contains(got, "«gizlendi»") {
		t.Fatalf("maske uygulanmadı: %q", got)
	}
}

// Anahtarsız sağlayıcıya "-" koyuyoruz; kısa değerler maskelenirse her tire
// kaybolur ve metin okunmaz hâle gelirdi.
func TestKisaDegerMaskelenmez(t *testing.T) {
	ps := []ProviderConfig{{ID: "yerel", BaseURL: "http://x", APIKey: "-"}}
	ham := "connect: connection refused - deneme"
	if got := sirlariGizle(ham, ps); got != ham {
		t.Fatalf("kısa değer maskelendi: %q", got)
	}
}

// sahtePi — testin sürebileceği küçük bir pi taklidi.
func sahtePi(t *testing.T, betik string) string {
	t.Helper()
	yol := filepath.Join(t.TempDir(), "sahte-pi")
	if err := os.WriteFile(yol, []byte(betik), 0o700); err != nil {
		t.Fatal(err)
	}
	return yol
}

// ── 7. models.json şeması: tanınmayan girdi kipi ──

// GERÇEK ARIZA: models.dev kataloğunda Qwen3.8 Max'in modalite listesi
// ["text","image","audio","video"] geliyordu; pi'nin şeması yalnız
// text/image tanıyor ve TEK geçersiz değer models.json'ın TAMAMINI
// düşürüyor. Sonuç: sağlayıcı doğru kayıtlıyken bile hiçbir model
// görünmüyor ve koşu «Model "…" not found» ile ölüyor.
//
// pi 0.84.2 ikilisi üzerinde tek tek ölçüldü: text ve image geçiyor;
// audio, video, pdf, document, file, json reddediliyor.
func TestTaninmayanGirdiKipiDosyayaYazilmaz(t *testing.T) {
	dizin := t.TempDir()
	ps := []ProviderConfig{{
		ID: "alibaba-token-plan", BaseURL: "https://ornek/v1", APIKey: "sk-x",
		Models: []ModelSpec{{
			ID:    "qwen3.8-max",
			Input: []string{"text", "image", "audio", "video"},
		}},
	}}
	if err := saglayicilariYaz(dizin, ps); err != nil {
		t.Fatalf("yaz: %v", err)
	}

	ham, err := os.ReadFile(filepath.Join(dizin, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, kip := range []string{"audio", "video"} {
		if strings.Contains(string(ham), kip) {
			t.Fatalf("%q models.json'a yazıldı — pi bütün dosyayı reddeder:\n%s", kip, ham)
		}
	}
	if !strings.Contains(string(ham), `"text"`) || !strings.Contains(string(ham), `"image"`) {
		t.Fatalf("tanınan kipler de elendi:\n%s", ham)
	}
}

// Hiçbir kip tanınmıyorsa alan HİÇ yazılmaz (boş dizi değil): pi alanı
// kendisi varsayılıyor.
func TestKipKalmazsaAlanYazilmaz(t *testing.T) {
	dizin := t.TempDir()
	ps := []ProviderConfig{{
		ID: "x", BaseURL: "https://ornek/v1",
		Models: []ModelSpec{{ID: "m", Input: []string{"audio"}}},
	}}
	if err := saglayicilariYaz(dizin, ps); err != nil {
		t.Fatal(err)
	}
	ham, err := os.ReadFile(filepath.Join(dizin, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ham), "input") {
		t.Fatalf("boş input alanı yazıldı:\n%s", ham)
	}
}
