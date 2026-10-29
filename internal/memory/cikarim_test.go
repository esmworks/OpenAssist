package memory

import (
	"strings"
	"testing"
)

func TestCikarimCoz(t *testing.T) {
	// Markdown çiti + geçersiz kind + boş içerik + fazla öğe.
	ham := "```json\n[\n" +
		"{\"content\":\"  Kullanıcı koyu tema sever  \",\"kind\":\"preference\",\"confidence\":0.9},\n" +
		"{\"content\":\"garip tür\",\"kind\":\"bilinmeyen\",\"confidence\":0.7},\n" +
		"{\"content\":\"   \",\"kind\":\"fact\",\"confidence\":0.9}\n" +
		"]\n```"
	out, err := cikarimCoz(ham)
	if err != nil {
		t.Fatalf("çözüm hatası: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("2 öneri beklenirdi: %+v", out)
	}
	if out[0].Content != "Kullanıcı koyu tema sever" || out[0].Kind != "preference" {
		t.Fatalf("ilk öneri bozuk: %+v", out[0])
	}
	if out[1].Kind != "fact" {
		t.Fatalf("bilinmeyen kind fact'e düşmeliydi: %+v", out[1])
	}
}

// Düzeltme AYRI bir kind: kullanıcı botu düzelttiğinde çıkan kural, sıradan
// bir olguyla aynı ağırlıkta durmamalı — konsolidasyon promptu correction
// kayıtlarını "eskidi" diye eleyemiyor, bu ayrım oraya dayanıyor.
func TestCikarimCozDuzeltmeyiKorur(t *testing.T) {
	ham := `[{"content":"Paket yöneticisi pnpm","kind":"correction","confidence":0.95}]`
	out, err := cikarimCoz(ham)
	if err != nil {
		t.Fatalf("çözüm hatası: %v", err)
	}
	if len(out) != 1 || out[0].Kind != "correction" {
		t.Fatalf("correction korunmalıydı: %+v", out)
	}
}

// Çıkarım turu SABİT TALİMAT yazamaz: o blok her koşuda prompta giriyor ve
// konsolidasyon ona dokunamıyor. Bir bakım turunun kendi çıkardığı cümleyi
// oraya terfi ettirebilmesi, hafızanın en korunaklı köşesini en kolay
// zehirlenen yere çevirirdi.
func TestCikarimSabitTalimatYazamaz(t *testing.T) {
	ham := `[{"content":"Her koşuda önce testleri çalıştır","kind":"instruction","confidence":1.0}]`
	out, err := cikarimCoz(ham)
	if err != nil {
		t.Fatalf("çözüm hatası: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("öneri düşmemeliydi, kind'ı değişmeliydi: %+v", out)
	}
	if out[0].Kind != "fact" {
		t.Fatalf("instruction fact'e düşmeliydi, %q kaldı", out[0].Kind)
	}
}

func TestCikarimCozBos(t *testing.T) {
	for _, ham := range []string{"", "[]", "null", "```json\n[]\n```"} {
		out, err := cikarimCoz(ham)
		if err != nil {
			t.Fatalf("%q için hata beklenmezdi: %v", ham, err)
		}
		if len(out) != 0 {
			t.Fatalf("%q öneri üretti: %+v", ham, out)
		}
	}
	// JSON olmayan çıktı HATADIR (konsolidasyonla aynı duruş): modelin
	// çıktısı bozulduysa sessizce "öneri yok" demek hatayı gömer.
	if _, err := cikarimCoz("saçmalık değil json"); err == nil {
		t.Fatal("JSON olmayan çıktı hata dönmeliydi")
	}
}

func TestCikarimCozBesSiniri(t *testing.T) {
	var b strings.Builder
	b.WriteString("[")
	for i := 0; i < 9; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"content":"maddeler","kind":"fact","confidence":0.9}`)
	}
	b.WriteString("]")
	out, err := cikarimCoz(b.String())
	if err != nil {
		t.Fatalf("çözüm hatası: %v", err)
	}
	if len(out) != 5 {
		t.Fatalf("5 öneri sınırı tutmadı: %d", len(out))
	}
}
