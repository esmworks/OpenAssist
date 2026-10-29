package coretools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/esmworks/OpenAssist/internal/gateway"
)

// Ekip araçları veritabanına inmeden ÖNCE eleyebildikleri hatalar. Boş üye
// listesiyle kurulan bir ekip kenar çubuğunda görünür ama kanalına yazılan
// her mesaj cevapsız kalır — bunu kurulum anında söylemek, kullanıcının bir
// hafta sonra "neden konuşmuyor" diye sormasından iyidir.
func TestCreateTeamUyesizKurulmaz(t *testing.T) {
	tool := aracBul(t, "create_team")

	if _, err := tool.Handler(context.Background(),
		json.RawMessage(`{"name":"3E Satış","members":[]}`)); err == nil ||
		!strings.Contains(err.Error(), "members") {
		t.Fatalf("üyesiz ekip reddedilmeliydi: %v", err)
	}
	if _, err := tool.Handler(context.Background(),
		json.RawMessage(`{"name":"  ","members":["Ayşe"]}`)); err == nil ||
		!strings.Contains(err.Error(), "name") {
		t.Fatalf("adsız ekip reddedilmeliydi: %v", err)
	}
}

// Kontrat katı: bilinmeyen alan sessizce yutulmaz (agent "coordinator" gibi
// olmayan bir alanla çağırırsa niyeti kaybolmasın, hata alsın).
func TestEkipAraclariBilinmeyenAlaniReddeder(t *testing.T) {
	tool := aracBul(t, "create_team")

	_, err := tool.Handler(context.Background(),
		json.RawMessage(`{"name":"x","members":["a"],"coordinator":"a"}`))
	if err == nil || !strings.Contains(err.Error(), "geçersiz argüman") {
		t.Fatalf("bilinmeyen alan reddedilmeliydi: %v", err)
	}
}

// LightTools listesi ad ad yazılıyor: kayıtlı olmayan bir ad yazıldığında
// hata sessizdir (bot o aracı hiç göremez, allowlist'te ölü satır durur).
func TestLightToolsHepsiKayitli(t *testing.T) {
	gw := gateway.New()
	Register(gw, NewService(nil))
	kayitli := map[string]bool{}
	for _, tool := range gw.Tools() {
		kayitli["mcp__gw__"+tool.Name] = true
	}
	for _, ad := range LightTools() {
		if !kayitli[ad] {
			t.Errorf("LightTools'ta kayıtlı olmayan araç: %s", ad)
		}
	}
}
