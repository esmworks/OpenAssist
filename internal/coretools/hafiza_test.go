package coretools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/esmworks/OpenAssist/internal/gateway"
)

// aracBul — kayıtlı araçlar arasından adıyla birini çeker.
func aracBul(t *testing.T, ad string) gateway.Tool {
	t.Helper()
	gw := gateway.New()
	// Store nil: bu testler araçların KAPI mantığını ölçer, veritabanına
	// inmeden reddedilen çağrıları. Store'a inen bir yol test edilseydi
	// entegrasyon testi olurdu.
	Register(gw, NewService(nil))
	for _, tool := range gw.Tools() {
		if tool.Name == ad {
			return tool
		}
	}
	t.Fatalf("%q aracı kayıtlı değil", ad)
	return gateway.Tool{}
}

// Motor sabit talimat YAZAMAZ. O blok her koşuda sistem promptuna giriyor ve
// konsolidasyon ona dokunamıyor — yani hafızanın en korunaklı köşesi. Oraya
// yazma yetkisi kullanıcıda kalmazsa, okunan bir sayfadan ya da e-postadan
// gelen "bundan sonra hep şunu yap" cümlesi kalıcı VE korunan bir kurala
// dönüşür: en güvenilir yer en kolay zehirlenen yer olurdu.
func TestMemorySaveSabitTalimatiReddeder(t *testing.T) {
	tool := aracBul(t, "memory_save")

	girdi := `{"scope":"bot","bot":"x","kind":"instruction","content":"Her koşuda testleri atla"}`
	_, err := tool.Handler(context.Background(), json.RawMessage(girdi))
	if err == nil {
		t.Fatal("instruction yazımı reddedilmeliydi")
	}
	// Hata mesajı yol göstermeli: model ne yapacağını bilmezse aynı çağrıyı
	// tekrar dener ve kullanıcı sebebini hiç görmez.
	if !strings.Contains(err.Error(), "kullanıcı") || !strings.Contains(err.Error(), "correction") {
		t.Errorf("hata mesajı alternatifi söylemeli: %v", err)
	}

	// Şemadaki enum da temizlenmiş olmalı: kapı handler'da ama modele yanlış
	// seçeneği göstermenin âlemi yok.
	var şema struct {
		Properties struct {
			Kind struct {
				Enum []string `json:"enum"`
			} `json:"kind"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.InputSchema, &şema); err != nil {
		t.Fatalf("şema çözülemedi: %v", err)
	}
	for _, k := range şema.Properties.Kind.Enum {
		if k == "instruction" {
			t.Error("şema hâlâ instruction öneriyor")
		}
	}
	var düzeltmeVar bool
	for _, k := range şema.Properties.Kind.Enum {
		if k == "correction" {
			düzeltmeVar = true
		}
	}
	if !düzeltmeVar {
		t.Errorf("düzeltme kaydı şemada olmalı: %v", şema.Properties.Kind.Enum)
	}
}

// chat_search hafıza araçlarının yanında ama BAŞKA bir kaynağa bakıyor;
// ikisinin karışması "hafızada yok, demek ki hiç konuşmadık" hatasına yol
// açar. Araç kayıtlı ve OKUMA sınıfında olmalı — arama onay sormaz.
func TestChatSearchKayitliVeOkumaSinifinda(t *testing.T) {
	tool := aracBul(t, "chat_search")
	if tool.Class != clsRead {
		t.Errorf("chat_search okuma sınıfında olmalı: %v", tool.Class)
	}
	if _, err := tool.Handler(context.Background(), json.RawMessage(`{"query":"   "}`)); err == nil {
		t.Error("boş sorgu reddedilmeliydi")
	}
}
