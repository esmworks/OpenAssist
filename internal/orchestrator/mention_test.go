package orchestrator

import (
	"testing"

	"github.com/google/uuid"

	db "github.com/esmworks/OpenAssist/internal/store/db"
)

func TestParseMention(t *testing.T) {
	cases := map[string]string{
		"@Bora raporu hazırla":        "Bora",
		"raporu @Bora hazırla":        "Bora",
		"@Bora, şunu yap":             "Bora",
		"selam":                       "",
		"@":                           "",
		"@, geri bildirim":            "",
		"@İki Kelime mı değil mi":     "İki",
		"eposta adı@ornek.com kalsın": "ornek.com", // ilk @'tan sonraki kelime
	}
	for giris, beklenen := range cases {
		if got := parseMention(giris); got != beklenen {
			t.Errorf("parseMention(%q) = %q, beklenen %q", giris, got, beklenen)
		}
	}
}

// TestGrupHedefleri — grup devrinin hedef seçimi. Buradaki her satır bir
// üretim arızasına karşılık gelir: yanlış eşleşme başka bota para harcatır,
// kaçan eşleşme zinciri sessizce keser.
func TestGrupHedefleri(t *testing.T) {
	koordinator := db.Bot{ID: uuid.New(), Name: "Satış Koordinatörü"}
	lead := db.Bot{ID: uuid.New(), Name: "Lead Araştırmacısı"}
	teklif := db.Bot{ID: uuid.New(), Name: "Teklif Hazırlayıcı"}
	satisOutbound := db.Bot{ID: uuid.New(), Name: "Satış Outbound"}
	uyeler := []db.Bot{koordinator, lead, teklif, satisOutbound}

	adlar := func(bots []db.Bot) []string {
		out := make([]string, 0, len(bots))
		for _, b := range bots {
			out = append(out, b.Name)
		}
		return out
	}
	esit := func(t *testing.T, got, want []string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("hedefler = %v, beklenen %v", got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("hedefler = %v, beklenen %v", got, want)
			}
		}
	}

	t.Run("boşluklu tam ad bölünmez", func(t *testing.T) {
		got := grupHedefleri("Fiyat için @Teklif Hazırlayıcı devralsın.", uyeler, koordinator.ID)
		esit(t, adlar(got), []string{"Teklif Hazırlayıcı"})
	})

	t.Run("birden çok üye sırayla ve tekil", func(t *testing.T) {
		got := grupHedefleri("@Lead Araştırmacısı araştır, sonra @Teklif Hazırlayıcı yazsın; @Lead Araştırmacısı tekrar bakma.",
			uyeler, koordinator.ID)
		esit(t, adlar(got), []string{"Lead Araştırmacısı", "Teklif Hazırlayıcı"})
	})

	t.Run("konuşan kendini çağıramaz", func(t *testing.T) {
		got := grupHedefleri("@Satış Koordinatörü bunu ben hallederim", uyeler, koordinator.ID)
		esit(t, adlar(got), nil)
	})

	t.Run("belirsiz tek kelime atlanır", func(t *testing.T) {
		// "@Satış" hem koordinatöre hem Outbound'a uyuyor: sessizce atlanır,
		// yanlış bota iş açmaktansa hiç açmamak yeğdir.
		got := grupHedefleri("@Satış bakar mısın", uyeler, lead.ID)
		esit(t, adlar(got), nil)
	})

	t.Run("tekil kısa ad tutar", func(t *testing.T) {
		got := grupHedefleri("@Teklif hazır mı", uyeler, koordinator.ID)
		esit(t, adlar(got), []string{"Teklif Hazırlayıcı"})
	})

	t.Run("bilinmeyen ad hedef üretmez", func(t *testing.T) {
		got := grupHedefleri("@HayaletÜye selam", uyeler, koordinator.ID)
		esit(t, adlar(got), nil)
	})

	t.Run("mention yoksa zincir açılmaz", func(t *testing.T) {
		got := grupHedefleri("Rapor hazır, elimde başka iş yok.", uyeler, koordinator.ID)
		esit(t, adlar(got), nil)
	})
}
