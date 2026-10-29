// Package publichttp — dışa açık ikinci HTTP yüzeyi (:8081, PRD §4):
// yalnız OAuth callback'leri ve (Faz 5'te) webhook'lar. Özel API burada
// YAŞAMAZ — iki yüzey ayrımı bilinçli (K güvenlik).
package publichttp

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/esmworks/OpenAssist/internal/config"
	"github.com/esmworks/OpenAssist/internal/connectors/google"
)

// stateTTL — OAuth state'in geçerlilik süresi; consent ekranında oyalanma payı.
const stateTTL = 10 * time.Minute

// states — tek kullanımlık CSRF state deposu (tek süreç, bellek yeter).
type states struct {
	mu sync.Mutex
	m  map[string]time.Time
}

func (s *states) issue() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, exp := range s.m { // süresi geçenleri fırsatçı temizle
		if now.After(exp) {
			delete(s.m, k)
		}
	}
	s.m[tok] = now.Add(stateTTL)
	return tok, nil
}

// consume — geçerliyse siler (tek kullanım); değilse false.
func (s *states) consume(tok string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.m[tok]
	if !ok {
		return false
	}
	delete(s.m, tok)
	return time.Now().Before(exp)
}

// Deps — public yüzeyin bağımlılıkları. Google nil ise OAuth uçları 503
// döner (yapılandırma eksik mesajıyla).
type Deps struct {
	Google *google.Service
}

func New(cfg config.Config, d Deps) *http.Server {
	mux := http.NewServeMux()
	st := &states{m: map[string]time.Time{}}

	mux.HandleFunc("GET /oauth/google/start", func(w http.ResponseWriter, r *http.Request) {
		if d.Google == nil {
			writeMsg(w, http.StatusServiceUnavailable, "Google yapılandırılmamış — GOOGLE_CLIENT_ID / GOOGLE_CLIENT_SECRET ve MASTER_KEY gerekli.")
			return
		}
		tok, err := st.issue()
		if err != nil {
			writeMsg(w, http.StatusInternalServerError, "state üretilemedi")
			return
		}
		http.Redirect(w, r, d.Google.AuthCodeURL(tok), http.StatusFound)
	})

	mux.HandleFunc("GET /oauth/google/callback", func(w http.ResponseWriter, r *http.Request) {
		if d.Google == nil {
			writeMsg(w, http.StatusServiceUnavailable, "Google yapılandırılmamış.")
			return
		}
		q := r.URL.Query()
		if e := q.Get("error"); e != "" { // kullanıcı consent'i reddetti
			writeMsg(w, http.StatusBadRequest, "Google bağlantısı reddedildi: "+e)
			return
		}
		if !st.consume(q.Get("state")) {
			writeMsg(w, http.StatusBadRequest, "state geçersiz ya da süresi dolmuş — /oauth/google/start ile yeniden başla.")
			return
		}
		code := q.Get("code")
		if code == "" {
			writeMsg(w, http.StatusBadRequest, "code eksik")
			return
		}
		conn, err := d.Google.Connect(r.Context(), code)
		if err != nil {
			slog.Error("google bağlama başarısız", "err", err)
			writeMsg(w, http.StatusBadGateway, "Bağlanamadı: "+err.Error())
			return
		}
		writeMsg(w, http.StatusOK, "Google bağlandı: "+conn.Account+" — bu sekmeyi kapatabilirsin.")
	})

	return &http.Server{
		Addr:              cfg.PublicHTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
}

// writeMsg — minimal, bağımlılıksız sonuç sayfası (tarayıcıda görülür).
func writeMsg(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_, _ = fmt.Fprintf(w, "<!doctype html><meta charset=utf-8><title>OpenAssist</title><body style=\"font-family:system-ui;max-width:40rem;margin:4rem auto\"><h2>OpenAssist</h2><p>%s</p></body>", html.EscapeString(msg))
}
