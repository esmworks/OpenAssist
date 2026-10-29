package coretools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/esmworks/OpenAssist/internal/gateway"
	"github.com/esmworks/OpenAssist/internal/policy"
)

// cagir — mutlu yol testleri httptest sunucusuna (127.0.0.1) bakar; SSRF
// kapısı orayı doğru biçimde reddettiği için kapı burada devre dışı.
// Kapının kendisi aşağıda ayrıca ölçülüyor.
func cagir(t *testing.T, hc *http.Client, args string) (string, error) {
	t.Helper()
	acik := func(string) error { return nil }
	return fetchURLHandler(hc, acik)(context.Background(), json.RawMessage(args))
}

func TestFetchGovdeVeDurumDoner(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"skor":"2-1"}`)
	}))
	defer srv.Close()

	got, err := cagir(t, srv.Client(), `{"url":"`+srv.URL+`"}`)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !strings.Contains(got, `{"skor":"2-1"}`) || !strings.Contains(got, "HTTP 200") {
		t.Fatalf("gövde/durum eksik: %q", got)
	}
}

// Sınır AŞILDIĞINDA kesildiği SÖYLENMELİ: sessizce kırpılan bir gövde,
// modelin yarım veriyi tam sanmasına yol açar.
func TestFetchSinirdaKesilirVeSoyler(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, strings.Repeat("x", 5000))
	}))
	defer srv.Close()

	got, err := cagir(t, srv.Client(), `{"url":"`+srv.URL+`","max_bytes":100}`)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	bas := strings.Index(got, "\n")
	son := strings.Index(got, "\n\n[...")
	if bas < 0 || son < 0 {
		t.Fatalf("çıktı biçimi beklenmedik: %q", got)
	}
	if govde := got[bas+1 : son]; len(govde) != 100 {
		t.Fatalf("sınır uygulanmadı: gövde %d bayt", len(govde))
	}
	if !strings.Contains(got, "kesildi") {
		t.Fatalf("kesildiği söylenmedi: %q", got)
	}
}

// İkili içerik modele VERİLMEZ. Accept başlığımız */*;q=0.1 ile bitiyor ve
// sunucuların çoğu Accept'i yok sayıyor; gövdeyi olduğu gibi dönseydik bir
// PDF adresi modelin bağlamına 100 KiB ikili gürültü boşaltırdı ve MCP
// katmanı bunu hataya bile çevirmezdi (json.Marshal geçersiz UTF-8'i sessizce
// U+FFFD yapar), yani model çöpü metin sanırdı.
func TestIkiliIcerikModeleVerilmez(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.7\x00\x01\x02\xff\xfe binary"))
	}))
	defer srv.Close()

	got, err := cagir(t, srv.Client(), `{"url":"`+srv.URL+`"}`)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if strings.Contains(got, "%PDF") || strings.Contains(got, "\xff") {
		t.Fatalf("ikili gövde modele verildi: %q", got)
	}
	if !strings.Contains(got, "metin değil") {
		t.Fatalf("neden boş döndüğü söylenmedi: %q", got)
	}
}

func TestMetinTurleriGecer(t *testing.T) {
	for _, ct := range []string{
		"application/json; charset=utf-8", "text/html", "text/plain",
		"application/xml", "application/problem+json", "image/svg+xml",
		"", // başlıksız uç: metin varsayılır
	} {
		if !metinMi(ct) {
			t.Errorf("%q metin sayılmadı — çoğu API böyle cevap veriyor", ct)
		}
	}
	for _, ct := range []string{"application/pdf", "image/png", "application/zip", "video/mp4"} {
		if metinMi(ct) {
			t.Errorf("%q metin sayıldı", ct)
		}
	}
}

// Tavana vurulduğunda "daha fazlası için max_bytes ver" DEMEMELİ: model
// zaten max_bytes vermişti, aynı öğüt onu aynı isteği tekrar denemeye iter.
func TestUstSinirdaTekrarOnerilmez(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, strings.Repeat("x", fetchEnBuyuk+2048))
	}))
	defer srv.Close()

	got, err := cagir(t, srv.Client(), `{"url":"`+srv.URL+`","max_bytes":5000000}`)
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	bas := strings.Index(got, "\n")
	son := strings.Index(got, "\n\n[...")
	if bas < 0 || son < 0 {
		t.Fatal("çıktı biçimi beklenmedik")
	}
	if govde := got[bas+1 : son]; len(govde) != fetchEnBuyuk {
		t.Fatalf("üst sınır uygulanmadı: gövde %d bayt", len(govde))
	}
	if strings.Contains(got, "max_bytes ver") {
		t.Fatalf("tavandayken yine max_bytes isteniyor — döngü daveti: %q", got[son:])
	}
	if !strings.Contains(got, "üst sınır") {
		t.Fatalf("tavana vurulduğu söylenmedi: %q", got[son:])
	}
}

// Adres aileleri DÖNÜŞÜMLÜ dizilmeli. Tek IP'ye bağlandığımız için Go'nun
// çift-yığın yarışı (Happy Eyeballs) devrede değil; çözümleyici önce AAAA
// dönerse (ölçüldü: thesportsdb 3×A + 3×AAAA, sıra garanti değil) IPv6'sı
// çalışmayan bir kapta arka arkaya ölü denemeler bütçeyi bitirirdi.
func TestAdresAileleriDonusumluDizilir(t *testing.T) {
	ip := net.ParseIP
	got := aileyeGoreDiz([]net.IP{
		ip("2606:4700::1"), ip("2606:4700::2"), ip("2606:4700::3"),
		ip("104.26.10.201"), ip("104.26.11.201"),
	})
	var sira []string
	for _, x := range got {
		sira = append(sira, x.String())
	}
	if len(got) != 5 {
		t.Fatalf("adres kaybedildi: %v", sira)
	}
	if got[0].To4() == nil {
		t.Fatalf("ilk deneme IPv6: %v — kabın kesin yolu IPv4", sira)
	}
	if got[1].To4() != nil {
		t.Fatalf("aileler dönüşümlü değil: %v", sira)
	}
}

// ── SSRF kapısı ────────────────────────────────────────────────────────
//
// Bu araç onay kapısında BEKLEMİYOR: koruma tamamen kodda. Kaptan bakınca
// 127.0.0.1:8082 bizim BELİRTEÇSİZ MCP yüzeyimiz, 169.254.169.254 ise bulut
// kimlik ucu — sınırsız bir GET aracı iç yüzeyi botun eline verirdi.

func TestOzelAdreslerReddedilir(t *testing.T) {
	for _, u := range []string{
		"http://127.0.0.1:8082/mcp",      // kendi MCP yüzeyimiz (belirteçsiz)
		"http://localhost:8080/api/bots", // kendi özel API'miz
		"http://LocalHost./",             // büyük harf + kök nokta
		"http://core.internal/",          // apaçık yerel ad
		"http://nas.local/",              // mDNS
		"http://169.254.169.254/latest/", // bulut kimlik ucu
		"http://10.0.0.5/",               // özel ağ
		"http://192.168.1.1/",            // özel ağ
		"http://172.16.0.1/",             // özel ağ
		"http://[::1]:8082/",             // IPv6 loopback
		"http://[::ffff:127.0.0.1]/",     // IPv4 eşlemeli IPv6
		"http://100.64.0.1/",             // CGNAT (IsPrivate DEĞİL)
		"http://198.18.0.1/",             // kıyas ağı (IsPrivate DEĞİL)
		"http://0.0.0.0/",                // belirsiz
		"file:///etc/passwd",             // şema
		"gopher://ornek.invalid/",        // şema
		"http://[fe80::1]/",              // link-local

		// IPv6'ya GÖMÜLÜ IPv4 biçimleri: hepsi 127.0.0.1'i gösteriyor ve
		// düzeltmeden önce üçü de bu kapıdan geçiyordu (ölçüldü) — Go'nun
		// To4'ü yalnız ::ffff: biçimini tanır.
		"http://[::127.0.0.1]/",      // IPv4-uyumlu
		"http://[64:ff9b::7f00:1]/",  // NAT64 (RFC 6052)
		"http://[2002:7f00:1::1]/",   // 6to4
		"http://[64:ff9b::a00:5]/",   // NAT64 ile 10.0.0.5
		"http://[2002:c0a8:101::1]/", // 6to4 ile 192.168.1.1

		// IsPrivate/IsGlobalUnicast'in kaçırdığı ayrılmış aralıklar.
		"http://240.0.0.1/",       // 240.0.0.0/4 ayrılmış
		"http://255.255.255.255/", // sınırsız yayın
		"http://192.0.0.170/",     // 192.0.0.0/24 IETF tahsisi
		"http://0.1.2.3/",         // 0.0.0.0/8 "bu ağ"
	} {
		if err := adresGuvenliMi(u); err == nil {
			t.Errorf("%s KABUL EDİLDİ — iç yüzey bota açılır", u)
		}
	}
}

func TestGenelAdresGecer(t *testing.T) {
	for _, u := range []string{
		"https://8.8.8.8/", "http://1.1.1.1/",
		"http://[2001:4860:4860::8888]/", // genel IPv6
		"https://www.thesportsdb.com/api/v1/json/3/eventslast.php?id=1",
	} {
		if err := adresGuvenliMi(u); err != nil {
			t.Errorf("%s reddedildi: %v", u, err)
		}
	}
}

// Ön kapı ad ÇÖZMEMELİ. Çözerdi ve çözüm bağlamsızdı (net.LookupIP ctx
// almaz): cevap vermeyen bir ad sunucusu aracın 20 sn'lik bütçesini aşıp
// koşuyu iptal edilemez biçimde bekletirdi. Ayrıca her istek en az iki,
// üç yönlendirmeli bir istek sekiz DNS sorgusu ederdi.
//
// .invalid asla çözülmez (RFC 2606) — eski kod burada "sunucu adı
// çözülemedi" derdi; yeni kod hiç sormadığı için sessizce geçmeli.
func TestOnKapiAdCozmez(t *testing.T) {
	bas := time.Now()
	if err := adresGuvenliMi("https://kesinlikle-boyle-bir-ad-yok.invalid/x"); err != nil {
		t.Fatalf("ön kapı ad çözmeye kalkıştı: %v", err)
	}
	if gecen := time.Since(bas); gecen > 100*time.Millisecond {
		t.Fatalf("ön kapı %s sürdü — ağ işi yapıyor", gecen)
	}
}

// Kapı yalnız ön kontrolde değil BAĞLANTI ANINDA da durmalı: iki çözüm
// arasında değişen bir DNS kaydı (rebinding) ön kontrolü aldatabilir.
func TestBaglantiAnindaOzelAdresKesilir(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "gizli")
	}))
	defer srv.Close()

	// guvenliIstemci gerçek koşudaki istemcidir; loopback'e bağlanmamalı.
	_, err := fetchURLHandler(guvenliIstemci(), nil)(context.Background(),
		json.RawMessage(`{"url":"`+srv.URL+`"}`))
	if err == nil {
		t.Fatal("loopback'e bağlanıldı — SSRF kapısı bağlantı anında tutmuyor")
	}
}

// Alan adı bağlanabilmeli. Bu testin sebebi bir SAHA HATASI: kapı, çevirici
// kancasına çözülmüş IP geldiğini varsayıyordu; oysa Go oraya "alan.adı:443"
// verir, net.ParseIP nil döner ve HER alan adı özel ağ sayılıp reddedilirdi —
// yani araç yalnız çıplak IP'lerle çalışıyordu. Canlı çağrıda ölçüldü.
func TestAlanAdiCozulupBaglanabilir(t *testing.T) {
	coz := func(_ context.Context, host string) ([]net.IP, error) {
		if host != "ornek.test" {
			t.Fatalf("beklenmeyen ad: %q", host)
		}
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	}
	got, err := denetliHedefler(context.Background(), "ornek.test:443", coz)
	if err != nil {
		t.Fatalf("alan adı reddedildi: %v", err)
	}
	if len(got) != 1 || got[0] != "93.184.216.34:443" {
		t.Fatalf("denetlenmiş hedef yanlış: %v", got)
	}
}

// Ad genel görünüp özel adrese çözülüyorsa kapı orada durmalı.
func TestAlanAdiOzelAdreseCozulurseReddedilir(t *testing.T) {
	for ad, ipler := range map[string][]string{
		"yalnız özel":  {"127.0.0.1"},
		"bulut kimlik": {"169.254.169.254"},
		// Karışık kayıt kapıyı aşındırmanın en kolay yolu: biri bile özelse tümü düşer.
		"karışık": {"93.184.216.34", "10.0.0.5"},
	} {
		coz := func(_ context.Context, _ string) ([]net.IP, error) {
			out := make([]net.IP, 0, len(ipler))
			for _, s := range ipler {
				out = append(out, net.ParseIP(s))
			}
			return out, nil
		}
		if _, err := denetliHedefler(context.Background(), "ornek.test:80", coz); err == nil {
			t.Errorf("%s: kabul edildi — iç yüzey bota açılır", ad)
		}
	}
}

// Yönlendirme kapıyı atlatmamalı: 302 ile 127.0.0.1'e sapmak klasik SSRF.
func TestYonlendirmeOzelAdreseSapamaz(t *testing.T) {
	var ulasildi atomic.Bool
	ic := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ulasildi.Store(true)
		_, _ = fmt.Fprint(w, "gizli iç yüzey")
	}))
	defer ic.Close()
	dis := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, ic.URL, http.StatusFound)
	}))
	defer dis.Close()

	hc := dis.Client()
	hc.CheckRedirect = guvenliIstemci().CheckRedirect
	got, err := cagir(t, hc, `{"url":"`+dis.URL+`"}`)

	// İddia DAR olmalı: "err != nil" yetmez — dış sunucu hiç ayağa kalkmasa
	// da, durum kodu değişse de test yeşil kalırdı ve asıl sav (302 ile
	// loopback'e sapmak kapıda durur) ölçüsüz kalırdı.
	if err == nil {
		t.Fatalf("yönlendirme geçti: %q", got)
	}
	if !errors.Is(err, errOzelAg) {
		t.Fatalf("başka bir sebeple düştü, kapı ölçülmedi: %v", err)
	}
	if ulasildi.Load() {
		t.Fatal("iç sunucuya İSTEK GİTTİ — kapı yalnız cevabı gizliyor")
	}
}

// Sınıf bildirimi kapının davranışını belirliyor: read+Sandboxed sorulmadan
// akar, DataSource koşuyu tainted yapar. Biri düşerse ürün ya kullanılmaz
// olur ya da güvenlik zinciri kopar.
func TestFetchSinifBildirimi(t *testing.T) {
	if !clsFetch.Sandboxed {
		t.Error("Sandboxed değil — okuma yine onay kapısında bekler")
	}
	if !clsFetch.DataSource {
		t.Error("DataSource değil — dış içerik okundu ama koşu tainted olmaz (§5.1)")
	}
	if clsFetch.Class != "read" {
		t.Errorf("sınıf %q — okuma olmalı", clsFetch.Class)
	}
}

// Bildirimin KAPIYA ULAŞTIĞI ölçülmeli, yalnız değişkende durduğu değil.
// Sezgisel sınıflandırıcı "fetch" önekine bakıp read+DataSource verir ama
// Sandboxed VERMEZ (bir MCP aracı normalde kabın dışına uzanır). Yani açık
// bildirim gateway → orkestratör yolunda düşerse araç sessizce onaya geri
// döner: maç skoruna bakmak yine düğmeye basmak olur.
func TestFetchOnayKapisindaBeklemez(t *testing.T) {
	gw := gateway.New()
	registerWebTools(gw)

	sinif, ok := gw.Classifications()["mcp__"+gateway.ServerName+"__fetch_url"]
	if !ok {
		t.Fatal("fetch_url sınıf bildirimi kapıya hiç ulaşmıyor")
	}
	if d := policy.Decide(sinif, false, 0, time.Now(), policy.DefaultSettings(), nil); d.Kind != policy.AutoAllow {
		t.Fatalf("okuma onaya düştü: %v (%s)", d.Kind, d.Reason)
	}
	// Zincirin öbür ucu: okuma serbest ama okuduktan sonra yan etki durur.
	yaz := policy.Classification{Class: policy.ClassExternal, Ruleable: true}
	if d := policy.Decide(yaz, true, 0, time.Now(), policy.DefaultSettings(), nil); d.Kind == policy.AutoAllow {
		t.Fatal("dış içerik okunmuş koşuda yan etki sorulmadan aktı (§5.1)")
	}
}
