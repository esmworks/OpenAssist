//go:build integration

package pi

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/esmworks/OpenAssist/internal/engine"
)

// Bu dosya GERÇEK pi ikilisiyle koşar (CI'da değil):
//
//	go test -tags integration ./internal/engine/pi/...
//
// Sağlayıcı ULAŞILAMAZ bir adrestir — LLM cevabı beklemiyoruz. Ölçtüğümüz
// şey protokolün kendisi: süreç doğuyor mu, eklenti yükleniyor mu, gateway
// araçlarını session_start'ta kaydediyor mu, hata yolu tur sonucuna doğru
// yansıyor mu, süreç temiz kapanıyor mu. Bunlar sürüm yükseltmesinde
// sessizce kayan şeylerdir.

func piVarMi(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("pi"); err != nil {
		t.Skip("pi PATH'te yok — canlı test atlandı")
	}
}

// sahteGateway — tools/list ve tools/call konuşan asgari MCP yüzeyi.
func sahteGateway(t *testing.T, cagrildi chan<- string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     any            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		select {
		case cagrildi <- req.Method:
		default:
		}

		var result any
		switch req.Method {
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{{
				"name":        "deneme_araci",
				"description": "canlı test aracı",
				"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
			}}}
		default:
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": "ok"}}}
		}
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func canliAdaptor(t *testing.T, gwURL string) *Adapter {
	t.Helper()
	kok := t.TempDir()
	a := &Adapter{
		ConfigDir:     kok,
		ExtensionsDir: filepath.Join(kok, "extensions"),
		// Tarifler artık DB'den gelir; testte kaynak doğrudan verilir.
		Providers: func(context.Context) ([]ProviderConfig, error) {
			return []ProviderConfig{{
				ID:      "oa-test",
				BaseURL: "http://127.0.0.1:9/v1", // kapalı port: bağlantı anında düşer
				APIKey:  "sk-canli-test",
				Models:  []ModelSpec{{ID: "test-model", ContextWindow: 100000, MaxTokens: 4096}},
			}}, nil
		},
		DefaultModel: "oa-test/test-model",
		Gateways:     []Gateway{{Name: "gw", URL: gwURL}},
	}
	return a
}

// Turun TAMAMI: süreç doğar, eklenti yüklenir, gateway'e bağlanır, hata
// yolundan geçer ve kapanır. pi geçici hataları 3 kez denediği için bu
// yaklaşık 15 saniye sürer — asılı kalmadığının da kanıtı.
func TestCanliTurHataylaKapanir(t *testing.T) {
	piVarMi(t)

	cagrildi := make(chan string, 8)
	srv := sahteGateway(t, cagrildi)
	a := canliAdaptor(t, srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	var olaylar []engine.Event
	res, err := a.SendTurn(ctx, engine.TurnInput{
		Prompt:     "merhaba",
		CWD:        t.TempDir(),
		Extensions: []string{OAGateway},
		OnAsk: func(context.Context, engine.Ask) engine.Answer {
			return engine.Answer{Behavior: "deny"}
		},
	}, func(e engine.Event) { olaylar = append(olaylar, e) })
	if err != nil {
		t.Fatalf("SendTurn taşıyıcı hatası verdi (tur sonucu bekleniyordu): %v", err)
	}

	// Sağlayıcı ulaşılamaz: tur BAŞARISIZ bitmeli ve sebep TAŞINMALI.
	if res.Ok {
		t.Error("ulaşılamaz sağlayıcıyla tur başarılı sayıldı")
	}
	if res.StopReason == "" || res.StopReason == "error" {
		t.Errorf("StopReason gerçek mesajı taşımıyor: %q", res.StopReason)
	}
	if res.ResumeCursor == "" {
		t.Error("imleç boş döndü — bir sonraki tur oturumu sürdüremezdi")
	}

	// Eklenti gerçekten yüklendi mi? Kanıtı gateway'e gelen çağrıdır.
	var listeGeldi bool
	for {
		select {
		case m := <-cagrildi:
			if m == "tools/list" {
				listeGeldi = true
			}
			continue
		default:
		}
		break
	}
	if !listeGeldi {
		t.Error("oa-gateway session_start'ta tools/list çağırmadı — araçlar modele hiç görünmezdi")
	}

	var hataVar bool
	for _, e := range olaylar {
		if e.Kind == engine.KindError {
			hataVar = true
		}
	}
	if !hataVar {
		t.Error("hata olayı akışa düşmedi")
	}
}

// Eksik eklenti sessizce atlanmamalı: onay kapısı orada yaşıyor.
func TestCanliEksikEklentiKosuAcmaz(t *testing.T) {
	piVarMi(t)

	srv := sahteGateway(t, make(chan string, 1))
	a := canliAdaptor(t, srv.URL)

	_, err := a.SendTurn(context.Background(), engine.TurnInput{
		Prompt:     "merhaba",
		CWD:        t.TempDir(),
		Extensions: []string{"olmayan-eklenti.ts"},
	}, func(engine.Event) {})
	if err == nil {
		t.Fatal("eksik eklentiyle koşu açıldı — onay kapısız çalışırdı")
	}
	if !strings.Contains(err.Error(), "olmayan-eklenti.ts") {
		t.Errorf("hata hangi eklentinin eksik olduğunu söylemiyor: %v", err)
	}
}

// Yoklama gerçek ikiliden sürüm ve model listesi okumalı.
func TestCanliProbe(t *testing.T) {
	piVarMi(t)

	srv := sahteGateway(t, make(chan string, 1))
	a := canliAdaptor(t, srv.URL)

	st := a.Probe(context.Background())
	if !st.Available {
		t.Fatalf("pi kurulu ama yoklama bulamadı: %s", st.Detail)
	}
	if st.Version == "" {
		t.Error("sürüm okunamadı")
	}
	// Tarifimizdeki model listede olmalı: models.json'ı biz yazdık.
	var bizimki bool
	for _, m := range st.Models {
		if strings.Contains(m.ID, "test-model") {
			bizimki = true
		}
	}
	if !bizimki {
		t.Errorf("yazdığımız sağlayıcı --list-models çıktısında yok: %+v", st.Models)
	}
}

// Ürettiğimiz models.json'ı GERÇEK pi kabul ediyor mu?
//
// Bu testin varlık sebebi somut: models.dev kataloğundan gelen
// ["text","image","audio","video"] kip listesi pi'nin şemasını düşürüyordu
// ve TEK geçersiz değer dosyanın TAMAMINI iptal ettiriyor — sağlayıcı
// doğru kayıtlıyken bile hiçbir model görünmüyor, koşu «Model … not found»
// ile ölüyordu. Birim testi bunu göremez: doğruyu bilen tek yer ikilinin
// kendi şeması.
//
// Yoklama yolunun tamamı koşuyor (hazirla → models.json → cleanEnv →
// pi --list-models), yani dosya ile ortamın eşleşmesi de ölçülüyor.
func TestCanliUrettigimizModelsJsonuPiKabulEder(t *testing.T) {
	piVarMi(t)

	dizin := t.TempDir()
	a := &Adapter{
		ConfigDir: dizin,
		Providers: func(context.Context) ([]ProviderConfig, error) {
			return []ProviderConfig{{
				ID: "oa-test", Name: "OA Test", BaseURL: "http://127.0.0.1:9/v1",
				API: "openai-responses", APIKey: "sk-canli-test",
				Models: []ModelSpec{{
					ID: "test-model", Name: "Test", Reasoning: true,
					// Katalogdan gelebilecek EN GENİŞ liste: pi yalnız ilk
					// ikisini tanıyor, gerisi elenmezse dosya reddedilir.
					Input:         []string{"text", "image", "audio", "video", "pdf"},
					ContextWindow: 1000000, MaxTokens: 131072,
				}},
			}}, nil
		},
	}

	st := a.Probe(context.Background())
	if !st.Available {
		t.Fatalf("pi çalışmadı: %s", st.Detail)
	}
	if len(st.Models) != 1 || st.Models[0].ID != "oa-test/test-model" {
		t.Fatalf("pi modelimizi görmedi — models.json reddedilmiş olabilir. detay: %s, modeller: %+v",
			st.Detail, st.Models)
	}

	// Dosya gerçekten temiz mi (pi ileride şemayı gevşetse bile kip
	// listesini biz dar tutuyoruz).
	ham, err := os.ReadFile(filepath.Join(dizin, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, kip := range []string{"audio", "video", "pdf"} {
		if strings.Contains(string(ham), kip) {
			t.Fatalf("tanınmayan kip %q dosyaya sızdı:\n%s", kip, ham)
		}
	}
}

// ── Ücretli uç: gerçek modelle görüntü okuma (ADIM 13) ─────────────────
//
// Yukarıdaki canlı testler protokolü ölçer, model çağırmaz (sağlayıcı
// ulaşılamaz bir adres). Ek→motor yolunun SON halkası ise ancak gerçek bir
// modelle görülebilir: pi'nin read aracı görüntüyü modele image bloğu olarak
// mi yolluyor, yoksa metin sanıp UTF-8 mi çözüyor?
//
// Bu yüzden bu test PARA HARCAR ve yalnız üç değişken verilince koşar:
//
//	OA_CANLI_PROVIDER_URL=https://api.…/v1 \
//	OA_CANLI_PROVIDER_KEY=sk-… \
//	OA_CANLI_MODEL=saglayici/model \
//	go test -tags integration -run CanliGoruntuOkuma ./internal/engine/pi/
//
// Model GÖRÜNTÜ girdisi kabul etmelidir (models.json'da input: image).
func TestCanliGoruntuOkuma(t *testing.T) {
	piVarMi(t)
	url, key, model := os.Getenv("OA_CANLI_PROVIDER_URL"), os.Getenv("OA_CANLI_PROVIDER_KEY"), os.Getenv("OA_CANLI_MODEL")
	if url == "" || key == "" || model == "" {
		t.Skip("OA_CANLI_PROVIDER_URL/KEY/MODEL yok — ücretli test atlandı")
	}

	kutu := t.TempDir()
	// Ek dizinini gerçek akıştaki gibi AYRI tut: motor dosyayı çalışma
	// dizininden değil, ekler kökünden okuyacak (orchestrator.ekBlok).
	ekDizin := t.TempDir()
	görüntü := filepath.Join(ekDizin, "kare.png")
	if err := os.WriteFile(görüntü, kirmiziKarePNG(t), 0o600); err != nil {
		t.Fatal(err)
	}

	srv := sahteGateway(t, make(chan string, 4))
	a := canliAdaptor(t, srv.URL)
	a.DefaultModel = model
	a.Providers = func(context.Context) ([]ProviderConfig, error) {
		sag, mdl, _ := strings.Cut(model, "/")
		return []ProviderConfig{{
			ID: sag, Name: sag, BaseURL: url, APIKey: key,
			Models: []ModelSpec{{
				ID: mdl, Name: mdl, Input: []string{"text", "image"},
				ContextWindow: 200000, MaxTokens: 4096,
			}},
		}}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// Prompt ekBlok'un ürettiğiyle aynı biçimde: dosya yolu + tek soru.
	prompt := "Ekli dosyalar (sunucuda, buradan erişebilirsin):\n" +
		"- kare.png (image): " + görüntü + "\n\n" +
		"Görüntüdeki tek rengi TEK KELİMEYLE söyle (kirmizi/yesil/mavi). Başka bir şey yazma."

	res, err := a.SendTurn(ctx, engine.TurnInput{
		Prompt:     prompt,
		CWD:        kutu,
		Extensions: []string{OAGateway},
		OnAsk: func(_ context.Context, ask engine.Ask) engine.Answer {
			// Görüntüyü okuması için read'e izin ver; gerisi kapalı.
			if strings.Contains(strings.ToLower(ask.Tool), "read") {
				return engine.Answer{Behavior: "allow", Source: "test"}
			}
			return engine.Answer{Behavior: "deny", Source: "test"}
		},
	}, func(engine.Event) {})
	if err != nil {
		t.Fatalf("SendTurn: %v", err)
	}
	if !res.Ok {
		t.Fatalf("tur başarısız: %s", res.StopReason)
	}
	cevap := strings.ToLower(res.FinalText)
	if !strings.Contains(cevap, "kirmizi") && !strings.Contains(cevap, "kırmızı") && !strings.Contains(cevap, "red") {
		t.Fatalf("model görüntüyü okuyamadı (kırmızı beklenirdi): %q", res.FinalText)
	}
	t.Logf("model cevabı: %q (maliyet %.4f USD)", res.FinalText, res.CostUSD)
}

// kirmiziKarePNG — 32×32 düz kırmızı PNG. İçeriği tarif edilebilir olsun diye
// düz renk: model görüyorsa tek kelimeyle söyler, görmüyorsa uydurur.
func kirmiziKarePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{R: 220, G: 20, B: 30, A: 255}}, image.Point{}, draw.Src)
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
