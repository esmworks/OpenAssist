package pi

import (
	"encoding/json"
	"testing"

	"github.com/esmworks/OpenAssist/internal/engine"
)

// tur — satırları sırayla parser'dan geçirir (adaptörün okuma döngüsü gibi).
func tur(t *testing.T, p *parser, lines ...string) []engine.Event {
	t.Helper()
	var out []engine.Event
	for _, l := range lines {
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(l), &head); err != nil {
			t.Fatalf("test verisi bozuk: %v", err)
		}
		out = append(out, p.parseLine(head.Type, []byte(l))...)
	}
	return out
}

func TestMetinAkisi(t *testing.T) {
	p := &parser{}
	evs := tur(t, p,
		`{"type":"message_update","usage":{"input":10,"output":2,"cost":{"total":0.01}},"assistantMessageEvent":{"type":"text_delta","delta":"Mer"}}`,
		`{"type":"message_update","usage":{"input":10,"output":4,"cost":{"total":0.02}},"assistantMessageEvent":{"type":"text_delta","delta":"haba"}}`,
		`{"type":"message_update","usage":{"input":10,"output":4,"cost":{"total":0.02}},"assistantMessageEvent":{"type":"text_end","content":"Merhaba"}}`,
	)
	if len(evs) != 3 {
		t.Fatalf("3 olay bekleniyordu: %d", len(evs))
	}
	if evs[0].Kind != engine.KindAssistantDelta || evs[0].Text != "Mer" {
		t.Fatalf("delta bekleniyordu: %+v", evs[0])
	}
	if evs[2].Kind != engine.KindAssistantText || evs[2].Text != "Merhaba" {
		t.Fatalf("tamamlanmış metin bekleniyordu: %+v", evs[2])
	}
	if p.usage.Output != 4 || p.usage.Cost.Total != 0.02 {
		t.Fatalf("kullanım son dolu değere ayarlanmalıydı: %+v", p.usage)
	}
}

// Sağlayıcı akış boyunca kullanım bildirmezse sayaçlar sıfır kalır ve
// tamamlanmada tek seferde gelir; boş anlık görüntü dolu olanı EZMEMELİ.
func TestBosKullanimDoluyuEzmez(t *testing.T) {
	p := &parser{}
	tur(t, p,
		`{"type":"message_update","usage":{"input":100,"output":50,"cost":{"total":0.5}},"assistantMessageEvent":{"type":"text_delta","delta":"x"}}`,
		`{"type":"message_update","usage":{"input":0,"output":0,"cost":{"total":0}},"assistantMessageEvent":{"type":"text_delta","delta":"y"}}`,
	)
	if p.usage.Input != 100 || p.usage.Output != 50 {
		t.Fatalf("boş kullanım dolu değeri ezmiş: %+v", p.usage)
	}
}

func TestAracOlaylari(t *testing.T) {
	p := &parser{}
	evs := tur(t, p,
		`{"type":"tool_execution_start","toolCallId":"c1","toolName":"bash","args":{"command":"ls"}}`,
		`{"type":"tool_execution_end","toolCallId":"c1","toolName":"bash","isError":true}`,
	)
	if len(evs) != 2 {
		t.Fatalf("2 olay bekleniyordu: %d", len(evs))
	}
	if evs[0].Kind != engine.KindToolStarted || evs[0].Tool.Name != "bash" || evs[0].Tool.ID != "c1" {
		t.Fatalf("tool.started bekleniyordu: %+v", evs[0])
	}
	if string(evs[0].Tool.Input) != `{"command":"ls"}` {
		t.Fatalf("araç girdisi ham geçmeliydi: %s", evs[0].Tool.Input)
	}
	if evs[1].Kind != engine.KindToolCompleted || !evs[1].Tool.IsError {
		t.Fatalf("hatalı tool.completed bekleniyordu: %+v", evs[1])
	}
}

// pi hatayı ayrı bir olayla değil, stopReason="error" taşıyan asistan
// mesajıyla bildirir. Bunu kaçırırsak tur "başarılı ama boş" görünür.
func TestHataMesajEndIleGelir(t *testing.T) {
	p := &parser{}
	evs := tur(t, p,
		`{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"401 Unauthorized"}}`,
	)
	if len(evs) != 1 || evs[0].Kind != engine.KindError {
		t.Fatalf("runtime.error bekleniyordu: %+v", evs)
	}
	if p.errMsg != "401 Unauthorized" {
		t.Fatalf("hata metni tutulmalıydı: %q", p.errMsg)
	}
}

func TestBasariliMesajEndSessiz(t *testing.T) {
	p := &parser{}
	evs := tur(t, p, `{"type":"message_end","message":{"role":"assistant","stopReason":"stop"}}`)
	if len(evs) != 0 {
		t.Fatalf("başarılı message_end olay üretmemeli: %+v", evs)
	}
	if p.errMsg != "" {
		t.Fatalf("hata yokken errMsg dolmamalı: %q", p.errMsg)
	}
}

// pi geçici hataları KENDİSİ yeniden dener (canlı ölçüm: 3 deneme, sonra
// auto_retry_end). İlk denemenin hatası kalıcı sayılsaydı, ikinci denemede
// gelen cevap kullanıcıya ulaşmasına rağmen koşu "başarısız" yazılırdı.
func TestBasariliYenidenDenemeHatayiSiler(t *testing.T) {
	p := &parser{}
	tur(t, p,
		`{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"Connection error."}}`,
		`{"type":"auto_retry_start","attempt":1,"maxAttempts":3}`,
		`{"type":"message_end","message":{"role":"assistant","stopReason":"stop"}}`,
		`{"type":"auto_retry_end","success":true,"attempt":1}`,
		`{"type":"agent_settled"}`,
	)
	if p.errMsg != "" {
		t.Fatalf("başarılı yeniden deneme hatayı silmedi: %q", p.errMsg)
	}
}

// Tükenen yeniden denemeler: yetkili kapanış auto_retry_end'dir.
func TestTukenenYenidenDenemeHatayiTutar(t *testing.T) {
	p := &parser{}
	evs := tur(t, p,
		`{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"Connection error."}}`,
		`{"type":"auto_retry_end","success":false,"attempt":3,"finalError":"Connection error."}`,
	)
	if p.errMsg != "Connection error." {
		t.Fatalf("son hata tutulmalıydı: %q", p.errMsg)
	}
	if len(evs) != 2 {
		t.Fatalf("hem mesaj hem kapanış hata olayı bekleniyordu: %+v", evs)
	}
}

// Kullanıcı mesajının kapanışı stopReason taşımaz; hata durumunu bozmamalı.
func TestKullaniciMesajEndHataySilmez(t *testing.T) {
	p := &parser{}
	tur(t, p,
		`{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"401"}}`,
		`{"type":"message_end","message":{"role":"user","content":[]}}`,
	)
	if p.errMsg != "401" {
		t.Fatalf("kullanıcı mesajı hatayı sildi: %q", p.errMsg)
	}
}

func TestTurSayaciVeSettle(t *testing.T) {
	p := &parser{}
	tur(t, p,
		`{"type":"turn_end","message":{}}`,
		`{"type":"turn_end","message":{}}`,
		`{"type":"agent_settled"}`,
	)
	if p.turns != 2 {
		t.Fatalf("2 tur bekleniyordu: %d", p.turns)
	}
	if !p.settled {
		t.Fatal("agent_settled görülmeliydi")
	}
}

// Tanınmayan olay türü koşuyu kırmaz: ham satırıyla kayda geçer.
func TestBilinmeyenOlayHamGecer(t *testing.T) {
	p := &parser{}
	satir := `{"type":"gelecek_surumden_yeni_olay","x":1}`
	evs := tur(t, p, satir)
	if len(evs) != 1 || evs[0].Kind != engine.KindUnknown {
		t.Fatalf("unknown bekleniyordu: %+v", evs)
	}
	if string(evs[0].Raw) != satir {
		t.Fatalf("ham satır korunmalıydı: %s", evs[0].Raw)
	}
}

func TestEklentiHatasiTuruDusurmez(t *testing.T) {
	p := &parser{}
	evs := tur(t, p,
		`{"type":"extension_error","extensionPath":"/x/oa-gateway.ts","event":"tool_call","error":"boom"}`,
	)
	if len(evs) != 1 || evs[0].Kind != engine.KindError {
		t.Fatalf("eklenti hatası olay üretmeliydi: %+v", evs)
	}
	if p.errMsg != "" {
		t.Fatal("eklenti hatası turu düşürmemeli")
	}
}
