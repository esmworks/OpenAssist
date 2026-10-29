//go:build integration

// Rutin motoru entegrasyon testleri: gerçek Postgres + sahte motor.
// Faz 7'nin üç kanıtı: (1) vadesi gelen rutin koşuya döner ve vade ilerler,
// (2) hafıza + skill sistem promptuna girer, (3) Ek A gece ritmi — sessiz
// saatte dış eylem parka düşer (held), sabah onayla resume olur.
package routines_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/esmworks/OpenAssist/internal/bus"
	"github.com/esmworks/OpenAssist/internal/engine"
	"github.com/esmworks/OpenAssist/internal/engine/enginetest"
	"github.com/esmworks/OpenAssist/internal/orchestrator"
	"github.com/esmworks/OpenAssist/internal/policy"
	"github.com/esmworks/OpenAssist/internal/routines"
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

func mkBot(t *testing.T, st *store.Store) db.Bot {
	t.Helper()
	bot, err := st.Q.CreateBot(context.Background(), db.CreateBotParams{
		Kind: "bot", Name: "rtn-" + t.Name(), Engine: "fake", AllowedTools: []string{},
	})
	if err != nil {
		t.Fatalf("bot: %v", err)
	}
	t.Cleanup(func() { _ = st.Q.DeleteBot(context.Background(), bot.ID) })
	return bot
}

func mkRoutine(t *testing.T, st *store.Store, botID uuid.UUID, due time.Time) db.Routine {
	t.Helper()
	r, err := st.Q.CreateRoutine(context.Background(), db.CreateRoutineParams{
		BotID: botID, Name: "rtn-" + t.Name(), TriggerKind: "cron",
		CronExpr: "*/15 * * * *", Timezone: "Europe/Istanbul",
		InputSource: "test kaynağı", ExpectedOutput: "test çıktısı",
		ApprovalNote: "gönderim onaylı", StalePolicy: "fail", NextRunAt: &due,
	})
	if err != nil {
		t.Fatalf("rutin: %v", err)
	}
	// Bot silinince rutin CASCADE ile gider; yine de açık temizlik.
	t.Cleanup(func() { _, _ = st.Q.DeleteRoutine(context.Background(), r.ID) })
	return r
}

func beginTxOf(st *store.Store) routines.BeginTx {
	return func(ctx context.Context) (db.DBTX, func(context.Context) error, func(context.Context) error, error) {
		tx, err := st.Pool().Begin(ctx)
		if err != nil {
			return nil, nil, nil, err
		}
		return tx, tx.Commit, tx.Rollback, nil
	}
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

func TestRoutineTickFiresDueRoutine(t *testing.T) {
	engines := map[string]engine.Executor{"fake": enginetest.PlainResponder("rutin bitti")}
	st, orch := setup(t, engines, time.Second)
	bot := mkBot(t, st)
	r := mkRoutine(t, st, bot.ID, time.Now().Add(-time.Minute))

	eng := routines.New(beginTxOf(st), orch, time.Hour)
	eng.Tick(context.Background())

	// routine_runs kaydından koşuyu bul.
	rows, err := st.Q.ListRoutineRuns(context.Background(), r.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("1 çalışma kaydı beklenirdi: %v (%d)", err, len(rows))
	}
	run := waitStatus(t, st, rows[0].ID, "completed")
	if run.Origin != "routine" {
		t.Errorf("origin=routine olmalı: %s", run.Origin)
	}
	if !strings.Contains(run.Prompt, "Zamanlanmış rutin") || !strings.Contains(run.Prompt, "test kaynağı") {
		t.Errorf("prompt rutin sözleşmesini taşımalı:\n%s", run.Prompt)
	}

	after, err := st.Q.GetRoutine(context.Background(), r.ID)
	if err != nil {
		t.Fatalf("rutin okunamadı: %v", err)
	}
	if after.NextRunAt == nil || !after.NextRunAt.After(time.Now()) {
		t.Errorf("next_run_at ileri tarihe kurulmalı: %v", after.NextRunAt)
	}
	if after.LastRunAt == nil {
		t.Error("last_run_at yazılmalı")
	}

	// Aynı vade ikinci kez tetiklenmez.
	eng.Tick(context.Background())
	rows2, _ := st.Q.ListRoutineRuns(context.Background(), r.ID)
	if len(rows2) != 1 {
		t.Errorf("ikinci tick yeni koşu üretmemeli: %d", len(rows2))
	}
}

func TestMemoryAndSkillEnterSystemPrompt(t *testing.T) {
	var captured atomic.Value // string
	fake := enginetest.Func(func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		captured.Store(in.SystemPrompt)
		return enginetest.PlainResponder("ok")(ctx, in, emit)
	})
	st, orch := setup(t, map[string]engine.Executor{"fake": fake}, time.Second)
	bot := mkBot(t, st)
	ctx := context.Background()

	um, err := st.Q.UpsertUserMemory(ctx, db.UpsertUserMemoryParams{
		Kind: "preference", Content: "Kullanıcı Türkçe iletişim ister", ContentHash: "h-" + t.Name() + "-u", Confidence: 1,
	})
	if err != nil {
		t.Fatalf("user memory: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Q.DeleteMemory(ctx, um.ID) })
	bm, err := st.Q.UpsertMemory(ctx, db.UpsertMemoryParams{
		Scope: "bot", BotID: uuid.NullUUID{UUID: bot.ID, Valid: true},
		Kind: "fact", Content: "Taslakları resmi dille yaz", ContentHash: "h-" + t.Name() + "-b", Confidence: 1,
	})
	if err != nil {
		t.Fatalf("bot memory: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Q.DeleteMemory(ctx, bm.ID) })
	// Sabit talimat (kind=instruction) AYRI bir bölüme girer: hafıza damıtılmış
	// bilgidir, bu ise uyulacak kural. Aynı blokta olsalardı konsolidasyon
	// kuralı bir olguyla birleştirebilir ya da bütçe dolunca kırpabilirdi.
	tm, err := st.Q.UpsertMemory(ctx, db.UpsertMemoryParams{
		Scope: "bot", BotID: uuid.NullUUID{UUID: bot.ID, Valid: true},
		Kind: "instruction", Content: "Onaysız e-posta gönderme", ContentHash: "h-" + t.Name() + "-t", Confidence: 1,
	})
	if err != nil {
		t.Fatalf("sabit talimat: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Q.DeleteMemory(ctx, tm.ID) })
	sk, err := st.Q.UpsertSkill(ctx, db.UpsertSkillParams{
		Name: "skill-" + t.Name(), WhenToUse: "e-posta taslağı gerektiğinde", Instructions: "1. Tara 2. Taslakla 3. Onaya bırak",
	})
	if err != nil {
		t.Fatalf("skill: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Q.DeleteSkill(ctx, sk.ID) })
	if err := st.Q.EnableSkillForBot(ctx, db.EnableSkillForBotParams{BotID: bot.ID, SkillID: sk.ID}); err != nil {
		t.Fatalf("skill enable: %v", err)
	}

	run, err := orch.EnqueueChat(ctx, bot, "merhaba", "api")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	waitStatus(t, st, run.ID, "completed")

	got, _ := captured.Load().(string)
	for _, want := range []string{"Kullanıcı Türkçe iletişim ister", "Taslakları resmi dille yaz", sk.Name, "Onaya bırak"} {
		if !strings.Contains(got, want) {
			t.Errorf("sistem promptu %q içermeli:\n%s", want, got)
		}
	}
	if strings.Index(got, "Kullanıcı Türkçe") > strings.Index(got, "Taslakları resmi") {
		t.Error("user scope bot scope'tan ÖNCE gelmeli (PRD §8)")
	}
	// Sabit talimat kendi başlığı altında ve hafızadan önce: kural, damıtılmış
	// bilginin arasında kaybolmasın.
	if !strings.Contains(got, "## Sabit talimatlar") || !strings.Contains(got, "Onaysız e-posta gönderme") {
		t.Errorf("sabit talimat bloğu prompta girmeli:\n%s", got)
	}
	if strings.Index(got, "Onaysız e-posta") > strings.Index(got, "Kullanıcı Türkçe") {
		t.Error("sabit talimatlar hafıza bloğundan ÖNCE gelmeli")
	}
}

// TestNightRoutineHeldThenMorningApproval — Ek A ritmi uçtan uca: sessiz
// saatte rutin koşar, dış eylem (taslak GÖNDERİMİ) parka düşer
// (approval_pending = held), sabah onayıyla resume olup tamamlanır.
func TestNightRoutineHeldThenMorningApproval(t *testing.T) {
	sendTool := "mcp__gw__gmail_send_draft"
	fake := enginetest.Func(func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		emit(engine.Event{Kind: engine.KindSessionStarted, SessionID: "gece", Model: "fake"})
		if in.ResumeCursor != "" {
			// Sabah: onay geldi, gönderim tamamlanır.
			res := &engine.TurnResult{Ok: true, StopReason: "success", FinalText: "taslak gönderildi", NumTurns: 1, ResumeCursor: "sabah"}
			emit(engine.Event{Kind: engine.KindTurnCompleted, Result: res})
			return res, nil
		}
		emit(engine.Event{Kind: engine.KindRequestOpened, Request: &engine.RequestInfo{Kind: "permission", Tool: sendTool}})
		ans := in.OnAsk(ctx, engine.Ask{Kind: "permission", Tool: sendTool, Input: json.RawMessage(`{"draft_id":"d1"}`)})
		emit(engine.Event{Kind: engine.KindRequestResolved, Request: &engine.RequestInfo{Kind: "permission", Tool: sendTool, Behavior: ans.Behavior, Source: ans.Source}})
		res := &engine.TurnResult{Ok: true, StopReason: "success", FinalText: "taslak hazır, gönderim onay bekliyor", NumTurns: 1, ResumeCursor: "gece-imlec"}
		emit(engine.Event{Kind: engine.KindTurnCompleted, Result: res})
		return res, nil
	})

	st, orch := setup(t, map[string]engine.Executor{"fake": fake}, 200*time.Millisecond,
		func(c *orchestrator.Config) {
			c.ToolClasses = map[string]policy.Classification{
				sendTool: {Class: policy.ClassExternal, Ruleable: true},
			}
		})

	// Sessiz saat: tüm günü kapsa (test her saatte koşabilir).
	ctx := context.Background()
	old, _ := st.Q.GetSystemInfo(ctx, "policy")
	pol := policy.Settings{QuietHoursStart: "00:00", QuietHoursEnd: "23:59", Timezone: "UTC", ApprovalTTLHours: 72}
	polJSON, _ := json.Marshal(pol)
	if err := st.Q.SetSystemInfo(ctx, db.SetSystemInfoParams{Key: "policy", Value: string(polJSON)}); err != nil {
		t.Fatalf("politika yazılamadı: %v", err)
	}
	t.Cleanup(func() {
		val := old.Value
		if val == "" {
			val = "{}"
		}
		_ = st.Q.SetSystemInfo(context.Background(), db.SetSystemInfoParams{Key: "policy", Value: val})
	})

	bot := mkBot(t, st)
	r := mkRoutine(t, st, bot.ID, time.Now().Add(-time.Minute))

	// Gece: rutin tetiklenir, gönderim sessiz saate takılıp parka düşer.
	routines.New(beginTxOf(st), orch, time.Hour).Tick(ctx)
	rows, err := st.Q.ListRoutineRuns(ctx, r.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("çalışma kaydı bekleniyordu: %v", err)
	}
	runID := rows[0].ID
	parked := waitStatus(t, st, runID, "approval_pending")
	if !strings.Contains(string(parked.PendingAsk), sendTool) {
		t.Fatalf("pending_ask gönderim aracını taşımalı: %s", parked.PendingAsk)
	}

	// Sabah: Onay Merkezi'nden onay → resume → tamamlanır.
	if err := orch.ResolveApproval(ctx, runID, "allow", "", false); err != nil {
		t.Fatalf("onay: %v", err)
	}
	done := waitStatus(t, st, runID, "completed")
	if !strings.Contains(done.FinalText, "gönderildi") {
		t.Errorf("resume sonrası final metin gönderimi raporlamalı: %q", done.FinalText)
	}
}

// TestSummaryReplacesOldMemoryLines — özet belgesi varsa ESKİ ham satırlar
// sistem promptuna girmez (K20): ikisi birden girerse aynı olgu iki kez
// söylenir, bütçe boşa gider ve prompt öneki her koşuda değişir (KV önbelleği
// ısınmaz).
//
// Ama özetten SONRA yazılan kayıt girer — asıl kilit o. Girmeseydi kullanıcı
// bota yeni bir tercih söylediğinde bot bunu bir sonraki gece turuna kadar
// (≥20 saat) bilmezdi.
func TestSummaryReplacesOldMemoryLines(t *testing.T) {
	var captured atomic.Value // string
	fake := enginetest.Func(func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		captured.Store(in.SystemPrompt)
		return enginetest.PlainResponder("ok")(ctx, in, emit)
	})
	st, orch := setup(t, map[string]engine.Executor{"fake": fake}, time.Second)
	bot := mkBot(t, st)
	ctx := context.Background()

	hamOlgu := "Bot hafızası ham satır kanıtı " + t.Name()
	bm, err := st.SaveMemory(ctx, store.MemoryDraft{
		Scope: "bot", BotID: uuid.NullUUID{UUID: bot.ID, Valid: true},
		Kind: "fact", Content: hamOlgu,
	})
	if err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Q.DeleteMemory(ctx, bm.ID) })

	// Özet yokken ham satır promptta olmalı (geriye uyum yolu).
	run, err := orch.EnqueueChat(ctx, bot, "merhaba", "api")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	waitStatus(t, st, run.ID, "completed")
	if got, _ := captured.Load().(string); !strings.Contains(got, hamOlgu) {
		t.Fatalf("özet yokken ham satır promptta olmalı:\n%s", got)
	}

	// Özet gelince ham satır düşmeli.
	ozetMetni := "Kullanıcı hakkında damıtılmış özet " + t.Name()
	if _, err := st.Q.UpsertMemorySummary(ctx, db.UpsertMemorySummaryParams{
		BotID: bot.ID, Content: ozetMetni, SourceCount: 1,
	}); err != nil {
		t.Fatalf("UpsertMemorySummary: %v", err)
	}
	t.Cleanup(func() { _ = st.Q.DeleteMemorySummary(ctx, bot.ID) })

	run2, err := orch.EnqueueChat(ctx, bot, "tekrar", "api")
	if err != nil {
		t.Fatalf("enqueue 2: %v", err)
	}
	waitStatus(t, st, run2.ID, "completed")

	got, _ := captured.Load().(string)
	if !strings.Contains(got, ozetMetni) {
		t.Errorf("özet promptta olmalı:\n%s", got)
	}
	if strings.Contains(got, hamOlgu) {
		t.Errorf("özet varken ESKİ ham satır promptta OLMAMALI (çift anlatım):\n%s", got)
	}

	// Özetten SONRA yazılan kayıt promptta OLMALI. Bu olmadan kullanıcının
	// "bundan sonra bana Erhan Bey de" demesi ile botun bunu öğrenmesi
	// arasında bir sonraki gece turu kadar (≥20 saat) zaman geçerdi.
	tazeOlgu := "Özetten sonra öğrenilen tercih " + t.Name()
	tm, err := st.SaveMemory(ctx, store.MemoryDraft{
		Scope: "bot", BotID: uuid.NullUUID{UUID: bot.ID, Valid: true},
		Kind: "preference", Content: tazeOlgu,
	})
	if err != nil {
		t.Fatalf("SaveMemory (taze): %v", err)
	}
	t.Cleanup(func() { _, _ = st.Q.DeleteMemory(ctx, tm.ID) })

	run3, err := orch.EnqueueChat(ctx, bot, "üçüncü", "api")
	if err != nil {
		t.Fatalf("enqueue 3: %v", err)
	}
	waitStatus(t, st, run3.ID, "completed")

	got, _ = captured.Load().(string)
	if !strings.Contains(got, tazeOlgu) {
		t.Errorf("özetten sonra yazılan kayıt promptta OLMALI:\n%s", got)
	}
	if !strings.Contains(got, ozetMetni) {
		t.Errorf("taze kayıt özetin yerini almamalı:\n%s", got)
	}
	if strings.Contains(got, hamOlgu) {
		t.Errorf("özetten ÖNCEKİ satır hâlâ girmemeli:\n%s", got)
	}
}
