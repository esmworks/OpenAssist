//go:build integration

// ADIM 3: aynı (bot, origin, key) üçlüsüyle tekrarlanan POST yeni koşu
// üretmez — istemci retry'i/çift tıklama aynı koşuya düşer. Anahtarsız
// gönderim eski davranışını korur; anahtar kapsamı bot+kanaldır.
package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
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
	"github.com/esmworks/OpenAssist/internal/store"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

// instantEngine — hemen biten motor: koşular kuyrukta birikmesin.
func instantEngine() enginetest.Func {
	return func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		emit(engine.Event{Kind: engine.KindSessionStarted, SessionID: "s", Model: "fake"})
		res := &engine.TurnResult{Ok: true, StopReason: "success", FinalText: "ok", NumTurns: 1, ResumeCursor: "c"}
		emit(engine.Event{Kind: engine.KindTurnCompleted, Result: res})
		return res, nil
	}
}

func TestChatIdempotency(t *testing.T) {
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

	cfg := orchestrator.DefaultConfig()
	cfg.Workers = 2
	cfg.PollEvery = 50 * time.Millisecond
	cfg.DataDir = t.TempDir()
	cfg.WorkerID = "test-" + t.Name()
	orch := orchestrator.New(st, map[string]engine.Executor{"instant": instantEngine()}, bus.New(), cfg)
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

	mkBot := func(name string) db.Bot {
		t.Helper()
		bot, err := st.Q.CreateBot(ctx, db.CreateBotParams{
			Kind: "bot", Name: name, Engine: "instant", AllowedTools: []string{},
		})
		if err != nil {
			t.Fatalf("bot: %v", err)
		}
		t.Cleanup(func() { _ = st.Q.DeleteBot(context.Background(), bot.ID) })
		return bot
	}
	post := func(botID, text, origin, key string) string {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"text": text, "origin": origin, "key": key})
		resp, err := ts.Client().Post(ts.URL+"/api/bots/"+botID+"/messages",
			"application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("POST → %d (202 beklenirdi)", resp.StatusCode)
		}
		var out struct {
			RunID string `json:"run_id"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("gövde: %v", err)
		}
		if out.RunID == "" {
			t.Fatal("run_id boş döndü")
		}
		return out.RunID
	}
	counts := func(botID string) (userMsgs, runs int) {
		t.Helper()
		id, err := uuid.Parse(botID)
		if err != nil {
			t.Fatalf("bot id: %v", err)
		}
		bot, err := st.Q.GetBot(ctx, id)
		if err != nil {
			t.Fatalf("bot: %v", err)
		}
		conv, err := st.BotConversation(ctx, bot)
		if err != nil {
			t.Fatalf("sohbet: %v", err)
		}
		msgs, err := st.Q.ListMessages(ctx, db.ListMessagesParams{ConversationID: conv.ID, Limit: 100})
		if err != nil {
			t.Fatalf("messages: %v", err)
		}
		for _, m := range msgs {
			if m.Role == "user" {
				userMsgs++
			}
		}
		rs, err := st.Q.ListRunsByBot(ctx, db.ListRunsByBotParams{BotID: id, Limit: 100})
		if err != nil {
			t.Fatalf("runs: %v", err)
		}
		return userMsgs, len(rs)
	}
	// Koşuların oturmasını bekler: cleanup botu silmeden önce worker yarım
	// kalan koşu bırakmasın (FK ihlali gürültüsü + flake).
	waitSettled := func(botID string) {
		t.Helper()
		id, err := uuid.Parse(botID)
		if err != nil {
			t.Fatalf("bot id: %v", err)
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			rs, err := st.Q.ListRunsByBot(ctx, db.ListRunsByBotParams{BotID: id, Limit: 100})
			if err != nil {
				t.Fatalf("runs: %v", err)
			}
			bekliyor := false
			for _, r := range rs {
				if r.Status == "queued" || r.Status == "running" {
					bekliyor = true
				}
			}
			if !bekliyor {
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("koşular 10 sn içinde oturmadı")
	}

	bot := mkBot("idem-" + t.Name())
	botID := bot.ID.String()

	// Aynı anahtar iki kez → TEK mesaj, TEK koşu, aynı run_id.
	r1 := post(botID, "selam", "api", "k1")
	r1dup := post(botID, "selam", "api", "k1")
	if r1 != r1dup {
		t.Fatalf("tekrar gönderim yeni koşu üretti: %s vs %s", r1, r1dup)
	}
	if mu, ru := counts(botID); mu != 1 || ru != 1 {
		t.Fatalf("tek anahtar t ekinlik k bozuk: %d mesaj/%d koşu (1/1 beklenirdi)", mu, ru)
	}

	// Farklı anahtar → yeni koşu.
	if r2 := post(botID, "ikinci", "api", "k2"); r2 == r1 {
		t.Fatal("farklı anahtar aynı koşuyu döndürdü")
	}
	if mu, ru := counts(botID); mu != 2 || ru != 2 {
		t.Fatalf("iki anahtar %d mesaj/%d koşu (2/2 beklenirdi)", mu, ru)
	}

	// Aynı anahtar, farklı KANAL → yeni gönderim (kapsam bot+kanal).
	rMac := post(botID, "selam", "mac", "k1")
	if rMac == r1 {
		t.Fatal("farklı kanaldaki aynı anahtar dedupe'a takıldı")
	}

	// Anahtarsız gönderim iki kez → iki koşu (eski davranış korunur).
	post(botID, "anahtarsız", "api", "")
	post(botID, "anahtarsız", "api", "")
	if mu, ru := counts(botID); mu != 5 || ru != 5 {
		t.Fatalf("anahtarsız yol bozuldu: %d mesaj/%d koşu (5/5 beklenirdi)", mu, ru)
	}

	// Anahtar kapsamı bot'a özel: aynı anahtar başka botta serbesttir.
	bot2 := mkBot("idem2-" + t.Name())
	rOther := post(bot2.ID.String(), "selam", "api", "k1")
	if rOther == r1 {
		t.Fatal("farklı bottaki aynı anahtar dedupe'a takıldı")
	}

	// Metin değişse bile AYNI anahtar ilk koşuyu döndürmeli — anahtar
	// gönderimin kimliğidir, içeriğin değil (retry'de gövde aynı kalır; bu
	// yalnız anahtarın içerikten bağımsız olduğunu belgeler).
	if r3 := post(botID, "farklı metin", "api", "k1"); r3 != r1 {
		t.Fatal("aynı anahtar metin değişince yeni koşu üretti")
	}

	waitSettled(botID)
	waitSettled(bot2.ID.String())

	mu, ru := counts(botID)
	if mu != 5 || ru != 5 {
		t.Fatalf("kapanış sayımı bozuk: %d mesaj/%d koşu (5/5 beklenirdi)", mu, ru)
	}
}
