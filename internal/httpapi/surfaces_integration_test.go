//go:build integration

// Faz 8 yüzey testleri: toplu onay vetoları gerçek Postgres + sahte motor +
// gerçek HTTP yığını üzerinden. K19 sınırı: sorular, tainted koşular ve
// silme benzeri araçlar toplu onaydan GEÇMEZ.
package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/esmworks/OpenAssist/internal/bus"
	"github.com/esmworks/OpenAssist/internal/config"
	"github.com/esmworks/OpenAssist/internal/engine"
	"github.com/esmworks/OpenAssist/internal/engine/enginetest"
	"github.com/esmworks/OpenAssist/internal/httpapi"
	"github.com/esmworks/OpenAssist/internal/orchestrator"
	"github.com/esmworks/OpenAssist/internal/policy"
	"github.com/esmworks/OpenAssist/internal/store"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

// parkingEngine — verilen aracı sorup cevaba göre biten motor; kısa
// approvalWait ile istek parka düşer.
func parkingEngine(tool, kind string) enginetest.Func {
	return func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		emit(engine.Event{Kind: engine.KindSessionStarted, SessionID: "s", Model: "fake"})
		if in.ResumeCursor != "" {
			res := &engine.TurnResult{Ok: true, StopReason: "success", FinalText: "bitti", NumTurns: 1, ResumeCursor: "c2"}
			emit(engine.Event{Kind: engine.KindTurnCompleted, Result: res})
			return res, nil
		}
		ask := engine.Ask{Kind: kind, Tool: tool, Input: json.RawMessage(`{}`)}
		if kind == "question" {
			ask.Tool = ""
			ask.Question = "Hangisi?"
			ask.Choices = []string{"A", "B"}
		}
		in.OnAsk(ctx, ask)
		res := &engine.TurnResult{Ok: true, StopReason: "success", FinalText: "beklemede", NumTurns: 1, ResumeCursor: "c1"}
		emit(engine.Event{Kind: engine.KindTurnCompleted, Result: res})
		return res, nil
	}
}

func TestBulkApproveLimits(t *testing.T) {
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

	// Not: soru (question) senaryosu yok — sorular ASLA park edilmez (Faz 5:
	// pencere dolarsa bot kendi kararını verir), approval_pending'e düşemez.
	// bulkApprove'daki soru vetosu savunma katmanıdır.
	engines := map[string]engine.Executor{
		"ask-safe":    parkingEngine("mcp__gw__gmail_send_draft", "permission"),
		"ask-delete":  parkingEngine("mcp__gw__delete_bot", "permission"),
		"ask-tainted": parkingEngine("mcp__gw__gmail_send_draft", "permission"),
	}
	cfg := orchestrator.DefaultConfig()
	cfg.Workers = 4
	cfg.ApprovalWait = 150 * time.Millisecond
	cfg.PollEvery = 100 * time.Millisecond
	cfg.DataDir = t.TempDir()
	cfg.WorkerID = "test-" + t.Name()
	cfg.ToolClasses = map[string]policy.Classification{
		"mcp__gw__gmail_send_draft": {Class: policy.ClassExternal, Ruleable: true},
		"mcp__gw__delete_bot":       {Class: policy.ClassWrite, Ruleable: false},
	}
	orch := orchestrator.New(st, engines, bus.New(), cfg)
	orch.Start(ctx)
	t.Cleanup(func() {
		cancel()
		orch.Wait()
		st.Close()
	})

	srv := httpapi.New(config.Config{HTTPAddr: ":0", APIToken: testBelirtec}, httpapi.Deps{DB: st, Store: st, Orch: orch, Bus: bus.New()})
	ts := httptest.NewServer(srv.Handler)
	belirtecliIstemci(ts)
	defer ts.Close()

	mk := func(engineName string, taint bool) uuid.UUID {
		t.Helper()
		bot, err := st.Q.CreateBot(ctx, db.CreateBotParams{
			Kind: "bot", Name: "blk-" + engineName + "-" + t.Name(), Engine: engineName, AllowedTools: []string{},
		})
		if err != nil {
			t.Fatalf("bot: %v", err)
		}
		t.Cleanup(func() { _ = st.Q.DeleteBot(context.Background(), bot.ID) })
		run, err := orch.EnqueueChat(ctx, bot, "iş", "api")
		if err != nil {
			t.Fatalf("enqueue: %v", err)
		}
		if taint {
			// Koşu başlamadan boyanamaz; tainted bayrağı ask anında koşuda
			// olsun diye SetRunTainted'ı park sonrası yazarız.
			waitStatus(t, st, run.ID, "approval_pending")
			if err := st.Q.SetRunTainted(ctx, run.ID); err != nil {
				t.Fatalf("taint: %v", err)
			}
			return run.ID
		}
		waitStatus(t, st, run.ID, "approval_pending")
		return run.ID
	}

	safeID := mk("ask-safe", false)
	deleteID := mk("ask-delete", false)
	taintedID := mk("ask-tainted", true)

	resp, err := ts.Client().Post(ts.URL+"/api/approvals/bulk", "application/json", bytes.NewReader(nil))
	if err != nil {
		t.Fatalf("bulk: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Results []struct {
			RunID  uuid.UUID `json:"run_id"`
			Status string    `json:"status"`
			Reason string    `json:"reason"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	got := map[uuid.UUID]string{}
	for _, r := range body.Results {
		got[r.RunID] = r.Status
	}
	if got[safeID] != "approved" {
		t.Errorf("güvenli taslak gönderimi onaylanmalıydı: %v", got[safeID])
	}
	for name, id := range map[string]uuid.UUID{"silme": deleteID, "tainted": taintedID} {
		if got[id] != "skipped" {
			t.Errorf("%s koşusu atlanmalıydı: %v", name, got[id])
		}
	}
	// Onaylanan koşu resume ile tamamlanır.
	waitStatus(t, st, safeID, "completed")
}

func waitStatus(t *testing.T, st *store.Store, runID uuid.UUID, want string) db.Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last db.Run
	for time.Now().Before(deadline) {
		run, err := st.Q.GetRun(context.Background(), runID)
		if err == nil {
			last = run
			if run.Status == want {
				return run
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("koşu %s durumuna gelmedi (son: %s)", want, last.Status)
	return last
}
