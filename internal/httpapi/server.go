// Package httpapi — özel API yüzeyi (:8080): bot CRUD, sohbet, koşu durumu,
// onay kararı ve SSE olay akışı. Public yüzey (:8081, webhook/OAuth) Faz 4'te.
//
// Doğrulama kuralı (docs/design Faz 0): API bizim kontratımızdır — bilinmeyen
// alan ve bozuk değer 400 döner (DisallowUnknownFields + Validate).
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/esmworks/OpenAssist/internal/bus"
	"github.com/esmworks/OpenAssist/internal/config"
	"github.com/esmworks/OpenAssist/internal/engine"
	"github.com/esmworks/OpenAssist/internal/orchestrator"
	"github.com/esmworks/OpenAssist/internal/providers"
	"github.com/esmworks/OpenAssist/internal/store"
	"github.com/esmworks/OpenAssist/internal/version"
)

// Pinger, sağlık kontrolünde veritabanını yoklayabilen her şeydir (testte sahte).
type Pinger interface {
	Ping(ctx context.Context) error
}

type Health struct {
	Status  string `json:"status"` // ok | degraded
	Version string `json:"version"`
	DB      string `json:"db"` // ok | not_configured | error
}

// Deps — API'nin bağımlılıkları. Store nil ise yalnız /healthz kayıtlıdır
// (iskelet modu).
type Deps struct {
	DB    Pinger
	Store *store.Store
	Orch  *orchestrator.Orchestrator
	Bus   *bus.Bus
	// Gateway — MCP konektör yüzeyi (/mcp). AYRI dinleyicide sunulur
	// (NewInternal): motor aynı makineden bağlanır (K15), yani loopback
	// yeter. nil ise kayıt edilmez.
	Gateway http.Handler
	// MacGateway — mac-runner MCP yüzeyi (/mcp/mac); dinamik: Mac bağlı
	// değilken araç listesi boştur (K2/K7). O da iç dinleyicidedir.
	MacGateway http.Handler
	// RunnerWS — Mac runner'ın outbound WebSocket ucu (/api/runner/ws).
	RunnerWS http.Handler
	// Engines — orkestratöre verilen motor haritasının aynısı; /api/engines
	// bunu tanıtır ve bot güncellemesi buna göre doğrulanır (K18).
	Engines map[string]engine.Executor
	// DataDir — eklerin diske yazılacağı kök (ADIM 5).
	DataDir string
	// Providers — sağlayıcı kaydı (Modeller ekranı). nil ise uçlar kapalı;
	// anahtar yazımı mühür ister, o yüzden servis kendi kapısını tutar.
	Providers *providers.Service
}

func New(cfg config.Config, d Deps) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthz(d.DB))

	if d.Store != nil {
		a := &api{
			st: d.Store, orch: d.Orch, bus: d.Bus, engines: d.Engines,
			dataDir: d.DataDir, providers: d.Providers,
		}
		mux.HandleFunc("GET /api/engines", a.listEngines)       // motor durumu + seçilebilir modeller (K18)
		mux.HandleFunc("GET /api/extensions", a.listExtensions) // kurulu eklentiler + kullanan botlar
		mux.HandleFunc("GET /api/extensions/catalog", a.extensionCatalog)
		mux.HandleFunc("POST /api/extensions", a.installExtension)
		mux.HandleFunc("DELETE /api/extensions/{id}", a.removeExtension)
		// Modeller ekranı: sağlayıcı tarifleri DB'de, katalog models.dev'den.
		mux.HandleFunc("GET /api/providers", a.listProviders)
		mux.HandleFunc("POST /api/providers", a.saveProvider)
		mux.HandleFunc("PUT /api/providers/{id}", a.saveProvider)
		mux.HandleFunc("DELETE /api/providers/{id}", a.deleteProvider)
		mux.HandleFunc("GET /api/models/catalog", a.modelCatalog)
		mux.HandleFunc("GET /api/bots", a.listBots)
		mux.HandleFunc("POST /api/bots", a.createBot)
		mux.HandleFunc("GET /api/bots/{id}", a.getBot)
		mux.HandleFunc("PATCH /api/bots/{id}", a.updateBot)
		mux.HandleFunc("DELETE /api/bots/{id}", a.deleteBot)
		mux.HandleFunc("GET /api/bots/{id}/messages", a.listMessages)
		mux.HandleFunc("POST /api/bots/{id}/messages", a.postMessage)
		mux.HandleFunc("PATCH /api/messages/{id}", a.editMessage)
		mux.HandleFunc("DELETE /api/bots/{id}/messages", a.clearChat) // Sohbeti Temizle
		mux.HandleFunc("POST /api/bots/{id}/attachments", a.uploadAttachment)
		mux.HandleFunc("GET /api/attachments/{id}/file", a.downloadAttachment)
		mux.HandleFunc("GET /api/bots/{id}/runs", a.listBotRuns)
		mux.HandleFunc("GET /api/runs", a.listRuns) // ?status= (varsayılan: approval_pending — Onay Merkezi)
		mux.HandleFunc("GET /api/runs/{id}", a.getRun)
		mux.HandleFunc("POST /api/runs/{id}/approval", a.resolveApproval)
		mux.HandleFunc("GET /api/runs/{id}/events", a.runEvents)
		mux.HandleFunc("GET /api/policy", a.getPolicy)
		mux.HandleFunc("PUT /api/policy", a.putPolicy)
		mux.HandleFunc("GET /api/rules", a.listRules)
		mux.HandleFunc("POST /api/rules", a.createRule)
		mux.HandleFunc("DELETE /api/rules/{id}", a.deleteRule)
		mux.HandleFunc("GET /api/connectors", a.listConnectors)
		mux.HandleFunc("DELETE /api/connectors/{id}", a.deleteConnector)
		mux.HandleFunc("GET /api/routines", a.listRoutines) // ?bot=<id>
		mux.HandleFunc("POST /api/routines", a.createRoutine)
		mux.HandleFunc("PATCH /api/routines/{id}", a.updateRoutine)
		mux.HandleFunc("DELETE /api/routines/{id}", a.deleteRoutine)
		mux.HandleFunc("GET /api/routines/{id}/runs", a.listRoutineRuns)
		mux.HandleFunc("GET /api/memories", a.listMemories)
		mux.HandleFunc("POST /api/memories", a.createMemory)                         // kullanıcının elle kaydı (sabit talimat dahil)
		mux.HandleFunc("GET /api/memories/instructions", a.listStandingInstructions) // prompta giren kurallar
		mux.HandleFunc("GET /api/messages/search", a.searchMessages)                 // ?q=…&bot=<id> — sohbet geçmişi
		mux.HandleFunc("GET /api/memories/search", a.searchMemories)                 // ?q=…&bot=<id>
		mux.HandleFunc("GET /api/memories/export", a.exportMemories)                 // insan-okur markdown (K20)
		mux.HandleFunc("POST /api/memories/import", a.importMemories)                // gövde: markdown
		mux.HandleFunc("POST /api/memories/{id}/restore", a.restoreMemory)           // katılmayı geri al (K20)
		mux.HandleFunc("DELETE /api/memories/{id}", a.deleteMemory)
		mux.HandleFunc("GET /api/skills", a.listSkills)
		mux.HandleFunc("GET /api/audit", a.listAudit)
		mux.HandleFunc("POST /api/approvals/bulk", a.bulkApprove) // Tümünü onayla (sınırlı, K19)
		mux.HandleFunc("GET /api/tasks", a.tasksBoard)
		mux.HandleFunc("GET /api/activity", a.activityFeed)
		mux.HandleFunc("GET /api/stats", a.stats) // ?days=7|30 — maliyet panosu
	}

	if d.RunnerWS != nil {
		mux.Handle("GET /api/runner/ws", d.RunnerWS)
	}

	return &http.Server{
		Addr: cfg.HTTPAddr,
		// Belirteç mux'un TAMAMINI sarar (/healthz hariç) — bkz. auth.go.
		Handler:           bearer(cfg.APIToken, mux),
		ReadHeaderTimeout: 5 * time.Second,
	}
}

// NewInternal — MCP yüzeyleri (/mcp, /mcp/mac) için ayrı dinleyici.
//
// Neden ayrı: özel API bir alan adına bağlanabiliyor (kullanıcının kendi
// Mac'inden erişebilmesi için), MCP ise makine-içidir — motor süreci aynı
// konteynerden bağlanır. Aynı mux'ta dursalardı ya MCP de dışa açılırdı ya
// da bearer'ı dört motorun MCP yapılandırmasına yazmak gerekirdi; ikincisi
// belirteci gereksizce yayar ve motor loglarına düşme riski taşır.
// INTERNAL_HTTP_ADDR bu yüzden 127.0.0.1'e bağlanır ve compose'da
// yayımlanmaz — dışarıdan erişilecek bir yol yok, o yüzden bearer da yok.
//
// İkisi de nil ise nil döner (iskelet modu).
func NewInternal(cfg config.Config, d Deps) *http.Server {
	if d.Gateway == nil && d.MacGateway == nil {
		return nil
	}
	mux := http.NewServeMux()
	if d.Gateway != nil {
		mux.Handle("/mcp", d.Gateway)
	}
	if d.MacGateway != nil {
		mux.Handle("/mcp/mac", d.MacGateway)
	}
	return &http.Server{
		Addr:              cfg.InternalHTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
}

type api struct {
	st        *store.Store
	orch      *orchestrator.Orchestrator
	bus       *bus.Bus
	engines   map[string]engine.Executor
	providers *providers.Service
	probes    probeCache
	katalog   katalogOnbellek
	// paketKatalog — pi paket galerisi (npm arama) önbelleği.
	paketKatalog paketKatalogOnbellek
	// dataDir — eklerin diske yazıldığı kök (ADIM 5).
	dataDir string
}

// ── ortak yardımcılar ──────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("yanıt yazılamadı", "err", err)
	}
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// decodeStrict — API kontratı katıdır: bilinmeyen alan hatadır.
func decodeStrict(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	// Gövdede ikinci bir JSON değeri de hatadır.
	if dec.More() {
		return errors.New("gövdede fazladan içerik var")
	}
	return nil
}

func healthz(db Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := Health{Status: "ok", Version: version.String(), DB: "not_configured"}
		if db != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if err := db.Ping(ctx); err != nil {
				h.Status, h.DB = "degraded", "error"
				slog.Warn("healthz: veritabanı yanıt vermiyor", "err", err)
			} else {
				h.DB = "ok"
			}
		}
		if h.Status != "ok" {
			writeJSON(w, http.StatusServiceUnavailable, h)
			return
		}
		writeJSON(w, http.StatusOK, h)
	}
}
