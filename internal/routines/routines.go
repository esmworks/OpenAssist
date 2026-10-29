// Package routines — rutin motoru (K11/K14): NE ZAMAN çalışır sorusunun
// sahibi. v1 yalnız cron tetikleyicisini işletir; şema interval+koşulu da
// taşır (K14). Zamanlayıcı tek kopya varsayımıyla çalışır (compose
// replicas:1, §4) — yine de ClaimDueRoutines FOR UPDATE SKIP LOCKED
// kullanır: iki kopya kazasında çift tetikleme olmaz.
package routines

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	db "github.com/esmworks/OpenAssist/internal/store/db"
)

// NextRun — cron ifadesi + timezone'dan bir sonraki çalışma anı (UTC döner).
// Timezone DB'de saklanır (§6 saat dilimi tuzağı): "hafta içi 18:00"
// Europe/Istanbul'da 18:00'dir, sunucu UTC olsa da.
func NextRun(cronExpr, timezone string, after time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("geçersiz timezone %q: %w", timezone, err)
	}
	sched, err := cron.ParseStandard(cronExpr)
	if err != nil {
		return time.Time{}, fmt.Errorf("geçersiz cron ifadesi %q: %w", cronExpr, err)
	}
	next := sched.Next(after.In(loc))
	if next.IsZero() {
		return time.Time{}, fmt.Errorf("cron ifadesi bir sonraki çalışmayı üretmedi: %q", cronExpr)
	}
	return next.UTC(), nil
}

// Validate — rutin tanımı kaydedilmeden önce cron+tz doğrulanır (K11:
// zorunlu alanlar; bozuk tarife sessizce hiç çalışmayan rutin demek).
func Validate(triggerKind, cronExpr, timezone string) error {
	if triggerKind != "cron" {
		return fmt.Errorf("v1 yalnız cron tetikleyicisini işletir (şema %q tipini taşır, motor sonra gelir — K14)", triggerKind)
	}
	_, err := NextRun(cronExpr, timezone, time.Now())
	return err
}

// BuildPrompt — rutin koşusunun promptu. K11 zorunlu alanları ve K12
// "hazırla, gönderme" varsayılanı prompta işlenir; bayat veri politikası
// açık talimattır (sessiz bozulma yok).
func BuildPrompt(r db.Routine) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Zamanlanmış rutin çalışması: %s\n", r.Name)
	if r.InputSource != "" {
		fmt.Fprintf(&b, "Girdi kaynağı: %s\n", r.InputSource)
	}
	if r.ExpectedOutput != "" {
		fmt.Fprintf(&b, "Beklenen çıktı: %s\n", r.ExpectedOutput)
	}
	if r.ApprovalNote != "" {
		fmt.Fprintf(&b, "Onay sınırı: %s\n", r.ApprovalNote)
	}
	switch r.StalePolicy {
	case "warn_stale":
		b.WriteString("Kaynak veriye ulaşamazsan: son bilinen veriyle devam et ama sonucun başında bunu AÇIKÇA belirt (hangi veri bayat, ne kadar eski).\n")
	default: // fail
		b.WriteString("Kaynak veriye ulaşamazsan: işi DURDUR ve hatayı bildir — eski veriyle sessizce çalışma.\n")
	}
	b.WriteString("Varsayılan mod: hazırla, gönderme (K12). Dışa etkili her adım onaya düşer. " +
		"Sonucu şu bölümlerle raporla: Olgular · Çıkarımlar/Varsayımlar · Tamamlanan · Onay bekleyen · Cevaplanamayan.")
	return b.String()
}

// Enqueuer — koşu kuyruğuna iş bırakabilen (orkestratör; testte sahte).
type Enqueuer interface {
	EnqueueChat(ctx context.Context, bot db.Bot, text, origin string) (db.Run, error)
}

// BeginTx — iş başına transaction açar; dönenler: sorgu yüzeyi, commit,
// rollback. Store'un pgx havuzundan türetilir (cmd/core kablolar).
type BeginTx func(ctx context.Context) (db.DBTX, func(context.Context) error, func(context.Context) error, error)

// Engine — zamanlayıcı döngüsü. Vadesi gelen rutini kilitleyip koşuya
// çevirir ve bir sonraki vadeyi yazar.
type Engine struct {
	beginTx  BeginTx
	enq      Enqueuer
	interval time.Duration
	done     chan struct{}
}

// New — beginTx: iş başına transaction; enq: orkestratör; interval 0 ise 30 sn.
func New(beginTx BeginTx, enq Enqueuer, interval time.Duration) *Engine {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &Engine{beginTx: beginTx, enq: enq, interval: interval, done: make(chan struct{})}
}

func (e *Engine) Start(ctx context.Context) {
	go func() {
		defer close(e.done)
		t := time.NewTicker(e.interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				e.Tick(ctx)
			}
		}
	}()
}

// Wait — döngünün kapanmasını bekler (graceful drain).
func (e *Engine) Wait() { <-e.done }

// Tick — bir zamanlayıcı turu (testler doğrudan çağırır).
func (e *Engine) Tick(ctx context.Context) {
	dbtx, commit, rollback, err := e.beginTx(ctx)
	if err != nil {
		slog.Warn("rutin turu: transaction açılamadı", "err", err)
		return
	}
	qtx := db.New(dbtx)
	due, err := qtx.ClaimDueRoutines(ctx)
	if err != nil {
		_ = rollback(ctx)
		slog.Warn("rutin turu: vadesi gelenler okunamadı", "err", err)
		return
	}
	for _, r := range due {
		e.fire(ctx, qtx, r)
	}
	if err := commit(ctx); err != nil {
		slog.Warn("rutin turu: commit başarısız", "err", err)
	}
}

// fire — tek rutini koşuya çevirir. Hata rutini durdurmaz: next_run_at yine
// ilerletilir (bozuk bot yapılandırması sonsuz tetikleme fırtınasına dönmesin),
// hata loglanır.
func (e *Engine) fire(ctx context.Context, qtx *db.Queries, r db.Routine) {
	next, err := NextRun(r.CronExpr, r.Timezone, time.Now())
	if err != nil {
		// Tarife artık geçersiz (örn. tz veritabanı değişti): rutini durdur.
		slog.Error("rutin tarifesi çözülemedi — rutin duraklatılıyor", "routine", r.Name, "err", err)
		if _, derr := qtx.SetRoutineEnabled(ctx, db.SetRoutineEnabledParams{ID: r.ID, Enabled: false}); derr != nil {
			slog.Warn("rutin duraklatılamadı", "err", derr)
		}
		return
	}
	if err := qtx.AdvanceRoutine(ctx, db.AdvanceRoutineParams{ID: r.ID, NextRunAt: &next}); err != nil {
		slog.Warn("rutin vadesi ilerletilemedi", "routine", r.Name, "err", err)
		return
	}

	bot, err := qtx.GetBot(ctx, r.BotID)
	if err != nil {
		slog.Error("rutin botu okunamadı", "routine", r.Name, "err", err)
		return
	}
	run, err := e.enq.EnqueueChat(ctx, bot, BuildPrompt(r), "routine")
	if err != nil {
		slog.Error("rutin koşusu kuyruğa alınamadı", "routine", r.Name, "err", err)
		return
	}
	if err := qtx.RecordRoutineRun(ctx, db.RecordRoutineRunParams{RoutineID: r.ID, RunID: run.ID}); err != nil {
		slog.Warn("rutin çalışma kaydı yazılamadı", "err", err)
	}
	if _, err := qtx.PruneRoutineRuns(ctx, r.ID); err != nil {
		slog.Warn("rutin geçmişi budanamadı", "err", err)
	}
	slog.Info("rutin tetiklendi", "routine", r.Name, "run", run.ID, "next", next)
}
