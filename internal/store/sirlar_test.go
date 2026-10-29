package store

import (
	"strings"
	"testing"
)

// Sır taramasının iki tarafı da testli: yakalaması gerekenler ve —en az onun
// kadar önemli— yakalamaMAsı gerekenler. Yanlış pozitif burada sessiz bir
// zarar: ajan meşru bir olguyu kaydedemez ve nedenini kullanıcı görmez.
func TestSirTara(t *testing.T) {
	sirlar := map[string]string{
		"openai anahtarı":    "kullanıcının anahtarı sk-proj-AbCdEf0123456789xyzQ",
		"anthropic anahtarı": "sk-ant-api03-0123456789abcdefghijKLMNOP",
		"github klasik":      "token ghp_0123456789abcdefghijABCDEFGHIJ012345",
		"github fine pat":    "github_pat_11ABCDEFG0123456789_abcdefghij",
		"aws":                "erişim: AKIAIOSFODNN7EXAMPLE",
		"slack bot":          "xoxb-123456789012-abcdefghijklmnop",
		"google":             "AIzaSyA0123456789abcdefghijklmnopqrstuvw",
		"slack webhook":      "https://hooks.slack.com/services/T00/B00/XXX",
		"özel anahtar":       "-----BEGIN RSA PRIVATE KEY-----",
		"bearer":             "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6",
		"tam sha256":         "parola karması 5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8",
		"base64 blok":        "gizli: QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVowMTIzNDU2Nzg5",
		// Adres YOLLARI gizleniyor ama sorgu dizesi gizlenmiyor: sırlar
		// adreslerde tam oraya yazılır.
		"adres sorgusunda sır": "geri dönüş https://ornek.com/cb?token=QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVowMTIzNDU2Nzg5",
		"adreste önekli sır":   "https://ornek.com/x/sk-proj-AbCdEf0123456789xyzQ",
	}
	for ad, metin := range sirlar {
		t.Run("yakalar/"+ad, func(t *testing.T) {
			err := SirTara(metin)
			if err == nil {
				t.Fatalf("sır yakalanmalıydı: %q", metin)
			}
			// Red mesajı sırrın kendisini geri yazmamalı — yoksa reddedilen
			// içerik koşu olaylarına ve oradan transkripte sızar.
			if len(err.Error()) > 0 && containsAny(err.Error(), []string{
				"sk-proj", "ghp_", "AKIA", "xoxb-", "AIzaSy", "eyJhbGci",
			}) {
				t.Errorf("hata metni sırrı tekrarlıyor: %v", err)
			}
		})
	}

	temizler := map[string]string{
		"düz tercih":         "Kullanıcı Türkçe iletişim ister",
		"hesap olgusu":       "Kullanıcının bağlı bir Google hesabı var (OAuth ile)",
		"uuid":               "tohum bot id'si 9f8c1e2a-4b6d-4c8e-9a1f-2d3e4f5a6b7c",
		"kısa hash alıntısı": "commit 3c5c315 ayar ekranlarını düzeltti",
		"sha256 öneki":       "içerik karması a3f9c1d2 ile başlıyor",
		"sayı dizisi":        "2026 yılında 15776 satır Go kodu vardı",
		"türkçe uzun cümle":  "Sabah rutini gece hazırladığı taslakları onaya sunar ve kullanıcı tek dokunuşla onaylar",
		"sk- ama kısa":       "sk-1234 gibi bir şey yazmıştı ama tam değil",

		// Kişisel asistan hafızasının en doğal içeriği: doküman/takvim/video
		// bağlantıları. Google Doc kimliği 44 karakter alfanümeriktir ve
		// öneksiz base64 desenine birebir uyar — adres yolları gizlenmeseydi
		// bu satırların hepsi "base64 sır" diye reddedilirdi.
		"google doc adresi": "Haftalık rapor https://docs.google.com/document/d/1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms adresinde",
		"drive dosyası":     "Sunum https://drive.google.com/file/d/1a2B3c4D5e6F7g8H9i0JkLmNoPqRsTuVwXyZ012345678/view",
		"youtube":           "Kullanıcı https://www.youtube.com/watch?v=dQw4w9WgXcQ videosunu paylaştı",
	}
	for ad, metin := range temizler {
		t.Run("geçirir/"+ad, func(t *testing.T) {
			if err := SirTara(metin); err != nil {
				t.Fatalf("meşru içerik reddedildi: %q → %v", metin, err)
			}
		})
	}
}

func containsAny(s string, parcalar []string) bool {
	for _, p := range parcalar {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}
