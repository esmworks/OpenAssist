// Package orchestrator — koşu yaşam döngüsünün sahibi: kuyruktan iş çeker
// (FOR UPDATE SKIP LOCKED, K6), motor adaptörünü sürer (K15/K18), olayları
// kalıcılaştırır (run_events + NDJSON tee, Ek B.2-5) ve onay durum makinesini
// işletir (kısa-bekleme → park → resume).
//
// Koşu durumları: queued → running → completed | failed | approval_pending;
// approval_pending → (karar) → queued (resume). Kalp atışı kesilirse reaper
// koşuyu kuyruğa geri koyar (deploy/çökme koruması, PRD §6).
package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esmworks/OpenAssist/internal/attachments"
	"github.com/esmworks/OpenAssist/internal/bus"
	"github.com/esmworks/OpenAssist/internal/engine"
	"github.com/esmworks/OpenAssist/internal/policy"
	"github.com/esmworks/OpenAssist/internal/store"
	db "github.com/esmworks/OpenAssist/internal/store/db"
	"github.com/esmworks/OpenAssist/internal/workspace"
)

type Config struct {
	Workers        int
	ApprovalWait   time.Duration // kısa-bekleme penceresi; dolunca park (K15)
	TurnTimeout    time.Duration
	HeartbeatEvery time.Duration
	StaleAfter     time.Duration // kalp atışı bundan eskiyse koşu sahipsizdir
	PollEvery      time.Duration // LISTEN/NOTIFY kaçarsa güvenlik ağı (K6)
	MaxAttempts    int
	// MaxGroupHops — grup kanalında üye→üye @mention zincirinin azami
	// derinliği (ADIM 10 ikinci aşama). Kullanıcının mesajı hop=0'dır;
	// koordinatörün çağırdığı üye hop=1. Sınır dolunca zincir durur ve
	// sohbete çip düşer — sınırsız bırakılsaydı iki bot birbirini
	// nezaketen sonsuza kadar çağırabilirdi (her tur gerçek para).
	MaxGroupHops int
	DataDir      string
	WorkerID     string
	// ToolClasses — MCP gateway'in açık sınıf bildirimleri (Faz 4);
	// sezgisel Classify'dan önce okunur. nil olabilir.
	ToolClasses map[string]policy.Classification
	// OnApprovalResolved — onay/cevap çözüldüğünde çağrılır (K16 çok kanallı
	// yarış): diğer kanallar kartlarını kapatsın diye. Bloklamamalı.
	OnApprovalResolved func(runID uuid.UUID, note string)
	// AfterRun — her BİTEN koşudan sonra çağrılır (ADIM 8: koşu sonrası hafıza
	// çıkarımı gibi arka plan bakımı). Koşudan AYRI goroutine'de, 90 sn tavanlı
	// bağlamla koşar; bloklamamalıdır (OnApprovalResolved deseni).
	AfterRun func(ctx context.Context, bot db.Bot, run db.Run)
}

func DefaultConfig() Config {
	host, _ := os.Hostname()
	return Config{
		Workers:        2,
		ApprovalWait:   60 * time.Second,
		TurnTimeout:    30 * time.Minute,
		HeartbeatEvery: 15 * time.Second,
		StaleAfter:     90 * time.Second,
		PollEvery:      5 * time.Second,
		MaxAttempts:    3,
		MaxGroupHops:   3,
		DataDir:        "./data",
		WorkerID:       fmt.Sprintf("%s-%d", host, os.Getpid()),
	}
}

var ErrNotAwaiting = errors.New("koşu onay/cevap beklemiyor")

type Orchestrator struct {
	st      *store.Store
	engines map[string]engine.Executor
	bus     *bus.Bus
	hub     *approvalHub
	cfg     Config

	kick chan struct{}
	wg   sync.WaitGroup
}

func New(st *store.Store, engines map[string]engine.Executor, b *bus.Bus, cfg Config) *Orchestrator {
	return &Orchestrator{
		st: st, engines: engines, bus: b, hub: newApprovalHub(), cfg: cfg,
		kick: make(chan struct{}, 1),
	}
}

// Start, worker havuzunu, reaper'ı ve LISTEN dinleyicisini başlatır.
func (o *Orchestrator) Start(ctx context.Context) {
	for i := 0; i < o.cfg.Workers; i++ {
		o.wg.Add(1)
		go o.workerLoop(ctx)
	}
	o.wg.Add(2)
	go o.reaperLoop(ctx)
	go o.listenLoop(ctx)
}

// Wait, nazik kapanışta çalışan koşuların bitmesini bekler (drain, PRD §6).
func (o *Orchestrator) Wait() { o.wg.Wait() }

// ── kuyruğa iş verme ───────────────────────────────────────────────────

// OriginAPI — kanal adları (messages.origin / runs.origin, K16).
const (
	OriginAPI      = "api"
	OriginTelegram = "telegram"
	OriginMac      = "mac"
	OriginRoutine  = "routine"
	// OriginBot — botlar arası devir (ADIM 6): koşuyu bir bot açtı. Depth-1
	// korumasının anahtarı: bu koşularda ask_bot araç listesinden düşer.
	OriginBot = "bot"
)

// askBotTool — devir aracının tam adı. pi.AskBotTool'un kopyası:
// orchestration katmanı adaptör paketlerine bağlanmaz (sözleşme yalnız adla
// yaşar, PRD B.2-6).
const askBotTool = "mcp__oa__ask_bot"

// EnqueueChat: kullanıcı mesajını kaydeder ve bot için koşu kuyruğa alır.
// origin, isteğin geldiği kanaldır (boşsa api) — koşu park edilip saatler
// sonra resume olsa bile sonuç doğru kanala bildirilebilsin diye koşuya da
// yazılır.
//
// Anahtarsız kanallar (telegram: update_id ile dedupe, routine, iç araçlar)
// bu yoldan geçer.
func (o *Orchestrator) EnqueueChat(ctx context.Context, bot db.Bot, text, origin string) (db.Run, error) {
	return o.enqueueChat(ctx, bot, text, origin, "", nil)
}

// EnqueueChatWithKey — aynı anahtarla tekrar eden gönderim yeni koşu üretmez,
// ilk gönderimin koşusunu döndürür (ADIM 3, HTTP yüzeyi). key boşsa
// EnqueueChat ile aynıdır. ekler doluysa ekler mesaja bağlanır ve motora
// dosya yollarıyla duyurulur (ADIM 5).
func (o *Orchestrator) EnqueueChatWithKey(ctx context.Context, bot db.Bot, text, origin, key string, ekler []uuid.UUID) (db.Run, error) {
	return o.enqueueChat(ctx, bot, text, origin, key, ekler)
}

// EditChatWithKey — mesajı düzenler ve botu yeniden üretir (ADIM 9). Taze
// oturum + satır içi geçmiş tekrarı store.EditChat'te; burada yalnız kuyruk
// uyandırılır (düzenlenen dal yeni bir koşudur).
func (o *Orchestrator) EditChatWithKey(ctx context.Context, bot db.Bot, eski db.Message, text, origin, key string) (db.Run, error) {
	if origin == "" {
		origin = OriginAPI
	}
	if bot.Kind == "team" {
		// Ekip kanalında düzenleme de üyeye yönlenir (ADIM 10).
		uye, err := o.grupUyesi(ctx, bot, text)
		if err != nil {
			return db.Run{}, err
		}
		run, created, err := o.st.EditChat(ctx, uye, eski, text, origin, key)
		if err != nil {
			return db.Run{}, err
		}
		if created {
			o.notify(ctx)
		}
		return run, nil
	}
	run, created, err := o.st.EditChat(ctx, bot, eski, text, origin, key)
	if err != nil {
		return db.Run{}, err
	}
	if created {
		o.notify(ctx)
	}
	return run, nil
}

// enqueueChat — tek gerçek giriş: kayıt (store, transaction) + kuyruk uyandırma.
// Tekrar eden gönderimde (created=false) kuyruk uyandırılmaz — o koşu zaten
// kuyrukta ya da çalışıyor.
func (o *Orchestrator) enqueueChat(ctx context.Context, bot db.Bot, text, origin, key string, ekler []uuid.UUID) (db.Run, error) {
	if origin == "" {
		origin = OriginAPI
	}
	prompt := text
	if len(ekler) > 0 {
		blok, err := o.ekBlok(ctx, ekler)
		if err != nil {
			return db.Run{}, err
		}
		prompt = text + "\n\n" + blok
	}
	if bot.Kind == "team" {
		// Ekip kanalı (ADIM 10): mesaj ekip sohbetine, koşu @mention ya da
		// varsayılan üyeye.
		return o.enqueueGrup(ctx, bot, prompt, text, origin, key, ekler)
	}
	run, created, err := o.st.SaveChat(ctx, bot, prompt, text, origin, key, ekler)
	if err != nil {
		return db.Run{}, err
	}
	if created {
		o.notify(ctx)
	}
	return run, nil
}

// ekBlok — ekleri motor promptuna giden yol bloğuna çevirir (ADIM 5).
// Görünen mesaj metni DEĞİŞMEZ: blok yalnız run.Prompt'ta yaşar. Motor CLI
// sunucuda koştuğu için dosyalara doğrudan erişir (read_file vb.).
func (o *Orchestrator) ekBlok(ctx context.Context, ids []uuid.UUID) (string, error) {
	ekler, err := o.st.Q.SelectAttachmentsByIDs(ctx, ids)
	if err != nil {
		return "", err
	}
	if len(ekler) != len(ids) {
		return "", fmt.Errorf("orchestrator: eklerin tümü yok (%d/%d)", len(ekler), len(ids))
	}
	satirlar := make([]string, 0, len(ekler)+1)
	satirlar = append(satirlar, "Ekli dosyalar (sunucuda, buradan erişebilirsin):")
	for _, e := range ekler {
		yol := attachments.Abs(o.cfg.DataDir, e.Path)
		satir := fmt.Sprintf("- %s (%s): %s", e.Name, e.Kind, yol)
		// Okunamayan görüntü SESSİZ kalmasın: motorun read aracı tanımadığı
		// bir görüntüyü (HEIC — her iPhone fotoğrafı) metin sanıp UTF-8 çözer,
		// eline çöp geçer ama "dosyayı okudum" der. Uyarı promptta olmazsa
		// yanlış cevabın sebebi hiçbir yerde görünmüyor.
		if e.Kind == "image" && !attachments.MotorGorur(yol) {
			satir += " — UYARI: bu görüntü biçimini göremezsin (read aracı yalnız" +
				" JPEG/PNG/GIF/WEBP/BMP tanır). İçeriğini uydurma;" +
				" kullanıcıdan JPEG/PNG sürümünü iste ya da dönüştür."
		}
		satirlar = append(satirlar, satir)
	}
	return strings.Join(satirlar, "\n"), nil
}

// ResolveApproval — onay/cevap tek kapıdan girer (çoklu kanal yarışı burada
// çözülür): önce süreç içi bekleyen istek (kısa yol), yoksa park edilmiş
// koşu (resume yolu). decision: "allow"|"deny" (izin) — answer: soru cevabı —
// always: "hep izin ver/reddet" (K17 vetoları geçerli).
func (o *Orchestrator) ResolveApproval(ctx context.Context, runID uuid.UUID, decision, answer string, always bool) error {
	if ask, ok := o.hub.pending(runID); ok {
		var ans engine.Answer
		switch {
		case ask.Kind == "question":
			if answer == "" {
				return errors.New("soru bekleyen koşu için 'answer' gerekli")
			}
			ans = engine.Answer{Message: answer, Source: "api"}
		case decision == "allow":
			ans = engine.Answer{Behavior: "allow", Always: always, Source: "api"}
		case decision == "deny":
			ans = engine.Answer{Behavior: "deny", Message: engine.DenyByUserNote, Always: always, Source: "api"}
		default:
			return errors.New("izin bekleyen koşu için decision allow|deny olmalı")
		}
		if o.hub.resolve(runID, ans) {
			o.announceResolved(runID, ans.Behavior, ans.Message)
			return nil // kısa yol: koşu süreç içinde devam ediyor
		}
		// Yarışı kaybettik (tam o anda park edildi) — park yoluna düş.
	}

	// Park yolu: izin kararı grant olarak taşınır; soru cevabı resume
	// prompt'una gömülür (grant_decision boş kalır — cevap izin değildir).
	var prompt string
	switch {
	case decision == "allow":
		prompt = "Onay verildi. Park edilen işi kaldığın yerden tamamla."
	case decision == "deny":
		prompt = "Onay reddedildi. O adımı yapmadan durumu toparla ve kısaca özetle."
	case answer != "":
		prompt = "Sorduğun sorunun cevabı: " + answer + "\nBuna göre kaldığın yerden devam et."
		decision = "" // grant değil
	default:
		return ErrNotAwaiting
	}
	run, err := o.st.Q.ResolveApproval(ctx, db.ResolveApprovalParams{
		ID: runID, GrantDecision: decision, Prompt: prompt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotAwaiting
	}
	if err != nil {
		return err
	}
	if always {
		o.maybeRuleFromParkedAsk(ctx, run, decision)
	}
	o.announceResolved(runID, decision, answer)
	o.notify(ctx)
	return nil
}

// announceResolved — kararı diğer kanallara duyurur (K16): Telegram kartı
// kapanır, "başka kanaldan yanıtlandı" yazılır. Kanal kendi kararını da
// duyar — kart kapatma tek kullanımlıktır, ikinci kapatış boşa düşer.
func (o *Orchestrator) announceResolved(runID uuid.UUID, decision, message string) {
	if o.cfg.OnApprovalResolved == nil {
		return
	}
	var note string
	switch {
	case decision == "allow":
		note = "✔ Onaylandı."
	case decision == "deny":
		note = "✖ Reddedildi."
	case message != "":
		note = "💬 Cevaplandı."
	default:
		note = "✔ İşlendi."
	}
	go o.cfg.OnApprovalResolved(runID, note)
}

// maybeRuleFromParkedAsk — park kartındaki "hep izin ver/reddet" kararını
// kurala çevirir; K17 vetoları burada da işler (yüksek risk ve tainted
// koşuda allow kuralı yazılmaz, deny her zaman yazılabilir).
func (o *Orchestrator) maybeRuleFromParkedAsk(ctx context.Context, run db.Run, decision string) {
	var pa engine.Ask
	if err := json.Unmarshal(run.PendingAsk, &pa); err != nil || pa.Tool == "" {
		return
	}
	cls := policy.ClassifyWith(o.cfg.ToolClasses, pa.Tool)
	if decision == "allow" && (!cls.Ruleable || run.Tainted) {
		slog.Info("hep-izin-ver kurala dönüştürülmedi (K17 vetosu)",
			"tool", pa.Tool, "ruleable", cls.Ruleable, "tainted", run.Tainted)
		return
	}
	o.createRule(ctx, run.BotID, run.ID, pa.Tool, decision)
}

// ClassifyTool — aracın etkin sınıfı (gateway bildirimleri + sezgisel);
// UI yüzeyleri (toplu onay vetoları, K19) buradan okur.
func (o *Orchestrator) ClassifyTool(tool string) policy.Classification {
	return policy.ClassifyWith(o.cfg.ToolClasses, tool)
}

// PendingAsk, çalışan koşunun süreç içi bekleyen isteğini döner (UI kartı).
func (o *Orchestrator) PendingAsk(runID uuid.UUID) (engine.Ask, bool) { return o.hub.pending(runID) }

func (o *Orchestrator) notify(ctx context.Context) {
	// Süreç içi tekme (v1 tek servis) + diğer instance'lar için NOTIFY.
	select {
	case o.kick <- struct{}{}:
	default:
	}
	if _, err := o.st.Pool().Exec(ctx, "SELECT pg_notify('oa_runs','')"); err != nil {
		slog.Warn("pg_notify başarısız (poll yakalar)", "err", err)
	}
}

// ── döngüler ───────────────────────────────────────────────────────────

func (o *Orchestrator) workerLoop(ctx context.Context) {
	defer o.wg.Done()
	tick := time.NewTicker(o.cfg.PollEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-o.kick:
		case <-tick.C: // NOTIFY kaçarsa güvenlik ağı
		}
		for {
			run, err := o.st.Q.ClaimNextRun(ctx, o.cfg.WorkerID)
			if errors.Is(err, pgx.ErrNoRows) {
				break
			}
			if err != nil {
				if ctx.Err() == nil {
					slog.Error("kuyruktan iş alınamadı", "err", err)
				}
				break
			}
			o.execute(ctx, run)
		}
	}
}

func (o *Orchestrator) reaperLoop(ctx context.Context) {
	defer o.wg.Done()
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			o.Reap(ctx)
		}
	}
}

// Reap — reaper'ın BİR turu: sahipsiz koşuları düşür/kuyruğa koy, süresi
// dolan onayları expire et. Döngüden ayrı durur ki 30 saniyelik tikeri
// beklemeden çağrılabilsin (testler bunu doğrudan sürer).
func (o *Orchestrator) Reap(ctx context.Context) {
	stale := o.cfg.StaleAfter.Seconds()
	maxAtt := int32(o.cfg.MaxAttempts)

	if ids, err := o.st.Q.FailStaleRuns(ctx, db.FailStaleRunsParams{StaleSecs: stale, MaxAttempts: maxAtt}); err == nil && len(ids) > 0 {
		slog.Error("deneme hakkı biten sahipsiz koşular hata durumuna düştü", "adet", len(ids))
		// Sessizce ölmesin. execute'un kendi hata yolu (failRun) bunu zaten
		// yapıyordu; reaper yolu atlanmıştı ve sonuç kullanıcı için aynı eski
		// dert: mesajını yazdın, cevap yok, ekranda HİÇBİR ŞEY olmuyor, sebep
		// yalnız sunucu logunda kalıyor.
		for _, id := range ids {
			o.hataCipi(ctx, id, sahipsizHata)
			payload, _ := json.Marshal(map[string]string{"error": sahipsizHata})
			o.publishEvent(ctx, id, string(engine.KindError), payload, nil)
			o.publishSettled(id, "failed")
		}
	}
	if n, err := o.st.Q.RequeueStaleRuns(ctx, db.RequeueStaleRunsParams{StaleSecs: stale, MaxAttempts: maxAtt}); err == nil && n > 0 {
		slog.Warn("sahipsiz koşular kuyruğa geri kondu", "adet", n)
	}
	if ttl := o.st.LoadPolicy(ctx).ApprovalTTLHours; ttl > 0 {
		if n, err := o.st.Q.ExpireStaleApprovals(ctx, float64(ttl)*3600); err == nil && n > 0 {
			slog.Warn("süresi dolan onaylar expired oldu (K14)", "adet", n)
		}
	}
}

func (o *Orchestrator) listenLoop(ctx context.Context) {
	defer o.wg.Done()
	for ctx.Err() == nil {
		if err := o.listenOnce(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("LISTEN koptu, yeniden bağlanılacak (poll devrede)", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

func (o *Orchestrator) listenOnce(ctx context.Context) error {
	conn, err := o.st.Pool().Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN oa_runs"); err != nil {
		return err
	}
	for {
		if _, err := conn.Conn().WaitForNotification(ctx); err != nil {
			return err
		}
		select {
		case o.kick <- struct{}{}:
		default:
		}
	}
}

// ── koşu yürütme ───────────────────────────────────────────────────────

func (o *Orchestrator) execute(ctx context.Context, run db.Run) {
	log := slog.With("run", run.ID.String(), "bot", run.BotID.String())

	bot, err := o.st.Q.GetBot(ctx, run.BotID)
	if err != nil {
		o.failRun(ctx, run.ID, "bot okunamadı: "+err.Error())
		return
	}
	exec := o.engines[run.Engine]
	if exec == nil {
		// Gölge kural (Ek B.2-4): bilinmeyen motor botu bozmaz, koşuyu reddeder.
		o.failRun(ctx, run.ID, fmt.Sprintf("motor %q bu sürümde tanınmıyor — bot korunur, koşu reddedildi", run.Engine))
		return
	}

	runCtx, cancel := context.WithTimeout(ctx, o.cfg.TurnTimeout)
	defer cancel()

	// Kalp atışı: reaper bizi sahipsiz sanmasın.
	hbCtx, hbStop := context.WithCancel(ctx)
	defer hbStop()
	go func() {
		t := time.NewTicker(o.cfg.HeartbeatEvery)
		defer t.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-t.C:
				if err := o.st.Q.HeartbeatRun(hbCtx, run.ID); err != nil && hbCtx.Err() == nil {
					log.Warn("kalp atışı yazılamadı", "err", err)
				}
			}
		}
	}()

	tee := newEventTee(o.cfg.DataDir, run.ID)
	defer tee.close()

	pol := o.st.LoadPolicy(ctx)

	// Taint izleme (§5.1): dış içerik okuyan araç koşuyu işaretler. Olay
	// akışı ve onay isteği farklı goroutine'lerden geldiği için atomik.
	var tainted atomic.Bool
	tainted.Store(run.Tainted)
	emit := func(ev engine.Event) {
		if ev.Kind == engine.KindToolStarted && ev.Tool != nil &&
			policy.ClassifyWith(o.cfg.ToolClasses, ev.Tool.Name).DataSource && !tainted.Swap(true) {
			if err := o.st.Q.SetRunTainted(ctx, run.ID); err != nil {
				log.Warn("tainted bayrağı yazılamadı", "err", err)
			}
			log.Info("koşu tainted işaretlendi", "tool", ev.Tool.Name)
		}
		o.persistEvent(ctx, run.ID, ev, tee)
	}

	park := &parkKutusu{}
	grantUsed := false
	onAsk := func(askCtx context.Context, ask engine.Ask) engine.Answer {
		if ask.Kind == "bot_message" {
			// Devir (ADIM 6): run-bağlamlı proxy'den gelir — kaynak koşu
			// GÜVENİLİR (bu closure'dur); insan kapısı yok, sohbete çip düşer.
			return o.answerBotMessage(askCtx, run, bot, ask)
		}
		if ask.Kind == "file" {
			// Giden dosya (ADIM 13): aynı güven gerekçesi — koşu ve bot bu
			// kapanıştan bilinir, dosya botun kendi kutusuyla sınırlı.
			return o.answerSendFile(askCtx, run, bot, ask)
		}
		if ask.Kind == "question" {
			// Sorular politika kapısından geçmez — insana gider.
			return o.waitForHuman(askCtx, run.ID, ask, park)
		}

		cls := policy.ClassifyWith(o.cfg.ToolClasses, ask.Tool)

		// Resume koşusu: park edilmiş isteğin tek kullanımlık kararı (K15).
		if run.GrantDecision != "" && !grantUsed {
			grantUsed = true
			if run.GrantDecision == "allow" {
				o.logAction(ctx, bot.ID, run.ID, ask.Tool, cls, "grant")
				return engine.Answer{Behavior: "allow", Source: "grant"}
			}
			return engine.Answer{Behavior: "deny", Message: engine.DenyByUserNote, Source: "grant"}
		}

		// Onay kapısı (K17): kural + vetolar (yüksek risk, tainted, kota,
		// sessiz saat). DB okumaları burada, karar saf policy.Decide'da.
		rule := o.lookupRule(ctx, bot.ID, ask.Tool)
		var quotaUsed int64
		if pol.DailyActionQuota > 0 {
			quotaUsed = o.countActionsToday(ctx, bot.ID, pol)
		}
		dec := policy.Decide(cls, tainted.Load(), quotaUsed, time.Now(), pol, rule)
		switch dec.Kind {
		case policy.AutoAllow:
			o.logAction(ctx, bot.ID, run.ID, ask.Tool, cls, "rule")
			if cls.DataSource {
				// Eylem günlüğü okumaları BİLİNÇLİ olarak tutmuyor (yoksa
				// zararsız okumalar günlük eylem kotasını yerdi, K17). Ama
				// dışarıya uzanan bir okuma sorulmadan geçtiyse bunun bir izi
				// olmalı: koşuyu tainted yapan ve sonraki her şeyi kısıtlayan
				// olay budur.
				log.Info("dış içerik okuması sorulmadan geçti",
					"tool", ask.Tool, "girdi", kisaGirdi(ask.Input))
			}
			return engine.Answer{Behavior: "allow", Source: "rule"}
		case policy.AutoDeny:
			return engine.Answer{Behavior: "deny", Message: "OpenAssist kuralı: " + dec.Reason, Source: "rule"}
		}

		// Kart "hep izin ver" sunabilsin diye kararı isteğe iliştiriyoruz.
		// Sunmadan önce buraya bakmak ŞART: aynı araç arka arkaya on kez
		// çağrıldığında (ör. on mükerrer hafıza kaydını silmek) kullanıcı
		// on ayrı karta basmak zorunda kalıyordu — her biri ayrı park/resume
		// turu, çünkü resume izni TEK KULLANIMLIK (K15). Çıkış kapısı kural
		// yazmaktı ve yüzeyde hiç görünmüyordu.
		ask.AllowAlways = dec.AllowAlwaysRule
		ans := o.waitForHuman(askCtx, run.ID, ask, park)
		if ans.Behavior == "allow" && ans.Source == "api" {
			o.logAction(ctx, bot.ID, run.ID, ask.Tool, cls, "api")
			if ans.Always && dec.AllowAlwaysRule {
				o.createRule(ctx, bot.ID, run.ID, ask.Tool, "allow")
			}
		}
		if ans.Behavior == "deny" && ans.Source == "api" && ans.Always {
			o.createRule(ctx, bot.ID, run.ID, ask.Tool, "deny")
		}
		return ans
	}

	// Çalışma dizini AÇILAMAZSA koşu burada durur. Eskiden yalnız uyarı
	// yazılıp var olmayan dizinle devam ediliyordu; Linux'ta exec bu durumda
	// hatayı İKİLİYE yıkıyor ("fork/exec /usr/local/bin/opencode: no such
	// file or directory") ve sebep motorda sanılıyordu. Gerçek sebep
	// söylensin.
	cwd, err := o.botWorkspace(bot.ID)
	if err != nil {
		o.failRun(ctx, run.ID, "çalışma dizini oluşturulamadı: "+err.Error())
		return
	}

	res, err := exec.SendTurn(runCtx, engine.TurnInput{
		Prompt: run.Prompt,
		// Ekip bloğu hafızadan SONRA gelir: bu turun kanal sözleşmesidir
		// (kim dinliyor, devir hakkı ne kadar), botun kalıcı kimliği değil.
		SystemPrompt: joinPrompts(bot.Description, bot.SystemPrompt,
			o.contextPrompt(ctx, bot.ID), o.ekipPrompt(ctx, run, bot)),
		Model:        run.Model,
		ResumeCursor: run.ResumeCursor,
		AllowedTools: toolsForRun(bot, run),
		Extensions:   bot.Extensions,
		CWD:          cwd,
		OnAsk:        onAsk,
	}, emit)
	if err != nil {
		o.failRun(ctx, run.ID, err.Error())
		return
	}
	// Motor "tur başarısız" diyebilir; SendTurn yine de hatasız döner —
	// başarısızlık taşıyıcıda değil SONUÇTA bildirilir (TurnResult.Ok).
	// Yalnız err'e bakmak, 400 dönen bir modeli ya da bütçe biten bir turu
	// BOŞ ama "completed" koşu olarak kaydederdi: kullanıcı hiçbir cevap
	// göremez, hiçbir hata da göremez. pi'de Ok, tur boyunca KindError
	// görülmemesidir (pi hatayı ayrı olayla değil, message_end içindeki
	// stopReason="error" ile bildirir).
	if !res.Ok {
		msg := res.StopReason
		if msg == "" {
			msg = "motor turu başarısız bildirdi"
		}
		o.failRun(ctx, run.ID, msg)
		return
	}

	// Motorun döndürdüğü imleci (bot, motor, konuşma) anahtarına yaz
	// (ADIM 7+10). run.Engine — koşuyu gerçekten yürüten motor.
	if run.ConversationID.Valid {
		if err := o.st.Q.UpsertBotCursor(ctx, db.UpsertBotCursorParams{
			BotID: bot.ID, Engine: run.Engine, ConversationID: run.ConversationID.UUID,
			Cursor: res.ResumeCursor,
		}); err != nil {
			log.Warn("bot imleci yazılamadı", "err", err)
		}
	}
	// Giden ek (ADIM 13): koşu sırasında send_file ile üretilen dosyalar botun
	// mesajına bağlanır. Metin BOŞ olsa bile mesaj yazılır — yoksa dosya
	// hiçbir yüzeyde görünmez (mesajı olmayan ek zaman tünelinde yoktur).
	gidenEkler, err := o.st.Q.ListAttachmentsByRun(ctx, uuid.NullUUID{UUID: run.ID, Valid: true})
	if err != nil {
		log.Warn("giden ekler okunamadı", "err", err)
	}
	if res.FinalText != "" || len(gidenEkler) > 0 {
		metin := res.FinalText
		if strings.TrimSpace(metin) == "" {
			metin = gidenEkCipi(gidenEkler)
		}
		if msgID, ok := o.botMesajiYaz(ctx, run, bot, metin); ok && len(gidenEkler) > 0 {
			if _, err := o.st.Q.BindRunAttachments(ctx, db.BindRunAttachmentsParams{
				RunID: uuid.NullUUID{UUID: run.ID, Valid: true}, MessageID: uuid.NullUUID{UUID: msgID, Valid: true},
			}); err != nil {
				log.Warn("giden ekler mesaja bağlanamadı", "err", err)
			}
		}
	}

	if parked := park.al(); parked != nil {
		askJSON, merr := json.Marshal(parked)
		if merr != nil {
			askJSON = []byte(`{}`)
		}
		if err := o.st.Q.ParkRun(ctx, db.ParkRunParams{
			ID: run.ID, PendingAsk: askJSON, ResultCursor: res.ResumeCursor,
			CostUsd: res.CostUSD, InputTokens: res.InputTokens,
			OutputTokens: res.OutputTokens, NumTurns: int32(res.NumTurns),
			FinalText: res.FinalText,
		}); err != nil {
			log.Error("koşu park edilemedi", "err", err)
			return
		}
		o.publishSettled(run.ID, "approval_pending")
		log.Info("koşu onay bekliyor", "tool", parked.Tool)
		return
	}

	if err := o.st.Q.CompleteRun(ctx, db.CompleteRunParams{
		ID: run.ID, FinalText: res.FinalText, ResultCursor: res.ResumeCursor,
		CostUsd: res.CostUSD, InputTokens: res.InputTokens,
		OutputTokens: res.OutputTokens, NumTurns: int32(res.NumTurns),
	}); err != nil {
		log.Error("koşu tamamlandı yazılamadı", "err", err)
		return
	}
	// Metin ÜRETMEDEN biten tur sessiz kalmasın: kullanıcı ekranda hiçbir şey
	// görmüyor, logda da hata yok (koşu "completed"). Motor yalnız araç
	// çağırıp konuşmadan bittiğinde tam olarak bu oluyordu. Dosya gönderen tur
	// sessiz DEĞİLDİR: ek mesajı zaten yazıldı, çip ikinci kez anlatırdı.
	if strings.TrimSpace(res.FinalText) == "" && len(gidenEkler) == 0 && run.ConversationID.Valid {
		o.sessizTurCipi(ctx, run)
	}
	o.devirCevapCipi(ctx, run, bot, res.FinalText)
	// Ekip kanalıysa cevaptaki @Ad çağrıları sıradaki üyeleri uyandırır
	// (ADIM 10 ikinci aşama). Koşu TAMAMLANDIKTAN sonra: park edilmiş bir
	// koşunun yarım cevabı zinciri başlatmamalı.
	o.grupHop(ctx, run, bot, res.FinalText)
	o.publishSettled(run.ID, "completed")
	// Kancaya BAYAT satır gitmesin: CompleteRun DB'yi güncelledi, yerel run
	// kopyası hâlâ sonuçsuz — AfterRun (ADIM 8) transkripti FinalText'ten
	// kuruyor; tazelemeden verilirse çıkarım boş transcript görür.
	run.FinalText = res.FinalText
	run.Status = "completed"
	o.arkaPlan(ctx, bot, run)
	log.Info("koşu tamamlandı", "maliyet_usd", res.CostUSD, "tur", res.NumTurns)
}

// waitForHuman — kısa-bekleme penceresi: cevap API'den yetişirse süreç içi
// devam; izinlerde pencere dolarsa park kararı döner.
func (o *Orchestrator) waitForHuman(askCtx context.Context, runID uuid.UUID, ask engine.Ask, park *parkKutusu) engine.Answer {
	// Bu tur ZATEN park edildi: bir daha bekleme, hemen park notunu ver.
	//
	// Park kararı turun tamamı içindir — kullanıcı cevap verdiğinde koşu
	// yeni bir turda devam eder, bu turda değil. Dolayısıyla ilk parktan
	// sonraki her istek de park edilecektir; her biri için kısa-bekleme
	// penceresini yeniden harcamak ölü zamandan başka bir şey değil.
	// Sahada ölçüldü: motor reddedilen komuttan sonra alternatif deniyor
	// (curl → node → command -v …), her deneme 60 saniye daha bekliyor ve
	// kullanıcı on dakika boyunca hiçbir şey olmadığını izliyordu.
	//
	// İlk park KORUNUR, üzerine yazılmaz: kullanıcının ekranda gördüğü ve
	// cevaplayacağı kart odur.
	if park.al() != nil && ask.Kind != "question" {
		// Bu istekler hiçbir yere yazılmıyor: hub'a asılmıyor (API önizlemesi
		// görmez), pending_ask'a girmiyor, eylem günlüğüne de düşmüyor. Log
		// olmasa kullanıcının şikâyeti aynen sürerdi: "on dakika uğraştı ama
		// ne yaptığı belli değil." Kart bir tane, denemeler burada görünür.
		slog.Info("park sonrası istek beklemeden reddedildi",
			"run", runID, "tool", ask.Tool, "park_tool", park.al().Tool)
		return engine.Answer{Behavior: "deny", Message: engine.DenyParkNote, Source: "timeout"}
	}

	ch := o.hub.put(runID, ask)
	defer o.hub.drop(runID)
	select {
	case ans := <-ch:
		return ans
	case <-time.After(o.cfg.ApprovalWait):
		if ask.Kind == "question" {
			return engine.Answer{Source: "timeout"} // proxy "kendi kararını ver" notunu düşer
		}
		park.koy(ask)
		return engine.Answer{Behavior: "deny", Message: engine.DenyParkNote, Source: "timeout"}
	case <-askCtx.Done():
		return engine.Answer{Behavior: "deny", Message: "koşu iptal edildi", Source: "policy"}
	}
}

// kisaGirdi — araç argümanlarının loga girecek kısa hâli.
func kisaGirdi(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// parkKutusu — turun park kararı; İKİ goroutine'den görülür.
//
// Yazan: motorun eklenti diyaloğu goroutine'i (pi rpc.go, her ui isteği ayrı
// goroutine + uiMu). Okuyan: SendTurn döndükten sonra execute'un kendisi.
// Aralarında bir buluşma noktası YOK — adaptör okuma döngüsünü (readDone)
// bekler ama ondan doğan ui goroutine'lerini beklemez. Yani pi tam da bir
// onay penceresi dolarken kapanırsa `park.koy` ile `park.al` çakışabilir:
// kilitsiz bırakılırsa bu, koşunun park edilmesi gerekirken "tamamlandı"
// yazılmasına (kart hiç görünmez) yol açabilecek bir veri yarışıdır.
type parkKutusu struct {
	mu  sync.Mutex
	ask *engine.Ask
}

// koy — İLK parkı tutar; sonrakiler üzerine yazmaz.
func (p *parkKutusu) koy(ask engine.Ask) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ask == nil {
		p.ask = &ask
	}
}

func (p *parkKutusu) al() *engine.Ask {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ask
}

func (o *Orchestrator) lookupRule(ctx context.Context, botID uuid.UUID, tool string) *policy.Rule {
	r, err := o.st.Q.GetRuleForTool(ctx, db.GetRuleForToolParams{
		Tool: tool, BotID: uuid.NullUUID{UUID: botID, Valid: true},
	})
	if err != nil {
		return nil // kural yok (ya da okunamadı → güvenli taraf: sorulur)
	}
	return &policy.Rule{Action: r.Action}
}

func (o *Orchestrator) countActionsToday(ctx context.Context, botID uuid.UUID, pol policy.Settings) int64 {
	n, err := o.st.Q.CountActionsSince(ctx, db.CountActionsSinceParams{
		BotID: botID, CreatedAt: pol.DayStart(time.Now()),
	})
	if err != nil {
		slog.Warn("kota sayacı okunamadı — güvenli taraf: dolu sayılır", "err", err)
		return int64(pol.DailyActionQuota) // fail-closed
	}
	return n
}

// logAction — yalnız yan-etkili sınıflar eylem günlüğüne düşer (kota sayacı
// + Aktivite yüzeyi).
func (o *Orchestrator) logAction(ctx context.Context, botID, runID uuid.UUID, tool string, cls policy.Classification, decidedBy string) {
	if cls.Class != policy.ClassWrite && cls.Class != policy.ClassExternal && cls.Class != policy.ClassExecute {
		return
	}
	if err := o.st.Q.LogAction(ctx, db.LogActionParams{
		BotID: botID, RunID: uuid.NullUUID{UUID: runID, Valid: true},
		Tool: tool, Class: string(cls.Class), DecidedBy: decidedBy,
	}); err != nil {
		slog.Warn("eylem günlüğüne yazılamadı", "err", err)
	}
}

func (o *Orchestrator) createRule(ctx context.Context, botID, runID uuid.UUID, tool, action string) {
	_, err := o.st.Q.UpsertApprovalRule(ctx, db.UpsertApprovalRuleParams{
		BotID: uuid.NullUUID{UUID: botID, Valid: true}, Tool: tool, Action: action,
		Note:         "onay kartından (hep " + map[string]string{"allow": "izin ver", "deny": "reddet"}[action] + ")",
		CreatedByRun: uuid.NullUUID{UUID: runID, Valid: true},
	})
	if err != nil {
		slog.Warn("onay kuralı yazılamadı", "err", err)
		return
	}
	slog.Info("onay kuralı oluşturuldu", "tool", tool, "action", action, "bot", botID.String())
}

// toolsForRun — botun araç listesine koşu bağlamını uygular (ADIM 6):
// bot-başlatımlı koşular (depth=1) ask_bot'u görmez. A→B çalışır, B→C ve
// döngüler yapısal olarak imkânsızdır (PRD B.2-6 — derinlik harness'ta).
func toolsForRun(bot db.Bot, run db.Run) []string {
	if run.Origin != OriginBot {
		return bot.AllowedTools
	}
	out := make([]string, 0, len(bot.AllowedTools))
	for _, t := range bot.AllowedTools {
		if t != askBotTool {
			out = append(out, t)
		}
	}
	return out
}

// answerBotMessage — ask_bot çağrısını işler (ADIM 6). Kaynak koşu ve bot bu
// kapanışa ait olduğundan taklit edilemez; devir hemen açılır, kullanıcıya
// onay kartı düşmez — kaynak botun sohbetine çip düşer.
func (o *Orchestrator) answerBotMessage(ctx context.Context, run db.Run, from db.Bot, ask engine.Ask) engine.Answer {
	hedef, err := o.devirHedefi(ctx, ask)
	if err != nil {
		return engine.Answer{Behavior: "deny", Source: "rule", Message: err.Error()}
	}
	if hedef.ID == from.ID {
		return engine.Answer{Behavior: "deny", Source: "rule", Message: "kendine devir yapamazsın — işi bu koşuda kendin yap"}
	}
	if hedef.Kind == "team" {
		return engine.Answer{
			Behavior: "deny", Source: "rule",
			Message: "ekip sohbeti henüz devralmıyor (v2) — üyelere tek tek devret (list_bots)",
		}
	}
	metin := strings.TrimSpace(ask.Message)
	if metin == "" {
		return engine.Answer{Behavior: "deny", Source: "rule", Message: "message boş olamaz"}
	}
	if _, err := o.st.DelegateChat(ctx, from, hedef, metin, run); err != nil {
		slog.Warn("devir açılamadı", "err", err, "run", run.ID.String())
		return engine.Answer{Behavior: "deny", Source: "rule", Message: "devir açılamadı: " + err.Error()}
	}
	o.notify(ctx)
	slog.Info("devir açıldı", "from", from.ID.String(), "to", hedef.ID.String(), "run", run.ID.String())
	return engine.Answer{
		Behavior: "allow", Source: "rule",
		Message: "✓ '" + hedef.Name + "'e iletildi. Karşı bot kendi sohbetinde çalışıyor; sonucu bu sohbete '← cevap' çipiyle düşecek.",
	}
}

// botMesajiYaz — botun cevabını KOŞUNUN kanalına yazar (ADIM 10: grupta üye
// cevabı ekip zaman tüneline düşer, üyenin kendi sohbetine değil). Dönen id
// giden ekleri bağlamak için gerekli (ADIM 13).
func (o *Orchestrator) botMesajiYaz(ctx context.Context, run db.Run, bot db.Bot, metin string) (uuid.UUID, bool) {
	convID := run.ConversationID.UUID
	if !run.ConversationID.Valid {
		conv, err := o.st.BotConversation(ctx, bot)
		if err != nil {
			slog.Warn("bot sohbeti açılamadı, mesaj yazılmadı", "err", err)
			return uuid.Nil, false
		}
		convID = conv.ID
	}
	msg, err := o.st.Q.CreateMessage(ctx, db.CreateMessageParams{
		BotID: bot.ID, ConversationID: convID,
		RunID: uuid.NullUUID{UUID: run.ID, Valid: true},
		Role:  "bot", Text: metin, Origin: run.Origin,
	})
	if err != nil {
		slog.Warn("bot mesajı yazılamadı", "err", err)
		return uuid.Nil, false
	}
	return msg.ID, true
}

// gidenEkCipi — dosya gönderip hiç konuşmayan turun mesaj metni. Boş metinli
// balon "cevap gelmedi" gibi okunuyordu; dosya adları en azından ne geldiğini
// söyler (ekin kendisi kartta zaten görünür).
func gidenEkCipi(ekler []db.Attachment) string {
	adlar := make([]string, 0, len(ekler))
	for _, e := range ekler {
		adlar = append(adlar, e.Name)
	}
	return "📎 " + strings.Join(adlar, ", ")
}

// GidenEkAzami — bir koşunun gönderebileceği dosya sayısı. Kapı sınır değil
// döngü kırıcıdır: kendi çıktısını dosyalayan bir bot (ya da enjeksiyon almış
// bir koşu) sohbeti yüzlerce ekle doldurabilirdi.
const GidenEkAzami = 10

// answerSendFile — send_file çağrısını işler (ADIM 13). Devir gibi run-bağlamlı
// kanaldan gelir: hangi botun hangi koşusu olduğu KAPANIŞTAN bilinir, motor
// taklit edemez. Onay kartı açılmaz — dosya botun kendi kutusundan çıkıp yalnız
// SAHİBİNİN sohbetine düşer; bu bir dış eylem değil, cevabın kendisidir.
//
// Dosya çalışma dizininden KOPYALANIR: çalışma dizini bir sonraki koşuda
// değişebilir/silinebilir, oysa mesajdaki ek kalıcı olmak zorunda.
func (o *Orchestrator) answerSendFile(ctx context.Context, run db.Run, bot db.Bot, ask engine.Ask) engine.Answer {
	red := func(mesaj string) engine.Answer {
		return engine.Answer{Behavior: "deny", Source: "rule", Message: mesaj}
	}
	yol := strings.TrimSpace(ask.Path)
	if yol == "" {
		return red("path boş olamaz")
	}
	kutu, err := o.botWorkspace(bot.ID)
	if err != nil {
		return red("çalışma dizini açılamadı: " + err.Error())
	}
	tam, err := kutudakiYol(kutu, yol)
	if err != nil {
		return red(err.Error())
	}
	bilgi, err := os.Stat(tam)
	if err != nil {
		return red("dosya bulunamadı: " + yol)
	}
	if bilgi.IsDir() {
		return red("dizin gönderilemez — tek dosya ver (gerekirse arşivle)")
	}
	if bilgi.Size() == 0 {
		return red("dosya boş: " + yol)
	}
	if bilgi.Size() > attachments.EkAzami {
		return red(fmt.Sprintf("dosya çok büyük (%d MB); üst sınır %d MB",
			bilgi.Size()>>20, attachments.EkAzami>>20))
	}
	sayi, err := o.st.Q.CountRunAttachments(ctx, uuid.NullUUID{UUID: run.ID, Valid: true})
	if err != nil {
		return red("ek sayısı okunamadı: " + err.Error())
	}
	if sayi >= GidenEkAzami {
		return red(fmt.Sprintf("bu koşuda en fazla %d dosya gönderebilirsin", GidenEkAzami))
	}

	ad := attachments.TemizAd(firstNonBlank(ask.Name, filepath.Base(tam)))
	f, err := os.Open(tam)
	if err != nil {
		return red("dosya açılamadı: " + err.Error())
	}
	defer func() { _ = f.Close() }()

	att, err := o.st.SaveRunAttachment(ctx, o.cfg.DataDir, bot.ID, run.ID, ad, f)
	if err != nil {
		slog.Warn("giden ek kaydedilemedi", "err", err, "run", run.ID.String())
		return red("dosya kaydedilemedi: " + err.Error())
	}
	slog.Info("giden ek eklendi", "bot", bot.ID.String(), "run", run.ID.String(),
		"ad", att.Name, "tur", att.Kind, "bayt", att.Size)
	return engine.Answer{
		Behavior: "allow", Source: "rule",
		Message: "✓ '" + att.Name + "' cevabına eklendi — kullanıcı dosyayı sohbette görecek. " +
			"İçeriğini metne kopyalama.",
	}
}

// kutudakiYol — motorun verdiği yolu botun çalışma dizinine hapseder.
// Sembolik bağ ÇÖZÜLDÜKTEN sonra bakılır: aksi hâlde kutunun içine kurulmuş
// bir bağ (`ln -s /etc/shadow rapor.txt`) kapıdan geçerdi — Faz 6'daki Swift
// runner kaçışının aynısı.
func kutudakiYol(kutu, yol string) (string, error) {
	if !filepath.IsAbs(yol) {
		yol = filepath.Join(kutu, yol)
	}
	kok, err := filepath.EvalSymlinks(kutu)
	if err != nil {
		return "", errors.New("çalışma dizini çözülemedi: " + err.Error())
	}
	tam, err := filepath.EvalSymlinks(filepath.Clean(yol))
	if err != nil {
		return "", errors.New("dosya bulunamadı: " + filepath.Base(yol))
	}
	rel, err := filepath.Rel(kok, tam)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("yalnız kendi çalışma dizinindeki dosyaları gönderebilirsin")
	}
	return tam, nil
}

func firstNonBlank(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// devirHedefi — ask'taki bot_id/bot_name ile hedef botu bulur. bot_id
// list_bots çıktısından gelmelidir (yetkili kaynak); bot_name tam adıyla,
// belirsizse hata döner.
func (o *Orchestrator) devirHedefi(ctx context.Context, ask engine.Ask) (db.Bot, error) {
	if ask.BotID != "" {
		id, err := uuid.Parse(ask.BotID)
		if err != nil {
			return db.Bot{}, errors.New("bot_id geçersiz — list_bots'tan kopyala")
		}
		return o.st.Q.GetBot(ctx, id)
	}
	if ask.BotName == "" {
		return db.Bot{}, errors.New("bot_id ya da bot_name ver (kaynak: list_bots)")
	}
	bots, err := o.st.Q.ListBots(ctx)
	if err != nil {
		return db.Bot{}, err
	}
	var eslesen []db.Bot
	for _, b := range bots {
		if strings.EqualFold(b.Name, strings.TrimSpace(ask.BotName)) || strings.Contains(b.Name, strings.TrimSpace(ask.BotName)) {
			eslesen = append(eslesen, b)
		}
	}
	switch len(eslesen) {
	case 0:
		return db.Bot{}, fmt.Errorf("%q adında bot yok (list_bots ile bak)", ask.BotName)
	case 1:
		return eslesen[0], nil
	default:
		return db.Bot{}, fmt.Errorf("%q birden çok bota uyuyor — bot_id ile devret", ask.BotName)
	}
}

// devirCevapCipi — devir alan botun cevabı, devri BAŞLATAN botun sohbetine
// tek satırlık çip olarak düşer (ADIM 6). Yeni koşu açmaz: zaten derinlik
// koruması ikinci adımı yasaklar; bu çip kullanıcının ve router'ın bilgi
// için izlediği yüzeydir.
func (o *Orchestrator) devirCevapCipi(ctx context.Context, run db.Run, cevaplayan db.Bot, final string) {
	if run.Origin != OriginBot || !run.ReplyToRunID.Valid {
		return
	}
	kaynak, err := o.st.Q.GetRun(ctx, run.ReplyToRunID.UUID)
	if err != nil {
		slog.Warn("devir kaynağı bulunamadı", "err", err)
		return
	}
	if kaynak.Origin == OriginBot {
		return
	}
	alan, err := o.st.Q.GetBot(ctx, kaynak.BotID)
	if err != nil {
		slog.Warn("devri başlatan bot bulunamadı", "err", err)
		return
	}
	conv, err := o.st.BotConversation(ctx, alan)
	if err != nil {
		slog.Warn("devir çipi için sohbet açılamadı", "err", err)
		return
	}
	text := "← " + cevaplayan.Name + " cevabını verdi: " + devirCevapKırp(final) +
		" (tamamı " + cevaplayan.Name + " sohbetinde)"
	if _, err := o.st.Q.CreateMessage(ctx, db.CreateMessageParams{
		BotID: alan.ID, ConversationID: conv.ID,
		Role:        "event",
		Text:        text,
		Origin:      OriginBot,
		SenderBotID: uuid.NullUUID{UUID: cevaplayan.ID, Valid: true},
	}); err != nil {
		slog.Warn("devir cevap çipi yazılamadı", "err", err)
	}
}

// devirCevapKırp — çip tek satır kalır; cevabın tamamı karşı botun sohbetinde.
func devirCevapKırp(metin string) string {
	metin = strings.Join(strings.Fields(metin), " ")
	if r := []rune(metin); len(r) > 140 {
		return string(r[:140]) + "…"
	}
	return metin
}

// arkaPlan — AfterRun kancasını koşudan ayrı goroutine'de koşturur (ADIM 8).
// Kanca paniği koşu işçisini öldürmesin diye recover'lanır; tavan 90 sn.
func (o *Orchestrator) arkaPlan(ctx context.Context, bot db.Bot, run db.Run) {
	if o.cfg.AfterRun == nil {
		return
	}
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	o.wg.Add(1)
	go func() {
		defer o.wg.Done()
		defer cancel()
		defer func() {
			if r := recover(); r != nil {
				slog.Error("arka plan kancası panikledi", "err", r)
			}
		}()
		o.cfg.AfterRun(bg, bot, run)
	}()
}

func (o *Orchestrator) failRun(ctx context.Context, runID uuid.UUID, msg string) {
	slog.Error("koşu başarısız", "run", runID.String(), "err", msg)
	if err := o.st.Q.FailRun(ctx, db.FailRunParams{ID: runID, Error: msg}); err != nil {
		slog.Error("hata durumu yazılamadı", "run", runID.String(), "err", err)
	}
	o.hataCipi(ctx, runID, msg)
	payload, _ := json.Marshal(map[string]string{"error": msg})
	o.publishEvent(ctx, runID, string(engine.KindError), payload, nil)
	o.publishSettled(runID, "failed")
}

// sahipsizHata — reaper'ın yazdığı hata; SQL'deki metnin aynısı (FailStaleRuns).
// Kullanıcının gördüğü cümle bu: koşuyu süren süreç öldü (deploy, çökme,
// OOM) ve deneme hakkı da bitmişti.
const sahipsizHata = "koşu sahipsiz kaldı ve deneme hakkı bitti (kalp atışı kesildi)"

// HataCipiOneki — başarısızlık çipinin sabit öneki. İstemciler bu öneke
// bakarak çipi hata kartı olarak çizer (Swift: LiveStore.chatMessages).
// Telegram'ın kart metniyle de aynı cümle — aynı olay her yüzeyde aynı
// okunur.
const HataCipiOneki = "⚠️ Koşu başarısız: "

// hataCipi — başarısızlığı SOHBETE yazar.
//
// NEDEN: kullanıcı mesajını yazıyor, cevap gelmiyor ve ekranda HİÇBİR ŞEY
// olmuyordu; sebep yalnız sunucu logunda kalıyordu. Telegram tarafı zaten
// "⚠️ Koşu başarısız: …" gösteriyordu, Mac sessizdi. Sebebi görmek cevapsız
// kalmaktan iyidir. role='event': konuşan bot değil, anlatılan olay.
func (o *Orchestrator) hataCipi(ctx context.Context, runID uuid.UUID, msg string) {
	run, err := o.st.Q.GetRun(ctx, runID)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("hata çipi için koşu okunamadı", "run", runID.String(), "err", err)
		}
		return
	}
	// Sohbetsiz koşu (rutin damıtma, hafıza konsolidasyonu): yazacak yer yok.
	if !run.ConversationID.Valid {
		return
	}
	if _, err := o.st.Q.CreateMessage(ctx, db.CreateMessageParams{
		BotID:          run.BotID,
		ConversationID: run.ConversationID.UUID,
		RunID:          uuid.NullUUID{UUID: runID, Valid: true},
		Role:           "event",
		Text:           HataCipiOneki + hataKirp(msg),
		Origin:         run.Origin,
	}); err != nil && ctx.Err() == nil {
		slog.Warn("hata çipi yazılamadı", "run", runID.String(), "err", err)
	}
}

// SessizTurCipi — motorun metin üretmeden bitirdiği turun sohbet karşılığı.
// Bilgi çipi (hata değil): koşu gerçekten başarılı bitti, söyleyecek sözü
// çıkmadı.
const SessizTurCipi = "ℹ️ Tur cevapsız bitti — motor metin üretmedi."

func (o *Orchestrator) sessizTurCipi(ctx context.Context, run db.Run) {
	if _, err := o.st.Q.CreateMessage(ctx, db.CreateMessageParams{
		BotID:          run.BotID,
		ConversationID: run.ConversationID.UUID,
		RunID:          uuid.NullUUID{UUID: run.ID, Valid: true},
		Role:           "event",
		Text:           SessizTurCipi,
		Origin:         run.Origin,
	}); err != nil && ctx.Err() == nil {
		slog.Warn("sessiz tur çipi yazılamadı", "run", run.ID.String(), "err", err)
	}
}

// hataKirp — motor hataları çok uzun olabiliyor (yığın izi, kullanım metni);
// kart okunur kalsın.
func hataKirp(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return "bilinmeyen hata"
	}
	if r := []rune(msg); len(r) > 500 {
		return string(r[:500]) + "…"
	}
	return msg
}

// publishSettled — koşunun durumu yazıldıktan SONRA yayınlanan kapanış
// işareti. turn.completed motordan durum yazımından önce aktığı için canlı
// akış (SSE) kapanma kararını buna bakarak verir; yoksa tamamlanmış koşuda
// akış açık kalır ve istemcide "yazıyor" göstergesi asılı kalırdı.
// Kalıcılaştırılmaz: yeniden bağlanan istemci gerçeği runs.status'tan okur.
func (o *Orchestrator) publishSettled(runID uuid.UUID, status string) {
	payload, _ := json.Marshal(map[string]string{"status": status})
	o.bus.Publish(bus.Event{
		RunID: runID, Kind: bus.KindRunSettled, Payload: payload, At: time.Now().UTC(),
	})
}

// ── olay kalıcılaştırma ────────────────────────────────────────────────

// persistEvent: content.delta yalnız canlı yayına gider (DB şişmez);
// diğer her olay run_events'e yazılır, NDJSON tee'ye de düşer (Ek B.2-5).
func (o *Orchestrator) persistEvent(ctx context.Context, runID uuid.UUID, ev engine.Event, tee *eventTee) {
	payload := marshalEventPayload(ev)
	if ev.Kind == engine.KindAssistantDelta {
		o.bus.Publish(bus.Event{RunID: runID, Kind: string(ev.Kind), Payload: payload, At: time.Now().UTC()})
		return
	}
	o.publishEvent(ctx, runID, string(ev.Kind), payload, tee)
}

func (o *Orchestrator) publishEvent(ctx context.Context, runID uuid.UUID, kind string, payload []byte, tee *eventTee) {
	id, err := o.st.Q.InsertRunEvent(ctx, db.InsertRunEventParams{RunID: runID, Kind: kind, Payload: payload})
	if err != nil && ctx.Err() == nil {
		slog.Warn("koşu olayı yazılamadı", "run", runID.String(), "kind", kind, "err", err)
	}
	ev := bus.Event{ID: id, RunID: runID, Kind: kind, Payload: payload, At: time.Now().UTC()}
	o.bus.Publish(ev)
	if tee != nil {
		tee.write(ev)
	}
}

// eventPayload — run_events.payload / SSE data gövdesi (JSON).
type eventPayload struct {
	SessionID string          `json:"session_id,omitempty"`
	Model     string          `json:"model,omitempty"`
	Text      string          `json:"text,omitempty"`
	Error     string          `json:"error,omitempty"`
	Tool      *toolPayload    `json:"tool,omitempty"`
	Request   *requestPayload `json:"request,omitempty"`
	Result    *resultPayload  `json:"result,omitempty"`
}

type toolPayload struct {
	ID      string          `json:"id,omitempty"`
	Name    string          `json:"name,omitempty"`
	Input   json.RawMessage `json:"input,omitempty"`
	IsError bool            `json:"is_error,omitempty"`
}

type requestPayload struct {
	Kind     string   `json:"kind"`
	Tool     string   `json:"tool,omitempty"`
	Question string   `json:"question,omitempty"`
	Choices  []string `json:"choices,omitempty"`
	Behavior string   `json:"behavior,omitempty"`
	Source   string   `json:"source,omitempty"`
}

type resultPayload struct {
	Ok           bool    `json:"ok"`
	StopReason   string  `json:"stop_reason"`
	CostUSD      float64 `json:"cost_usd"`
	NumTurns     int     `json:"num_turns"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
}

func marshalEventPayload(ev engine.Event) []byte {
	p := eventPayload{SessionID: ev.SessionID, Model: ev.Model, Text: ev.Text, Error: ev.Err}
	if ev.Tool != nil {
		p.Tool = &toolPayload{ID: ev.Tool.ID, Name: ev.Tool.Name, Input: ev.Tool.Input, IsError: ev.Tool.IsError}
	}
	if ev.Request != nil {
		p.Request = &requestPayload{
			Kind: ev.Request.Kind, Tool: ev.Request.Tool, Question: ev.Request.Question,
			Choices: ev.Request.Choices, Behavior: ev.Request.Behavior, Source: ev.Request.Source,
		}
	}
	if ev.Result != nil {
		p.Result = &resultPayload{
			Ok: ev.Result.Ok, StopReason: ev.Result.StopReason, CostUSD: ev.Result.CostUSD,
			NumTurns: ev.Result.NumTurns, InputTokens: ev.Result.InputTokens, OutputTokens: ev.Result.OutputTokens,
		}
	}
	b, err := json.Marshal(p)
	if err != nil {
		return []byte(`{}`)
	}
	return b
}

// ── yardımcılar ────────────────────────────────────────────────────────

func (o *Orchestrator) botWorkspace(botID uuid.UUID) (string, error) {
	dir, err := workspace.Dir(o.cfg.DataDir, botID)
	if err != nil {
		slog.Error("bot çalışma dizini açılamadı", "err", err, "dizin", dir)
		return "", err
	}
	return dir, nil
}

// Hafıza bölümünün sistem promptunda kaplayabileceği azami karakter. Bütçe
// olmadan hafıza her hafta biraz daha büyür ve bir gün asıl görevi bağlam
// penceresinden iter (K20). ~2000 token karşılığı.
const (
	hafizaPromptBütçesi = 8000
	// Özet varken bütçe ikiye bölünür: damıtılmış metin ile özetten sonra
	// yazılmış taze kayıtlar. Taze pay küçük — bir gecelik yazım oraya rahat
	// sığar, sığmazsa zaten bir sonraki tur onu özete katacak.
	hafizaOzetBütçesi = 6000
	hafizaTazeBütçesi = hafizaPromptBütçesi - hafizaOzetBütçesi
	// Prompta girecek azami ham kayıt sayısı.
	hafizaSatirSiniri = 50
	// Sabit talimat bloğu HER koşuda basılır, o yüzden tavanı sert: blok
	// büyüdükçe her koşunun bağlam bütçesinden yer yer. Yirmi kural / 2000
	// karakter bir kullanıcının gerçekten aklında tuttuğu kadar; fazlası
	// zaten talimat değil hafızadır.
	sabitTalimatSiniri  = 20
	sabitTalimatBütçesi = 2000
)

// contextPrompt — katmanlı hafıza + etkin skill'ler sistem promptuna girer
// (PRD §8: scope sırası önce user, sonra bot; K11: skill = NASIL bilgisi).
// Hata akışı kırmaz: hafızasız koşu, koşmayan koşudan iyidir.
//
// Hafıza iki yoldan gelebilir (K20):
//
//	Özet belgesi — konsolidasyon turunun ürettiği damıtılmış metin. Yalnız o
//	tur yazdığı için koşular arası DEĞİŞMEZ; sistem promptu öneki kararlı
//	kalır ve motorun KV önbelleği her koşuda baştan ısınmaz.
//	Ham satırlar — özet henüz üretilmemişse (yeni bot, ilk gece gelmemiş)
//	eski davranış: kayıtlar tek tek basılır.
//
// Özet varken ESKİ kayıtlar tekrar basılmaz (aynı olgu iki kez söylenir, bütçe
// boşa gider), ama özetten SONRA yazılanlar basılır. Bu şart: özet gece
// üretiliyor ve tur bot başına en az 20 saatte bir koşuyor — taze satırlar
// olmasa kullanıcının "bundan sonra bana Erhan Bey de" demesi ile botun bunu
// öğrenmesi arasında neredeyse bir gün geçerdi.
func (o *Orchestrator) contextPrompt(ctx context.Context, botID uuid.UUID) string {
	var b strings.Builder

	// Sabit talimatlar hafızadan ÖNCE ve hafızadan AYRI yazılır: bunlar
	// damıtılmış bilgi değil, uyulacak kurallardır. Hafıza bloğunun içine
	// karışsalardı özet turunda "benzer" bir olguyla birleşebilir ya da bütçe
	// dolduğunda kırpılabilirlerdi — ikisi de sessiz kayıp.
	if talimat := o.sabitTalimatlar(ctx, botID); talimat != "" {
		b.WriteString("## Sabit talimatlar\n")
		b.WriteString("Bunları KULLANICI yazdı ve her koşuda geçerlidir; hafıza kayıtlarından önce gelirler.\n")
		b.WriteString(talimat)
		b.WriteString("\n")
	}

	if ozet, err := o.st.Q.GetMemorySummary(ctx, botID); err == nil && strings.TrimSpace(ozet.Content) != "" {
		b.WriteString("## Kalıcı hafıza (özet)\n")
		b.WriteString(kirpUTF8(ozet.Content, hafizaOzetBütçesi))
		if taze := o.tazeHafiza(ctx, botID, ozet.GeneratedAt); taze != "" {
			b.WriteString("\n\n### Özetten sonra eklenenler\n")
			b.WriteString(taze)
		}
		b.WriteString("\n\nDetay gerekirse `memory_search` / `memory_list` ile hafızaya bakabilirsin.\n")
	} else if mems, err := o.st.Q.ListMemoriesForPrompt(ctx, db.ListMemoriesForPromptParams{BotID: uuid.NullUUID{UUID: botID, Valid: true}, Limit: hafizaSatirSiniri}); err != nil {
		slog.Warn("hafıza okunamadı", "err", err)
	} else if len(mems) > 0 {
		b.WriteString("## Kalıcı hafıza\n")
		b.WriteString(kirpUTF8(hafizaSatirlari(mems), hafizaPromptBütçesi))
	}

	skills, err := o.st.Q.ListSkillsForBot(ctx, botID)
	if err != nil {
		slog.Warn("skill listesi okunamadı", "err", err)
	} else if len(skills) > 0 {
		b.WriteString("\n## Etkin skill'ler\n")
		for _, sk := range skills {
			fmt.Fprintf(&b, "### %s\nNe zaman: %s\n%s\n", sk.Name, sk.WhenToUse, sk.Instructions)
		}
	}
	return b.String()
}

// sabitTalimatlar — kullanıcının elle yazdığı kuralların prompt bloğu; yoksa
// boş dize (başlık da basılmaz — boş bir "Sabit talimatlar" başlığı modele
// "burada bir şey vardı" diye okunur).
//
// Kayıt sahibi kim olursa olsun içerik kullanıcı direktifi olarak sunulur:
// bloğa yalnız kullanıcı yazabilir (konsolidasyon ve `memory_save` bu kind'a
// dokunamaz), o yüzden burada "veri, talimat değil" çitine gerek yok — çit
// tam tersini söyler ve kuralı zayıflatırdı.
//
// Hata akışı kırmaz ama SESSİZ de kalmaz: talimatsız koşu, koşmayan koşudan
// iyidir; yine de kullanıcının yasağı okunamadıysa log'a düşmeli.
func (o *Orchestrator) sabitTalimatlar(ctx context.Context, botID uuid.UUID) string {
	mems, err := o.st.Q.ListStandingInstructions(ctx, db.ListStandingInstructionsParams{
		BotID: uuid.NullUUID{UUID: botID, Valid: true},
		Limit: sabitTalimatSiniri,
	})
	if err != nil {
		slog.Warn("sabit talimatlar okunamadı", "err", err)
		return ""
	}
	if len(mems) == 0 {
		return ""
	}
	var b strings.Builder
	for _, m := range mems {
		fmt.Fprintf(&b, "- %s\n", strings.TrimSpace(m.Content))
	}
	return kirpUTF8Not(b.String(), sabitTalimatBütçesi,
		"… (SABİT TALİMATLARIN TAMAMI SIĞMADI — kalanları göremiyorsun, kullanıcıya söyle)")
}

// hafizaSatirlari — kayıtları prompt satırlarına çevirir.
func hafizaSatirlari(mems []db.Memory) string {
	var b strings.Builder
	for _, m := range mems {
		fmt.Fprintf(&b, "- [%s/%s] %s\n", m.Scope, m.Kind, m.Content)
	}
	return b.String()
}

// tazeHafiza — özet üretildikten sonra yazılan/değişen kayıtların prompt
// bloğu; yoksa boş dize. Hata akışı kırmaz: taze satırsız koşu, koşmayan
// koşudan iyidir.
func (o *Orchestrator) tazeHafiza(ctx context.Context, botID uuid.UUID, ozetZamani time.Time) string {
	mems, err := o.st.Q.ListMemoriesForPromptSince(ctx, db.ListMemoriesForPromptSinceParams{
		BotID: uuid.NullUUID{UUID: botID, Valid: true},
		Since: ozetZamani,
		Limit: hafizaSatirSiniri,
	})
	if err != nil {
		slog.Warn("özet sonrası hafıza okunamadı", "err", err)
		return ""
	}
	if len(mems) == 0 {
		return ""
	}
	return kirpUTF8(hafizaSatirlari(mems), hafizaTazeBütçesi)
}

// kirpUTF8 — metni azami rune sayısına kırpar. İki incelik:
//
//	Rune sayarız, bayt değil: Türkçe metinde bayt kesimi çok baytlı bir
//	karakteri ortadan böler (aynı hata local_audit'te bir kez yaşandı).
//	Kesim satır sınırına çekilir: yarım kalmış bir hafıza satırı modele
//	eksik bir olgu olarak okunur — hiç olmamasından kötüdür.
//
// Satır sonu ARAMASI da rune dizisinde yapılır. Bayt indeksiyle rune eşiğini
// karşılaştırmak (`strings.LastIndexByte` sonucunu azami/2 ile kıyaslamak)
// Türkçe metinde eşiği yaklaşık iki katına şişirirdi: çoğu karakter 2 bayt
// olduğu için "yarıdan fazlasını atma" koruması gerçekte çeyrek noktada da
// geçer ve bütçenin büyük kısmı boşa giderdi.
func kirpUTF8(s string, azami int) string {
	return kirpUTF8Not(s, azami, "… (hafıza kırpıldı — tamamı için memory_list)")
}

// kirpUTF8Not — kırpma notunu çağıran seçer. Hafıza için "detayı memory_list
// ile al" doğru not; sabit talimat için YANLIŞ olurdu: o blok bir araçla
// tamamlanamaz, kesilen kural modelin göremeyeceği bir kuraldır ve bunu
// kullanıcının duyması gerekir.
func kirpUTF8Not(s string, azami int, not string) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= azami {
		return s
	}
	kesik := r[:azami]
	for i := len(kesik) - 1; i > azami/2; i-- {
		if kesik[i] == '\n' {
			kesik = kesik[:i]
			break
		}
	}
	return strings.TrimRight(string(kesik), "\n") + "\n" + not
}

func joinPrompts(parts ...string) string {
	var out []string
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, "\n\n")
}

// eventTee — koşu olaylarının ham NDJSON kopyası (debug altın madeni, Ek B.2-5).
// Yazma hatası akışı asla kırmaz.
type eventTee struct {
	f    *os.File
	warn sync.Once
}

func newEventTee(dataDir string, runID uuid.UUID) *eventTee {
	dir := filepath.Join(dataDir, "run-events")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return &eventTee{}
	}
	f, err := os.OpenFile(filepath.Join(dir, runID.String()+".ndjson"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return &eventTee{}
	}
	return &eventTee{f: f}
}

func (t *eventTee) write(ev bus.Event) {
	if t.f == nil {
		return
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return
	}
	if _, err := t.f.Write(append(line, '\n')); err != nil {
		t.warn.Do(func() { slog.Warn("olay tee dosyasına yazılamadı", "err", err) })
	}
}

func (t *eventTee) close() {
	if t.f != nil {
		_ = t.f.Close()
	}
}

// ── ekip kanalı (ADIM 10) ─────────────────────────────────────────────

// enqueueGrup — ekip sohbetine mesaj: mesaj EKİBİN kanalına yazılır, koşu
// @Adı mention'ıyla seçilen ya da varsayılan (ilk) üyeye açılır. Tek
// konuşmacı, sıralı — kuyruk zaten bot başına işliyor.
func (o *Orchestrator) enqueueGrup(ctx context.Context, ekip db.Bot, prompt, text, origin, key string, ekler []uuid.UUID) (db.Run, error) {
	uye, err := o.grupUyesi(ctx, ekip, text)
	if err != nil {
		return db.Run{}, err
	}
	run, created, err := o.st.GrupChat(ctx, ekip, uye, prompt, text, origin, key, ekler)
	if err != nil {
		return db.Run{}, err
	}
	if created {
		o.notify(ctx)
	}
	slog.Info("ekip koşusu açıldı", "ekip", ekip.Name, "uye", uye.Name)
	return run, nil
}

// grupUyesi — @Adı mention'ını tek üyeye, yoksa ilk üyeye çevirir. Belirsiz
// ya da bilinmeyen ad hatadır (sessiz yanlış yönlendirme onay kartından
// beterdir: fail-closed).
func (o *Orchestrator) grupUyesi(ctx context.Context, ekip db.Bot, text string) (db.Bot, error) {
	uyeler, err := o.st.Q.ListTeamMembers(ctx, ekip.ID)
	if err != nil {
		return db.Bot{}, err
	}
	if len(uyeler) == 0 {
		return db.Bot{}, fmt.Errorf("ekipte üye yok — önce üye ekle (PATCH /api/bots/%s)", ekip.ID)
	}
	ad := parseMention(text)
	if ad == "" {
		// position sıralı; ilk üye varsayılan konuşmacıdır.
		return uyeSatiri(uyeler[0]), nil
	}
	var eslesen []db.Bot
	for _, u := range uyeler {
		b := uyeSatiri(u)
		if strings.EqualFold(b.Name, ad) || strings.Contains(b.Name, ad) {
			eslesen = append(eslesen, b)
		}
	}
	switch len(eslesen) {
	case 0:
		return db.Bot{}, fmt.Errorf("%q adında üye yok — mention ile hedefle ya da boş bırak", ad)
	case 1:
		return eslesen[0], nil
	default:
		return db.Bot{}, fmt.Errorf("%q birden çok üyeye uyuyor — adı tam yaz", ad)
	}
}

// parseMention — metindeki ilk @Adı belirtimini döndürür (kelime sonuna dek).
func parseMention(text string) string {
	i := strings.Index(text, "@")
	if i < 0 {
		return ""
	}
	alan := strings.Fields(text[i:])[0]
	ad := strings.TrimPrefix(alan, "@")
	ad = strings.Trim(ad, ".,;:!?()")
	// "@" tek başına ya da yalnız noktalama ise mention sayılmaz.
	if ad == "" {
		return ""
	}
	return ad
}

// uyeSatiri — ListTeamMembers satırını bot kaydına çevirir (JOIN çıktısı ile
// bots satırı aynı alanları taşır, tipi farklıdır).
func uyeSatiri(u db.ListTeamMembersRow) db.Bot {
	return db.Bot{
		ID: u.ID, Kind: u.Kind, Name: u.Name, Description: u.Description,
		SystemPrompt: u.SystemPrompt, ColorPrimary: u.ColorPrimary,
		ColorSecondary: u.ColorSecondary, Section: u.Section, Engine: u.Engine,
		Model: u.Model, AllowedTools: u.AllowedTools, Pinned: u.Pinned,
		Temperature: u.Temperature, Extensions: u.Extensions,
	}
}

// ── ekip kanalı: koordinatör dağıtımı (ADIM 10 ikinci aşama) ──────────
//
// Model: ekipte AYRI bir "şef" satırı yok — kullanıcının mention'suz mesajı
// zaten position=0'daki üyeye gider ve o üye koordinatördür. Dağıtım, o
// üyenin cevabındaki @Ad çağrılarının AYNI kanalda yeni koşu açmasıyla olur.
// Ekibin kendisini koşturmak (kind='team' satırını bir bot gibi çalıştırmak)
// da düşünüldü ve elendi: her mesaja fazladan bir LLM turu bindirirdi ve
// kullanıcı ekip sohbetinde asıl işi yapan botun değil, aracının sesini
// duyardı.

// grupBaglami — koşu bir GRUP kanalında mı koşuyor? Öyleyse ekip satırını ve
// üyeleri döndürür. Grup değilse ok=false — koşuların çoğu birebir sohbettedir,
// bu bir hata değildir.
func (o *Orchestrator) grupBaglami(ctx context.Context, run db.Run) (db.Bot, []db.Bot, bool) {
	if !run.ConversationID.Valid {
		return db.Bot{}, nil, false
	}
	conv, err := o.st.Q.GetConversation(ctx, run.ConversationID.UUID)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("konuşma okunamadı", "run", run.ID.String(), "err", err)
		}
		return db.Bot{}, nil, false
	}
	if conv.Kind != "group" {
		return db.Bot{}, nil, false
	}
	ekip, err := o.st.Q.GetBot(ctx, conv.BotID)
	if err != nil {
		slog.Warn("ekip okunamadı", "konusma", conv.ID.String(), "err", err)
		return db.Bot{}, nil, false
	}
	satirlar, err := o.st.Q.ListTeamMembers(ctx, ekip.ID)
	if err != nil {
		slog.Warn("ekip üyeleri okunamadı", "ekip", ekip.Name, "err", err)
		return db.Bot{}, nil, false
	}
	uyeler := make([]db.Bot, 0, len(satirlar))
	for _, u := range satirlar {
		uyeler = append(uyeler, uyeSatiri(u))
	}
	return ekip, uyeler, len(uyeler) > 0
}

// ekipPrompt — grup kanalında koşan üyenin sistem promptuna giren blok: kanal
// kimin, kimler dinliyor, devir nasıl yapılır. Birebir sohbette boş döner.
//
// Liste HER TURDA prompta basılır (PRD Ek C.2 chief-of-staff kalıbı): üye
// listesi değişkendir, prompt bayatlar; bu yüzden blok kimlik/rol için
// yetkilidir ama "şu an müsait mi" gibi anlık bilgi taşımaz.
func (o *Orchestrator) ekipPrompt(ctx context.Context, run db.Run, bot db.Bot) string {
	ekip, uyeler, ok := o.grupBaglami(ctx, run)
	if !ok {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Ekip sohbeti: %s\n", ekip.Name)
	b.WriteString("Bu kanalda kullanıcı tek bir botla değil EKİPLE konuşuyor. " +
		"Cevabın ekibin ortak zaman tünelinde SENİN adınla görünür; kullanıcı ve " +
		"diğer üyeler aynı tüneli okur.\n\nÜyeler:\n")
	for _, u := range uyeler {
		isaret := ""
		if u.ID == bot.ID {
			isaret = "  ← SEN"
		}
		gorev := kirpUTF8(strings.Join(strings.Fields(u.Description), " "), grupRolBütçesi)
		fmt.Fprintf(&b, "- @%s — %s%s\n", u.Name, gorev, isaret)
	}
	if o.cfg.MaxGroupHops <= 0 {
		// Devir kapalı (MAX_GROUP_HOPS=0): liste yine basılır — üye kiminle
		// aynı kanalda olduğunu bilmeli — ama çağırma sözleşmesi verilmez.
		return b.String()
	}
	if kalan := o.cfg.MaxGroupHops - int(run.GroupHop); kalan > 0 {
		fmt.Fprintf(&b, "\nİşin bir parçası BAŞKA bir üyenin alanına giriyorsa, kendi payına düşeni "+
			"bitirdikten sonra cevabının sonunda @Adı ile ona seslen — çağırdığın üye aynı kanalda "+
			"devam eder ve ne beklediğini senin cümlenden okur. Birden çok üye çağırabilirsin; "+
			"bu zincirde %d devir hakkı kaldı. Kullanıcı tüneli zaten görüyor: başkasının yazdığını "+
			"tekrarlama, teşekkür/onay turu açtırma, gerek yoksa kimseyi çağırma.\n", kalan)
	} else {
		b.WriteString("\nDevir hakkı bitti: bu turda @ ile başka üye çağıramazsın. İşi kendin bitir ya da " +
			"kullanıcıya kimin devralması gerektiğini söyle.\n")
	}
	return b.String()
}

const (
	// grupRolBütçesi — üye listesinde bir rol tarifinin azami uzunluğu.
	// Liste her turda basılır; uzun tarifler asıl işin bağlamını yer.
	grupRolBütçesi = 200
	// grupDevirBütçesi — devredilen cevabın prompta gömülen azami uzunluğu.
	grupDevirBütçesi = 4000
	// grupHedefAzami — TEK cevabın uyandırabileceği azami üye sayısı.
	// MaxGroupHops yalnız DERİNLİĞİ sınırlar; genişlik açık kalsaydı yedi
	// kişilik bir ekipte üç devirlik zincir 7+49+343 koşuya kadar açılabilirdi
	// (her biri gerçek para). Üç, tasarımın da gösterdiği ölçü: bir mesaja
	// birkaç bot cevap verir, ekibin tamamı değil.
	grupHedefAzami = 3
)

// GrupSinirCipi — devir zinciri sınıra dayandığında sohbete düşen çip öneki.
// Mac istemcisi de bu metni tanır (tel protokolü: iki taraf tek metin).
const GrupSinirCipi = "⛔️ Ekip devri sınırına ulaşıldı"

// grupHop — biten grup koşusunun cevabındaki @Ad çağrılarını üyelere çözer ve
// her biri için AYNI kanalda yeni koşu açar. Sınır dolmuşsa koşu açılmaz,
// yerine sohbete çip düşer: zincirin neden durduğu görünmeli, yoksa kullanıcı
// çağrılan botu boşuna bekler.
func (o *Orchestrator) grupHop(ctx context.Context, run db.Run, konusan db.Bot, final string) {
	if o.cfg.MaxGroupHops <= 0 || strings.TrimSpace(final) == "" {
		return
	}
	ekip, uyeler, ok := o.grupBaglami(ctx, run)
	if !ok {
		return
	}
	hedefler := grupHedefleri(final, uyeler, konusan.ID)
	if len(hedefler) == 0 {
		return
	}
	if int(run.GroupHop) >= o.cfg.MaxGroupHops {
		o.grupSinirCipi(ctx, run,
			fmt.Sprintf("en çok %d devir", o.cfg.MaxGroupHops), hedefler)
		return
	}
	if len(hedefler) > grupHedefAzami {
		// Fazlası sessizce düşmez: kullanıcı kimin çağrılmadığını görsün,
		// gerekiyorsa kendisi @ ile çağırsın.
		o.grupSinirCipi(ctx, run,
			fmt.Sprintf("bir cevapta en çok %d üye", grupHedefAzami), hedefler[grupHedefAzami:])
		hedefler = hedefler[:grupHedefAzami]
	}
	prompt := grupDevirPrompt(ekip.Name, konusan.Name, final)
	acilan := 0
	for _, h := range hedefler {
		yeni, err := o.st.GrupHopChat(ctx, h, run.ConversationID.UUID, prompt,
			run.Origin, run.GroupHop+1, run.ID)
		if err != nil {
			slog.Warn("ekip devri açılamadı", "ekip", ekip.Name, "uye", h.Name, "err", err)
			continue
		}
		acilan++
		slog.Info("ekip devri açıldı", "ekip", ekip.Name, "cagiran", konusan.Name,
			"uye", h.Name, "hop", yeni.GroupHop, "run", yeni.ID.String())
	}
	if acilan > 0 {
		o.notify(ctx)
	}
}

// grupSinirCipi — sınıra dayanan zincirin sohbetteki izi. neden: hangi sınıra
// dayandığı (derinlik ya da tek cevaptaki üye sayısı).
func (o *Orchestrator) grupSinirCipi(ctx context.Context, run db.Run, neden string, atlanan []db.Bot) {
	if len(atlanan) == 0 {
		return
	}
	adlar := make([]string, 0, len(atlanan))
	for _, h := range atlanan {
		adlar = append(adlar, h.Name)
	}
	metin := fmt.Sprintf("%s (%s) — çağrılmayan: %s. Devam etmesi için sen @%s yazabilirsin.",
		GrupSinirCipi, neden, strings.Join(adlar, ", "), adlar[0])
	if _, err := o.st.Q.CreateMessage(ctx, db.CreateMessageParams{
		BotID:          run.BotID,
		ConversationID: run.ConversationID.UUID,
		RunID:          uuid.NullUUID{UUID: run.ID, Valid: true},
		Role:           "event",
		Text:           metin,
		Origin:         run.Origin,
	}); err != nil && ctx.Err() == nil {
		slog.Warn("grup sınır çipi yazılamadı", "run", run.ID.String(), "err", err)
	}
}

// grupDevirPrompt — çağrılan üyenin turu, çağıranın cevabıyla açılır. Metin
// kırpılır: uzun bir rapor devredilirken çağrılanın bağlam bütçesi asıl işine
// kalmalı (kanalın tamamı zaten üyenin kendi grup imlecinde duruyor).
func grupDevirPrompt(ekip, cagiran, final string) string {
	return fmt.Sprintf("[%s — ekip sohbeti] %s cevabında seni @ ile çağırdı:\n\n%s\n\n"+
		"Sıra sende: kendi alanına düşeni yap ve kanala kısa bir cevap yaz. "+
		"Kullanıcı bu tüneli okuyor — çağıranın söylediklerini tekrarlama.",
		ekip, cagiran, kirpUTF8Not(final, grupDevirBütçesi, "\n…(kısaltıldı)"))
}

// grupHedefleri — metindeki "@Ad" çağrılarını üyelere çözer (saf fonksiyon).
//
// Ad ARANIR, ad PARÇALANMAZ: üye adları boşluk içerebilir ("Lead
// Araştırmacısı"), bu yüzden önce her üyenin TAM adı metinde "@Ad" olarak
// aranır; tutmazsa "@" sonrası tek kelime tek bir üyeye ön ek/parça olarak
// uyuyorsa o üye alınır. Belirsiz ya da bilinmeyen ad SESSİZCE atlanır —
// burada metni kullanıcı değil model yazdı; iyi bir cevabı bir yazım hatası
// yüzünden hataya çevirmek yanlış olurdu (kullanıcının mention'ı hâlâ
// fail-closed: bkz. grupUyesi).
//
// Konuşanın kendisi hedef olamaz: bot kendi adını anarak sonsuz zincir açmasın.
func grupHedefleri(metin string, uyeler []db.Bot, konusanID uuid.UUID) []db.Bot {
	var hedefler []db.Bot
	secili := map[uuid.UUID]bool{konusanID: true}
	al := func(u db.Bot) {
		if secili[u.ID] {
			return
		}
		secili[u.ID] = true
		hedefler = append(hedefler, u)
	}
	// Her "@" TEK KEZ tüketilir. Önce tam ad denenir, tutmazsa o çağrının
	// kelimesi denenir — iki eşleştirme ayrı ayrı taransaydı "@Satış
	// Koordinatörü" hem koordinatörü bulur hem de artakalan "Satış" kelimesi
	// "Satış Outbound"a uyup İKİNCİ bir botu boşuna uyandırırdı.
	for i := 0; i < len(metin); {
		j := strings.Index(metin[i:], "@")
		if j < 0 {
			break
		}
		i += j + 1
		kalan := metin[i:]

		// 1) Tam ad, en uzunu kazanır ("@Satış Outbound" ile "@Satış" ayrımı).
		enIyi := -1
		for k := range uyeler {
			ad := uyeler[k].Name
			if len(ad) > 0 && len(ad) <= len(kalan) && strings.EqualFold(kalan[:len(ad)], ad) &&
				(enIyi < 0 || len(ad) > len(uyeler[enIyi].Name)) {
				enIyi = k
			}
		}
		if enIyi >= 0 {
			al(uyeler[enIyi])
			i += len(uyeler[enIyi].Name)
			continue
		}

		// 2) Tek kelimelik kısaltma: YALNIZ tek üyeye uyuyorsa.
		alanlar := strings.Fields(kalan)
		if len(alanlar) == 0 {
			break
		}
		kelime := strings.Trim(alanlar[0], ".,;:!?()\"'")
		i += len(alanlar[0])
		if kelime == "" {
			continue
		}
		altKelime := strings.ToLower(kelime)
		eslesen := -1
		for k := range uyeler {
			if strings.Contains(strings.ToLower(uyeler[k].Name), altKelime) {
				if eslesen >= 0 {
					eslesen = -1 // belirsiz: hiçbirini seçme
					break
				}
				eslesen = k
			}
		}
		if eslesen >= 0 {
			al(uyeler[eslesen])
		}
	}
	return hedefler
}
