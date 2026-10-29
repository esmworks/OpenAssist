//go:build integration

// Onay Merkezi, HENÜZ PARK EDİLMEMİŞ isteği de görmeli.
//
// Sahada ölçülen şikâyet: "onay hemen gelmiyor, kapatıp açıyorum bir anda
// düşüyor." Sebebi liste yalnız DB'den okumaktı — kısa-bekleme penceresi
// (B.3, varsayılan 60 sn) boyunca koşu hâlâ 'running' olduğu için Onay
// Merkezi bomboş görünüyor, kart ancak pencere dolup koşu park edince
// beliriyordu. Oysa sunucu isteği ilk saniyede biliyor (approvalHub).
package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/esmworks/OpenAssist/internal/bus"
	"github.com/esmworks/OpenAssist/internal/config"
	"github.com/esmworks/OpenAssist/internal/engine"
	"github.com/esmworks/OpenAssist/internal/httpapi"
	"github.com/esmworks/OpenAssist/internal/orchestrator"
	"github.com/esmworks/OpenAssist/internal/policy"
	"github.com/esmworks/OpenAssist/internal/store"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

func TestOnayMerkeziCanliIstegiHemenGosterir(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL tanımlı değil — entegrasyon testi atlandı")
	}
	ctx, cancel := context.WithCancel(context.Background())
	st, err := store.Open(ctx, url)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Pencere UZUN: koşu bu süre boyunca 'running' kalır. Testin bütün mesele
	// ettiği an burası — eski kod bu aralıkta boş liste dönüyordu.
	cfg := orchestrator.DefaultConfig()
	cfg.Workers = 2
	cfg.ApprovalWait = 8 * time.Second
	cfg.PollEvery = 100 * time.Millisecond
	cfg.DataDir = t.TempDir()
	cfg.WorkerID = "test-" + t.Name()
	cfg.ToolClasses = map[string]policy.Classification{
		"mcp__gw__gmail_send_draft": {Class: policy.ClassExternal, Ruleable: true},
	}
	orch := orchestrator.New(st, map[string]engine.Executor{
		"ask-live": parkingEngine("mcp__gw__gmail_send_draft", "permission"),
	}, bus.New(), cfg)
	orch.Start(ctx)
	t.Cleanup(func() {
		cancel()
		orch.Wait()
		st.Close()
	})

	srv := httpapi.New(config.Config{HTTPAddr: ":0", APIToken: testBelirtec},
		httpapi.Deps{DB: st, Store: st, Orch: orch, Bus: bus.New()})
	ts := httptest.NewServer(srv.Handler)
	belirtecliIstemci(ts)
	defer ts.Close()

	bot, err := st.Q.CreateBot(ctx, db.CreateBotParams{
		Kind: "bot", Name: "canli-" + t.Name(), Engine: "ask-live", AllowedTools: []string{},
	})
	if err != nil {
		t.Fatalf("bot: %v", err)
	}
	t.Cleanup(func() { _ = st.Q.DeleteBot(context.Background(), bot.ID) })

	run, err := orch.EnqueueChat(ctx, bot, "taslağı gönder", "api")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	type askDTO struct {
		Kind        string `json:"kind"`
		Tool        string `json:"tool"`
		AllowAlways bool   `json:"allow_always"`
	}
	type runDTO struct {
		ID         string `json:"id"`
		Status     string `json:"status"`
		PendingAsk askDTO `json:"pending_ask"`
	}
	listele := func() []runDTO {
		t.Helper()
		res, err := ts.Client().Get(ts.URL + "/api/runs?status=approval_pending")
		if err != nil {
			t.Fatalf("liste: %v", err)
		}
		defer res.Body.Close()
		var out []runDTO
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatalf("liste çözülemedi: %v", err)
		}
		return out
	}

	// Kart PENCERE DOLMADAN görünmeli. Pencere 8 sn; 3 sn içinde çıkmazsa
	// kullanıcı yine ekrana bakıp bekliyor demektir.
	var bulundu *runDTO
	son := time.Now().Add(3 * time.Second)
	for time.Now().Before(son) {
		for _, r := range listele() {
			if r.ID == run.ID.String() {
				bulundu = &r
				break
			}
		}
		if bulundu != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if bulundu == nil {
		t.Fatal("canlı istek Onay Merkezi'nde görünmedi — kullanıcı pencere " +
			"dolana kadar (varsayılanda 60 sn) boş ekrana bakar")
	}
	if bulundu.PendingAsk.Tool != "mcp__gw__gmail_send_draft" {
		t.Fatalf("kart hangi araç için olduğunu söylemiyor: %+v", bulundu.PendingAsk)
	}
	if !bulundu.PendingAsk.AllowAlways {
		t.Errorf("canlı kartta 'hep izin ver' kararı yok: %+v", bulundu.PendingAsk)
	}
	if bulundu.Status != "approval_pending" {
		t.Errorf("durum %q — istemci bunu onay kartı saymaz", bulundu.Status)
	}

	// KANIT: koşu DB'de hâlâ 'running'. Yani kart park edilmiş kayıttan değil,
	// süreç içi bekleyen istekten geliyor.
	ham, err := st.Q.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("koşu okunamadı: %v", err)
	}
	if ham.Status != "running" {
		t.Fatalf("koşu zaten park etmiş (%s) — test canlı yolu ölçmedi", ham.Status)
	}

	// Pencere içinde verilen cevap HIZLI yoldan gider: koşu park/resume turu
	// yaşamadan devam eder. Pencerenin var oluş sebebi budur.
	if err := orch.ResolveApproval(ctx, run.ID, "allow", "", false); err != nil {
		t.Fatalf("canlı istek cevaplanamadı: %v", err)
	}
	bitis := time.Now().Add(5 * time.Second)
	for time.Now().Before(bitis) {
		got, err := st.Q.GetRun(ctx, run.ID)
		if err == nil && got.Status == "completed" {
			return
		}
		if err == nil && got.Status == "approval_pending" {
			t.Fatal("pencere içinde cevaplanan koşu yine park etti — hızlı yol kırık")
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("koşu zamanında tamamlanmadı")
}
