package telegram

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esmworks/OpenAssist/internal/attachments"
	"github.com/esmworks/OpenAssist/internal/bus"
	"github.com/esmworks/OpenAssist/internal/engine"
	"github.com/esmworks/OpenAssist/internal/orchestrator"
	"github.com/esmworks/OpenAssist/internal/policy"
	"github.com/esmworks/OpenAssist/internal/store"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

// Config — gateway ayarları.
type Config struct {
	// PollTimeout — getUpdates askıda kalma süresi (saniye).
	PollTimeout int
	// EditEvery — akan yanıtta mesaj düzenleme sıklığı; Telegram rate
	// limitine takılmamak için (~20 mesaj/dk sohbet başına).
	EditEvery time.Duration
	// RetryWait — polling hatasında bekleme.
	RetryWait time.Duration
	// ToolClasses — gateway araçlarının açık sınıf bildirimleri (Faz 4);
	// "hep izin ver" butonunun gösterilip gösterilmeyeceğini belirler.
	ToolClasses map[string]policy.Classification
	// DataDir — Telegram'dan indirilen eklerin diske yazılacağı kök (ADIM 5).
	DataDir string
	// MediaGroupWait — albüm toplama penceresi (ADIM 13). Telegram çoklu
	// fotoğrafı ayrı update'ler olarak yollar; bu süre boyunca aynı grup
	// kimliğinden gelenler tek koşuda birleşir.
	MediaGroupWait time.Duration
}

func DefaultConfig() Config {
	return Config{
		PollTimeout: 50, EditEvery: 2 * time.Second, RetryWait: 3 * time.Second,
		MediaGroupWait: varsayilanAlbumBekleme,
	}
}

// varsayilanAlbumBekleme — albüm penceresi. Uzun tutmak cevabı geciktirir,
// kısa tutmak albümü bölerdi; 1,5 sn ölçülen gecikmenin (parçalar arası
// onlarca ms) çok üstünde, insanın fark etmeyeceği kadar kısa.
const varsayilanAlbumBekleme = 1500 * time.Millisecond

// medyaGrubu — pencere dolana kadar biriken albüm parçaları.
type medyaGrubu struct {
	mesajlar []*Message
	chat     db.TelegramChat
}

// Gateway — Telegram kanalı. Koşuyu orkestratör üzerinden başlatır, canlı
// olayları bus'tan izler, onay kartlarını inline butonlarla sunar.
type Gateway struct {
	api *API
	st  *store.Store
	orc *orchestrator.Orchestrator
	bus *bus.Bus
	cfg Config

	wg sync.WaitGroup

	// gruplar — açık albüm pencereleri (media_group_id → parçalar).
	grupMu  sync.Mutex
	gruplar map[string]*medyaGrubu
}

func New(api *API, st *store.Store, orc *orchestrator.Orchestrator, b *bus.Bus, cfg Config) *Gateway {
	return &Gateway{api: api, st: st, orc: orc, bus: b, cfg: cfg}
}

// SetOrchestrator — kurulum sırası kilidini çözer: gateway orkestratörden
// ÖNCE kurulur (OnApprovalResolved kancasını verebilmek için), orkestratör
// hazır olunca buradan bağlanır. Run'dan önce çağrılmalıdır.
func (g *Gateway) SetOrchestrator(o *orchestrator.Orchestrator) { g.orc = o }

// Run — long polling döngüsü; ctx iptal edilene kadar çalışır. Çağıran
// goroutine'de bloklar.
func (g *Gateway) Run(ctx context.Context) error {
	if g.api.Token == "" {
		return ErrEmptyToken
	}
	me, err := g.api.GetMe(ctx)
	if err != nil {
		return fmt.Errorf("telegram: token doğrulanamadı: %w", err)
	}
	slog.Info("telegram gateway ayakta", "bot", me.Username)

	var offset int64
	for ctx.Err() == nil {
		ups, err := g.api.GetUpdates(ctx, offset, g.cfg.PollTimeout)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			slog.Warn("telegram getUpdates hatası", "err", err)
			select {
			case <-ctx.Done():
			case <-time.After(g.cfg.RetryWait):
			}
			continue
		}
		for _, up := range ups {
			offset = up.UpdateID + 1
			g.handleUpdate(ctx, up)
		}
	}
	g.wg.Wait() // koşu izleyicileri bitsin (drain)
	return ctx.Err()
}

func (g *Gateway) handleUpdate(ctx context.Context, up Update) {
	switch {
	case up.CallbackQuery != nil:
		g.handleCallback(ctx, up.CallbackQuery)
	case up.Message != nil && (up.Message.Text != "" || up.Message.HasEk()):
		g.handleMessage(ctx, up.Message)
	}
}

// ── gelen mesaj ────────────────────────────────────────────────────────

func (g *Gateway) handleMessage(ctx context.Context, m *Message) {
	// Allowlist: satır yoksa sohbet izinli değildir — SESSİZ ret (K16).
	chat, err := g.st.Q.GetTelegramChat(ctx, m.Chat.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		slog.Warn("telegram: izinsiz sohbetten mesaj yok sayıldı", "chat_id", m.Chat.ID)
		return
	}
	if err != nil {
		slog.Error("telegram: allowlist okunamadı", "err", err)
		return
	}

	// Metin ekli mesajda caption'dır: fotoğrafın altındaki "bu faturayı özetle"
	// yazısı text alanında GELMEZ (ADIM 13 — eskiden sessizce kayboluyordu).
	text := m.Metin()
	if strings.HasPrefix(text, "/") {
		g.handleCommand(ctx, chat, text)
		return
	}

	// Albüm: Telegram çoklu fotoğrafı ayrı update'ler olarak yollar, hepsi aynı
	// media_group_id ile. Kısa bir pencere boyunca toplanır ve TEK koşu açılır —
	// yoksa üç fotoğraf üç ayrı koşu, üç ayrı motor turu demekti (ve caption
	// yalnız birinde olduğu için diğer ikisi bağlamsız koşardı).
	if m.MediaGroupID != "" {
		g.grubaEkle(ctx, chat, m)
		return
	}

	g.mesajlariIsle(ctx, chat, []*Message{m})
}

// mesajlariIsle — tek mesaj ya da bir albümün tamamı: ekler indirilir, tek
// koşu açılır.
func (g *Gateway) mesajlariIsle(ctx context.Context, chat db.TelegramChat, msgs []*Message) {
	if len(msgs) == 0 {
		return
	}
	var metinler []string
	ekVar := false
	for _, m := range msgs {
		if s := m.Metin(); s != "" {
			metinler = append(metinler, s)
		}
		if m.HasEk() {
			ekVar = true
		}
	}
	text := strings.Join(metinler, "\n")

	bot, err := g.activeBot(ctx, chat)
	if err != nil {
		g.reply(ctx, chat.ChatID, err.Error())
		return
	}

	// Bot bir soru sorup park ettiyse bu mesaj YENİ koşu değil, o sorunun
	// cevabıdır (Telegram'da "yanıtla" jesti yok — sohbet akışı doğal olan).
	// EKLİ mesaj cevap sayılmaz: dosya yeni bir iştir (ADIM 5).
	if !ekVar {
		if run, ok := g.pendingQuestion(ctx, bot.ID); ok {
			if err := g.orc.ResolveApproval(ctx, run.ID, "", text, false); err == nil {
				g.closeCard(ctx, run.ID, "💬 Cevaplandı: "+text)
				g.followResume(ctx, chat.ChatID, run.ID)
				return
			}
			// Yarışı kaybettik (başka kanal cevapladı) — normal akışa düş.
		}
	}

	// Ekler indirilir, koşuya bağlanır; motor dosyaları yollarıyla görür (ADIM 5).
	var ekler []uuid.UUID
	for _, m := range msgs {
		if !m.HasEk() {
			continue
		}
		ids, err := g.downloadEks(ctx, bot, m)
		if err != nil {
			g.reply(ctx, chat.ChatID, "Ek alınamadı: "+err.Error())
			return
		}
		ekler = append(ekler, ids...)
	}

	run, err := g.orc.EnqueueChatWithKey(ctx, bot, text, orchestrator.OriginTelegram, "", ekler)
	if err != nil {
		g.reply(ctx, chat.ChatID, "Koşu başlatılamadı: "+err.Error())
		return
	}
	g.watchRun(ctx, chat.ChatID, bot, run)
}

// grubaEkle — albüm parçasını biriktirir; ilk parça pencereyi başlatır.
//
// Pencere BİR KEZ kurulur (timer.Reset yok): sonradan gelen parça çalışmakta
// olan boşaltmayla yarışırdı ve wg sayacı bozulurdu. Telegram albümün tüm
// parçalarını milisaniyeler içinde yolluyor; pencereden sonra gelen bir
// straggler kendi koşusunu açar — kaybolmaz.
func (g *Gateway) grubaEkle(ctx context.Context, chat db.TelegramChat, m *Message) {
	bekleme := g.cfg.MediaGroupWait
	if bekleme <= 0 {
		bekleme = varsayilanAlbumBekleme
	}
	g.grupMu.Lock()
	defer g.grupMu.Unlock()
	if g.gruplar == nil {
		g.gruplar = map[string]*medyaGrubu{}
	}
	grp, acik := g.gruplar[m.MediaGroupID]
	if !acik {
		grp = &medyaGrubu{chat: chat}
		g.gruplar[m.MediaGroupID] = grp
		id := m.MediaGroupID
		g.wg.Add(1)
		time.AfterFunc(bekleme, func() {
			defer g.wg.Done()
			g.grubuBosalt(ctx, id)
		})
	}
	grp.mesajlar = append(grp.mesajlar, m)
}

// grubuBosalt — pencere dolunca albümü tek koşuya çevirir.
func (g *Gateway) grubuBosalt(ctx context.Context, id string) {
	g.grupMu.Lock()
	grp := g.gruplar[id]
	delete(g.gruplar, id)
	g.grupMu.Unlock()
	if grp == nil || ctx.Err() != nil {
		return
	}
	g.mesajlariIsle(ctx, grp.chat, grp.mesajlar)
}

// downloadEks — mesajdaki ekleri indirip ek satırlarına çevirir (ADIM 5).
// Fotoğrafta en büyük boyut (dizinin sonu) kullanılır; belgede file_name
// korunur, yoksa "belge" denir.
func (g *Gateway) downloadEks(ctx context.Context, bot db.Bot, m *Message) ([]uuid.UUID, error) {
	type kaynak struct {
		fileID, name string
	}
	var kaynaklar []kaynak
	if m.Document != nil {
		name := m.Document.FileName
		if name == "" {
			name = "belge"
		}
		kaynaklar = append(kaynaklar, kaynak{m.Document.FileID, name})
	} else if len(m.Photo) > 0 {
		en := m.Photo[len(m.Photo)-1]
		kaynaklar = append(kaynaklar, kaynak{en.FileID, "foto.jpg"})
	}

	out := make([]uuid.UUID, 0, len(kaynaklar))
	for _, k := range kaynaklar {
		filePath, err := g.api.GetFile(ctx, k.fileID)
		if err != nil {
			return nil, err
		}
		bts, err := g.api.DownloadFile(ctx, filePath)
		if err != nil {
			return nil, err
		}
		att, err := g.st.SaveAttachmentFile(ctx, g.cfg.DataDir, bot, k.name, bytes.NewReader(bts))
		if err != nil {
			return nil, err
		}
		out = append(out, att.ID)
	}
	return out, nil
}

// pendingQuestion — botun park edilmiş SORU koşusu (izin isteği değil).
// Birden çoksa en yenisi; izin bekleyen koşular butonlarla cevaplanır.
func (g *Gateway) pendingQuestion(ctx context.Context, botID uuid.UUID) (db.Run, bool) {
	runs, err := g.st.Q.ListRunsByBotAndStatus(ctx, db.ListRunsByBotAndStatusParams{
		BotID: botID, Status: "approval_pending", Limit: 5,
	})
	if err != nil {
		return db.Run{}, false
	}
	for _, r := range runs {
		var ask engine.Ask
		if json.Unmarshal(r.PendingAsk, &ask) == nil && ask.Kind == "question" {
			return r, true
		}
	}
	return db.Run{}, false
}

// activeBot — sohbetin aktif botu; seçilmemişse tek bot varsa o, yoksa
// kullanıcıdan /talk ile seçmesi istenir (router bot Faz 7'de gelecek).
func (g *Gateway) activeBot(ctx context.Context, chat db.TelegramChat) (db.Bot, error) {
	if chat.ActiveBotID.Valid {
		bot, err := g.st.Q.GetBot(ctx, chat.ActiveBotID.UUID)
		if err == nil {
			return bot, nil
		}
		// Bot silinmiş: seçimi düşür, aşağıdaki yola devam et.
		_ = g.st.Q.SetTelegramActiveBot(ctx, db.SetTelegramActiveBotParams{ChatID: chat.ChatID})
	}
	bots, err := g.singleBots(ctx)
	if err != nil {
		return db.Bot{}, errors.New("bot listesi okunamadı")
	}
	switch len(bots) {
	case 0:
		return db.Bot{}, errors.New("henüz bot yok — Mac uygulamasından ya da API'den bir bot oluştur")
	case 1:
		_ = g.st.Q.SetTelegramActiveBot(ctx, db.SetTelegramActiveBotParams{
			ChatID: chat.ChatID, ActiveBotID: uuid.NullUUID{UUID: bots[0].ID, Valid: true},
		})
		return bots[0], nil
	default:
		return db.Bot{}, errors.New("hangi botla konuşayım? /bots ile listeye bak, /talk <ad> ile seç")
	}
}

// singleBots — ekipler hariç botlar (ekip sohbeti v2).
func (g *Gateway) singleBots(ctx context.Context) ([]db.Bot, error) {
	all, err := g.st.Q.ListBots(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]db.Bot, 0, len(all))
	for _, b := range all {
		if b.Kind == "bot" {
			out = append(out, b)
		}
	}
	return out, nil
}

func (g *Gateway) handleCommand(ctx context.Context, chat db.TelegramChat, text string) {
	cmd, arg, _ := strings.Cut(text, " ")
	cmd = strings.ToLower(strings.TrimSuffix(cmd, "@")) // /bots@BotAdi biçimi
	if i := strings.Index(cmd, "@"); i > 0 {
		cmd = cmd[:i]
	}
	arg = strings.TrimSpace(arg)

	switch cmd {
	case "/start", "/yardim", "/help":
		g.reply(ctx, chat.ChatID, "OpenAssist bağlı.\n"+
			"/bots — botları listeler\n"+
			"/talk <ad> — sohbeti o bota yönlendirir\n"+
			"/durum — bekleyen onayları gösterir\n"+
			"Düz mesaj yazarsan aktif bota gider.")
	case "/bots":
		bots, err := g.singleBots(ctx)
		if err != nil || len(bots) == 0 {
			g.reply(ctx, chat.ChatID, "Henüz bot yok.")
			return
		}
		var b strings.Builder
		b.WriteString("Botlar:\n")
		for _, bot := range bots {
			mark := "  "
			if chat.ActiveBotID.Valid && chat.ActiveBotID.UUID == bot.ID {
				mark = "▸ "
			}
			fmt.Fprintf(&b, "%s%s\n", mark, bot.Name)
		}
		b.WriteString("\n/talk <ad> ile geçiş yap.")
		g.reply(ctx, chat.ChatID, b.String())
	case "/talk":
		if arg == "" {
			g.reply(ctx, chat.ChatID, "Kullanım: /talk <bot adı>")
			return
		}
		bots, _ := g.singleBots(ctx)
		for _, bot := range bots {
			if strings.EqualFold(bot.Name, arg) {
				if err := g.st.Q.SetTelegramActiveBot(ctx, db.SetTelegramActiveBotParams{
					ChatID: chat.ChatID, ActiveBotID: uuid.NullUUID{UUID: bot.ID, Valid: true},
				}); err != nil {
					g.reply(ctx, chat.ChatID, "Seçim kaydedilemedi.")
					return
				}
				g.reply(ctx, chat.ChatID, "Artık "+bot.Name+" ile konuşuyorsun.")
				return
			}
		}
		g.reply(ctx, chat.ChatID, "Öyle bir bot yok. /bots ile listeye bak.")
	case "/durum":
		runs, err := g.st.Q.ListRunsByStatus(ctx, db.ListRunsByStatusParams{Status: "approval_pending", Limit: 10})
		if err != nil {
			g.reply(ctx, chat.ChatID, "Durum okunamadı.")
			return
		}
		if len(runs) == 0 {
			g.reply(ctx, chat.ChatID, "Bekleyen onay yok.")
			return
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%d onay bekliyor:\n", len(runs))
		for _, r := range runs {
			var ask engine.Ask
			_ = json.Unmarshal(r.PendingAsk, &ask)
			fmt.Fprintf(&b, "• %s — %s\n", askLabel(ask), r.Prompt)
		}
		g.reply(ctx, chat.ChatID, b.String())
	default:
		g.reply(ctx, chat.ChatID, "Bilinmeyen komut. /yardim yazabilirsin.")
	}
}

func (g *Gateway) reply(ctx context.Context, chatID int64, text string) {
	if _, err := g.api.SendMessage(ctx, chatID, text, nil); err != nil {
		slog.Warn("telegram mesaj gönderilemedi", "chat_id", chatID, "err", err)
	}
}

// ── koşu izleme: akan yanıt → mesaj düzenleme ──────────────────────────

// watchRun — koşunun canlı olaylarını izler; tek mesajı düzenleyerek
// ilerlemeyi gösterir, onay isteğinde kart açar, bitince özetle kapatır.
func (g *Gateway) watchRun(ctx context.Context, chatID int64, bot db.Bot, run db.Run) {
	live, cancel := g.bus.Subscribe(run.ID)
	msgID, err := g.api.SendMessage(ctx, chatID, "⏳ "+bot.Name+" düşünüyor…", nil)
	if err != nil {
		slog.Warn("telegram: koşu mesajı açılamadı", "err", err)
		cancel()
		return
	}

	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		defer cancel()
		g.streamRun(ctx, chatID, msgID, run.ID, live)
	}()
}

func (g *Gateway) streamRun(ctx context.Context, chatID, msgID int64, runID uuid.UUID, live <-chan bus.Event) {
	// Abone olmadan önce koşu bitmiş olabilir (hızlı koşu, mesaj gönderimi
	// yavaş kaldı): durumu bir kez yokla — SSE'deki subscribe→backfill
	// deseninin Telegram'daki karşılığı.
	if run, err := g.st.Q.GetRun(ctx, runID); err == nil && isSettled(run.Status) {
		g.finishRun(ctx, chatID, msgID, runID, "")
		return
	}

	var (
		text     strings.Builder
		lastEdit time.Time
		lastSent string
	)
	// Güvenlik ağı: bus yavaş istemcide olay DÜŞÜRÜR (256 tampon). Olay
	// kaçarsa mesaj "düşünüyor…" halinde asılı kalmasın diye periyodik yokla.
	sweep := time.NewTicker(5 * time.Second)
	defer sweep.Stop()
	flush := func(force bool) {
		body := strings.TrimSpace(text.String())
		if body == "" || body == lastSent {
			return
		}
		if !force && time.Since(lastEdit) < g.cfg.EditEvery {
			return
		}
		if err := g.api.EditMessageText(ctx, chatID, msgID, body, nil); err != nil {
			slog.Warn("telegram: mesaj düzenlenemedi", "err", err)
			return
		}
		lastEdit, lastSent = time.Now(), body
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-sweep.C:
			if run, err := g.st.Q.GetRun(ctx, runID); err == nil && isSettled(run.Status) {
				flush(true)
				g.finishRun(ctx, chatID, msgID, runID, text.String())
				return
			}
		case ev, ok := <-live:
			if !ok {
				return
			}
			var p eventPayload
			_ = json.Unmarshal(ev.Payload, &p)

			switch ev.Kind {
			case string(engine.KindAssistantDelta), string(engine.KindAssistantText):
				text.WriteString(p.Text)
				flush(false)
			case string(engine.KindToolStarted):
				if p.Tool != nil && p.Tool.Name != "" {
					// Araç adımı akışta görünsün; metin gelince üstüne yazılır.
					_ = g.api.EditMessageText(ctx, chatID, msgID,
						strings.TrimSpace(text.String())+"\n🔧 "+p.Tool.Name+"…", nil)
					lastEdit = time.Now()
				}
			case string(engine.KindRequestOpened):
				// Kartı HEMEN aç: kısa-bekleme penceresi içinde tek dokunuşla
				// cevaplanabilsin (K16). Cevaplanmazsa koşu park edilir ve
				// kart finishRun'da tazelenir.
				if p.Request != nil {
					g.openAskCard(ctx, chatID, msgID, runID, engine.Ask{
						Kind: p.Request.Kind, Tool: p.Request.Tool,
						Question: p.Request.Question, Choices: p.Request.Choices,
					}, strings.TrimSpace(text.String()))
					lastEdit, lastSent = time.Now(), ""
				}
			case string(engine.KindRequestResolved):
				// Pencere içinde cevaplandı (ya da kural işledi): butonları
				// kaldır. source=timeout park demektir — kart açık kalmalı.
				if p.Request != nil && p.Request.Source != "timeout" {
					g.closeCard(ctx, runID, resolvedNote(p.Request))
				}
			case string(engine.KindTurnCompleted):
				flush(true)
				g.finishRun(ctx, chatID, msgID, runID, text.String())
				return
			case string(engine.KindError):
				_ = g.api.EditMessageText(ctx, chatID, msgID, "⚠️ Hata: "+p.Error, nil)
				return
			}
		}
	}
}

// finishRun — turn bittiğinde koşunun son durumuna göre kapanış: tamamlandı
// (final metin), park (onay kartı) ya da hata. streamed, akış sırasında
// biriken metindir (DB'deki final metin yetişmezse yedek).
func (g *Gateway) finishRun(ctx context.Context, chatID, msgID int64, runID uuid.UUID, streamed string) {
	// turn.completed olayı koşunun DB durumu yazılmadan ÖNCE yayınlanır;
	// durum oturana kadar kısa süre bekle (yoksa kart hiç açılmaz, metin
	// boş görünür).
	run, ok := g.awaitSettled(ctx, runID)
	if !ok {
		if s := strings.TrimSpace(streamed); s != "" {
			_ = g.api.EditMessageText(ctx, chatID, msgID, s, nil)
		}
		return
	}
	switch run.Status {
	case "approval_pending":
		g.openApprovalCard(ctx, chatID, msgID, run, streamed)
	case "failed":
		_ = g.api.EditMessageText(ctx, chatID, msgID, orchestrator.HataCipiOneki+run.Error, nil)
	case "expired":
		_ = g.api.EditMessageText(ctx, chatID, msgID, "⏱ Onay süresi doldu — istek işlenmedi.", nil)
	default:
		body := firstNonEmpty(run.FinalText, streamed, "(boş yanıt)")
		_ = g.api.EditMessageText(ctx, chatID, msgID, body, nil)
		g.gidenEkleriYolla(ctx, chatID, run.ID)
	}
}

// gidenEkleriYolla — botun koşu sırasında send_file ile ürettiği dosyaları
// sohbete yükler (ADIM 13). Metin cevabından SONRA gider: dosya bağlamıyla
// birlikte okunur, tek başına düşen dosya "bu ne?" sorusu doğurur.
//
// Hata koşuyu düşürmez: dosya Mac istemcisinde ve /api/attachments'ta
// duruyor, Telegram yalnız bir kanal.
func (g *Gateway) gidenEkleriYolla(ctx context.Context, chatID int64, runID uuid.UUID) {
	ekler, err := g.st.Q.ListAttachmentsByRun(ctx, uuid.NullUUID{UUID: runID, Valid: true})
	if err != nil {
		slog.Warn("telegram: giden ekler okunamadı", "err", err, "run", runID.String())
		return
	}
	for _, e := range ekler {
		metot, alan := "sendDocument", "document"
		// Fotoğraf olarak gönderilen görüntü sohbette önizlemeli görünür ama
		// Telegram onu yeniden sıkıştırır ve 10 MB'ı reddeder; büyük ya da
		// motorun/Telegram'ın tanımadığı biçimler belge olarak gider (HEIC
		// dahil — bozuk önizleme yerine indirilebilir dosya).
		if e.Kind == "image" && e.Size <= SendPhotoAzami &&
			attachments.MotorGorur(attachments.Abs(g.cfg.DataDir, e.Path)) {
			metot, alan = "sendPhoto", "photo"
		}
		if err := g.api.SendFile(ctx, chatID, metot, alan,
			attachments.Abs(g.cfg.DataDir, e.Path), e.Name, e.Name); err != nil {
			slog.Warn("telegram: ek gönderilemedi", "err", err, "ad", e.Name)
			g.reply(ctx, chatID, "📎 "+e.Name+" gönderilemedi: "+err.Error())
		}
	}
}

// awaitSettled — koşu terminal ya da onay bekler duruma gelene kadar kısaca
// yoklar. Motor sonucu ile DB yazımı arasındaki pencereyi kapatır.
func (g *Gateway) awaitSettled(ctx context.Context, runID uuid.UUID) (db.Run, bool) {
	const (
		timeout = 5 * time.Second
		every   = 100 * time.Millisecond
	)
	deadline := time.Now().Add(timeout)
	var last db.Run
	for time.Now().Before(deadline) {
		run, err := g.st.Q.GetRun(ctx, runID)
		if err != nil {
			return db.Run{}, false
		}
		last = run
		if isSettled(run.Status) {
			return run, true
		}
		select {
		case <-ctx.Done():
			return last, false
		case <-time.After(every):
		}
	}
	return last, false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// ── onay kartı ─────────────────────────────────────────────────────────

// openApprovalCard — park edilen koşunun kartını tazeler (kart istek anında
// zaten açılmıştı; park kartı final metni ve taze token'larla yeniden çizilir).
func (g *Gateway) openApprovalCard(ctx context.Context, chatID, msgID int64, run db.Run, streamed string) {
	var ask engine.Ask
	_ = json.Unmarshal(run.PendingAsk, &ask)
	body := firstNonEmpty(run.FinalText, streamed, "Onayın gerekiyor.")
	if run.Tainted {
		body += "\n⚠️ Bu koşu dış içerik okudu — otomatik izinler geçersiz (§5.1)."
	}
	g.drawCard(ctx, chatID, msgID, run.ID, ask, body, run.Tainted)
}

// openAskCard — istek AÇILDIĞI anda kart (kısa-bekleme penceresi içinde tek
// dokunuşla cevap, K16). Koşu henüz çalışıyor: tainted bayrağı DB'den okunur.
func (g *Gateway) openAskCard(ctx context.Context, chatID, msgID int64, runID uuid.UUID, ask engine.Ask, streamed string) {
	tainted := false
	if run, err := g.st.Q.GetRun(ctx, runID); err == nil {
		tainted = run.Tainted
	}
	g.drawCard(ctx, chatID, msgID, runID, ask, firstNonEmpty(streamed, "Onayın gerekiyor."), tainted)
}

// drawCard — kartı çizer: eski token'lar iptal, yenileri üretilir; kart
// kaydı açılır (resolved=false).
func (g *Gateway) drawCard(ctx context.Context, chatID, msgID int64, runID uuid.UUID, ask engine.Ask, body string, tainted bool) {
	// Kart yeniden çiziliyorsa eski butonların token'ları geçersizleşmeli.
	if err := g.st.Q.DeleteApprovalTokensForRun(ctx, runID); err != nil {
		slog.Warn("telegram: eski token'lar silinemedi", "err", err)
	}
	kb, err := g.buildKeyboard(ctx, runID, ask, tainted)
	if err != nil {
		slog.Error("telegram: onay token'ı üretilemedi", "err", err)
		return
	}
	body += "\n\n🔐 " + askLabel(ask)
	if err := g.api.EditMessageText(ctx, chatID, msgID, body, kb); err != nil {
		slog.Warn("telegram: onay kartı açılamadı", "err", err)
		return
	}
	if err := g.st.Q.UpsertTelegramCard(ctx, db.UpsertTelegramCardParams{
		RunID: runID, ChatID: chatID, MessageID: msgID,
	}); err != nil {
		slog.Warn("telegram: kart kaydedilemedi", "err", err)
	}
}

func (g *Gateway) buildKeyboard(ctx context.Context, runID uuid.UUID, ask engine.Ask, tainted bool) (*InlineKeyboard, error) {
	// Soru kartı: şıklar butona döner. Şık yoksa buton da yok — kullanıcı
	// düz mesajla cevaplar (handleMessage park edilmiş soruya yönlendirir).
	if ask.Kind == "question" {
		if len(ask.Choices) == 0 {
			return nil, nil
		}
		rows := make([][]InlineButton, 0, len(ask.Choices))
		for _, c := range ask.Choices {
			tok, err := g.newAnswerToken(ctx, runID, c)
			if err != nil {
				return nil, err
			}
			rows = append(rows, []InlineButton{{Text: c, CallbackData: tok}})
		}
		return &InlineKeyboard{InlineKeyboard: rows}, nil
	}
	allow, err := g.newToken(ctx, runID, "allow")
	if err != nil {
		return nil, err
	}
	deny, err := g.newToken(ctx, runID, "deny")
	if err != nil {
		return nil, err
	}
	rows := [][]InlineButton{{
		{Text: "✅ Onayla", CallbackData: allow},
		{Text: "❌ Reddet", CallbackData: deny},
	}}
	// "Hep izin ver" yalnız kural yazılabilen ve tainted OLMAYAN koşularda
	// sunulur; sunucu vetosu ayrıca ResolveApproval'da da işler (K17).
	if !tainted && policy.ClassifyWith(g.cfg.ToolClasses, ask.Tool).Ruleable {
		always, err := g.newToken(ctx, runID, "allow_always")
		if err != nil {
			return nil, err
		}
		rows = append(rows, []InlineButton{{Text: "✅ Hep izin ver", CallbackData: always}})
	}
	return &InlineKeyboard{InlineKeyboard: rows}, nil
}

func (g *Gateway) newToken(ctx context.Context, runID uuid.UUID, action string) (string, error) {
	return g.newTokenWithAnswer(ctx, runID, action, "")
}

// newAnswerToken — soru şıkkını taşıyan token; cevap metni sunucuda durur,
// callback_data yalnız rastgele token'dır.
func (g *Gateway) newAnswerToken(ctx context.Context, runID uuid.UUID, choice string) (string, error) {
	return g.newTokenWithAnswer(ctx, runID, "answer", choice)
}

func (g *Gateway) newTokenWithAnswer(ctx context.Context, runID uuid.UUID, action, answer string) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	// callback_data 64 bayt sınırlı — 32 karakter base64url rahat sığar.
	tok := base64.RawURLEncoding.EncodeToString(b)
	if err := g.st.Q.CreateApprovalToken(ctx, db.CreateApprovalTokenParams{
		Token: tok, RunID: runID, Action: action, Answer: answer,
	}); err != nil {
		return "", err
	}
	return tok, nil
}

// handleCallback — buton tıklaması. Token tek kullanımlıktır; kart başka
// kanaldan çözülmüşse zarifçe "zaten işlendi" denir (çok kanallı yarış).
func (g *Gateway) handleCallback(ctx context.Context, cb *CallbackQuery) {
	if cb.Message == nil {
		return
	}
	// Allowlist callback'lerde de geçerli.
	if _, err := g.st.Q.GetTelegramChat(ctx, cb.Message.Chat.ID); err != nil {
		_ = g.api.AnswerCallbackQuery(ctx, cb.ID, "Yetkin yok.")
		return
	}

	tok, err := g.st.Q.UseApprovalToken(ctx, cb.Data)
	if errors.Is(err, pgx.ErrNoRows) {
		_ = g.api.AnswerCallbackQuery(ctx, cb.ID, "Bu buton artık geçerli değil.")
		return
	}
	if err != nil {
		_ = g.api.AnswerCallbackQuery(ctx, cb.ID, "Karar işlenemedi.")
		return
	}

	decision, always, answer := "allow", false, ""
	switch tok.Action {
	case "deny":
		decision = "deny"
	case "allow_always":
		always = true
	case "answer":
		decision, answer = "", tok.Answer
	}

	err = g.orc.ResolveApproval(ctx, tok.RunID, decision, answer, always)
	if errors.Is(err, orchestrator.ErrNotAwaiting) {
		// Başka kanal (Mac) daha hızlıydı — kartı kapat, kullanıcıyı bilgilendir.
		_ = g.api.AnswerCallbackQuery(ctx, cb.ID, "Bu istek zaten işlendi.")
		g.closeCard(ctx, tok.RunID, "✔ Bu istek başka bir kanaldan yanıtlandı.")
		return
	}
	if err != nil {
		_ = g.api.AnswerCallbackQuery(ctx, cb.ID, "Karar işlenemedi: "+err.Error())
		return
	}

	note := "✅ Onaylandı (Telegram)"
	switch {
	case answer != "":
		note = "💬 Cevaplandı: " + answer
	case decision == "deny":
		note = "❌ Reddedildi (Telegram)"
	case always:
		note = "✅ Onaylandı — bundan sonra otomatik (Telegram)"
	}
	_ = g.api.AnswerCallbackQuery(ctx, cb.ID, note)
	g.closeCard(ctx, tok.RunID, note)

	// Karar sonrası koşu resume olur; akışı yeni bir mesajda sürdür.
	// (Ret de resume eder — bot durumu toparlayıp özetler.)
	g.followResume(ctx, cb.Message.Chat.ID, tok.RunID)
}

// closeCard — kartın butonlarını kaldırır ve sonucu yazar; tek kullanımlık
// (ikinci çağrı satır döndürmez).
func (g *Gateway) closeCard(ctx context.Context, runID uuid.UUID, note string) {
	card, err := g.st.Q.ResolveTelegramCard(ctx, runID)
	if err != nil {
		return // kart yok ya da zaten kapalı
	}
	run, err := g.st.Q.GetRun(ctx, runID)
	body := note
	if err == nil && strings.TrimSpace(run.FinalText) != "" {
		body = run.FinalText + "\n\n" + note
	}
	// markup vermeyerek butonları kaldırıyoruz.
	if err := g.api.EditMessageText(ctx, card.ChatID, card.MessageID, body, nil); err != nil {
		slog.Warn("telegram: kart kapatılamadı", "err", err)
	}
	_ = g.st.Q.DeleteApprovalTokensForRun(ctx, runID)
}

// followResume — onaydan sonra devam eden koşu için yeni bir akış mesajı
// açar (eski kart karar notuyla kapalı kaldı).
func (g *Gateway) followResume(ctx context.Context, chatID int64, runID uuid.UUID) {
	live, cancel := g.bus.Subscribe(runID)
	msgID, err := g.api.SendMessage(ctx, chatID, "⏳ Devam ediyor…", nil)
	if err != nil {
		cancel()
		return
	}
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		defer cancel()
		g.streamRun(ctx, chatID, msgID, runID, live)
	}()
}

// OnApprovalResolved — orkestratörün çok kanallı yarış kancası (K16):
// karar hangi kanaldan gelirse gelsin Telegram kartı kapanır. Kendi
// kararımızda kart zaten kapalıdır (tek kullanımlık) — ikinci kapatış
// sessizce boşa düşer.
func (g *Gateway) OnApprovalResolved(runID uuid.UUID, note string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	g.closeCard(ctx, runID, note)
}

// ── yardımcılar ────────────────────────────────────────────────────────

// eventPayload — orkestratörün yayınladığı olay gövdesi (okuma tarafı).
type eventPayload struct {
	Text  string `json:"text,omitempty"`
	Error string `json:"error,omitempty"`
	Tool  *struct {
		Name string `json:"name,omitempty"`
	} `json:"tool,omitempty"`
	Request *requestPayload `json:"request,omitempty"`
}

type requestPayload struct {
	Kind     string   `json:"kind"`
	Tool     string   `json:"tool,omitempty"`
	Question string   `json:"question,omitempty"`
	Choices  []string `json:"choices,omitempty"`
	Behavior string   `json:"behavior,omitempty"`
	Source   string   `json:"source,omitempty"`
}

// resolvedNote — kartın kapanış notu; kararın hangi kanaldan geldiğini
// açıkça yazar (çok kanallı yarışta kullanıcı şaşırmasın).
func resolvedNote(r *requestPayload) string {
	who := map[string]string{
		"api": "başka kanaldan", "rule": "kural gereği",
		"grant": "onayınla", "policy": "politika gereği",
	}[r.Source]
	if who == "" {
		who = r.Source
	}
	switch r.Behavior {
	case "allow":
		return "✔ İzin verildi (" + who + ")."
	case "deny":
		return "✖ Reddedildi (" + who + ")."
	default:
		return "💬 Cevaplandı (" + who + ")."
	}
}

// isSettled — koşu için Telegram'ın söyleyecek son sözü var mı: terminal
// durumlar + onay bekleyen (kart açılır). 'queued'/'running' beklemeye devam.
func isSettled(status string) bool {
	switch status {
	case "completed", "failed", "canceled", "expired", "approval_pending":
		return true
	}
	return false
}

func askLabel(ask engine.Ask) string {
	if ask.Kind == "question" {
		if ask.Question != "" {
			return ask.Question
		}
		return "Bot bir soru sordu."
	}
	if ask.Tool != "" {
		return ask.Tool + " izni isteniyor."
	}
	return "İzin isteniyor."
}
