//go:build integration

// Faz 3 entegrasyon testleri: onay kapısı (kural / taint / hep-izin-ver / TTL)
// gerçek Postgres + sahte motorla uçtan uca.
package orchestrator_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/esmworks/OpenAssist/internal/engine"
	"github.com/esmworks/OpenAssist/internal/engine/enginetest"
	"github.com/esmworks/OpenAssist/internal/orchestrator"
	"github.com/esmworks/OpenAssist/internal/policy"
	"github.com/esmworks/OpenAssist/internal/store"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

// mkRule — kural yazar ve test bitince siler: testler aynı veritabanını
// paylaşır, kalan kural (özellikle bot_id NULL olan genel kural) sonraki
// testleri zehirler.
func mkRule(t *testing.T, st *store.Store, p db.UpsertApprovalRuleParams) {
	t.Helper()
	rule, err := st.Q.UpsertApprovalRule(context.Background(), p)
	if err != nil {
		t.Fatalf("kural yazılamadı: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Q.DeleteApprovalRule(context.Background(), rule.ID) })
}

func TestRuleAutoAllows(t *testing.T) {
	st, o := setup(t, map[string]engine.Executor{
		"fake": enginetest.AskingWriter("Kuralla yazıldı.", "olmadı"),
	}, 200*time.Millisecond) // pencere kısa: kural işlemezse park olurdu
	bot := mkBot(t, st, "fake")

	mkRule(t, st, db.UpsertApprovalRuleParams{
		BotID: uuid.NullUUID{UUID: bot.ID, Valid: true}, Tool: "Write", Action: "allow", Note: "test",
	})

	run, err := o.EnqueueChat(context.Background(), bot, "not.txt yaz", "")
	if err != nil {
		t.Fatal(err)
	}
	done := waitStatus(t, st, run.ID, "completed")
	if done.FinalText != "Kuralla yazıldı." {
		t.Errorf("kural oto-izin vermedi: %q", done.FinalText)
	}
	// Eylem günlüğüne düşmeli (kota sayacı + Aktivite).
	n, _ := st.Q.CountActionsSince(context.Background(), db.CountActionsSinceParams{
		BotID: bot.ID, CreatedAt: time.Now().Add(-time.Minute),
	})
	if n != 1 {
		t.Errorf("eylem günlüğünde 1 kayıt bekleniyordu, geldi: %d", n)
	}
}

func TestDenyRuleAutoDenies(t *testing.T) {
	st, o := setup(t, map[string]engine.Executor{
		"fake": enginetest.AskingWriter("yazılMAmalıydı", "Reddedildi, yazamadım."),
	}, 10*time.Second) // pencere uzun: kural işlemezse test zaman aşımına düşerdi
	bot := mkBot(t, st, "fake")

	mkRule(t, st, db.UpsertApprovalRuleParams{
		Tool: "Write", Action: "deny", Note: "genel yasak", // bot_id NULL = tüm botlar
	})

	run, err := o.EnqueueChat(context.Background(), bot, "not.txt yaz", "")
	if err != nil {
		t.Fatal(err)
	}
	done := waitStatus(t, st, run.ID, "completed")
	if done.FinalText != "Reddedildi, yazamadım." {
		t.Errorf("deny kuralı işlemedi: %q", done.FinalText)
	}
}

// taintingWriter — önce dış içerik okur (WebFetch → tainted), sonra Write
// izni ister: allow kuralı olsa bile sorulmalı (§5.1).
func taintingWriter() enginetest.Func {
	return func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		emit(engine.Event{Kind: engine.KindSessionStarted, SessionID: "fake", Model: "fake"})
		emit(engine.Event{Kind: engine.KindToolStarted, Tool: &engine.ToolInfo{ID: "w1", Name: "WebFetch"}})
		emit(engine.Event{Kind: engine.KindToolCompleted, Tool: &engine.ToolInfo{ID: "w1"}})

		ans := engine.Answer{Behavior: "deny"}
		if in.OnAsk != nil {
			ans = in.OnAsk(ctx, engine.Ask{Kind: "permission", Tool: "Write"})
		}
		res := &engine.TurnResult{Ok: true, StopReason: "success", CostUSD: 0.001, NumTurns: 1, ResumeCursor: "fake-imlec"}
		if ans.Behavior == "allow" {
			res.FinalText = "TAINTED KOŞUDA KURALLA YAZILDI — GÜVENLİK DELİĞİ"
		} else {
			res.FinalText = "Onay bekliyorum."
		}
		emit(engine.Event{Kind: engine.KindTurnCompleted, Result: res})
		return res, nil
	}
}

func TestTaintVoidsAllowRule(t *testing.T) {
	st, o := setup(t, map[string]engine.Executor{"fake": taintingWriter()}, 200*time.Millisecond)
	bot := mkBot(t, st, "fake")

	// Allow kuralı VAR — ama koşu tainted olacağı için işlememeli.
	mkRule(t, st, db.UpsertApprovalRuleParams{
		BotID: uuid.NullUUID{UUID: bot.ID, Valid: true}, Tool: "Write", Action: "allow",
	})

	run, err := o.EnqueueChat(context.Background(), bot, "oku ve yaz", "")
	if err != nil {
		t.Fatal(err)
	}
	parked := waitStatus(t, st, run.ID, "approval_pending")
	if !parked.Tainted {
		t.Error("koşu tainted işaretlenmeliydi (WebFetch)")
	}
	if parked.FinalText != "Onay bekliyorum." {
		t.Errorf("tainted koşuda kural işledi: %q", parked.FinalText)
	}
}

func TestAlwaysAllowCreatesRuleAndSecondRunFlows(t *testing.T) {
	st, o := setup(t, map[string]engine.Executor{
		"fake": enginetest.AskingWriter("Yazıldı.", "Onay bekliyorum."),
	}, 300*time.Millisecond)
	bot := mkBot(t, st, "fake")

	// 1. koşu: park → "hep izin ver" ile onay.
	run1, err := o.EnqueueChat(context.Background(), bot, "not.txt yaz", "")
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, st, run1.ID, "approval_pending")
	if err := o.ResolveApproval(context.Background(), run1.ID, "allow", "", true); err != nil {
		t.Fatalf("ResolveApproval(always): %v", err)
	}
	waitStatus(t, st, run1.ID, "completed")

	// Kural oluşmuş olmalı.
	rule, err := st.Q.GetRuleForTool(context.Background(), db.GetRuleForToolParams{
		Tool: "Write", BotID: uuid.NullUUID{UUID: bot.ID, Valid: true},
	})
	if err != nil || rule.Action != "allow" {
		t.Fatalf("hep-izin-ver kurala dönüşmedi: %v %+v", err, rule)
	}
	t.Cleanup(func() { _, _ = st.Q.DeleteApprovalRule(context.Background(), rule.ID) })

	// 2. koşu: kural sayesinde sorulmadan akmalı (park yok).
	run2, err := o.EnqueueChat(context.Background(), bot, "bir daha yaz", "")
	if err != nil {
		t.Fatal(err)
	}
	done := waitStatus(t, st, run2.ID, "completed")
	if done.FinalText != "Yazıldı." || done.Attempt != 1 {
		t.Errorf("ikinci koşu kuralla akmalıydı: %q attempt=%d", done.FinalText, done.Attempt)
	}
}

// TestGatewayClassOverrideTaints — gateway'in açık sınıf bildirimi onay
// kapısına akar: sezgisel "mcp__gw__gmail_search"ü external sayardı (taint
// yok), bildirim read+DataSource yapar → koşu boyanır (Faz 4 kablolaması).
func TestGatewayClassOverrideTaints(t *testing.T) {
	reader := enginetest.Func(func(_ context.Context, _ engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		emit(engine.Event{Kind: engine.KindSessionStarted, SessionID: "fake", Model: "fake"})
		emit(engine.Event{Kind: engine.KindToolStarted, Tool: &engine.ToolInfo{ID: "g1", Name: "mcp__gw__gmail_search"}})
		emit(engine.Event{Kind: engine.KindToolCompleted, Tool: &engine.ToolInfo{ID: "g1"}})
		res := &engine.TurnResult{Ok: true, StopReason: "success", CostUSD: 0.001, NumTurns: 1, FinalText: "3 okunmamış var.", ResumeCursor: "fake-imlec"}
		emit(engine.Event{Kind: engine.KindTurnCompleted, Result: res})
		return res, nil
	})
	st, o := setup(t, map[string]engine.Executor{"fake": reader}, time.Second,
		func(c *orchestrator.Config) {
			c.ToolClasses = map[string]policy.Classification{
				"mcp__gw__gmail_search": {Class: policy.ClassRead, DataSource: true, Ruleable: true},
			}
		})
	bot := mkBot(t, st, "fake")

	run, err := o.EnqueueChat(context.Background(), bot, "gelen kutusuna bak", "")
	if err != nil {
		t.Fatal(err)
	}
	done := waitStatus(t, st, run.ID, "completed")
	if !done.Tainted {
		t.Error("gateway bildirimi DataSource dedi — koşu tainted olmalıydı")
	}
}

func TestExpireStaleApprovals(t *testing.T) {
	st, o := setup(t, map[string]engine.Executor{
		"fake": enginetest.AskingWriter("x", "Onay bekliyorum."),
	}, 200*time.Millisecond)
	bot := mkBot(t, st, "fake")

	run, err := o.EnqueueChat(context.Background(), bot, "yaz", "")
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, st, run.ID, "approval_pending")

	// Parkı geçmişe çek → TTL dolmuş gibi.
	if _, err := st.Pool().Exec(context.Background(),
		"UPDATE runs SET finished_at = now() - interval '100 hours' WHERE id = $1", run.ID); err != nil {
		t.Fatal(err)
	}
	n, err := st.Q.ExpireStaleApprovals(context.Background(), 72*3600)
	if err != nil || n != 1 {
		t.Fatalf("1 koşu expire olmalıydı: n=%d err=%v", n, err)
	}
	got, _ := st.Q.GetRun(context.Background(), run.ID)
	if got.Status != "expired" {
		t.Errorf("durum expired olmalı: %s", got.Status)
	}
	// Süresi dolmuş koşu artık onaylanamaz.
	if err := o.ResolveApproval(context.Background(), run.ID, "allow", "", false); err == nil {
		t.Error("expired koşuya onay kabul edilmemeli")
	}

	// pending_ask JSON'u API sözleşmesine uygun (küçük harf alanlar).
	var pa map[string]any
	if err := json.Unmarshal(got.PendingAsk, &pa); err != nil || pa["tool"] != "Write" {
		t.Errorf("pending_ask sözleşmesi bozuk: %s", got.PendingAsk)
	}
}

// Park edilmiş istek "hep izin ver" KARARINI taşımalı.
//
// Neden: kart bu bilgi olmadan düğmeyi gösteremez, gösteremeyince de aynı
// aracı arka arkaya çağıran botta (on mükerrer hafıza kaydını silmek gibi)
// kullanıcı her adım için ayrı karta basmak zorunda kalır — üstelik her kart
// ayrı bir park/resume turudur, çünkü resume izni TEK KULLANIMLIKTIR (K15).
// Sahada ölçülen yorgunluk buydu. Kararı istemci kendi hesaplamıyor: kural
// mantığı iki yerde yaşarsa sessizce ayrışır.
func TestParkEdilmisIstekHepIzinKarariniTasir(t *testing.T) {
	st, o := setup(t, map[string]engine.Executor{
		"fake": enginetest.AskingWriter("yazdım", "olmadı"),
	}, 100*time.Millisecond)
	bot := mkBot(t, st, "fake")

	run, err := o.EnqueueChat(context.Background(), bot, "not.txt yaz", "")
	if err != nil {
		t.Fatal(err)
	}
	parked := waitStatus(t, st, run.ID, "approval_pending")

	var pa map[string]any
	if err := json.Unmarshal(parked.PendingAsk, &pa); err != nil {
		t.Fatalf("pending_ask okunamadı: %v", err)
	}
	if pa["allow_always"] != true {
		t.Fatalf("kart 'hep izin ver' sunamaz — kullanıcı aynı araç için tek tek "+
			"onaylamak zorunda kalır: %s", parked.PendingAsk)
	}
}

// Tainted koşuda "hep izin ver" SUNULMAMALI: §5.1 otomatik izinleri
// geçersiz kılıyor, düğme gösterilseydi kullanıcıya tutulmayacak bir söz
// verilmiş olurdu (kural yazılır ama taint onu her koşuda ezer).
func TestTaintliKosudaHepIzinSunulmaz(t *testing.T) {
	st, o := setup(t, map[string]engine.Executor{"fake": taintingWriter()}, 100*time.Millisecond)
	bot := mkBot(t, st, "fake")

	run, err := o.EnqueueChat(context.Background(), bot, "oku ve yaz", "")
	if err != nil {
		t.Fatal(err)
	}
	parked := waitStatus(t, st, run.ID, "approval_pending")
	if !parked.Tainted {
		t.Fatal("koşu tainted işaretlenmeliydi")
	}
	var pa map[string]any
	if err := json.Unmarshal(parked.PendingAsk, &pa); err != nil {
		t.Fatalf("pending_ask okunamadı: %v", err)
	}
	if pa["allow_always"] == true {
		t.Fatalf("tainted koşuda 'hep izin ver' sunuldu — tutulamayacak söz: %s", parked.PendingAsk)
	}
}

// Reaper'ın öldürdüğü koşu SOHBETTE görünmeli.
//
// Sahada ölçüldü (üretim logu, 2026-08-16 20:33): deploy sırasında uçuşta
// olan bir koşu sahipsiz kaldı, deneme hakkı da bitmişti ve reaper onu
// 'failed' yaptı — ama bunu yalnız sunucu logu bildi. Kullanıcı açısından
// bot mesajı aldı ve hiç cevap vermedi. execute'un kendi hata yolu (failRun)
// sohbete çip yazıyordu; reaper yolu atlanmıştı.
func TestSahipsizKosuSohbeteYazilir(t *testing.T) {
	st, o := setup(t, map[string]engine.Executor{
		"fake": enginetest.AskingWriter("yazdım", "olmadı"),
	}, 100*time.Millisecond)
	bot := mkBot(t, st, "fake")

	ctx := context.Background()
	run, err := o.EnqueueChat(ctx, bot, "bir iş yap", "")
	if err != nil {
		t.Fatal(err)
	}
	// Koşuyu "sahipsiz + hakkı bitmiş" hâle getir: süreç ölmüş, kalp atışı
	// kesilmiş, denemeler tükenmiş.
	waitStatus(t, st, run.ID, "approval_pending")
	if _, err := st.Pool().Exec(ctx, `
		UPDATE runs SET status='running', attempt=99, heartbeat_at=now() - interval '1 hour'
		WHERE id=$1`, run.ID); err != nil {
		t.Fatalf("koşu sahipsizleştirilemedi: %v", err)
	}

	o.Reap(ctx)

	got, err := st.Q.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "failed" {
		t.Fatalf("reaper koşuyu düşürmedi: %s", got.Status)
	}

	msgs, err := st.Q.ListMessages(ctx, db.ListMessagesParams{
		ConversationID: got.ConversationID.UUID, Limit: 50,
	})
	if err != nil {
		t.Fatalf("mesajlar okunamadı: %v", err)
	}
	var cip string
	for _, m := range msgs {
		if strings.HasPrefix(m.Text, orchestrator.HataCipiOneki) {
			cip = m.Text
		}
	}
	if cip == "" {
		t.Fatal("sohbete hata çipi düşmedi — kullanıcı botun neden sustuğunu göremez")
	}
	if !strings.Contains(cip, "sahipsiz") {
		t.Fatalf("çip sebebi söylemiyor: %q", cip)
	}
}
