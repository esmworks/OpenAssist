// core — OpenAssist kontrol düzlemi giriş noktası.
//
// Alt komutlar:
//
//	serve        HTTP yüzeyini başlatır (varsayılan)
//	migrate      gömülü göçleri uygular ve çıkar
//	healthcheck  çalışan sunucunun /healthz ucunu yoklar — distroless imajda
//	             curl/wget yoktur, compose healthcheck bu alt komutu kullanır (PRD §4)
//	version      sürüm bilgisini basar
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/esmworks/OpenAssist/internal/bus"
	"github.com/esmworks/OpenAssist/internal/config"
	"github.com/esmworks/OpenAssist/internal/connectors/google"
	"github.com/esmworks/OpenAssist/internal/coretools"
	"github.com/esmworks/OpenAssist/internal/crypto"
	"github.com/esmworks/OpenAssist/internal/engine"
	"github.com/esmworks/OpenAssist/internal/engine/pi"
	"github.com/esmworks/OpenAssist/internal/gateway"
	"github.com/esmworks/OpenAssist/internal/httpapi"
	"github.com/esmworks/OpenAssist/internal/memory"
	"github.com/esmworks/OpenAssist/internal/orchestrator"
	"github.com/esmworks/OpenAssist/internal/providers"
	"github.com/esmworks/OpenAssist/internal/publichttp"
	"github.com/esmworks/OpenAssist/internal/routines"
	"github.com/esmworks/OpenAssist/internal/runner"
	"github.com/esmworks/OpenAssist/internal/store"
	db "github.com/esmworks/OpenAssist/internal/store/db"
	"github.com/esmworks/OpenAssist/internal/telegram"
	"github.com/esmworks/OpenAssist/internal/version"
)

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	var err error
	switch cmd {
	case "serve":
		err = serve()
	case "migrate":
		err = migrate()
	case "healthcheck":
		err = healthcheck()
	case "version":
		fmt.Println(version.String())
	default:
		fmt.Fprintf(os.Stderr, "bilinmeyen komut: %q (serve|migrate|healthcheck|version)\n", cmd)
		os.Exit(2)
	}
	if err != nil {
		slog.Error(cmd+" başarısız", "err", err)
		os.Exit(1)
	}
}

func serve() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	lvl, _ := cfg.SlogLevel() // Load doğruladı
	opts := &slog.HandlerOptions{Level: lvl}
	var handler slog.Handler = slog.NewTextHandler(os.Stderr, opts)
	if cfg.LogFormat == "json" {
		handler = slog.NewJSONHandler(os.Stderr, opts) // log toplayıcılar (Faz 9)
	}
	slog.SetDefault(slog.New(handler))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	deps := httpapi.Deps{}
	var orch *orchestrator.Orchestrator
	var gsvc *google.Service
	if cfg.DatabaseURL != "" {
		st, err := store.Open(ctx, cfg.DatabaseURL)
		if err != nil {
			return err
		}
		defer st.Close()
		if cfg.MigrateOnStart {
			if err := st.Migrate(ctx); err != nil {
				return err
			}
		}

		ocfg := orchestrator.DefaultConfig()

		// Mac runner köprüsü (Faz 6, K7): her zaman açık — Mac bağlı değilken
		// /mcp/mac araç listesi boştur, motor araçları hiç görmez (K2).
		hub := runner.NewHub()
		hub.Audit = func(actx context.Context, device, tool string, args json.RawMessage, ok bool, result string) {
			// Denetim kaydı akışı kırmaz; koşu bağlamı MCP katmanında
			// bilinmediğinden run_id boş kalır (korelasyon v1.5).
			if len(args) == 0 {
				args = []byte("{}")
			}
			if err := st.Q.RecordLocalAudit(actx, db.RecordLocalAuditParams{
				Device: device, Tool: tool, Args: args, Ok: ok, Result: clip(result, 2000),
			}); err != nil {
				slog.Warn("local_audit yazılamadı", "err", err)
			}
		}
		deps.RunnerWS = hub.WSHandler()
		deps.MacGateway = gateway.NewDynamic(hub.GatewayTools).Handler()
		// MCP adresleri İÇ dinleyiciden türetilir (httpapi.NewInternal), özel
		// API'den değil: özel API bearer ister ve bir alan adına bağlanabilir,
		// MCP ise makine-içi ve belirteçsizdir.
		ocfg.ToolClasses = runner.StaticClasses()

		// MCP gateway (Faz 4/7): her zaman açık — K13 yönetim araçları
		// (bot/rutin/hafıza/skill) buradan sunulur; Google yapılandırılmışsa
		// konektör araçları da eklenir. Sınıf bildirimleri onay kapısına gider.
		gw := gateway.New()
		ctools := coretools.NewService(st)
		coretools.Register(gw, ctools)

		// Mühür Google'a bağlı DEĞİL: sağlayıcı anahtarları da aynı yoldan
		// saklanıyor. MASTER_KEY yoksa sealer nil kalır ve anahtar YAZILAMAZ
		// (sessizce düz metne düşmek yerine özellik kapanır).
		var sealer *crypto.Sealer
		if cfg.MasterKey != "" {
			var err error
			if sealer, err = crypto.New(cfg.MasterKey); err != nil {
				return err
			}
		}
		if cfg.GoogleEnabled() {
			gsvc = google.NewService(cfg.GoogleClientID, cfg.GoogleClientSecret,
				cfg.PublicBaseURL+"/oauth/google/callback", st.Q, sealer)
			google.RegisterTools(gw, gsvc)
		}
		deps.Gateway = gw.Handler()
		for tool, cls := range gw.Classifications() {
			ocfg.ToolClasses[tool] = cls // runner sınıflarının üstüne eklenir
		}
		gatewayURL := localURL(cfg.InternalHTTPAddr, "/mcp")
		slog.Info("mcp gateway açık", "url", gatewayURL, "araç", len(gw.Tools()))

		// Sağlayıcı kaydı (göç 00016): tarifler DB'de yaşar, koşu yolu her
		// turda oradan okur. Ortamdaki PI_PROVIDER_* yalnız TABLO BOŞKEN
		// tohum olarak girer — bu özellikten önceki kurulumlar kesintisiz
		// geçsin diye; sonrasında tek kaynak DB'dir.
		psvc := providers.New(st, sealer)
		if err := psvc.Tohumla(ctx, piTohumu(cfg)); err != nil {
			return err
		}
		deps.Providers = psvc

		// Kimlik yoklaması: tohumlamadan SONRA ve ORTAMA değil TABLOYA bak —
		// tarifin yaşadığı yer artık orası. Hiç sağlayıcı yokken pi'nin
		// seçebileceği tek bir model bile olmaz ve her koşu düşer; sebebi
		// açılışta bir kez, ne yapılacağını söyleyerek yaz.
		if n, err := st.Q.CountProviders(ctx); err == nil && n == 0 && len(cfg.PiEnv) == 0 {
			slog.Warn("tanımlı sağlayıcı yok — koşular model bulamayacak",
				"yapılacak", "Mac'te Ayarlar › Modeller'den sağlayıcı (adres + API anahtarı) ve model ekle")
		}

		b := bus.New()
		// K18 motor kaydı: tek motor pi. MCP yüzeyleri (çekirdek gateway +
		// Mac runner) motora oa-gateway EKLENTİSİYLE girer — pi'nin yerleşik
		// MCP istemcisi yoktur, bu bilinçli bir tasarım kararıdır ve
		// çekirdeğin dışındaki her şey eklentiyle takılır.
		piDurum := motorDurumDizini(cfg.DataDir)
		piAdapter := &pi.Adapter{
			CLI: cfg.PiCLI,
			// Oturumlar, eklentiler ve models.json VERİ DİZİNİNDE yaşasın:
			// kap içinde kalırsa her dağıtım tüm sohbetlerin imlecini
			// geçersiz kılar.
			ConfigDir:     piDurum,
			ExtensionsDir: filepath.Join(piDurum, "extensions"),
			Providers:     psvc.Tarifler,
			DefaultModel:  cfg.PiDefaultModel,
			Thinking:      cfg.PiThinking,
			Gateways: []pi.Gateway{
				{Name: gateway.ServerName, URL: gatewayURL},
				{Name: "mac", URL: localURL(cfg.InternalHTTPAddr, "/mcp/mac")},
			},
			Env: cfg.PiEnv,
		}
		engines := map[string]engine.Executor{"pi": piAdapter}
		slog.Info("motor kayıtlı", "motor", "pi")
		ocfg.Workers = cfg.Workers
		ocfg.ApprovalWait = time.Duration(cfg.ApprovalWaitSeconds) * time.Second
		ocfg.MaxGroupHops = cfg.MaxGroupHops
		ocfg.DataDir = cfg.DataDir

		// Telegram kanalı (Faz 5, K16): gateway orkestratörden önce kurulur
		// ki çok kanallı yarış kancası (OnApprovalResolved) bağlanabilsin.
		var tg *telegram.Gateway
		if cfg.TelegramEnabled() {
			tcfg := telegram.DefaultConfig()
			tcfg.ToolClasses = ocfg.ToolClasses
			tcfg.DataDir = cfg.DataDir // Telegram'dan gelen ekler buraya iner (ADIM 5)
			tg = telegram.New(telegram.NewAPI(cfg.TelegramBotToken), st, nil, b, tcfg)
			ocfg.OnApprovalResolved = tg.OnApprovalResolved
		}

		// Koşu sonrası hafıza çıkarımı (ADIM 8, K20): AfterRun kancası koşudan
		// ayrı, imleçsiz/araçsız bir turda transkripti okur; öneriler sır
		// kapısı + dedup + revizyonlu normal yazma yolundan geçer. Kanca
		// orkestratörden ÖNCE bağlanmalı — Config kopyalanır.
		if cfg.MemoryInferAfterRun {
			ocfg.AfterRun = memory.NewInferrer(st, engines, cfg.DataDir).AfterRun
		}
		orch = orchestrator.New(st, engines, b, ocfg)
		orch.Start(ctx)
		ctools.SetEnqueuer(orch)

		// Rutin motoru (Faz 7, K11/K14): vadesi gelen rutinleri koşuya çevirir.
		beginTx := func(ctx context.Context) (db.DBTX, func(context.Context) error, func(context.Context) error, error) {
			tx, err := st.Pool().Begin(ctx)
			if err != nil {
				return nil, nil, nil, err
			}
			return tx, tx.Commit, tx.Rollback, nil
		}
		rtn := routines.New(beginTx, orch, 30*time.Second)
		rtn.Start(ctx)
		defer rtn.Wait() // zamanlayıcı turu drain edilir (deploy koruması, §6)

		// Hafıza konsolidasyonu (Faz 11, K20): gece turu hafızayı birleştirir
		// ve prompta giren özeti üretir. Rutin motorundan AYRI çünkü botun
		// sohbetine girmez — imleçsiz, araçsız, kendi turunda koşar.
		mcon := memory.New(st, engines, memory.Config{DataDir: cfg.DataDir})
		mcon.Start(ctx)
		defer mcon.Wait()

		// Tohum botlar (K13): hiç bot yoksa ilk konuşmanın muhatabı olan
		// Kişisel Asistan (router) ve v1 hedef botu Gelen Kutusu yaratılır.
		if err := seedDefaultBots(ctx, st); err != nil {
			return err
		}

		if tg != nil {
			tg.SetOrchestrator(orch)
			if err := seedTelegramAllowlist(ctx, st, cfg.TelegramAllowedChatIDs); err != nil {
				return err
			}
			go func() {
				if err := tg.Run(ctx); err != nil && ctx.Err() == nil {
					slog.Error("telegram gateway durdu", "err", err)
				}
			}()
		}

		deps.DB, deps.Store, deps.Orch, deps.Bus = st, st, orch, b
		deps.Engines = engines // /api/engines aynı haritayı tanıtır (K18)
		deps.DataDir = cfg.DataDir
	} else {
		slog.Warn("DATABASE_URL boş — veritabanısız iskelet modunda çalışılıyor (yalnız /healthz)")
	}

	srv := httpapi.New(cfg, deps)
	pub := publichttp.New(cfg, publichttp.Deps{Google: gsvc})
	// İç yüzey: MCP (/mcp, /mcp/mac) — loopback, belirteçsiz. İskelet modunda
	// gateway kurulmadığı için nil döner.
	internal := httpapi.NewInternal(cfg, deps)
	errc := make(chan error, 3)
	go func() { errc <- srv.ListenAndServe() }()
	go func() { errc <- pub.ListenAndServe() }()
	if internal != nil {
		go func() { errc <- internal.ListenAndServe() }()
	}
	slog.Info("openassist core ayakta", "addr", cfg.HTTPAddr, "public", cfg.PublicHTTPAddr,
		"mcp", cfg.InternalHTTPAddr, "version", version.String())

	select {
	case <-ctx.Done():
		// Nazik kapanış: yeni iş alınmaz, çalışan koşular drain edilir (PRD §6).
	case err := <-errc:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	shutdownErr := srv.Shutdown(shutdownCtx)
	_ = pub.Shutdown(shutdownCtx) // public yüzeyde uzun ömürlü istek yok
	if orch != nil {
		orch.Wait() // stop_grace_period bu bekleyişi kapsar (compose 60s)
	}
	// MCP EN SONA: drain edilen koşu hâlâ araç çağırır. Shutdown boştaki
	// bağlantıları kapatıp dinleyiciyi bıraktığı için erken kapatmak, yeni
	// bağlantı açan bir araç çağrısını "connection refused" ile düşürürdü —
	// yarım iş kaybolmasın diye orch.Wait()'ten sonra.
	if internal != nil {
		_ = internal.Shutdown(shutdownCtx)
	}
	return shutdownErr
}

// seedDefaultBots — ilk açılışta (hiç bot yokken) K13 onboarding'inin
// muhatabı Kişisel Asistan (router) ve v1 hedef botu Gelen Kutusu yaratılır.
// Router botun allowed_tools'u yalnız HAFİF yönetim araçlarını içerir —
// rutin CRUD ve bot silme onay kapısına düşer (K13 onay dengesi).
func seedDefaultBots(ctx context.Context, st *store.Store) error {
	bots, err := st.Q.ListBots(ctx)
	if err != nil || len(bots) > 0 {
		return err
	}
	routerTools := append(coretools.LightTools(), pi.AskUserTool, pi.AskBotTool)
	if _, err := st.Q.CreateBot(ctx, db.CreateBotParams{
		Kind: "bot", Name: "Kişisel Asistan",
		Description: "Sen kullanıcının kişisel asistanı ve ön bürosusun (router). İlk konuşmada kullanıcıyı tanı: " +
			"işini, araçlarını, tekrar eden görevlerini sor; öğrendiklerini memory_save ile kalıcılaştır. " +
			"İhtiyaca göre yeni botlar kur (create_bot), rol tariflerini kullanıcıyla pazarlık ederek netleştir, " +
			"birlikte çalışacak botları create_team ile EKİP yap (ekibin kendi grup sohbeti olur; ilk üye " +
			"koordinatördür ve cevabında @Ad ile diğerlerini çağırır), " +
			"rutin öner (create_routine onaylıdır; oluşturunca run_routine_now ile test et). " +
			"İşleri ask_bot ile doğru bota devret — bot id'sini list_bots'tan al; devirleri açıkça söyle. " +
			"Cevap '← … cevabı' çipiyle sohbete düşer.",
		ColorPrimary: "#b18cf0", ColorSecondary: "#7b4fd6",
		Engine: "pi", AllowedTools: routerTools,
		Extensions: []string{pi.OAGateway},
	}); err != nil {
		return fmt.Errorf("router bot tohumlanamadı: %w", err)
	}
	if _, err := st.Q.CreateBot(ctx, db.CreateBotParams{
		Kind: "bot", Name: "Gelen Kutusu",
		Description: "Sen e-posta asistanısın. Gelen kutusunu tarar, cevap bekleyenleri bulur ve TASLAK hazırlarsın. " +
			"Varsayılan mod: hazırla, gönderme (K12) — gmail_create_draft kullan, gönderim (gmail_send_draft) her zaman onaya düşer. " +
			"Gece koşularında taslaklar sabah Onay Merkezi'nde bekler. Sonuçları beş bölümde raporla: " +
			"Olgular · Çıkarımlar · Tamamlanan · Onay bekleyen · Cevaplanamayan.",
		ColorPrimary: "#7ed6a0", ColorSecondary: "#35a25f",
		Engine: "pi",
		AllowedTools: []string{
			"mcp__gw__gmail_search", "mcp__gw__gmail_read", "mcp__gw__gmail_create_draft",
			"mcp__gw__calendar_list_events", pi.AskUserTool,
		},
		Extensions: []string{pi.OAGateway},
	}); err != nil {
		return fmt.Errorf("gelen kutusu botu tohumlanamadı: %w", err)
	}
	slog.Info("tohum botlar oluşturuldu", "botlar", "Kişisel Asistan, Gelen Kutusu")
	return nil
}

// seedTelegramAllowlist — env'deki izinli chat_id'leri allowlist'e yazar.
// Kaldırma elle yapılır (DELETE /api/telegram/chats) — env'den düşen id
// otomatik silinmez, kasıtlı: yanlış yapılandırma sessizce erişim kesmesin.
func seedTelegramAllowlist(ctx context.Context, st *store.Store, ids []int64) error {
	for _, id := range ids {
		if _, err := st.Q.UpsertTelegramChat(ctx, db.UpsertTelegramChatParams{
			ChatID: id, Label: "env",
		}); err != nil {
			return fmt.Errorf("telegram allowlist yazılamadı (%d): %w", id, err)
		}
	}
	return nil
}

func migrate() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.DatabaseURL == "" {
		return errors.New("migrate için DATABASE_URL gerekli")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return err
	}
	slog.Info("göçler uygulandı")
	return nil
}

func healthcheck() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(healthURL(cfg.HTTPAddr))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz durum kodu %d", resp.StatusCode)
	}
	return nil
}

// healthURL, dinleme adresini yerel yoklama URL'ine çevirir
// (":8080" ve "0.0.0.0:8080" → 127.0.0.1).
func healthURL(addr string) string { return localURL(addr, "/healthz") }

// clip — denetim kaydında dev sonuç metni saklamayız. Kesim rune sınırına
// çekilir: bayt ortasından kesilen UTF-8'i Postgres reddeder (22021) ve
// denetim kaydı sessizce kaybolurdu.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// localURL — dinleme adresinden aynı makine URL'i türetir; motorun gateway
// bağlantısı da bunu kullanır (claude CLI core ile aynı makinede koşar, K15).
func localURL(addr, path string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://127.0.0.1:8080" + path
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + path
}

// piTohumu — ortamdaki PI_PROVIDER_* tarifini İLK AÇILIŞ TOHUMUNA çevirir.
//
// Sağlayıcılar artık veritabanında yaşar (göç 00016) ve Modeller ekranından
// yönetilir; bu tohum yalnız sağlayıcı tablosu bomboşken bir kez içeri alınır
// (kimlik ya da adres boşsa hiç). Amaç bu özellikten önceki kurulumların
// kesintisiz geçmesi — sonrasında ortam değişkeni söz sahibi değildir, yoksa
// her yeniden başlatma kullanıcının düzenlemesini geri alırdı.
func piTohumu(cfg config.Config) providers.Tohum {
	t := providers.Tohum{
		ID:      cfg.PiProviderID,
		Name:    cfg.PiProviderID,
		BaseURL: cfg.PiProviderBaseURL,
		API:     cfg.PiProviderAPI,
		APIKey:  cfg.PiProviderKey,
	}
	if t.API == "" {
		t.API = "openai-completions"
	}
	for _, m := range cfg.PiModels {
		t.Models = append(t.Models, providers.TohumModel{
			ID:            m.ID,
			ContextWindow: m.ContextWindow,
			MaxTokens:     m.MaxTokens,
		})
	}
	return t
}

// motorDurumDizini — motorun KALICI durumu (pi config dizini: models.json,
// oturumlar, eklentiler) veri dizinine alınır.
//
// Varsayılan yer ev dizinidir ve ev dizini kabın içindedir: her dağıtımda
// silinir. Oysa oturum imleçleri veritabanında yaşamaya devam eder, yani
// dağıtımdan sonraki ilk turda motor "Session not found" der ve sohbet
// kırılır. Veri dizini kalıcı diskte (DATA_DIR) durur; oluşturulamazsa boş
// dönülür — motor kendi varsayılanına düşer, koşu düşmez.
func motorDurumDizini(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	dir := filepath.Join(dataDir, "engine-state")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Warn("motor durum dizini açılamadı — oturumlar kap ömrüyle sınırlı kalacak",
			"dizin", dir, "err", err)
		return ""
	}
	return dir
}
