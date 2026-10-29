package httpapi

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
)

// acikYollar — belirteç istemeyen uçlar.
//
// Yalnız /healthz: konteynerin kendi sağlık yoklaması (`core healthcheck`)
// buraya vurur ve Docker healthcheck'i buna bağlıdır; token isteseydik
// yanlış yapılandırılmış bir belirteç kabı "unhealthy" gösterip deploy'u
// düşürürdü. Sızan tek bilgi sürüm dizesi ve db "ok|error" — bilinçli bedel.
var acikYollar = map[string]bool{"/healthz": true}

// bearer — özel API'nin kapısı. VARSAYILAN RET: mux'un tamamını sarar, tek
// tek uçları değil. Uç ekleyen biri sarmayı unutamaz — yeni yol otomatik
// korumalıdır, açılması için acikYollar'a bilinçli yazılması gerekir.
func bearer(token string, next http.Handler) http.Handler {
	beklenen := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if acikYollar[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}
		// Boş belirteç = kapalı kapı. Bu dal olmasaydı sabit süreli
		// karşılaştırma boş beklenen ile boş geleni EŞİT sayardı: başlıksız
		// her istek geçerdi. config.Validate() zaten boş token'la açılışa
		// izin vermiyor ama koruma buraya da yazılır — New() doğrudan
		// çağrılabiliyor (testler öyle yapıyor) ve o yol sessizce
		// korumasız bir sunucu üretmemeli.
		if len(beklenen) == 0 {
			slog.Error("api: API_TOKEN boş — tüm istekler reddediliyor", "path", r.URL.Path)
			writeErr(w, http.StatusUnauthorized, "sunucuda API_TOKEN tanımlı değil")
			return
		}
		gelen := belirtecCikar(r)
		// Sabit süreli karşılaştırma: uzunluk kontrolü yapmadan doğrudan
		// subtle'a veriyoruz (farklı uzunlukta 0 döner), böylece cevap
		// süresi token'ın kaç karakterinin tuttuğunu ele vermez.
		if subtle.ConstantTimeCompare(gelen, beklenen) != 1 {
			// Belirteç loglanmaz; yalnız nereye vurulduğu.
			slog.Warn("api: yetkisiz istek", "path", r.URL.Path, "uzak", r.RemoteAddr)
			w.Header().Set("WWW-Authenticate", `Bearer realm="openassist"`)
			writeErr(w, http.StatusUnauthorized, "geçersiz ya da eksik API belirteci")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// belirtecCikar — "Authorization: Bearer <token>" başlığını okur.
//
// Yalnız başlık kabul edilir, sorgu parametresi DEĞİL: URL'ler erişim
// loglarına, proxy kayıtlarına ve Referer'a düşer. WebSocket dahil tüm
// istemcilerimiz (Swift URLSession) başlık gönderebiliyor.
func belirtecCikar(r *http.Request) []byte {
	h := r.Header.Get("Authorization")
	const on = "bearer "
	if len(h) <= len(on) || !strings.EqualFold(h[:len(on)], on) {
		return nil
	}
	return []byte(strings.TrimSpace(h[len(on):]))
}
