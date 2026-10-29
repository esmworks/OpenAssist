package memory

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	db "github.com/esmworks/OpenAssist/internal/store/db"
)

func TestDisaAktarIceAktarGidisDonus(t *testing.T) {
	bot := db.Bot{ID: uuid.New(), Name: "Gelen Kutusu"}
	mems := []db.Memory{
		{Scope: "user", Kind: "preference", Content: "Kullanıcı Türkçe iletişim ister"},
		{Scope: "user", Kind: "fact", Content: "Saat dilimi Europe/Istanbul"},
		{
			Scope: "bot", BotID: uuid.NullUUID{UUID: bot.ID, Valid: true},
			Kind: "instruction", Content: "Taslakları resmi dille yaz",
		},
	}

	metin := DisaAktar(mems, []db.Bot{bot}, time.Now())
	satirlar, err := IceAktarCoz(metin)
	if err != nil {
		t.Fatalf("kendi çıktımızı okuyamadık: %v\n%s", err, metin)
	}
	if len(satirlar) != 3 {
		t.Fatalf("3 satır beklenir, %d geldi: %+v", len(satirlar), satirlar)
	}
	if satirlar[0].Scope != "user" || satirlar[0].Kind != "preference" {
		t.Errorf("user satırı yanlış: %+v", satirlar[0])
	}
	if satirlar[2].Scope != "bot" || satirlar[2].BotAdi != "Gelen Kutusu" {
		t.Errorf("bot satırı yanlış: %+v", satirlar[2])
	}
	if satirlar[2].Content != "Taslakları resmi dille yaz" {
		t.Errorf("içerik bozuldu: %q", satirlar[2].Content)
	}
}

func TestDisaAktarCokSatirliIcerigiDuzler(t *testing.T) {
	// Çok satırlı bir olgu ayrıştırmayı belirsizleştirir; tek satıra indirilir
	// ki gidiş-dönüş bozulmasın.
	mems := []db.Memory{{Scope: "user", Kind: "fact", Content: "birinci satır\nikinci satır"}}
	metin := DisaAktar(mems, nil, time.Now())
	satirlar, err := IceAktarCoz(metin)
	if err != nil {
		t.Fatalf("IceAktarCoz: %v", err)
	}
	if len(satirlar) != 1 || satirlar[0].Content != "birinci satır ikinci satır" {
		t.Errorf("düzleştirme başarısız: %+v", satirlar)
	}
}

// TestDisaAktarSureVeGuveniTasir — geçici bir olgu gidiş-dönüşte kalıcılaşmamalı.
// Alanlar taşınmasaydı içe aktarma ExpiresAt=nil yazar, content_hash aynı
// olduğu için upsert var olan kaydın expires_at'ini de NULL ile ezerdi:
// "kullanıcı bu hafta İzmir'de" haftalar sonra hâlâ promptta.
func TestDisaAktarSureVeGuveniTasir(t *testing.T) {
	son := time.Date(2026, 9, 1, 10, 30, 0, 0, time.UTC)
	mems := []db.Memory{
		{Scope: "user", Kind: "fact", Content: "Kullanıcı bu hafta İzmir'de", ExpiresAt: &son, Confidence: 0.6},
		{Scope: "user", Kind: "fact", Content: "Kalıcı olgu", Confidence: 1},
	}
	metin := DisaAktar(mems, nil, time.Now())
	satirlar, err := IceAktarCoz(metin)
	if err != nil {
		t.Fatalf("IceAktarCoz: %v", err)
	}
	if len(satirlar) != 2 {
		t.Fatalf("2 satır beklenir: %+v", satirlar)
	}
	if satirlar[0].ExpiresAt == nil || !satirlar[0].ExpiresAt.Equal(son) {
		t.Errorf("expires taşınmadı: %+v", satirlar[0].ExpiresAt)
	}
	if satirlar[0].Confidence < 0.59 || satirlar[0].Confidence > 0.61 {
		t.Errorf("conf taşınmadı: %v", satirlar[0].Confidence)
	}
	// Varsayılan değerler satırı kirletmemeli.
	if satirlar[1].ExpiresAt != nil || satirlar[1].Confidence != 0 {
		t.Errorf("varsayılan kayıt ek alan yazmamalı: %+v", satirlar[1])
	}
	if strings.Contains(metin, "conf=1.00") {
		t.Errorf("varsayılan güven yazılmamalı:\n%s", metin)
	}
}

func TestIceAktarCozTarihBicimleri(t *testing.T) {
	// Dışa aktarma tam damga yazar; insan elle yalnız tarih yazabilmeli.
	metin := aktarimBaslik + "\n## user\n- [fact expires=2026-09-01] tarih biçimi\n"
	satirlar, err := IceAktarCoz(metin)
	if err != nil {
		t.Fatalf("IceAktarCoz: %v", err)
	}
	if satirlar[0].ExpiresAt == nil || satirlar[0].ExpiresAt.Year() != 2026 {
		t.Errorf("yalnız tarih kabul edilmeliydi: %+v", satirlar[0].ExpiresAt)
	}
}

func TestIceAktarCozHatalari(t *testing.T) {
	gecerliBaslik := aktarimBaslik + "\n"

	hatalar := map[string]string{
		"başlıksız dosya":   "## user\n\n- [fact] bir şey\n",
		"kapsamsız satır":   gecerliBaslik + "- [fact] kapsam başlığı yok\n",
		"bilinmeyen tür":    gecerliBaslik + "## user\n- [gizli] tanınmayan tür\n",
		"boş içerik":        gecerliBaslik + "## user\n- [fact]   \n",
		"kapanmamış köşeli": gecerliBaslik + "## user\n- [fact bozuk\n",
		"anlaşılmaz satır":  gecerliBaslik + "## user\n* yanlış madde işareti\n",
		"boş bot adı":       gecerliBaslik + "## bot: \n- [fact] x\n",
		// Ek alanlar da hoşgörüsüz: sessizce atlamak, kullanıcının yazdığı
		// alanın kaybolduğunu gizlerdi.
		"bilinmeyen alan":  gecerliBaslik + "## user\n- [fact sahte=1] x\n",
		"bozuk tarih":      gecerliBaslik + "## user\n- [fact expires=yarın] x\n",
		"aralık dışı conf": gecerliBaslik + "## user\n- [fact conf=1.5] x\n",
		"ad=değer değil":   gecerliBaslik + "## user\n- [fact expires] x\n",
	}
	for ad, metin := range hatalar {
		t.Run(ad, func(t *testing.T) {
			// Sessizce atlamak yerine hata: kullanıcının elle düzenlediği
			// dosyanın yarısının yüklenmesi, hiç yüklenmemesinden kötü.
			if _, err := IceAktarCoz(metin); err == nil {
				t.Errorf("hata beklenirdi:\n%s", metin)
			}
		})
	}
}

func TestIceAktarCozYorumlariGecer(t *testing.T) {
	metin := aktarimBaslik + `
# Dışa aktarma: 2026-08-14T20:00:00Z
# elle düzenlenebilir

## user

- [fact] tek olgu
`
	satirlar, err := IceAktarCoz(metin)
	if err != nil {
		t.Fatalf("IceAktarCoz: %v", err)
	}
	if len(satirlar) != 1 || satirlar[0].Content != "tek olgu" {
		t.Errorf("yorumlar atlanmalıydı: %+v", satirlar)
	}
}

func TestDisaAktarBilinmeyenBotuIdileYazar(t *testing.T) {
	// Bot silinmiş ama hafızası duruyorsa dışa aktarım düşmemeli.
	botID := uuid.New()
	mems := []db.Memory{{
		Scope: "bot", BotID: uuid.NullUUID{UUID: botID, Valid: true},
		Kind: "fact", Content: "sahipsiz olgu",
	}}
	metin := DisaAktar(mems, nil, time.Now())
	if !strings.Contains(metin, botID.String()) {
		t.Errorf("bilinmeyen bot id'siyle yazılmalıydı:\n%s", metin)
	}
}
