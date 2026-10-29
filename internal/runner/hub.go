// Package runner — Mac runner köprüsü (PRD K7/K18). Transport ayrımı net:
// Claude Code ↔ Go = MCP (Streamable HTTP, localhost); Go ↔ Swift runner =
// WebSocket — bu runner TRANSPORT'udur, MCP değil. Go, motora "mac"
// MCP sunucusu olarak görünür ve çağrıları buradan runner'a iletir.
//
// K2 kanıtı: Mac bağlı değilse araç listesi BOŞTUR — motor araçları hiç
// görmez, sunucu botları çalışmaya devam eder. Yerel iş kuyruğa alınmaz,
// beklemez, telafi edilmez (K7 kapsam kararı).
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/esmworks/OpenAssist/internal/gateway"
	"github.com/esmworks/OpenAssist/internal/policy"
)

// ErrNotConnected — Mac bağlı değil; araç hatası olarak modele gider
// (devir kartı: bot kullanıcıya "Mac'ini aç" der).
var ErrNotConnected = errors.New("mac runner bağlı değil — yerel dosya işleri için Mac'te OpenAssist uygulamasının açık olması gerekir")

// callTimeout — tek araç çağrısının runner'da tamamlanma sınırı (yerel
// onay diyaloğu bekleyebilir: cömert tutulur).
const callTimeout = 120 * time.Second

// pingAralik / pingZamanAsimi — canlı tutma.
//
// NEDEN: bağlantı boşta dururken (kullanıcı yazmıyorken) hiç çerçeve akmaz;
// arada duran ters vekil — bizde Cloudflare — boşta kalan WebSocket'i ~100
// saniyede kapatıyordu. Mac her iki dakikada bir "bağlandı/ayrıldı" yapıyor,
// arada kalan boşlukta GatewayTools BOŞ dönüyor, yani bot Mac araçlarını hiç
// görmüyordu. Ping ters vekilin sayacını sıfırlar.
//
// Ping aynı zamanda ölü bağlantıyı tespit eder: pong gelmezse okuma döngüsü
// kapatılır, Swift tarafı yeniden bağlanır. TCP keepalive'a güvenilemez —
// vekil TCP'yi ayakta tutup WebSocket'i kapatabilir.
const (
	pingAralik     = 30 * time.Second
	pingZamanAsimi = 10 * time.Second
)

// ── tel protokolü (Go ↔ Swift, JSON metin çerçeveleri) ─────────────────

// helloFrame — runner'ın ilk mesajı: kimlik + araç ilanı.
type helloFrame struct {
	Type    string     `json:"type"` // "hello"
	Name    string     `json:"name"`
	Version string     `json:"version"`
	Tools   []ToolDecl `json:"tools"`
}

// ToolDecl — runner'ın ilan ettiği araç. Class policy sınıfına çevrilir;
// bilinmeyen sınıf güvenli tarafa external sayılır.
type ToolDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	Class       string          `json:"class"` // read | write | external | execute
}

type callFrame struct {
	Type string          `json:"type"` // "call"
	ID   string          `json:"id"`
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args"`
}

type resultFrame struct {
	Type  string `json:"type"` // "result"
	ID    string `json:"id"`
	OK    bool   `json:"ok"`
	Text  string `json:"text,omitempty"`
	Error string `json:"error,omitempty"`
}

// ── hub ────────────────────────────────────────────────────────────────

// session — bağlı tek runner (v1: tek Mac).
type session struct {
	conn  *websocket.Conn
	name  string
	tools []ToolDecl
	// writeMu — websocket.Conn tek eşzamanlı yazar ister.
	writeMu sync.Mutex
	// pending — BU oturuma gönderilmiş, cevabı beklenen çağrılar (hub.mu
	// korur). Oturuma bağlı olması şart: eski bağlantı düşerken yenisinin
	// bekleyen çağrılarını kapatmamalı (yeniden bağlanma yarışı).
	pending map[string]chan resultFrame
}

// AuditFunc — yerel işlem günlüğü kancası (§8 local_audit; nil olabilir).
type AuditFunc func(ctx context.Context, device, tool string, args json.RawMessage, ok bool, result string)

// Hub — runner bağlantısının sahibi. Aynı anda tek oturum: yeni bağlantı
// eskisini düşürür (Mac uyandı/yeniden bağlandı senaryosu).
type Hub struct {
	mu   sync.Mutex
	sess *session
	// Audit — her araç çağrısının sonucu buraya düşer (kablolama sırasında
	// atanır, sunum başladıktan sonra değişmez).
	Audit AuditFunc
	// ping — canlı tutma zamanlaması; sıfır alanlar varsayılana düşer.
	// Yalnız testler (saniyelerce beklememek için) doldurur.
	ping pingAyari
}

// pingAyari — canlı tutma zamanlaması: periyot ve pong bekleme süresi.
type pingAyari struct{ her, bekle time.Duration }

func (p pingAyari) degerler() (her, bekle time.Duration) {
	her, bekle = p.her, p.bekle
	if her <= 0 {
		her = pingAralik
	}
	if bekle <= 0 {
		bekle = pingZamanAsimi
	}
	return her, bekle
}

func NewHub() *Hub {
	return &Hub{}
}

// Connected — runner bağlı mı (UI durum göstergesi).
func (h *Hub) Connected() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sess != nil
}

// GatewayTools — dinamik MCP araç kaynağı: bağlıyken ilan edilen araçlar,
// değilken BOŞ liste (K2: sunucu listeden düşer, yetenek ilanı bedavaya).
func (h *Hub) GatewayTools() []gateway.Tool {
	h.mu.Lock()
	decls := []ToolDecl(nil)
	if h.sess != nil {
		decls = h.sess.tools
	}
	h.mu.Unlock()

	out := make([]gateway.Tool, 0, len(decls))
	for _, d := range decls {
		d := d
		out = append(out, gateway.Tool{
			Name:        d.Name,
			Description: d.Description,
			InputSchema: d.InputSchema,
			Class:       classFor(d.Class),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				return h.Call(ctx, d.Name, args)
			},
		})
	}
	return out
}

// classFor — runner ilanındaki sınıf adı → policy sınıfı. Bilinmeyen ad
// güvenli tarafa external (sorulmadan çalışmaz); execute asla kural alamaz.
func classFor(name string) policy.Classification {
	switch name {
	case "read":
		return policy.Classification{Class: policy.ClassRead, Ruleable: true}
	case "write":
		return policy.Classification{Class: policy.ClassWrite, Ruleable: true}
	case "execute":
		return policy.Classification{Class: policy.ClassExecute, Ruleable: false}
	default:
		return policy.Classification{Class: policy.ClassExternal, Ruleable: true}
	}
}

// StaticClasses — v1 runner araç setinin sınıfları (orkestratör ToolClasses
// haritası açılışta kurulur; araç SETİ sabittir, erişilebilirlik dinamiktir).
// Yerel okuma taint YAPMAZ (§5.1 dış içerik içindir).
func StaticClasses() map[string]policy.Classification {
	return map[string]policy.Classification{
		"mcp__mac__read_file":  {Class: policy.ClassRead, Ruleable: true},
		"mcp__mac__list_dir":   {Class: policy.ClassRead, Ruleable: true},
		"mcp__mac__write_file": {Class: policy.ClassWrite, Ruleable: true},
		// Çöpe taşıma geri alınabilir (kalıcı silme YOK, K güvenlik) —
		// yine de yazma sınıfı: onay kapısından geçer, kural yazılabilir.
		"mcp__mac__trash_item": {Class: policy.ClassWrite, Ruleable: true},
	}
}

// Call — aracı bağlı runner'da çalıştırır ve sonucu bekler.
func (h *Hub) Call(ctx context.Context, tool string, args json.RawMessage) (text string, err error) {
	h.mu.Lock()
	sess := h.sess
	h.mu.Unlock()
	if sess == nil {
		return "", ErrNotConnected
	}
	if h.Audit != nil {
		// Denetim kaydı çağrının kendi iptal/zaman aşımından etkilenmemeli:
		// zaman aşımı tam da kaydedilmesi gereken bir sonuçtur. (Defer'ler
		// LIFO koşar — aşağıdaki cancel() bu defer'den ÖNCE çalışır.)
		actx := context.WithoutCancel(ctx)
		defer func() {
			result := text
			if err != nil {
				result = err.Error()
			}
			h.Audit(actx, sess.name, tool, args, err == nil, result)
		}()
	}

	id := uuid.NewString()
	ch := make(chan resultFrame, 1)
	h.mu.Lock()
	sess.pending[id] = ch
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(sess.pending, id)
		h.mu.Unlock()
	}()

	frame, err := json.Marshal(callFrame{Type: "call", ID: id, Tool: tool, Args: args})
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	sess.writeMu.Lock()
	err = sess.conn.Write(ctx, websocket.MessageText, frame)
	sess.writeMu.Unlock()
	if err != nil {
		return "", fmt.Errorf("runner'a yazılamadı: %w", err)
	}

	select {
	case <-ctx.Done():
		return "", fmt.Errorf("runner cevabı zaman aşımı (%s): %w", tool, ctx.Err())
	case res, ok := <-ch:
		if !ok {
			return "", ErrNotConnected // bağlantı sonuç gelmeden koptu
		}
		if !res.OK {
			return "", errors.New(res.Error)
		}
		return res.Text, nil
	}
}

// register — yeni oturumu bağlar; varsa eskisini düşürür.
func (h *Hub) register(s *session) {
	h.mu.Lock()
	old := h.sess
	h.sess = s
	h.mu.Unlock()
	if old != nil {
		_ = old.conn.Close(websocket.StatusPolicyViolation, "yeni bağlantı geldi")
	}
}

// unregister — oturum düştü: YALNIZ o oturumun bekleyen çağrıları hatayla
// kapanır (yeni bağlantının çağrılarına dokunulmaz).
func (h *Hub) unregister(s *session) {
	h.mu.Lock()
	if h.sess == s {
		h.sess = nil
	}
	for id, ch := range s.pending {
		close(ch)
		delete(s.pending, id)
	}
	h.mu.Unlock()
}

// WSHandler — /api/runner/ws (özel yüzey). Runner outbound bağlanır (K7:
// Mac'te port açılmaz).
func (h *Hub) WSHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		h.serve(r.Context(), conn)
	})
}

// canliTut — bağlantı kapanana kadar periyodik ping. Cevapsız kalırsa
// bağlantıyı ölü sayıp okuma döngüsünü düşürür (Swift tarafı yeniden bağlanır).
func (h *Hub) canliTut(ctx context.Context, conn *websocket.Conn, name string, iptal context.CancelFunc) {
	her, bekle := h.ping.degerler()
	t := time.NewTicker(her)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			pctx, pcancel := context.WithTimeout(ctx, bekle)
			err := conn.Ping(pctx)
			pcancel()
			if err != nil {
				// ctx zaten bittiyse bu normal kapanış — gürültü yapma.
				if ctx.Err() == nil {
					slog.Info("runner ping cevapsız, bağlantı düşürülüyor", "name", name, "err", err)
					iptal()
				}
				return
			}
		}
	}
}

func (h *Hub) serve(ctx context.Context, conn *websocket.Conn) {
	// İlk çerçeve hello olmalı.
	var hello helloFrame
	readCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	_, data, err := conn.Read(readCtx)
	cancel()
	if err != nil || json.Unmarshal(data, &hello) != nil || hello.Type != "hello" {
		_ = conn.Close(websocket.StatusProtocolError, "ilk çerçeve hello olmalı")
		return
	}
	sess := &session{conn: conn, name: hello.Name, tools: hello.Tools, pending: map[string]chan resultFrame{}}
	h.register(sess)
	slog.Info("runner bağlandı", "name", hello.Name, "araç", len(hello.Tools))
	defer func() {
		h.unregister(sess)
		slog.Info("runner ayrıldı", "name", hello.Name)
		_ = conn.CloseNow()
	}()

	// Canlı tutma, okuma döngüsüyle BİRLİKTE koşmalı: pong'u okuyan aşağıdaki
	// Read'dir (kütüphane şartı). İptal edilebilir bir ctx türetiyoruz ki
	// cevapsız ping okumayı da düşürsün.
	ctx, iptal := context.WithCancel(ctx)
	defer iptal()
	go h.canliTut(ctx, conn, hello.Name, iptal)

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var res resultFrame
		if json.Unmarshal(data, &res) != nil || res.Type != "result" {
			continue // bilinmeyen çerçeve: yok say (yabancı kontrat toleransı)
		}
		h.mu.Lock()
		ch, ok := sess.pending[res.ID]
		if ok {
			delete(sess.pending, res.ID)
		}
		h.mu.Unlock()
		if ok {
			ch <- res
		}
	}
}
