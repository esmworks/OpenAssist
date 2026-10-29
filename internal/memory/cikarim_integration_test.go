//go:build integration

// ADIM 8 — koşu sonrası hafıza çıkarımı: biten koşunun ardından AfterRun
// kancası ucuz turda tutanaki okur; güven eşiği + sır kapısından geçen
// öneriler normal yazma yoluna düşer, gerisi sessizce elenir.
package memory_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/esmworks/OpenAssist/internal/bus"
	"github.com/esmworks/OpenAssist/internal/engine"
	"github.com/esmworks/OpenAssist/internal/engine/enginetest"
	"github.com/esmworks/OpenAssist/internal/memory"
	"github.com/esmworks/OpenAssist/internal/orchestrator"
	"github.com/esmworks/OpenAssist/internal/store"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

// ciftMotor — sohbet turada düz cevap, çıkarım turada (NoTools) öneri listesi
// döner. Liste iki meşru öneri + bir sır + bir düşük güven içerir: kayıt
// aşamasında yalnız iki meşru kalmalı.
func ciftMotor() enginetest.Func {
	return func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		emit(engine.Event{Kind: engine.KindSessionStarted, SessionID: "s", Model: "fake"})
		final := "Raporu tablo halinde hazırladım; üç sütun ve beş satır var, özet istersen ekleyebilirim."
		if in.NoTools {
			if !strings.Contains(in.Prompt, "KOŞU TUTANAK") {
				final = `[]` // tutanak yoksa önerme — istemci hatası görünür olsun
			} else {
				final = `[
					{"content":"Kullanıcı raporları tablo halinde ister","kind":"preference","confidence":0.9},
					{"content":"Haftalık özet maili cuma sabahı gider","kind":"fact","confidence":0.8},
					{"content":"sk-live-1234abcSIRBURADA","kind":"fact","confidence":0.9},
					{"content":"Düşük güvenli öneri","kind":"preference","confidence":0.2}
				]`
			}
		}
		res := &engine.TurnResult{Ok: true, StopReason: "success", NumTurns: 1,
			FinalText: final, ResumeCursor: "c"}
		emit(engine.Event{Kind: engine.KindTurnCompleted, Result: res})
		return res, nil
	}
}

func TestCikarimAfterRun(t *testing.T) {
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

	engines := map[string]engine.Executor{"cikarim-motor": ciftMotor()}
	cfg := orchestrator.DefaultConfig()
	cfg.Workers = 2
	cfg.PollEvery = 50 * time.Millisecond
	cfg.DataDir = t.TempDir()
	cfg.WorkerID = "test-" + t.Name()
	cfg.AfterRun = memory.NewInferrer(st, engines, cfg.DataDir).AfterRun
	o := orchestrator.New(st, engines, bus.New(), cfg)
	o.Start(ctx)
	t.Cleanup(func() {
		cancel()
		o.Wait()
		st.Close()
	})

	bot, err := st.Q.CreateBot(ctx, db.CreateBotParams{
		Kind: "bot", Name: "cikarim-" + t.Name(), Engine: "cikarim-motor",
		AllowedTools: []string{},
	})
	if err != nil {
		t.Fatalf("bot: %v", err)
	}
	t.Cleanup(func() { _ = st.Q.DeleteBot(context.Background(), bot.ID) })

	run, err := o.EnqueueChat(ctx, bot, "raporları tablo halinde hazırla", "")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// Koşu bitsin, ardından çıkarım kayıtları düşsün (ayrı goroutine).
	deadline := time.Now().Add(5 * time.Second)
	for {
		guncel, err := st.Q.GetRun(ctx, run.ID)
		if err != nil {
			t.Fatalf("GetRun: %v", err)
		}
		if guncel.Status == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("koşu bitmedi: %s", guncel.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Kayıtlar tek tek düşüyor: ilkini görüp çıkmak yarışa girer (ikincisi
	// yoldayken "1 kayıt" diye patlardı). Beklenen sayıya kadar bekle, sonra
	// fazlası da görünsün diye bir tur daha oku.
	botMemleri := func() []db.Memory {
		mems, err := st.Q.ListMemories(ctx, 200)
		if err != nil {
			t.Fatalf("ListMemories: %v", err)
		}
		var liste []db.Memory
		for _, m := range mems {
			if m.BotID.Valid && m.BotID.UUID == bot.ID {
				liste = append(liste, m)
			}
		}
		return liste
	}
	var botMems []db.Memory
	memDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(memDeadline) {
		botMems = botMemleri()
		if len(botMems) >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	botMems = botMemleri()

	// KAPILAR: düşük güven + sır içeren öneriler düşmeli; iki meşru kalmalı.
	var icerikler []string
	for _, m := range botMems {
		icerikler = append(icerikler, m.Content)
	}
	if len(botMems) != 2 {
		t.Fatalf("2 kayıt beklenirdi, geldi: %v", icerikler)
	}
	gecen := map[string]bool{}
	for _, m := range botMems {
		gecen[m.Content] = true
		if m.Confidence < 0.5 {
			t.Fatalf("eşik altı öneri yazıldı: %q (%.2f)", m.Content, m.Confidence)
		}
		if !m.SourceRunID.Valid || m.SourceRunID.UUID != run.ID {
			t.Fatalf("kaynak koşu damgası yanlış: %v", m.SourceRunID)
		}
	}
	if !gecen["Kullanıcı raporları tablo halinde ister"] || !gecen["Haftalık özet maili cuma sabahı gider"] {
		t.Fatalf("meşru öneriler eksik: %v", icerikler)
	}

	// Çıkarım turu maliyet defterine 'inference' origin'iyle düşer.
	runs, err := st.Q.ListRunsByBot(ctx, db.ListRunsByBotParams{BotID: bot.ID, Limit: 10})
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	var defter bool
	for _, r := range runs {
		if r.Origin == "inference" {
			defter = true
		}
	}
	if !defter {
		t.Fatalf("inference origin'li maliyet satırı yok: %+v", runs)
	}
}
