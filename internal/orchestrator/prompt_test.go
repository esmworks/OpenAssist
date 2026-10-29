package orchestrator

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestKirpUTF8(t *testing.T) {
	t.Run("bütçe altındaki metne dokunmaz", func(t *testing.T) {
		in := "- [user/fact] Kullanıcı Türkçe konuşur\n- [bot/instruction] Taslakları resmi yaz"
		if got := kirpUTF8(in, 200); got != in {
			t.Errorf("kısa metin değişmemeli:\n%q", got)
		}
	})

	t.Run("çok baytlı karakteri ortadan bölmez", func(t *testing.T) {
		// Türkçe karakterler 2 bayt: bayt kesimi yapılsaydı burada geçersiz
		// UTF-8 üretilirdi (local_audit'te bir kez yaşandı).
		in := strings.Repeat("şğüöçİ", 200)
		got := kirpUTF8(in, 50)
		if !utf8.ValidString(got) {
			t.Fatalf("geçersiz UTF-8 üretildi: %q", got)
		}
		if strings.ContainsRune(got, '�') {
			t.Errorf("bozuk karakter var: %q", got)
		}
	})

	t.Run("satır ortasından kesmez", func(t *testing.T) {
		var b strings.Builder
		for i := 0; i < 40; i++ {
			b.WriteString("- [bot/fact] Bu bir hafıza satırıdır ve epey uzundur\n")
		}
		got := kirpUTF8(b.String(), 300)
		govde, _, _ := strings.Cut(got, "\n… (")
		for _, satir := range strings.Split(govde, "\n") {
			if satir == "" {
				continue
			}
			// Yarım satır modele eksik bir olgu olarak okunur — her satır
			// başladığı gibi bitmeli.
			if !strings.HasSuffix(satir, "uzundur") {
				t.Fatalf("yarım satır kaldı: %q", satir)
			}
		}
	})

	t.Run("kırpıldığını söyler", func(t *testing.T) {
		got := kirpUTF8(strings.Repeat("a", 500), 100)
		if !strings.Contains(got, "kırpıldı") {
			t.Errorf("kırpma sessiz olmamalı: %q", got)
		}
	})

	t.Run("çok baytlı metinde bütçeyi boşa harcamaz", func(t *testing.T) {
		// "Yarıdan fazlasını atma" koruması rune ölçeğinde çalışmalı. Bayt
		// indeksi rune eşiğiyle karşılaştırılsaydı Türkçe metinde eşik ~1.5
		// kat şişer ve koruma delinirdi: aşağıdaki metinde ilk satır sonu
		// rune 400'de (bütçenin %40'ı, korumanın reddetmesi gereken yer) ama
		// bayt olarak ~600 — eski kod kesimi oraya çekip bütçenin %60'ını
		// çöpe atardı.
		kisa := strings.Repeat("ğ", 399) + "\n"  // satır sonu: rune 399
		uzun := strings.Repeat("ş", 1200) + "\n" // sonraki sınır bütçenin ötesinde
		got := kirpUTF8(kisa+uzun, 1000)
		govde, _, _ := strings.Cut(got, "\n… (")
		if n := utf8.RuneCountInString(govde); n < 900 {
			t.Errorf("1000 rune bütçenin yalnız %d'i kullanıldı — eşik yanlış ölçekte", n)
		}
	})

	t.Run("tek uzun satırda da kırpar", func(t *testing.T) {
		// Satır sınırı yoksa (tek devasa paragraf) yine de bütçeye inmeli:
		// LastIndexByte -1 döner, kesim ham rune sınırında kalır.
		got := kirpUTF8(strings.Repeat("uzun paragraf ", 500), 100)
		if utf8.RuneCountInString(got) > 200 {
			t.Errorf("bütçe aşıldı: %d rune", utf8.RuneCountInString(got))
		}
	})
}

// TestHataKirpUzunMesaji — motor hataları yığın izi/kullanım metniyle
// gelebiliyor; sohbetteki hata kartı okunur kalsın.
func TestHataKirpUzunMesaji(t *testing.T) {
	if got := hataKirp("  "); got != "bilinmeyen hata" {
		t.Errorf("boş mesaj: %q", got)
	}
	if got := hataKirp(" kısa "); got != "kısa" {
		t.Errorf("kırpma gerekmiyorsa dokunulmamalı: %q", got)
	}
	uzun := strings.Repeat("ı", 600) // çok baytlı: rune sayılmalı, bayt değil
	got := hataKirp(uzun)
	if r := []rune(got); len(r) != 501 || !strings.HasSuffix(got, "…") {
		t.Errorf("500 rune + ellips bekleniyordu, %d rune geldi", len(r))
	}
}
