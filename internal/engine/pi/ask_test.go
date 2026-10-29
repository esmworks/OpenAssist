package pi

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/esmworks/OpenAssist/internal/engine"
)

// yazicı — rpcConn'un stdin'i yerine geçen tampon.
type yazici struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (y *yazici) Write(p []byte) (int, error) {
	y.mu.Lock()
	defer y.mu.Unlock()
	return y.buf.Write(p)
}
func (y *yazici) Close() error { return nil }
func (y *yazici) metin() string {
	y.mu.Lock()
	defer y.mu.Unlock()
	return y.buf.String()
}

// cevapAl — yazılan extension_ui_response satırını çözer.
func cevapAl(t *testing.T, y *yazici) map[string]any {
	t.Helper()
	satirlar := strings.Split(strings.TrimSpace(y.metin()), "\n")
	var out map[string]any
	if err := json.Unmarshal([]byte(satirlar[len(satirlar)-1]), &out); err != nil {
		t.Fatalf("cevap çözülemedi (%q): %v", y.metin(), err)
	}
	return out
}

func izinIstegi(t *testing.T, arac string) uiRequest {
	t.Helper()
	zarf, err := json.Marshal(askEnvelope{Kind: "permission", Tool: arac})
	if err != nil {
		t.Fatal(err)
	}
	return uiRequest{ID: "u-1", Method: "input", Title: askMarker, Placeholder: string(zarf)}
}

func TestZarfCozYapisalIstek(t *testing.T) {
	ask, yapisal := zarfCoz(izinIstegi(t, "mcp__gw__gmail_send"))
	if !yapisal {
		t.Fatal("oa-ask zarfı tanınmadı")
	}
	if ask.Kind != "permission" || ask.Tool != "mcp__gw__gmail_send" {
		t.Errorf("zarf yanlış çözüldü: %+v", ask)
	}
}

// Üçüncü parti eklentiler işareti bilmez ama istekleri YİNE DE kullanıcıya
// gitmeli — sessizce geçirmek onay kapısında delik olurdu.
func TestZarfCozUcuncuParti(t *testing.T) {
	ask, yapisal := zarfCoz(uiRequest{Method: "confirm", Title: "Tehlikeli", Message: "Silinsin mi?"})
	if yapisal {
		t.Error("üçüncü parti diyalog yapısal sayıldı")
	}
	if ask.Kind != "permission" {
		t.Errorf("confirm izin sorusu olmalıydı: %q", ask.Kind)
	}
	if !strings.Contains(ask.Question, "Silinsin mi?") {
		t.Errorf("soru metni kayboldu: %q", ask.Question)
	}

	secim, _ := zarfCoz(uiRequest{Method: "select", Title: "Seç", Options: []string{"a", "b"}})
	if secim.Kind != "question" || len(secim.Choices) != 2 {
		t.Errorf("select seçenekleri taşınmadı: %+v", secim)
	}
}

// Bozuk zarf sessizce geçmez: genel eşlemeye düşer, istek yine sorulur.
func TestBozukZarfGenelEslemeyeDuser(t *testing.T) {
	ask, yapisal := zarfCoz(uiRequest{Method: "input", Title: askMarker, Placeholder: "{bozuk"})
	if yapisal {
		t.Fatal("bozuk zarf yapısal sayıldı")
	}
	if ask.Kind != "question" {
		t.Errorf("bozuk zarf soruya düşmeliydi: %q", ask.Kind)
	}
}

func TestOnAskYoksaFailClosed(t *testing.T) {
	y := &yazici{}
	c := newRPCConn(y)
	a := &Adapter{}

	a.handleUI(context.Background(), c, izinIstegi(t, "bash"), engine.TurnInput{}, func(engine.Event) {})

	var reply askReply
	if err := json.Unmarshal([]byte(cevapAl(t, y)["value"].(string)), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Behavior != "deny" {
		t.Errorf("işleyicisiz istek reddedilmedi: %+v", reply)
	}
}

// Ön-izin: allowlist'teki araç sorulmadan geçer (kart açılmaz).
func TestAllowlistKartAcmadanGecer(t *testing.T) {
	y := &yazici{}
	c := newRPCConn(y)
	a := &Adapter{}

	soruldu := false
	in := engine.TurnInput{
		AllowedTools: []string{"mcp__gw__gmail_search"},
		OnAsk: func(context.Context, engine.Ask) engine.Answer {
			soruldu = true
			return engine.Answer{Behavior: "deny"}
		},
	}

	var olaylar []engine.Event
	a.handleUI(context.Background(), c, izinIstegi(t, "mcp__gw__gmail_search"), in,
		func(e engine.Event) { olaylar = append(olaylar, e) })

	if soruldu {
		t.Error("ön-izinli araç için onay kapısı çağrıldı")
	}
	if len(olaylar) != 0 {
		t.Errorf("ön-izinli araç için istek olayı düştü: %+v", olaylar)
	}
	var reply askReply
	if err := json.Unmarshal([]byte(cevapAl(t, y)["value"].(string)), &reply); err != nil {
		t.Fatal(err)
	}
	if reply.Behavior != "allow" {
		t.Errorf("ön-izinli araç geçmedi: %+v", reply)
	}
}

// Listede OLMAYAN araç kapıya düşer — allowlist katalog filtresi değil.
func TestAllowlistDisiAracSorulur(t *testing.T) {
	y := &yazici{}
	c := newRPCConn(y)
	a := &Adapter{}

	soruldu := false
	in := engine.TurnInput{
		AllowedTools: []string{"mcp__gw__gmail_search"},
		OnAsk: func(context.Context, engine.Ask) engine.Answer {
			soruldu = true
			return engine.Answer{Behavior: "allow", Source: "api"}
		},
	}
	a.handleUI(context.Background(), c, izinIstegi(t, "bash"), in, func(engine.Event) {})

	if !soruldu {
		t.Error("listede olmayan araç sorulmadan geçti")
	}
}

func TestDevirKartAcmaz(t *testing.T) {
	y := &yazici{}
	c := newRPCConn(y)
	a := &Adapter{}

	zarf, _ := json.Marshal(askEnvelope{Kind: "bot_message", BotName: "Gelen Kutusu", Message: "bak"})
	req := uiRequest{ID: "u-9", Method: "input", Title: askMarker, Placeholder: string(zarf)}

	var olaylar []engine.Event
	in := engine.TurnInput{OnAsk: func(context.Context, engine.Ask) engine.Answer {
		return engine.Answer{Behavior: "allow", Message: "iletildi"}
	}}
	a.handleUI(context.Background(), c, req, in, func(e engine.Event) { olaylar = append(olaylar, e) })

	for _, e := range olaylar {
		if e.Kind == engine.KindRequestOpened {
			t.Error("devir için onay kartı açıldı — sohbete çip düşmeliydi")
		}
	}
}

// Tek yönlü metotlara cevap YAZILMAZ: pi beklemiyor, yazsaydık protokolde
// eşleşmeyen bir id dolaşırdı.
func TestTekYonluMetotlaraCevapYok(t *testing.T) {
	y := &yazici{}
	c := newRPCConn(y)
	a := &Adapter{}

	for _, m := range []string{"notify", "setStatus", "setWidget", "setTitle", "set_editor_text"} {
		a.handleUI(context.Background(), c, uiRequest{ID: "x", Method: m}, engine.TurnInput{}, func(engine.Event) {})
	}
	if y.metin() != "" {
		t.Errorf("tek yönlü metoda cevap yazıldı: %q", y.metin())
	}
}

// Onay merkezi koşu başına TEK bekleyen istek tutar; adaptör diyalogları
// sıraya almazsa iki kart birbirini ezer.
func TestEszamanliDiyaloglarSirayaGirer(t *testing.T) {
	y := &yazici{}
	c := newRPCConn(y)

	var mu sync.Mutex
	esZamanli, tepe := 0, 0
	c.onUIRequest = func(uiRequest) {
		mu.Lock()
		esZamanli++
		if esZamanli > tepe {
			tepe = esZamanli
		}
		mu.Unlock()

		time.Sleep(20 * time.Millisecond)

		mu.Lock()
		esZamanli--
		mu.Unlock()
	}

	for i := 0; i < 5; i++ {
		c.dispatch([]byte(`{"type":"extension_ui_request","id":"u","method":"confirm"}`))
	}

	sonuna := time.After(2 * time.Second)
	for {
		mu.Lock()
		bitti := tepe > 0 && esZamanli == 0
		mu.Unlock()
		if bitti {
			break
		}
		select {
		case <-sonuna:
			t.Fatal("diyaloglar bitmedi")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if tepe != 1 {
		t.Errorf("aynı anda %d diyalog işlendi — onay merkezi tek istek tutuyor, kartlar birbirini ezerdi", tepe)
	}
}

// Süreç öldükten sonra açılan komut süresiz beklememeli.
func TestOlmusSurecteKomutBeklemez(t *testing.T) {
	c := newRPCConn(&yazici{})
	c.readLoop(strings.NewReader("")) // hemen EOF → failAll

	bitti := make(chan error, 1)
	go func() {
		_, err := c.command(context.Background(), map[string]any{"type": "get_last_assistant_text"})
		bitti <- err
	}()

	select {
	case err := <-bitti:
		if err == nil {
			t.Fatal("kapalı bağlantıda komut başarılı döndü")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("kapalı bağlantıda komut asılı kaldı")
	}
}

var _ io.WriteCloser = (*yazici)(nil)
