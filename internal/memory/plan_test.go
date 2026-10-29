package memory

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	db "github.com/esmworks/OpenAssist/internal/store/db"
)

// memsVarsayilan — 10 kayıtlık standart küme (yarı eşiği 5'te).
func memsVarsayilan() []db.Memory { return mems(10) }

// mems — n adet sahte kayıt; testler yalnız id'lerini kullanır.
func mems(n int) []db.Memory {
	out := make([]db.Memory, n)
	for i := range out {
		out[i] = db.Memory{ID: uuid.New(), Scope: "bot", Kind: "fact"}
	}
	return out
}

func TestPlanCoz(t *testing.T) {
	t.Run("markdown çitini aşar", func(t *testing.T) {
		// Motor talimata rağmen çit ya da giriş cümlesi ekleyebilir; plan
		// bunun yüzünden düşmemeli.
		metin := "İşte plan:\n```json\n{\"birlestir\":[],\"ele\":[],\"ozet\":\"tek satır\"}\n```\n"
		p, err := planCoz(metin)
		if err != nil {
			t.Fatalf("planCoz: %v", err)
		}
		if p.Ozet != "tek satır" {
			t.Errorf("özet okunamadı: %q", p.Ozet)
		}
	})

	t.Run("JSON yoksa hata", func(t *testing.T) {
		if _, err := planCoz("hafıza zaten temiz, bir şey yapmadım"); err == nil {
			t.Error("JSON olmayan cevap hata vermeli")
		}
	})

	t.Run("şemada olmayan alan reddedilir", func(t *testing.T) {
		// Model başka bir şey anlamışsa yarısını uygulamaktansa turu düşür.
		_, err := planCoz(`{"birlestir":[],"ele":[],"ozet":"x","yeni_hafiza":[{"content":"uydurma"}]}`)
		if err == nil {
			t.Error("bilinmeyen alan reddedilmeli — model kontratı genişletemez")
		}
	})
}

func TestPlanDogrula(t *testing.T) {
	t.Run("listede olmayan kayda dokunamaz", func(t *testing.T) {
		m := memsVarsayilan()
		p := Plan{Ozet: "özet", Ele: []Eleme{{ID: uuid.NewString(), Gerekce: "bayat"}}}
		_, err := planDogrula(p, m)
		if err == nil || !strings.Contains(err.Error(), "listede olmayan") {
			t.Fatalf("yabancı id reddedilmeliydi: %v", err)
		}
	})

	t.Run("aynı kayıt iki kez kullanılamaz", func(t *testing.T) {
		m := memsVarsayilan()
		id := m[0].ID.String()
		p := Plan{
			Ozet:      "özet",
			Birlestir: []Birlestirme{{Tut: m[1].ID.String(), Kat: []string{id}, Icerik: "birleşik"}},
			Ele:       []Eleme{{ID: id, Gerekce: "çelişik"}},
		}
		_, err := planDogrula(p, m)
		if err == nil || !strings.Contains(err.Error(), "iki kez") {
			t.Fatalf("çelişkili plan reddedilmeliydi: %v", err)
		}
	})

	t.Run("yarıdan fazlasını düşüren plan tümden reddedilir", func(t *testing.T) {
		// En önemli kilit: bozuk ya da yanlış anlamış bir tur hafızayı
		// boşaltmasın.
		m := memsVarsayilan()
		var ele []Eleme
		for i := 0; i < 6; i++ {
			ele = append(ele, Eleme{ID: m[i].ID.String(), Gerekce: "bayat"})
		}
		_, err := planDogrula(Plan{Ozet: "özet", Ele: ele}, m)
		if err == nil || !strings.Contains(err.Error(), "yarıdan fazlası") {
			t.Fatalf("kitle eleme reddedilmeliydi: %v", err)
		}

		// Sınırın altı geçmeli: 5/10 yarıdan fazla DEĞİL.
		if _, err := planDogrula(Plan{Ozet: "özet", Ele: ele[:5]}, m); err != nil {
			t.Fatalf("sınırdaki plan geçmeliydi: %v", err)
		}
	})

	t.Run("boş plan meşrudur", func(t *testing.T) {
		// No-op kapısı (Ek C.1): hafıza temizse doğru cevap hiçbir şey
		// yapmamaktır. Yine de özet güncellenir.
		if _, err := planDogrula(Plan{Ozet: "hafıza zaten derli toplu"}, memsVarsayilan()); err != nil {
			t.Fatalf("boş plan geçerli olmalı: %v", err)
		}
	})

	t.Run("özetsiz plan reddedilir", func(t *testing.T) {
		if _, err := planDogrula(Plan{}, memsVarsayilan()); err == nil {
			t.Error("özet zorunlu — prompta girecek olan o")
		}
	})

	t.Run("geçen plan id'leri çözülmüş döner", func(t *testing.T) {
		// Uygulama adımı yalnız çözülmüş planı kabul ediyor; ham metni orada
		// uuid.MustParse ile çözmek, doğrulamada bir dal atlandığı gün arka
		// plan goroutine'inde panic ve komple süreç ölümü demekti.
		m := memsVarsayilan()
		p := Plan{
			Ozet:      "özet",
			Birlestir: []Birlestirme{{Tut: m[0].ID.String(), Kat: []string{m[1].ID.String()}, Icerik: "birleşik"}},
			Ele:       []Eleme{{ID: m[2].ID.String(), Gerekce: "bayat"}},
		}
		coz, err := planDogrula(p, m)
		if err != nil {
			t.Fatalf("planDogrula: %v", err)
		}
		if coz.Birlestir[0].Tut != m[0].ID || coz.Birlestir[0].Kat[0] != m[1].ID {
			t.Errorf("birleştirme id'leri çözülmedi: %+v", coz.Birlestir[0])
		}
		if coz.Ele[0].ID != m[2].ID {
			t.Errorf("eleme id'si çözülmedi: %+v", coz.Ele[0])
		}
	})

	t.Run("kimseyi katmayan birleştirme reddedilir", func(t *testing.T) {
		// "Birleştirme" adı altında tek kaydın içeriğini yeniden yazmak,
		// gözden kaçan bir düzenleme yolu olurdu.
		m := memsVarsayilan()
		p := Plan{Ozet: "özet", Birlestir: []Birlestirme{{Tut: m[0].ID.String(), Icerik: "yeni metin"}}}
		if _, err := planDogrula(p, m); err == nil {
			t.Error("boş kat listesi reddedilmeli")
		}
	})
}
