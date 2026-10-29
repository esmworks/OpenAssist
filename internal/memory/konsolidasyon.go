// Package memory — gece hafıza konsolidasyonu (K20).
//
// Neden ayrı bir motor, bot rutini değil: rutinler `EnqueueChat` üzerinden
// koşar ve botun `resume_cursor`unu kullanır — yani botun SOHBETİNİN içine
// girer. Bakım işi olan konsolidasyon o sohbeti kirletir, imleci ilerletir ve
// her gece her bot için Aktivite/Görevler akışına bir koşu düşürür. Bu yüzden
// konsolidasyon kendi turunda, imleçsiz (her seferinde temiz oturum) ve
// araçsız çalışır: motordan tek bir JSON planı ister, planı core uygular.
//
// Güvenlik dengesi: burada bir model kullanıcının hafızasını yeniden yazıyor.
// Üç kilit var — (1) plan yalnız BİZİM verdiğimiz id'lere dokunabilir, yeni
// kayıt yaratamaz; (2) tek turda kayıtların yarısından fazlasını eleyen plan
// tümden reddedilir; (3) her yazım revizyonlu, `memory_revert` ile geri
// alınabilir.
package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/esmworks/OpenAssist/internal/engine"
	"github.com/esmworks/OpenAssist/internal/store"
	db "github.com/esmworks/OpenAssist/internal/store/db"
	"github.com/esmworks/OpenAssist/internal/workspace"
)

// Config — konsolidasyon turu ayarları.
type Config struct {
	// Interval — zamanlayıcı turu (0 = 1 saat). Tur SIKLIĞI değil, uygunluk
	// KONTROLÜ sıklığıdır: bot başına gerçek konsolidasyon MinAra'da bir olur.
	Interval time.Duration
	// MinAra — aynı bot iki konsolidasyon arasında en az bu kadar bekler
	// (0 = 20 saat). Günde bir gece koşusuna denk gelir.
	MinAra time.Duration
	// MinKayit — bu sayının altında hafızası olan bot konsolide edilmez
	// (0 = 5). Üç satırı birleştirmek için model çağırmaya değmez.
	MinKayit int
	// Model — konsolidasyon için kullanılacak model; boşsa botun modeli.
	// Ucuz bir model işaret etmek için var (iş damıtma, akıl yürütme değil).
	Model string
	// DataDir — bot çalışma dizinlerinin kökü (orkestratörle AYNI olmalı).
	// Boş bırakılırsa motor süreci sunucunun kendi dizininde koşar ve CLI'ın
	// oturum/önbellek yazımları konteynerde sessizce düşebilir.
	DataDir string
}

func (c Config) normalize() Config {
	if c.Interval <= 0 {
		c.Interval = time.Hour
	}
	if c.MinAra <= 0 {
		c.MinAra = 20 * time.Hour
	}
	if c.MinKayit <= 0 {
		c.MinKayit = 5
	}
	return c
}

// Consolidator — zamanlayıcı döngüsü.
type Consolidator struct {
	st      *store.Store
	engines map[string]engine.Executor
	cfg     Config
	done    chan struct{}
}

func New(st *store.Store, engines map[string]engine.Executor, cfg Config) *Consolidator {
	return &Consolidator{st: st, engines: engines, cfg: cfg.normalize(), done: make(chan struct{})}
}

func (c *Consolidator) Start(ctx context.Context) {
	go func() {
		defer close(c.done)
		t := time.NewTicker(c.cfg.Interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				c.Tick(ctx)
			}
		}
	}()
}

// Wait — döngünün kapanmasını bekler (graceful drain, §4).
func (c *Consolidator) Wait() { <-c.done }

// Rapor — bir botun tur sonucu (testler ve loglar için).
type Rapor struct {
	Bot        string
	Okunan     int
	Birlestime int
	Elenen     int
	OzetUzunuk int
	Atlandi    string // boşsa koştu; doluysa neden atlandığı
}

// Tick — bir zamanlayıcı turu. Hata tek botu düşürür, turu değil: bir botun
// bozuk hafızası diğerlerinin konsolidasyonunu engellemez.
func (c *Consolidator) Tick(ctx context.Context) {
	pol := c.st.LoadPolicy(ctx)
	// Sessiz saat tanımlıysa konsolidasyon oraya çekilir: kullanıcı gün
	// içinde bota sorarken hafızası altından değişmesin. Tanımlı değilse
	// (kullanıcı kapatmış) beklemek için sebep yok.
	if pol.QuietHoursStart != "" && !pol.InQuietHours(time.Now()) {
		return
	}
	bots, err := c.st.Q.ListBots(ctx)
	if err != nil {
		slog.Warn("konsolidasyon: bot listesi okunamadı", "err", err)
		return
	}
	for _, bot := range bots {
		rapor, err := c.ConsolidateBot(ctx, bot)
		switch {
		case err != nil:
			slog.Warn("konsolidasyon başarısız", "bot", bot.Name, "err", err)
		case rapor.Atlandi != "":
			slog.Debug("konsolidasyon atlandı", "bot", bot.Name, "neden", rapor.Atlandi)
		default:
			slog.Info("hafıza konsolide edildi", "bot", bot.Name,
				"okunan", rapor.Okunan, "birleştirme", rapor.Birlestime, "elenen", rapor.Elenen)
		}
	}
}

// ConsolidateBot — tek botun hafızasını konsolide eder. Testler doğrudan
// çağırır (zamanlayıcıyı beklemeden).
func (c *Consolidator) ConsolidateBot(ctx context.Context, bot db.Bot) (Rapor, error) {
	rapor := Rapor{Bot: bot.Name}

	// Tek kopya kilidi. Zamanlayıcı tek kopya varsayar (compose replicas:1),
	// ama deploy sırasında eski ve yeni kap kısa süre birlikte koşar: iki tur
	// aynı kayıtları okuyup iki ayrı plan uygularsa ikincisi artık geçersiz
	// bir plan yazar. Rutin motoru aynı riski FOR UPDATE SKIP LOCKED ile
	// karşılıyor; kuyruk satırı olmayan bu iş için karşılığı advisory kilit.
	birak, kilitAlindi, err := c.st.TryAdvisoryLock(ctx, store.HafizaKilidi(bot.ID))
	if err != nil {
		return rapor, fmt.Errorf("konsolidasyon kilidi: %w", err)
	}
	if !kilitAlindi {
		rapor.Atlandi = "başka bir kopya bu botu konsolide ediyor"
		return rapor, nil
	}
	defer birak()

	// Kilit alındıktan SONRA okunur: kilidi bekleyen kopya, kazanan kopyanın
	// tazelediği generated_at'i görüp atlar.
	if ozet, err := c.st.Q.GetMemorySummary(ctx, bot.ID); err == nil {
		if time.Since(ozet.GeneratedAt) < c.cfg.MinAra {
			rapor.Atlandi = "yakın zamanda konsolide edildi"
			return rapor, nil
		}
	}

	mems, err := c.st.Q.ListMemoriesForConsolidation(ctx, uuid.NullUUID{UUID: bot.ID, Valid: true})
	if err != nil {
		return rapor, fmt.Errorf("hafıza okunamadı: %w", err)
	}
	rapor.Okunan = len(mems)
	if len(mems) < c.cfg.MinKayit {
		rapor.Atlandi = fmt.Sprintf("%d kayıt (asgari %d)", len(mems), c.cfg.MinKayit)
		return rapor, nil
	}

	exec, ok := c.engines[bot.Engine]
	if !ok {
		return rapor, fmt.Errorf("motor kayıtlı değil: %s", bot.Engine)
	}
	model := c.cfg.Model
	if model == "" {
		model = bot.Model
	}
	cwd, err := workspace.Dir(c.cfg.DataDir, bot.ID)
	if err != nil {
		slog.Warn("konsolidasyon çalışma dizini açılamadı", "bot", bot.Name, "err", err)
	}

	basladi := time.Now()
	res, err := exec.SendTurn(ctx, engine.TurnInput{
		SystemPrompt: sistemPromptu,
		Prompt:       kullaniciPromptu(bot, mems),
		Model:        model,
		CWD:          cwd,
		// İmleç YOK: konsolidasyon her seferinde temiz oturumda koşar, botun
		// sohbet zincirine dokunmaz.
		ResumeCursor: "",
		// Araç YOK: iş damıtma. NoTools MCP yüzeylerini hiç takmaz ve gömülü
		// araçları kapatır — boş AllowedTools yetmezdi, katalog görünür kalır
		// ve model her denemede bir tur yakardı. OnAsk da nil: motor yine de
		// bir şey sorarsa fail-closed reddedilir.
		NoTools: true,
	}, func(engine.Event) {})
	// Maliyet turun sonucundan bağımsız kaydedilir: reddedilen bir plan da
	// para harcamıştır, /api/stats onu görmezse fatura panoyla uzlaşmaz.
	maliyetKaydet(c.st, ctx, bot, model, "consolidation", "hafıza konsolidasyonu", basladi, res, err)
	if err != nil {
		return rapor, fmt.Errorf("motor turu: %w", err)
	}
	if res == nil || !res.Ok {
		return rapor, errors.New("motor turu başarısız döndü")
	}

	ham, err := planCoz(res.FinalText)
	if err != nil {
		return rapor, err
	}
	plan, err := planDogrula(ham, mems)
	if err != nil {
		return rapor, err
	}

	ozet := strings.TrimSpace(plan.Ozet)
	if ozet == "" {
		return rapor, errors.New("plan özet üretmedi")
	}
	if err := store.SirTara(ozet); err != nil {
		return rapor, fmt.Errorf("özet reddedildi: %w", err)
	}

	rapor.Birlestime, rapor.Elenen, err = c.planUygula(ctx, bot, plan, ozet, len(mems))
	if err != nil {
		return rapor, err
	}
	rapor.OzetUzunuk = len([]rune(ozet))
	return rapor, nil
}

// planUygula — birleştirmeleri, elemeleri ve özeti TEK transaction'da yazar.
//
// Atomiklik burada isteğe bağlı değil: iki birleştirmenin ilki yazılıp ikincisi
// çakışırsa (aynı kapsamda aynı içerik) kullanıcı hem birleşmiş hem birleşmemiş
// kayıtların karışımıyla, üstelik eski özetle kalırdı. Yarısı uygulanmış bir
// hafıza, hiç uygulanmamıştan kötüdür.
//
// Sıra da önemli: önce hedefin içeriği güncellenir, sonra kaynaklar katılmış
// işaretlenir. Ters sırada bir hata olursa kayıtlar hem elenmiş hem birleşmemiş
// kalırdı.
func (c *Consolidator) planUygula(ctx context.Context, bot db.Bot, plan CozulmusPlan, ozet string, kaynakSayisi int) (birlestirme, elenen int, err error) {
	err = c.st.InMemoryTx(ctx, func(w *store.MemoryWriter) error {
		birlestirme, elenen = 0, 0 // yeniden deneme ihtimaline karşı sayaçlar sıfırlanır
		for _, m := range plan.Birlestir {
			if _, err := w.Revise(ctx, m.Tut, m.Icerik,
				store.YazarKonsolidasyon, m.Gerekce, uuid.NullUUID{}); err != nil {
				return fmt.Errorf("birleştirme yazılamadı (%s): %w", m.Tut, err)
			}
			for _, kaynak := range m.Kat {
				if err := w.Supersede(ctx, kaynak, uuid.NullUUID{UUID: m.Tut, Valid: true}); err != nil {
					return fmt.Errorf("katma yazılamadı (%s): %w", kaynak, err)
				}
			}
			birlestirme++
		}
		for _, d := range plan.Ele {
			// superseded_by boş: "emekliye ayrıldı", "başkasına katıldı" değil.
			if err := w.Supersede(ctx, d.ID, uuid.NullUUID{}); err != nil {
				return fmt.Errorf("eleme yazılamadı (%s): %w", d.ID, err)
			}
			elenen++
		}
		// Özet de aynı transaction'da: kayıtlar birleşip özet yazılamazsa
		// prompt hafızanın artık var olmayan haline atıfta bulunurdu.
		if _, err := w.UpsertSummary(ctx, bot.ID, ozet, int32(kaynakSayisi)); err != nil {
			return fmt.Errorf("özet yazılamadı: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return birlestirme, elenen, nil
}

// maliyetKaydet — arka plan turunun maliyetini koşu defterine (runs) yazar.
//
// Bakım turu kuyruğa girmez ama para harcar: 10 botlu bir kurulumda ayda ~300
// çağrı. Kaydedilmezse Faz 9 maliyet panosu toplamı olduğundan düşük gösterir
// ve kullanıcı faturayla paneli uzlaştıramaz. Aktivite akışı bu origin'i
// filtreler, yani defter dolar ama akış kirlenmez. origin çağırandan gelir:
// konsolidasyon | inference (ADIM 8, K20).
//
// Kayıt hatası turu düşürmez: maliyet defteri, işin kendisinden daha az önemli.
func maliyetKaydet(st *store.Store, ctx context.Context, bot db.Bot, model, origin, prompt string, basladi time.Time, res *engine.TurnResult, turHatasi error) {
	p := db.RecordBackgroundRunParams{
		BotID: bot.ID, Prompt: prompt, Engine: bot.Engine, Model: model,
		Origin: origin, Status: "completed", StartedAt: &basladi,
	}
	if res != nil {
		p.CostUsd, p.InputTokens, p.OutputTokens = res.CostUSD, res.InputTokens, res.OutputTokens
		p.NumTurns = int32(res.NumTurns)
	}
	if turHatasi != nil || res == nil || !res.Ok {
		p.Status = "failed"
		p.Error = origin + " turu başarısız"
		if turHatasi != nil {
			p.Error = turHatasi.Error()
		}
	}
	if _, err := st.Q.RecordBackgroundRun(ctx, p); err != nil {
		slog.Warn("arka plan turu deftere yazılamadı", "bot", bot.Name, "origin", origin, "err", err)
	}
}
