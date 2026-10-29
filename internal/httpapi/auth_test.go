package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/esmworks/OpenAssist/internal/config"
	"github.com/esmworks/OpenAssist/internal/httpapi"
)

// testBelirtec — testlerin kullandığı sahte API_TOKEN. config'in 32 karakter
// alt sınırının üstünde tutulur ki testler üretimle aynı uzunluk sınıfında
// koşsun.
const testBelirtec = "test-token-0123456789abcdef0123456789abcdef"

// kapiSunucusu — gerçek New() bileşimi, DB'siz (yalnız /healthz kayıtlı).
// Kayıtlı olmayan yollar 404 verir; kapı çalışıyorsa belirteçsiz istek oraya
// hiç ulaşmaz ve 401 döner.
func kapiSunucusu(t *testing.T, token string) *httptest.Server {
	t.Helper()
	srv := httpapi.New(config.Config{HTTPAddr: ":0", APIToken: token}, httpapi.Deps{})
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)
	return ts
}

func iste(t *testing.T, ts *httptest.Server, yol, yetki string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+yol, nil)
	if err != nil {
		t.Fatal(err)
	}
	if yetki != "" {
		req.Header.Set("Authorization", yetki)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestBearerKapisi(t *testing.T) {
	ts := kapiSunucusu(t, testBelirtec)

	tablo := []struct {
		ad, yol, yetki string
		beklenen       int
	}{
		{"healthz belirteçsiz açık", "/healthz", "", http.StatusOK},
		// Kapıyı geçen istek mux'a ulaşır; rota kayıtlı olmadığı için 404 —
		// 401 OLMAMASI kapının açıldığının kanıtı.
		{"doğru belirteç kapıyı geçer", "/api/bots", "Bearer " + testBelirtec, http.StatusNotFound},
		{"şema harf duyarsız", "/api/bots", "bearer " + testBelirtec, http.StatusNotFound},
		{"başlıksız ret", "/api/bots", "", http.StatusUnauthorized},
		{"yanlış belirteç ret", "/api/bots", "Bearer yanlis", http.StatusUnauthorized},
		{"doğru belirtecin öneki ret", "/api/bots", "Bearer " + testBelirtec[:20], http.StatusUnauthorized},
		{"şemasız ham belirteç ret", "/api/bots", testBelirtec, http.StatusUnauthorized},
		{"Basic ret", "/api/bots", "Basic " + testBelirtec, http.StatusUnauthorized},
		{"boş bearer ret", "/api/bots", "Bearer ", http.StatusUnauthorized},
		// Bilinmeyen yol da korumalı: varsayılan RET. Kapı tek tek uçlara
		// değil mux'un tamamına takılı — yeni bir uç eklendiğinde sarmayı
		// unutmak mümkün değil.
		{"bilinmeyen yol ret", "/api/yok", "", http.StatusUnauthorized},
		{"mcp artık bu yüzeyde yok", "/mcp", "Bearer " + testBelirtec, http.StatusNotFound},
	}
	for _, d := range tablo {
		t.Run(d.ad, func(t *testing.T) {
			if got := iste(t, ts, d.yol, d.yetki).StatusCode; got != d.beklenen {
				t.Errorf("%s %q = %d, beklenen %d", d.yol, d.yetki, got, d.beklenen)
			}
		})
	}
}

// TestBearerBosBelirtecKapali — regresyon: sabit süreli karşılaştırma boş
// beklenen ile boş geleni EŞİT sayar. Açık koruma olmasaydı API_TOKEN'sız
// kurulan bir sunucu herkese açık olurdu.
func TestBearerBosBelirtecKapali(t *testing.T) {
	ts := kapiSunucusu(t, "")
	if got := iste(t, ts, "/api/bots", "").StatusCode; got != http.StatusUnauthorized {
		t.Errorf("boş API_TOKEN + başlıksız istek = %d, beklenen 401", got)
	}
	if got := iste(t, ts, "/api/bots", "Bearer ").StatusCode; got != http.StatusUnauthorized {
		t.Errorf("boş API_TOKEN + boş bearer = %d, beklenen 401", got)
	}
	// /healthz kapı dışıdır — konteyner sağlık yoklaması her hâlükârda çalışır.
	if got := iste(t, ts, "/healthz", "").StatusCode; got != http.StatusOK {
		t.Errorf("healthz = %d, beklenen 200", got)
	}
}

// TestWWWAuthenticate — 401'de istemciye şema bildirilir (RFC 7235).
func TestWWWAuthenticate(t *testing.T) {
	ts := kapiSunucusu(t, testBelirtec)
	if got := iste(t, ts, "/api/bots", "").Header.Get("WWW-Authenticate"); got == "" {
		t.Error("401 yanıtında WWW-Authenticate başlığı olmalı")
	}
}

// TestIcYuzeyMCPTasindi — MCP yüzeyi AYRI dinleyicidedir ve belirteç istemez:
// motor aynı makinededir (K15). Aynı mux'ta kalsaydı ya dışa açılırdı ya da
// belirteci motorun MCP yapılandırmasına yazmak gerekirdi.
func TestIcYuzeyMCPTasindi(t *testing.T) {
	gw := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	cfg := config.Config{HTTPAddr: ":0", InternalHTTPAddr: "127.0.0.1:0", APIToken: testBelirtec}
	srv := httpapi.NewInternal(cfg, httpapi.Deps{Gateway: gw, MacGateway: gw})
	if srv == nil {
		t.Fatal("gateway verildiğinde iç sunucu kurulmalı")
	}
	if srv.Addr != cfg.InternalHTTPAddr {
		t.Errorf("iç sunucu adresi = %q, beklenen %q", srv.Addr, cfg.InternalHTTPAddr)
	}
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)
	for _, yol := range []string{"/mcp", "/mcp/mac"} {
		if got := iste(t, ts, yol, "").StatusCode; got != http.StatusTeapot {
			t.Errorf("%s belirteçsiz erişilebilir olmalı, geldi: %d", yol, got)
		}
	}

	// Gateway yoksa dinleyici hiç kurulmaz (iskelet modu).
	if httpapi.NewInternal(cfg, httpapi.Deps{}) != nil {
		t.Error("gateway yokken iç sunucu kurulmamalı")
	}
}
