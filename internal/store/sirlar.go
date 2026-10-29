package store

import (
	"fmt"
	"regexp"
	"strings"
)

// Sır taraması — hafıza yazma yolunun kapısı (K20).
//
// Neden burada, kabul anında: hafıza BİR kez yazılır, sonra her koşunun sistem
// promptunda BİN kez okunur. Ajanın önerisine bir anahtar sızarsa Postgres'e
// düşer, oradan her prompta girer ve motorun her turunda tekrar tekrar dışarı
// gider. Okuma tarafında temizlemek geç kalmış olur.
//
// Karar: maskeleme değil RED. Maskelenmiş kayıt ("kullanıcının anahtarı
// [GİZLENDİ]") gelecekte hiçbir işe yaramaz, ama hafızada yer tutar ve doğru
// bilgi kaydedilmiş sanılır. Reddedilince ajan gerekçeyi görür ve olguyu
// anahtarsız yeniden yazabilir ("kullanıcının OpenAI hesabı var").
var sirDesenleri = []struct {
	ad    string
	desen *regexp.Regexp
	// oneksiz — desen sabit bir önek aramıyor, yalnız uzunluk/karakter
	// kümesine bakıyor. Bu desenler adres YOLLARI gizlenmiş metne uygulanır
	// (aşağıdaki gerekçe).
	oneksiz bool
}{
	// Sağlayıcı anahtarları — önek sabit, gövde uzunluğu değişken. Adres
	// içinde geçseler de gerçek bulgudur, metnin tamamında aranır.
	{ad: "OpenAI/Anthropic API anahtarı", desen: regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}`)},
	{ad: "GitHub belirteci", desen: regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{20,})`)},
	{ad: "AWS erişim anahtarı", desen: regexp.MustCompile(`\b(AKIA|ASIA)[A-Z0-9]{16}\b`)},
	{ad: "Slack belirteci", desen: regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}`)},
	{ad: "Google API anahtarı", desen: regexp.MustCompile(`\bAIza[A-Za-z0-9_-]{35}\b`)},
	{ad: "Slack webhook adresi", desen: regexp.MustCompile(`hooks\.slack\.com/services/`)},
	{ad: "özel anahtar bloğu", desen: regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{ad: "Authorization başlığı", desen: regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._-]{20,}`)},

	// Öneksiz sırlar: uzun, boşluksuz, yüksek entropili diziler. Eşikler
	// yanlış pozitifi kesmek için bilinçli yüksek — UUID (en uzun parçası 12
	// onaltılık) ve sha256 ÖNEKİ ("a3f9…" gibi kısa alıntılar) tetiklemez;
	// tam bir sha256 (64) ya da 40+ karakterlik base64 tetikler.
	{ad: "onaltılık sır (32+ hane)", desen: regexp.MustCompile(`\b[0-9a-fA-F]{32,}\b`), oneksiz: true},
	{ad: "base64 sır (40+ karakter)", desen: regexp.MustCompile(`\b[A-Za-z0-9+/]{40,}={0,2}\b`), oneksiz: true},
}

// adresDeseni — http(s) adresinin şema+host+yol kısmı. Sorgu dizesi (`?…`) ve
// çapa (`#…`) BİLİNÇLİ olarak dışarıda kalır: sırlar adreslerde tam oraya
// yazılır (`?token=…`), yol segmentleri ise kaynak kimliğidir.
var adresDeseni = regexp.MustCompile(`https?://[^\s?#]+`)

// adresYollariniGizle — adres yollarını aynı uzunlukta boşlukla değiştirir
// (uzunluk korunur ki hata mesajı üretmek gerekirse konum kaymasın).
//
// Neden gerekli: kişisel asistan hafızasının en doğal içeriği doküman,
// takvim ve video bağlantılarıdır. Bir Google Doc kimliği 44 karakter
// alfanümeriktir ve öneksiz base64 desenine birebir uyar — maskeleme
// olmadan "raporun şu adreste" gibi meşru her olgu reddedilirdi. Aynı kapı
// markdown içe aktarmada da olduğu için kullanıcının elle yazdığı satırlar
// da düşerdi.
func adresYollariniGizle(metin string) string {
	return adresDeseni.ReplaceAllStringFunc(metin, func(u string) string {
		return strings.Repeat(" ", len(u))
	})
}

// SirTara — metinde sır deseni varsa açıklayıcı hata döner, yoksa nil.
// Hata metni ajana gider: ne bulunduğunu söyler, sırrın kendisini TEKRARLAMAZ
// (aksi halde red mesajı sırrı koşu olaylarına geri yazardı).
//
// Store'da duruyor ki HER yazma yolu aynı kapıdan geçsin: ajanın
// `memory_save` önerisi, gece konsolidasyonunun ürettiği birleşik metin ve
// markdown içe aktarma. Aracın içine konsaydı diğer iki yol açık kalırdı.
func SirTara(metin string) error {
	gizli := adresYollariniGizle(metin)
	for _, d := range sirDesenleri {
		aranan := metin
		if d.oneksiz {
			aranan = gizli
		}
		if d.desen.MatchString(aranan) {
			return fmt.Errorf(
				"içerikte %s görünüyor — hafızaya sır yazılmaz (her koşunun sistem promptuna girer). "+
					"Olguyu sırrın kendisi olmadan yeniden yaz (örn. \"kullanıcının X hesabı bağlı\")",
				d.ad)
		}
	}
	return nil
}
