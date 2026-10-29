//go:build integration

// Orkestratör entegrasyon testleri: gerçek Postgres + sahte motor.
// Kuyruk (claim/heartbeat), olay kalıcılığı ve onay durum makinesinin
// (kısa yol / park / resume) uçtan uca kanıtı — CI'da her push'ta koşar.
package orchestrator_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/esmworks/OpenAssist/internal/bus"
	"github.com/esmworks/OpenAssist/internal/engine"
	"github.com/esmworks/OpenAssist/internal/engine/enginetest"
	"github.com/esmworks/OpenAssist/internal/orchestrator"
	"github.com/esmworks/OpenAssist/internal/store"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

func setup(t *testing.T, engines map[string]engine.Executor, approvalWait time.Duration, opts ...func(*orchestrator.Config)) (*store.Store, *orchestrator.Orchestrator) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL tanımlı değil — entegrasyon testi atlandı")
	}
	ctx, cancel := context.WithCancel(context.Background())
	st, err := store.Open(ctx, url)
	if err != nil {
		cancel()
		t.Fatalf("store: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		cancel()
		t.Fatalf("migrate: %v", err)
	}

	cfg := orchestrator.DefaultConfig()
	cfg.Workers = 2
	cfg.ApprovalWait = approvalWait
	cfg.PollEvery = 100 * time.Millisecond
	cfg.HeartbeatEvery = time.Second
	cfg.DataDir = t.TempDir()
	cfg.WorkerID = "test-" + t.Name()
	for _, opt := range opts {
		opt(&cfg)
	}

	o := orchestrator.New(st, engines, bus.New(), cfg)
	o.Start(ctx)
	t.Cleanup(func() {
		cancel()
		o.Wait()
		st.Close()
	})
	return st, o
}

func mkBot(t *testing.T, st *store.Store, engineName string) db.Bot {
	t.Helper()
	bot, err := st.Q.CreateBot(context.Background(), db.CreateBotParams{
		Kind: "bot", Name: "test-" + t.Name(), Engine: engineName, AllowedTools: []string{},
	})
	if err != nil {
		t.Fatalf("bot oluşturulamadı: %v", err)
	}
	return bot
}

func waitStatus(t *testing.T, st *store.Store, runID uuid.UUID, want string) db.Run {
	const timeout = 5 * time.Second
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		run, err := st.Q.GetRun(context.Background(), runID)
		if err != nil {
			t.Fatalf("GetRun: %v", err)
		}
		if run.Status == want {
			return run
		}
		if run.Status == "failed" && want != "failed" {
			t.Fatalf("koşu beklenmedik şekilde başarısız: %s", run.Error)
		}
		time.Sleep(50 * time.Millisecond)
	}
	run, _ := st.Q.GetRun(context.Background(), runID)
	t.Fatalf("koşu %s durumuna gelmedi (şu an: %s)", want, run.Status)
	return db.Run{}
}

func TestChatRunCompletes(t *testing.T) {
	st, o := setup(t, map[string]engine.Executor{
		"fake": enginetest.PlainResponder("Merhaba, hazırım."),
	}, time.Second)
	bot := mkBot(t, st, "fake")

	run, err := o.EnqueueChat(context.Background(), bot, "selam", "")
	if err != nil {
		t.Fatalf("EnqueueChat: %v", err)
	}
	done := waitStatus(t, st, run.ID, "completed")

	if done.FinalText != "Merhaba, hazırım." || done.CostUsd <= 0 {
		t.Errorf("sonuç alanları eksik: %+v", done)
	}
	// Bot imleci (motor+kana) anahtarına yazılmalı (ADIM 7+10).
	convC, cerr := st.BotConversation(context.Background(), bot)
	if cerr != nil {
		t.Fatalf("sohbet: %v", cerr)
	}
	c, err := st.Q.GetBotCursor(context.Background(), db.GetBotCursorParams{
		BotID: bot.ID, Engine: "fake", ConversationID: convC.ID,
	})
	if err != nil || c.Cursor != "fake-imlec" {
		t.Errorf("bot imleci yazılmamış: %q (%v)", c.Cursor, err)
	}
	// Sohbette kullanıcı + bot mesajı olmalı.
	conv, _ := st.BotConversation(context.Background(), bot)
	msgs, _ := st.Q.ListMessages(context.Background(), db.ListMessagesParams{ConversationID: conv.ID, Limit: 10})
	if len(msgs) != 2 {
		t.Fatalf("2 mesaj bekleniyordu, geldi: %d", len(msgs))
	}
	// Olaylar kalıcılaşmalı (session.started + assistant.text + turn.completed).
	evs, _ := st.Q.ListRunEventsAfter(context.Background(), db.ListRunEventsAfterParams{RunID: run.ID, ID: 0})
	kinds := map[string]bool{}
	for _, e := range evs {
		kinds[e.Kind] = true
	}
	for _, k := range []string{"session.started", "assistant.text", "turn.completed"} {
		if !kinds[k] {
			t.Errorf("olay eksik: %s (eldekiler: %v)", k, kinds)
		}
	}
}

func TestParkThenResumeApproved(t *testing.T) {
	st, o := setup(t, map[string]engine.Executor{
		"fake": enginetest.AskingWriter("Dosya yazıldı.", "Onay bekliyorum, yazamadım."),
	}, 300*time.Millisecond) // pencere hızla dolsun → park
	bot := mkBot(t, st, "fake")

	run, err := o.EnqueueChat(context.Background(), bot, "not.txt yaz", "")
	if err != nil {
		t.Fatalf("EnqueueChat: %v", err)
	}

	// 1. aşama: pencere doldu → park.
	parked := waitStatus(t, st, run.ID, "approval_pending")
	if !strings.Contains(string(parked.PendingAsk), "Write") {
		t.Fatalf("pending_ask park edilen aracı taşımalı: %s", parked.PendingAsk)
	}
	if parked.FinalText != "Onay bekliyorum, yazamadım." {
		t.Errorf("park final metni bozuk: %q", parked.FinalText)
	}

	// 2. aşama: onay → resume → aynı koşu tamamlanır (K15 kanıtı, kuyruklu hali).
	if err := o.ResolveApproval(context.Background(), run.ID, "allow", "", false); err != nil {
		t.Fatalf("ResolveApproval: %v", err)
	}
	done := waitStatus(t, st, run.ID, "completed")
	if done.FinalText != "Dosya yazıldı." {
		t.Errorf("resume sonrası final metin: %q", done.FinalText)
	}
	if done.Attempt < 2 {
		t.Errorf("resume ikinci claim olmalıydı, attempt=%d", done.Attempt)
	}
}

func TestShortPathApprovalWhileRunning(t *testing.T) {
	st, o := setup(t, map[string]engine.Executor{
		"fake": enginetest.AskingWriter("Kısa yoldan yazıldı.", "olmadı"),
	}, 10*time.Second) // pencere uzun: cevap süreç içinde yetişecek
	bot := mkBot(t, st, "fake")

	run, err := o.EnqueueChat(context.Background(), bot, "not.txt yaz", "")
	if err != nil {
		t.Fatalf("EnqueueChat: %v", err)
	}

	// Koşu pencere içinde beklerken onay ver (süreç içi kısa yol).
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, waiting := o.PendingAsk(run.ID); waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("bekleyen istek görünmedi")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := o.ResolveApproval(context.Background(), run.ID, "allow", "", false); err != nil {
		t.Fatalf("ResolveApproval (kısa yol): %v", err)
	}

	done := waitStatus(t, st, run.ID, "completed")
	if done.FinalText != "Kısa yoldan yazıldı." {
		t.Errorf("kısa yol final metni: %q", done.FinalText)
	}
	if done.Attempt != 1 {
		t.Errorf("kısa yolda park olmamalı — attempt=%d", done.Attempt)
	}
}

func TestUnknownEngineFailsRunKeepsBot(t *testing.T) {
	st, o := setup(t, map[string]engine.Executor{}, time.Second)
	bot := mkBot(t, st, "hic-bilinmeyen-motor")

	run, err := o.EnqueueChat(context.Background(), bot, "merhaba", "")
	if err != nil {
		t.Fatalf("EnqueueChat: %v", err)
	}
	failed := waitStatus(t, st, run.ID, "failed")
	if !strings.Contains(failed.Error, "tanınmıyor") {
		t.Errorf("gölge kural mesajı bekleniyordu: %q", failed.Error)
	}
	// Bot bozulmadı (Ek B.2-4).
	if _, err := st.Q.GetBot(context.Background(), bot.ID); err != nil {
		t.Errorf("bot korunmalıydı: %v", err)
	}
}

// TestBasarisizKosuSohbeteCipYazar — regresyon: koşu düşünce ekranda hiçbir
// şey olmuyordu. Kullanıcı mesajını yazıyor, cevap gelmiyor, sebep yalnız
// sunucu logunda kalıyordu (Telegram kartı sebebi gösteriyordu, Mac
// sessizdi). Sebebi görmek cevapsız kalmaktan iyidir.
func TestBasarisizKosuSohbeteCipYazar(t *testing.T) {
	st, o := setup(t, map[string]engine.Executor{}, time.Second)
	bot := mkBot(t, st, "hic-bilinmeyen-motor")
	ctx := context.Background()

	run, err := o.EnqueueChat(ctx, bot, "merhaba", "")
	if err != nil {
		t.Fatalf("EnqueueChat: %v", err)
	}
	failed := waitStatus(t, st, run.ID, "failed")

	conv, err := st.BotConversation(ctx, bot)
	if err != nil {
		t.Fatalf("sohbet: %v", err)
	}
	msgs, err := st.Q.ListMessages(ctx, db.ListMessagesParams{ConversationID: conv.ID, Limit: 10})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	var cip *db.Message
	for i, m := range msgs {
		if m.Role == "event" && strings.HasPrefix(m.Text, orchestrator.HataCipiOneki) {
			cip = &msgs[i]
		}
	}
	if cip == nil {
		t.Fatalf("hata çipi yazılmadı: %+v", msgs)
	}
	// Sebep çipin İÇİNDE olmalı: "başarısız" demek yetmez, kullanıcı ne
	// yapacağını sebepten anlıyor.
	if !strings.Contains(cip.Text, failed.Error) {
		t.Errorf("çip sebebi taşımıyor: %q (koşu hatası: %q)", cip.Text, failed.Error)
	}
	if !cip.RunID.Valid || cip.RunID.UUID != run.ID {
		t.Errorf("çip koşuya bağlanmalı: %+v", cip.RunID)
	}
}

// TestMetinsizTurSohbeteBilgiCipiYazar — regresyon: motor metin üretmeden
// (yalnız araç çağırıp) bittiğinde koşu "completed" oluyor, sohbete hiçbir
// şey yazılmıyordu: kullanıcı boş ekrana bakıyor, logda da hata yok.
func TestMetinsizTurSohbeteBilgiCipiYazar(t *testing.T) {
	sessiz := enginetest.Func(func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		res := &engine.TurnResult{Ok: true, StopReason: "success", NumTurns: 1, ResumeCursor: "c"}
		emit(engine.Event{Kind: engine.KindTurnCompleted, Result: res})
		return res, nil
	})
	st, o := setup(t, map[string]engine.Executor{"fake": sessiz}, time.Second)
	bot := mkBot(t, st, "fake")
	ctx := context.Background()

	run, err := o.EnqueueChat(ctx, bot, "merhaba", "")
	if err != nil {
		t.Fatalf("EnqueueChat: %v", err)
	}
	waitStatus(t, st, run.ID, "completed")

	conv, err := st.BotConversation(ctx, bot)
	if err != nil {
		t.Fatalf("sohbet: %v", err)
	}
	msgs, err := st.Q.ListMessages(ctx, db.ListMessagesParams{ConversationID: conv.ID, Limit: 10})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	var cip bool
	for _, m := range msgs {
		if m.Role == "event" && m.Text == orchestrator.SessizTurCipi {
			cip = true
		}
		if m.Role == "bot" {
			t.Errorf("metinsiz turda bot mesajı yazılmamalı: %q", m.Text)
		}
	}
	if !cip {
		t.Fatalf("bilgi çipi yazılmadı: %+v", msgs)
	}
}
