package coretools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/esmworks/OpenAssist/internal/gateway"
	"github.com/esmworks/OpenAssist/internal/policy"
)

// fetch_url — botun internetten okumasının BİRİNCİ SINIF yolu.
//
// Neden var: bu araç olmadan bot webe ancak `bash` + `curl` ile uzanabiliyordu
// ve orada iki duvara birden çarpıyordu. (1) `bash` en yüksek risk sınıfıdır —
// her komut ayrı onaya düşer, kural yazılamaz (K17, doğru karar); bir maç
// skoruna bakmak için kullanıcının üç kez düğmeye basması gerekiyordu.
// (2) Kapta `curl` ve `python3` yok, bot bunu ancak DENEYEREK öğrenebiliyordu
// ve her deneme yeni bir onay turu demekti — sahada ölçüldü: on dakika, sonuç
// yok. Okumak için keyfi komut çalıştırmak yanlış katmandı.
//
// Sınıf: read + DataSource + Sandboxed.
//   - read/Sandboxed: iş sunucunun kendi kabında biter, kullanıcının
//     makinesine uzanmaz → onay kapısında beklemez.
//   - DataSource: dış içerik okundu, koşu TAINTED olur → bundan sonraki her
//     yan etki (mesaj gönder, dosya yaz, komut çalıştır) insana sorulur
//     (§5.1). Serbestlik okumada, kapı eylemde.
var clsFetch = policy.Classification{
	Class: policy.ClassRead, DataSource: true, Ruleable: true, Sandboxed: true,
}

const (
	fetchZamanAsimi = 20 * time.Second
	fetchVarsayilan = 100 << 10 // 100 KiB
	fetchEnBuyuk    = 1 << 20   // 1 MiB
	// fetchDenemeSuresi — TEK bir IP'ye bağlanma denemesinin üst sınırı.
	// Toplam bütçe 20 sn; bir adres ailesi ölüyse tek deneme onu yemesin.
	fetchDenemeSuresi = 5 * time.Second
	fetchEnCokYonlen  = 5
)

func registerWebTools(gw *gateway.Gateway) {
	gw.Register(gateway.Tool{
		Name: "fetch_url",
		Description: "Bir http(s) adresini GET ile okur ve gövdeyi metin olarak döner. " +
			"Güncel bilgi (skor, hava, döviz, haber) ve genel API çağrıları için bunu kullan — " +
			"okumak için bash/curl'e gerek yok. Yalnız genel internet adresleri; " +
			"özel ağ ve loopback adresleri reddedilir.",
		InputSchema: schema(map[string]any{
			"url": str("http:// veya https:// ile başlayan tam adres"),
			"max_bytes": map[string]any{
				"type": "integer",
				"description": fmt.Sprintf("Okunacak en çok bayt (varsayılan %d, üst sınır %d)",
					fetchVarsayilan, fetchEnBuyuk),
			},
		}, "url"),
		Class:   clsFetch,
		Handler: fetchURLHandler(nil, nil),
	})
}

type fetchArgs struct {
	URL      string `json:"url"`
	MaxBytes int    `json:"max_bytes"`
}

// fetchURLHandler — istemci ve adres kapısı enjekte edilebilir.
//
// Üretimde ikisi de nil geçilir; testler httptest sunucusuna (127.0.0.1)
// ulaşabilmek için kendi taşıyıcısını ve kapısını verir. Kapının KENDİSİ
// ayrıca doğrudan test edilir (adresGuvenliMi + guvenliIstemci), yani
// enjeksiyon güvenliği ölçüsüz bırakmıyor.
func fetchURLHandler(hc *http.Client, kapi func(string) error) func(context.Context, json.RawMessage) (string, error) {
	if hc == nil {
		hc = guvenliIstemci()
	}
	if kapi == nil {
		kapi = adresGuvenliMi
	}
	return func(ctx context.Context, raw json.RawMessage) (string, error) {
		var a fetchArgs
		if err := decode(raw, &a); err != nil {
			return "", err
		}
		if err := kapi(a.URL); err != nil {
			return "", err
		}
		sinir := a.MaxBytes
		if sinir <= 0 {
			sinir = fetchVarsayilan
		}
		// Tavana vurduğunu SÖYLEMEK gerek: sessizce kırpıp "daha fazlası için
		// max_bytes ver" demek, modele zaten yaptığı şeyi öğütlemek olur ve
		// aynı isteği tekrar tekrar denemesine yol açar.
		tavanaVurdu := sinir > fetchEnBuyuk
		if tavanaVurdu {
			sinir = fetchEnBuyuk
		}

		ctx, cancel := context.WithTimeout(ctx, fetchZamanAsimi)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
		if err != nil {
			return "", fmt.Errorf("adres okunamadı: %w", err)
		}
		// Kimliğimizi söylüyoruz: engellenmek, sessizce bot gibi davranmaktan iyi.
		req.Header.Set("User-Agent", "OpenAssist/1.0 (+https://github.com/esmworks/OpenAssist)")
		req.Header.Set("Accept", "text/*, application/json;q=0.9, */*;q=0.1")

		res, err := hc.Do(req)
		if err != nil {
			return "", fmt.Errorf("adrese ulaşılamadı: %w", err)
		}
		defer func() { _ = res.Body.Close() }()

		// İKİLİ içerik modele verilmez. Accept başlığımız `*/*;q=0.1` ile
		// bitiyor ve sunucuların çoğu Accept'i zaten yok sayıyor; bir PDF ya
		// da görsel adresi, gövdeyi olduğu gibi dönseydik modelin bağlamına
		// 100 KiB ikili gürültü boşaltırdı. MCP katmanı bunu hataya da
		// çevirmezdi: json.Marshal geçersiz UTF-8'i sessizce U+FFFD yapar,
		// yani model çöpü "metin" sanıp üstüne yorum üretirdi.
		ct := res.Header.Get("Content-Type")
		if !metinMi(ct) {
			boyut := "boyut bilinmiyor"
			if res.ContentLength >= 0 {
				boyut = fmt.Sprintf("%d bayt", res.ContentLength)
			}
			return fmt.Sprintf("HTTP %d · %s\n\n[metin değil (%s) — bu araç ikili içeriği okuyamaz]",
				res.StatusCode, ct, boyut), nil
		}

		// Sınır +1 okunur ki "tam sınırda bitti" ile "kesildi" ayrılabilsin.
		govde, err := io.ReadAll(io.LimitReader(res.Body, int64(sinir)+1))
		if err != nil {
			return "", fmt.Errorf("gövde okunamadı: %w", err)
		}
		kesildi := len(govde) > sinir
		if kesildi {
			govde = govde[:sinir]
		}

		var b strings.Builder
		fmt.Fprintf(&b, "HTTP %d · %s\n", res.StatusCode, ct)
		b.Write(govde)
		switch {
		case kesildi && tavanaVurdu:
			fmt.Fprintf(&b, "\n\n[... %d bayttan sonra kesildi; bu araçtaki üst sınır bu, "+
				"daha fazlası istenemez — adresi daralt ya da parça parça oku]", sinir)
		case kesildi:
			fmt.Fprintf(&b, "\n\n[... %d bayttan sonra kesildi; daha fazlası için max_bytes ver "+
				"(üst sınır %d)]", sinir, fetchEnBuyuk)
		}
		return b.String(), nil
	}
}

// metinMi — gövde modele metin olarak verilebilir mi.
//
// Sunucu bir şey söylemediyse metin varsayılır: başlıksız uçlar yaygın ve
// çoğu JSON döner; sessiz kalmayı ikili saymak aracı kullanılmaz yapardı.
func metinMi(ct string) bool {
	tur, _, err := mime.ParseMediaType(ct)
	if err != nil || tur == "" {
		return true
	}
	if strings.HasPrefix(tur, "text/") ||
		strings.HasSuffix(tur, "+json") || strings.HasSuffix(tur, "+xml") {
		return true
	}
	switch tur {
	case "application/json", "application/xml", "application/javascript",
		"application/x-ndjson", "application/graphql", "application/x-www-form-urlencoded":
		return true
	}
	return false
}

// ── SSRF koruması ──────────────────────────────────────────────────────
//
// Bu araç onay kapısında BEKLEMEDİĞİ için koruma koda gömülmek zorunda.
// Kapın içinden bakınca 127.0.0.1:8082 bizim MCP yüzeyimizdir ve BELİRTEÇ
// İSTEMEZ (bilinçli: makine-içi sayılıyor); 169.254.169.254 ise bulut
// sağlayıcılarının kimlik uçları. Sınırsız bir GET aracı, kendi iç
// yüzeyimizi botun eline verirdi.

var errOzelAg = errors.New("yalnız genel internet adresleri okunabilir " +
	"(loopback, özel ağ ve bulut kimlik adresleri reddedilir)")

// adresGuvenliMi — ŞEMA ve apaçık yerel adlar için ucuz ön kapı.
//
// Burada ad ÇÖZÜLMEZ; bu bilinçli. (1) Çözüm zaten asıl kapının işi
// (denetliHedefler, bağlantı anında) — burada da çözmek her isteği en az iki,
// üç yönlendirmeli bir isteği sekiz DNS sorgusu yapardı. (2) Buradaki çözüm
// BAĞLAMSIZDI: cevap vermeyen bir ad sunucusu, aracın 20 saniyelik bütçesini
// aşıp koşuyu iptal edilemez biçimde bekletirdi (net.LookupIP ctx almaz).
// Yani bu kapı hatayı ERKEN ve anlaşılır vermek için var, güvenliğin dayanağı
// değil — dayanak denetliHedefler.
func adresGuvenliMi(ham string) error {
	if strings.TrimSpace(ham) == "" {
		return errors.New("url boş")
	}
	u, err := url.Parse(ham)
	if err != nil {
		return fmt.Errorf("url ayrıştırılamadı: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("yalnız http/https okunur, geldi: %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("url'de sunucu adı yok")
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if !ipGenelMi(ip) {
			return errOzelAg
		}
		return nil
	}
	if yerelAdMi(host) {
		return errOzelAg
	}
	return nil
}

// yerelAdMi — çözmeye gerek kalmadan yerel olduğu belli olan adlar.
func yerelAdMi(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "localhost" {
		return true
	}
	for _, son := range []string{".localhost", ".local", ".internal", ".home.arpa"} {
		if strings.HasSuffix(h, son) {
			return true
		}
	}
	return false
}

// ipGenelMi — adres genel internete mi ait.
//
// IPv6 içine GÖMÜLÜ IPv4 biçimleri ayrıca açılır: Go'nun To4'ü yalnız
// ::ffff:a.b.c.d biçimini tanır, ötekiler onun için "genel tekil yayın"dır.
// Ölçüldü — düzeltmeden önce ::127.0.0.1 (IPv4-uyumlu), 64:ff9b::7f00:1
// (NAT64) ve 2002:7f00:1::1 (6to4) bu kapıdan GEÇİYORDU; hepsi 127.0.0.1'i,
// yani belirteçsiz iç MCP yüzeyimizi gösteriyor.
//
// Gömülü biçimler standart testlerden SONRA açılır: ::1 ile :: kendileri
// zaten loopback/belirsiz sayılır, önce açsaydık onları 0.0.0.1 ve 0.0.0.0
// diye yeniden yorumlardık.
func ipGenelMi(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	if v4 := gomuluIPv4(ip); v4 != nil && !ip.Equal(v4) {
		return ipGenelMi(v4)
	}
	if v4 := ip.To4(); v4 != nil {
		switch {
		case v4[0] == 0: // 0.0.0.0/8 "bu ağ"
			return false
		case v4[0] == 100 && v4[1]&0xc0 == 64: // 100.64.0.0/10 CGNAT
			return false
		case v4[0] == 192 && v4[1] == 0 && v4[2] == 0: // 192.0.0.0/24 IETF protokol tahsisi
			return false
		case v4[0] == 198 && v4[1]&0xfe == 18: // 198.18.0.0/15 kıyas ağı
			return false
		case v4[0]&0xf0 == 240: // 240.0.0.0/4 ayrılmış (255.255.255.255 dahil)
			return false
		}
	}
	return true
}

// nat64Onek — 64:ff9b::/96, RFC 6052'nin iyi bilinen NAT64 öneki.
var nat64Onek = []byte{0x00, 0x64, 0xff, 0x9b, 0, 0, 0, 0, 0, 0, 0, 0}

// gomuluIPv4 — IPv6 adresinin içine gömülü IPv4'ü çıkarır (yoksa nil).
func gomuluIPv4(ip net.IP) net.IP {
	if v4 := ip.To4(); v4 != nil {
		return v4 // dört baytlık ya da ::ffff:a.b.c.d
	}
	if len(ip) != net.IPv6len {
		return nil
	}
	switch {
	case bytes.Equal(ip[:12], nat64Onek):
		return net.IPv4(ip[12], ip[13], ip[14], ip[15]).To4()
	case ip[0] == 0x20 && ip[1] == 0x02: // 2002::/16 — 6to4, IPv4 2-5. baytlarda
		return net.IPv4(ip[2], ip[3], ip[4], ip[5]).To4()
	case bytes.Equal(ip[:12], make([]byte, 12)): // ::a.b.c.d — IPv4-uyumlu (RFC 4291'de kaldırıldı)
		return net.IPv4(ip[12], ip[13], ip[14], ip[15]).To4()
	}
	return nil
}

// cozucu — ana bilgisayar adını adreslere çeviren birim (testte taklit edilir).
type cozucu func(ctx context.Context, host string) ([]net.IP, error)

func varsayilanCozucu(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	return net.DefaultResolver.LookupIP(ctx, "ip", host)
}

// denetliHedefler — bağlanılacak adresleri DENETLENMİŞ IP'lerle döner.
//
// Neden ad değil de IP ile bağlanıyoruz: Go, çözümü çeviriciye bırakır ve
// DialContext'e "alan.adı:443" gelir (ölçüldü). Adı burada kendimiz çözüp
// bağlantıyı doğruladığımız IP'ye kurunca "çözümle" ile "bağlan" arasındaki
// pencere kapanır — DNS rebinding'in tutunacağı yer kalmaz. TLS bundan
// etkilenmez: http.Transport, sunucu adını çevrilen adresten değil istekteki
// alan adından alır.
//
// Çözümlenen adreslerden BİRİ bile özel ağdaysa tümü reddedilir; "biri özel
// biri genel" dönen bir kayıt, kapıyı aşındırmanın en kolay yoludur.
func denetliHedefler(ctx context.Context, adres string, coz cozucu) ([]string, error) {
	host, port, err := net.SplitHostPort(adres)
	if err != nil {
		return nil, err
	}
	ipler, err := coz(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("sunucu adı çözülemedi: %w", err)
	}
	if len(ipler) == 0 {
		return nil, fmt.Errorf("sunucu adı çözülemedi: %s", host)
	}
	out := make([]string, 0, len(ipler))
	for _, ip := range aileyeGoreDiz(ipler) {
		if !ipGenelMi(ip) {
			return nil, errOzelAg
		}
		out = append(out, net.JoinHostPort(ip.String(), port))
	}
	return out, nil
}

// aileyeGoreDiz — adres ailelerini dönüşümlü dizer (IPv4, IPv6, IPv4, …).
//
// Tek bir IP'ye bağlandığımız için Go'nun kendi çift-yığın yarışını (Happy
// Eyeballs) devre dışı bırakmış oluyoruz; sıralamayı o yüzden kendimiz
// dengeliyoruz. Ölçüldü: www.thesportsdb.com 3×A + 3×AAAA dönüyor ve sıra
// garanti değil — IPv6'sı olmayan ama paketleri sessizce düşüren bir ağda
// arka arkaya ölü AAAA denemeleri 20 saniyelik bütçeyi bitirirdi. IPv4 önce,
// çünkü kabın kesin olan yolu o.
func aileyeGoreDiz(ipler []net.IP) []net.IP {
	var v4, v6 []net.IP
	for _, ip := range ipler {
		if ip.To4() != nil {
			v4 = append(v4, ip)
		} else {
			v6 = append(v6, ip)
		}
	}
	out := make([]net.IP, 0, len(ipler))
	for i := 0; i < len(v4) || i < len(v6); i++ {
		if i < len(v4) {
			out = append(out, v4[i])
		}
		if i < len(v6) {
			out = append(out, v6[i])
		}
	}
	return out
}

// guvenliIstemci — bağlantı anında IP denetimi yapan istemci.
//
// Kontrol DialContext'te: ad çözümü ile bağlantı arasında değişen bir DNS
// kaydı (rebinding) ancak burada yakalanır. Yönlendirmeler de aynı kapıdan
// geçer — 302 ile 127.0.0.1'e sapmak bu yüzden işe yaramaz.
func guvenliIstemci() *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Client{
		Timeout: fetchZamanAsimi,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, ag, adres string) (net.Conn, error) {
				hedefler, err := denetliHedefler(ctx, adres, varsayilanCozucu)
				if err != nil {
					return nil, err
				}
				// Sırayla dene: ilk IP ulaşılamaz olabilir (IPv6-only ağ vb.).
				// Her denemenin KENDİ süresi var — ölü bir adres ailesi tüm
				// bütçeyi yemesin (dialer.Timeout tek başına 10 sn'ye kadar
				// bloklardı ve iki ölü adres turu bitirirdi).
				var sonHata error
				for _, h := range hedefler {
					dctx, iptal := context.WithTimeout(ctx, fetchDenemeSuresi)
					conn, err := dialer.DialContext(dctx, ag, h)
					iptal() // bağlantı kurulduysa iptal onu etkilemez (net.Dialer sözleşmesi)
					if err == nil {
						return conn, nil
					}
					sonHata = err
				}
				return nil, sonHata
			},
			// Özel bir DialContext verildiğinde net/http HTTP/2'yi KENDİLİĞİNDEN
			// açmaz (bkz. Transport.ForceAttemptHTTP2 belgesi) — açıkça
			// istemezsek her istek sessizce HTTP/1.1'e düşerdi.
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 15 * time.Second,
			MaxIdleConns:          4,
			// Elle kurulan Transport'ta varsayılan 0'dır: boşta bağlantı hiç
			// kapanmaz, NAT/ara katman onu sessizce düşürünce sessiz dönem
			// sonrası ilk istek EOF alır. DefaultTransport'un değeri budur.
			IdleConnTimeout: 90 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= fetchEnCokYonlen {
				return fmt.Errorf("çok fazla yönlendirme (%d)", len(via))
			}
			return adresGuvenliMi(req.URL.String())
		},
	}
}
