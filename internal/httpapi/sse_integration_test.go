//go:build integration

// SSE kapanış testi: koşu bittiğinde akış KAPANIR. Motorun turn.completed
// olayı koşunun DB durumu yazılmadan önce aktığı için akış ona bakarak
// kapanamaz (durum hâlâ 'running' görünür) — kapanış kararını durum
// yazıldıktan sonra gelen run.settled verir. Regresyon: akış açık kalırsa
// Mac istemcisinde "yazıyor" göstergesi cevaptan sonra da asılı kalır.
package httpapi_test

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/esmworks/OpenAssist/internal/bus"
	"github.com/esmworks/OpenAssist/internal/config"
	"github.com/esmworks/OpenAssist/internal/engine"
	"github.com/esmworks/OpenAssist/internal/engine/enginetest"
	"github.com/esmworks/OpenAssist/internal/httpapi"
	"github.com/esmworks/OpenAssist/internal/orchestrator"
	"github.com/esmworks/OpenAssist/internal/store"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

// slowSettleEngine — cevabı akıtıp turn.completed yayınlar, sonra dönmeden
// önce bekler. Bu bekleme motor olayı ile DB'ye yazılan durum arasındaki
// gerçek pencereyi büyütür: turn.completed'a bakan bir akış burada yanlışlıkla
// açık kalır.
func slowSettleEngine(gap time.Duration) enginetest.Func {
	return func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		emit(engine.Event{Kind: engine.KindSessionStarted, SessionID: "s", Model: "fake"})
		emit(engine.Event{Kind: engine.KindAssistantDelta, Text: "Merhaba"})
		res := &engine.TurnResult{
			Ok: true, StopReason: "success", FinalText: "Merhaba", NumTurns: 1, ResumeCursor: "c1",
		}
		emit(engine.Event{Kind: engine.KindTurnCompleted, Result: res})
		select {
		case <-ctx.Done():
		case <-time.After(gap):
		}
		return res, nil
	}
}

func TestRunEventsStreamClosesOnCompletion(t *testing.T) {
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

	// Orkestratör ve HTTP yüzeyi AYNI bus'ı paylaşır — canlı SSE budur.
	b := bus.New()
	cfg := orchestrator.DefaultConfig()
	cfg.Workers = 1
	cfg.PollEvery = 50 * time.Millisecond
	cfg.DataDir = t.TempDir()
	cfg.WorkerID = "test-" + t.Name()
	orch := orchestrator.New(st, map[string]engine.Executor{
		"slow-settle": slowSettleEngine(750 * time.Millisecond),
	}, b, cfg)
	orch.Start(ctx)
	t.Cleanup(func() {
		cancel()
		orch.Wait()
		st.Close()
	})

	srv := httpapi.New(config.Config{HTTPAddr: ":0", APIToken: testBelirtec}, httpapi.Deps{DB: st, Store: st, Orch: orch, Bus: b})
	ts := httptest.NewServer(srv.Handler)
	belirtecliIstemci(ts)
	defer ts.Close()

	bot, err := st.Q.CreateBot(ctx, db.CreateBotParams{
		Kind: "bot", Name: "sse-" + t.Name(), Engine: "slow-settle", AllowedTools: []string{},
	})
	if err != nil {
		t.Fatalf("bot: %v", err)
	}
	t.Cleanup(func() { _ = st.Q.DeleteBot(context.Background(), bot.ID) })

	run, err := orch.EnqueueChat(ctx, bot, "selam", "api")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// Akışa koşu daha kuyruktayken bağlan: kapanış canlı yoldan gelmeli.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		ts.URL+"/api/runs/"+run.ID.String()+"/events", nil)
	if err != nil {
		t.Fatalf("istek: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("akış: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	type frame struct{ kind string }
	frames := make(chan frame, 64)
	go func() {
		defer close(frames)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if k, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
				frames <- frame{kind: k}
			}
		}
	}()

	// Akış, motor bittikten sonra kendiliğinden kapanmalı. Keepalive güvenlik
	// ağı 25 sn'de bir; bu süre onun ALTINDA, yani kapanışı run.settled
	// getirmek zorunda.
	deadline := time.After(10 * time.Second)
	var seen []string
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatalf("akış stream.end göndermeden kapandı: %v", seen)
			}
			seen = append(seen, f.kind)
			if f.kind == "stream.end" {
				// Kapanış ancak durum yazıldıktan sonra gelmeli.
				waitStatus(t, st, run.ID, "completed")
				return
			}
		case <-deadline:
			t.Fatalf("akış kapanmadı — istemcide 'yazıyor' göstergesi asılı kalır (gelen: %v)", seen)
		}
	}
}
