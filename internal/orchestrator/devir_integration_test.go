//go:build integration

// ADIM 6 — botlar arası devir: ask_bot çağrısı (run-bağlamlı onAsk kanalı)
// hedef bota origin='bot' koşusu açar; kaynak sohbete "iletildi" çipi,
// cevap gelince "← cevap" çipi düşer. Depth-1: devralan koşunun araç
// listesinden ask_bot düşer. Kendine devir yasaktır.
package orchestrator_test

import (
	"context"
	"strings"
	"testing"

	"github.com/esmworks/OpenAssist/internal/engine"
	"github.com/esmworks/OpenAssist/internal/engine/enginetest"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

// devirCevapMotor — devri alan bot: gelen araç listesini yakalar, hızlı biter.
func devirCevapMotor(gotTools chan []string) enginetest.Func {
	return func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		emit(engine.Event{Kind: engine.KindSessionStarted, SessionID: "s", Model: "fake"})
		gotTools <- in.AllowedTools
		res := &engine.TurnResult{Ok: true, StopReason: "success", FinalText: "Toplu notlar hazır; ikisi onay bekliyor", NumTurns: 1, ResumeCursor: "c"}
		emit(engine.Event{Kind: engine.KindTurnCompleted, Result: res})
		return res, nil
	}
}

// devirSorMotor — devri açan bot: ask_bot çağırır, cevabı kanala bildirir.
// hedefID kutusundan hedefi OKUMA ZAMANINDA alır (motorlar botlardan önce
// kurulur; kutu test içinde doldurulur).
func devirSorMotor(hedefID []string, cevap chan string) enginetest.Func {
	return func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		emit(engine.Event{Kind: engine.KindSessionStarted, SessionID: "s", Model: "fake"})
		ans := in.OnAsk(ctx, engine.Ask{Kind: "bot_message", BotID: hedefID[0],
			Message: "haftalık notları topla ve derle"})
		cevap <- ans.Message
		res := &engine.TurnResult{Ok: true, StopReason: "success", FinalText: "İşi devrettim.", NumTurns: 1, ResumeCursor: "c"}
		emit(engine.Event{Kind: engine.KindTurnCompleted, Result: res})
		return res, nil
	}
}

func TestBotDevirZinciri(t *testing.T) {
	ctx := context.Background()
	gotTools := make(chan []string, 1)
	cevap := make(chan string, 1)
	hedefID := make([]string, 1)

	st, o := setup(t, map[string]engine.Executor{
		"devir-sor":   devirSorMotor(hedefID, cevap),
		"devir-cevap": devirCevapMotor(gotTools),
	}, 0)

	hedef, err := st.Q.CreateBot(ctx, db.CreateBotParams{
		Kind: "bot", Name: "devir-hedef-" + t.Name(), Engine: "devir-cevap",
		AllowedTools: []string{"mcp__oa__ask_bot", "mcp__gw__list_bots"},
	})
	if err != nil {
		t.Fatalf("hedef bot: %v", err)
	}
	t.Cleanup(func() { _ = st.Q.DeleteBot(context.Background(), hedef.ID) })

	kaynak, err := st.Q.CreateBot(ctx, db.CreateBotParams{
		Kind: "bot", Name: "devir-kaynak-" + t.Name(), Engine: "devir-sor",
		AllowedTools: []string{"mcp__oa__ask_bot"},
	})
	if err != nil {
		t.Fatalf("kaynak bot: %v", err)
	}
	t.Cleanup(func() { _ = st.Q.DeleteBot(context.Background(), kaynak.ID) })

	hedefID[0] = hedef.ID.String()

	kaynakRun, err := o.EnqueueChat(ctx, kaynak, "şu işi ilet", "")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if ans := <-cevap; ans == "" || strings.HasPrefix(ans, "devir açılamadı") {
		t.Fatalf("ask_bot cevabı hatalı: %q", ans)
	}
	waitStatus(t, st, kaynakRun.ID, "completed")

	// Hedef koşusu: origin bot, kaynağa bağlı, devir metnini taşıyor.
	runs, err := st.Q.ListRunsByBot(ctx, db.ListRunsByBotParams{BotID: hedef.ID, Limit: 5})
	if err != nil || len(runs) != 1 {
		t.Fatalf("hedef koşusu beklenirdi: %d (%v)", len(runs), err)
	}
	hedefRun := runs[0]
	if hedefRun.Origin != "bot" || !hedefRun.ReplyToRunID.Valid || hedefRun.ReplyToRunID.UUID != kaynakRun.ID {
		t.Fatalf("hedef koşusu yanlış açıldı: origin=%q reply=%v", hedefRun.Origin, hedefRun.ReplyToRunID)
	}
	if !strings.Contains(hedefRun.Prompt, "notları") {
		t.Fatalf("devir metni hedefe ulaşmadı: %q", hedefRun.Prompt)
	}
	waitStatus(t, st, hedefRun.ID, "completed")

	// Depth-1: devralan koşunun araç listesinde ask_bot OLMAMALI.
	for _, tname := range <-gotTools {
		if tname == "mcp__oa__ask_bot" {
			t.Fatalf("depth-1 ihlali: devralan koşu ask_bot'u gördü: %v", <-gotTools)
		}
	}

	// Hedef sohbette kullanıcı mesajı GÖNDEREN damgalı.
	hedefConv, err := st.BotConversation(ctx, hedef)
	if err != nil {
		t.Fatalf("hedef sohbet: %v", err)
	}
	hedefMsgs, _ := st.Q.ListMessages(ctx, db.ListMessagesParams{ConversationID: hedefConv.ID, Limit: 10})
	var devirMsg *db.Message
	for i := range hedefMsgs {
		if hedefMsgs[i].Role == "user" {
			devirMsg = &hedefMsgs[i]
		}
	}
	if devirMsg == nil || !devirMsg.SenderBotID.Valid || devirMsg.SenderBotID.UUID != kaynak.ID {
		t.Fatalf("hedef mesajda gönderen damgası yok/yanlış: %+v", devirMsg)
	}
	if devirMsg.Origin != "bot" {
		t.Fatalf("hedef mesajın kanal damgası yanlış: %q", devirMsg.Origin)
	}

	// Kaynak sohbette iki çip: "iletildi" + "cevabını verdi".
	kaynakConv, err := st.BotConversation(ctx, kaynak)
	if err != nil {
		t.Fatalf("kaynak sohbet: %v", err)
	}
	kaynakMsgs, _ := st.Q.ListMessages(ctx, db.ListMessagesParams{ConversationID: kaynakConv.ID, Limit: 10})
	var iletildi, cevapCipi bool
	for _, m := range kaynakMsgs {
		if m.Role != "event" {
			continue
		}
		switch {
		case strings.Contains(m.Text, "iletildi"):
			iletildi = true
		case strings.Contains(m.Text, "cevabını verdi"):
			cevapCipi = true
		}
	}
	if !iletildi || !cevapCipi {
		t.Fatalf("çipler eksik: iletildi=%v cevap=%v", iletildi, cevapCipi)
	}
}

func TestDevirKendineYasak(t *testing.T) {
	ctx := context.Background()
	kendiID := make([]string, 1)
	cevap := make(chan string, 1)

	st, o := setup(t, map[string]engine.Executor{
		"kendi-devir": devirSorMotor(kendiID, cevap),
	}, 0)

	bot, err := st.Q.CreateBot(ctx, db.CreateBotParams{
		Kind: "bot", Name: "kendi-" + t.Name(), Engine: "kendi-devir",
		AllowedTools: []string{"mcp__oa__ask_bot"},
	})
	if err != nil {
		t.Fatalf("bot: %v", err)
	}
	t.Cleanup(func() { _ = st.Q.DeleteBot(context.Background(), bot.ID) })
	kendiID[0] = bot.ID.String()

	run, err := o.EnqueueChat(ctx, bot, "kendine devret", "")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if ans := <-cevap; !strings.Contains(ans, "kendine") {
		t.Fatalf("kendine devir veto edilmedi: %q", ans)
	}
	waitStatus(t, st, run.ID, "completed")

	// Yeni koşu AÇILMAMALI: botun toplam koşusu 1 (yalnız kaynak).
	runs, err := st.Q.ListRunsByBot(ctx, db.ListRunsByBotParams{BotID: bot.ID, Limit: 5})
	if err != nil || len(runs) != 1 {
		t.Fatalf("kendine devir koşu açtı: %d koşu (%v)", len(runs), err)
	}
}
