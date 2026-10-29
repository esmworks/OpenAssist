//go:build integration

// Faz 5 entegrasyon testleri: Telegram kanalı uçtan uca — gerçek Postgres,
// sahte motor, sahte Bot API. Allowlist, koşu akışı, onay kartı, tek
// kullanımlık token ve çok kanallı yarış burada kanıtlanır.
package telegram

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

const allowedChat = int64(555001)

type harness struct {
	fake *fakeBotAPI
	gw   *Gateway
	st   *store.Store
	orc  *orchestrator.Orchestrator
	bot  db.Bot
	// ctx — gateway'in yaşam ctx'i; üretimde Run(ctx) bunu taşır. Testte de
	// aynısı kullanılmalı: Background verilirse izleyici goroutine'ler
	// kapanışta asılır (orkestratör durur, koşu asla oturmaz).
	ctx context.Context
}

func setup(t *testing.T, exec engine.Executor, approvalWait time.Duration) *harness {
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

	b := bus.New()
	f := newFakeBotAPI(t)
	dataDir := t.TempDir()
	gcfg := DefaultConfig()
	gcfg.EditEvery = 0 // testte throttle kapalı: her parça hemen yazılsın
	// Ekler orkestratörle AYNI kökte yaşamalı: gateway indirir, motor promptu
	// aynı yoldan okur (ADIM 5/12).
	gcfg.DataDir = dataDir
	gcfg.MediaGroupWait = 200 * time.Millisecond // testte albüm penceresi kısa
	gw := New(f.api(), st, nil, b, gcfg)

	ocfg := orchestrator.DefaultConfig()
	ocfg.Workers = 1
	ocfg.ApprovalWait = approvalWait
	ocfg.PollEvery = 100 * time.Millisecond
	ocfg.DataDir = dataDir
	ocfg.WorkerID = "tg-test-" + t.Name()
	ocfg.OnApprovalResolved = gw.OnApprovalResolved

	orc := orchestrator.New(st, map[string]engine.Executor{"fake": exec}, b, ocfg)
	orc.Start(ctx)
	gw.SetOrchestrator(orc)

	// Sohbeti allowlist'e al (env seed'inin yaptığı iş).
	if _, err := st.Q.UpsertTelegramChat(context.Background(), db.UpsertTelegramChatParams{
		ChatID: allowedChat, Label: "test",
	}); err != nil {
		cancel()
		t.Fatalf("allowlist: %v", err)
	}

	bot, err := st.Q.CreateBot(context.Background(), db.CreateBotParams{
		Kind: "bot", Name: "tg-" + t.Name(), Engine: "fake", AllowedTools: []string{},
	})
	if err != nil {
		cancel()
		t.Fatalf("bot: %v", err)
	}
	// Tek bot senaryosunu garantiye al: sohbetin aktif botu bu.
	if err := st.Q.SetTelegramActiveBot(context.Background(), db.SetTelegramActiveBotParams{
		ChatID: allowedChat, ActiveBotID: uuid.NullUUID{UUID: bot.ID, Valid: true},
	}); err != nil {
		cancel()
		t.Fatalf("aktif bot: %v", err)
	}

	t.Cleanup(func() {
		cancel()
		orc.Wait()
		gw.wg.Wait()
		_, _ = st.Q.DeleteTelegramChat(context.Background(), allowedChat)
		st.Close()
	})
	return &harness{fake: f, gw: gw, st: st, orc: orc, bot: bot, ctx: ctx}
}

func (h *harness) send(text string) {
	h.gw.handleUpdate(h.ctx, Update{
		UpdateID: 1,
		Message:  &Message{MessageID: 1, Chat: Chat{ID: allowedChat, Type: "private"}, Text: text},
	})
}

// sendFoto — caption'lı fotoğraf gönderimi (ADIM 5/12). grup boş değilse
// albüm parçasıdır.
func (h *harness) sendFoto(id int64, caption, grup string) {
	h.gw.handleUpdate(h.ctx, Update{
		UpdateID: id,
		Message: &Message{
			MessageID: id, Chat: Chat{ID: allowedChat, Type: "private"},
			Caption: caption, MediaGroupID: grup,
			Photo: []PhotoSize{{FileID: "small", FileSize: 100}, {FileID: "buyuk", FileSize: 9000}},
		},
	})
}

// botEkleri — botun ek satırları (mesaja bağlı olsun olmasın).
func (h *harness) botEkleri(t *testing.T) []db.Attachment {
	t.Helper()
	conv, err := h.st.BotConversation(context.Background(), h.bot)
	if err != nil {
		t.Fatalf("sohbet: %v", err)
	}
	msgs, err := h.st.Q.ListMessages(context.Background(), db.ListMessagesParams{ConversationID: conv.ID, Limit: 50})
	if err != nil {
		t.Fatalf("mesajlar: %v", err)
	}
	ids := make([]uuid.UUID, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.ID)
	}
	ekler, err := h.st.Q.ListAttachmentsByMessages(context.Background(), ids)
	if err != nil {
		t.Fatalf("ekler: %v", err)
	}
	return ekler
}

// click — inline butona basış (callback_query).
func (h *harness) click(t *testing.T, id, data string, msgID int64) {
	t.Helper()
	h.gw.handleUpdate(h.ctx, Update{
		UpdateID:      2,
		CallbackQuery: &CallbackQuery{ID: id, Data: data, Message: &Message{MessageID: msgID, Chat: Chat{ID: allowedChat}}},
	})
}

// waitFor — koşullar asenkron (izleyici goroutine'i); kısa aralıklarla yokla.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("koşul sağlanmadı: %s", what)
}

// lastEditText — son editMessageText çağrısının metni.
func (h *harness) lastEditText() string {
	edits := h.fake.callsOf("editMessageText")
	if len(edits) == 0 {
		return ""
	}
	s, _ := edits[len(edits)-1].Body["text"].(string)
	return s
}

func TestTelegramChatFlow(t *testing.T) {
	h := setup(t, enginetest.PlainResponder("Merhaba, hazırım."), time.Second)

	h.send("selam")

	waitFor(t, "yanıt mesaja yazılmalı", func() bool {
		return strings.Contains(h.lastEditText(), "Merhaba, hazırım.")
	})

	// Koşu ve mesajlar telegram kanalıyla damgalanmalı (K16).
	runs, _ := h.st.Q.ListRunsByBot(context.Background(), db.ListRunsByBotParams{BotID: h.bot.ID, Limit: 5})
	if len(runs) != 1 || runs[0].Origin != orchestrator.OriginTelegram {
		t.Fatalf("koşu kanalı yanlış: %+v", runs)
	}
	conv, cerr := h.st.BotConversation(context.Background(), h.bot)
	if cerr != nil {
		t.Fatalf("sohbet: %v", cerr)
	}
	msgs, _ := h.st.Q.ListMessages(context.Background(), db.ListMessagesParams{ConversationID: conv.ID, Limit: 10})
	for _, m := range msgs {
		if m.Origin != orchestrator.OriginTelegram {
			t.Errorf("mesaj kanalı yanlış: %s = %s", m.Role, m.Origin)
		}
	}
}

// ADIM 5'in Telegram ucu kodu vardı ama testi yoktu (ADIM 13'de kapatıldı):
// fotoğraf indirilir, ek satırı açılır ve caption KOŞUNUN METNİ olur.
func TestTelegramFotoEkiVeCaption(t *testing.T) {
	var promptlar []string
	h := setup(t, enginetest.Func(func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		promptlar = append(promptlar, in.Prompt)
		res := &engine.TurnResult{Ok: true, StopReason: "success", NumTurns: 1, FinalText: "fotoğrafı aldım", ResumeCursor: "c"}
		emit(engine.Event{Kind: engine.KindTurnCompleted, Result: res})
		return res, nil
	}), time.Second)
	h.fake.lock()
	h.fake.dosya = []byte{0xff, 0xd8, 0xff, 0xe0, 'J', 'F', 'I', 'F'} // gerçek JPEG imzası
	h.fake.unlock()

	h.sendFoto(11, "bu faturayı özetle", "")

	waitFor(t, "yanıt gelmeli", func() bool {
		return strings.Contains(h.lastEditText(), "fotoğrafı aldım")
	})

	ekler := h.botEkleri(t)
	if len(ekler) != 1 {
		t.Fatalf("bir ek bekleniyordu: %+v", ekler)
	}
	if ekler[0].Kind != "image" {
		t.Errorf("tür içerikten saptanmalı: %+v", ekler[0])
	}
	// En BÜYÜK boyut indirilmeli (dizinin sonu) — küçük önizleme değil.
	if got := h.fake.callsOf("getFile")[0].Body["file_id"]; got != "buyuk" {
		t.Errorf("en büyük fotoğraf indirilmeli, indirilen: %v", got)
	}
	// Caption koşuya metin olarak girmeli, ek yolu da prompta.
	if len(promptlar) != 1 || !strings.Contains(promptlar[0], "bu faturayı özetle") {
		t.Fatalf("caption prompta girmedi: %q", promptlar)
	}
	if !strings.Contains(promptlar[0], "Ekli dosyalar") {
		t.Errorf("ek bloğu yok: %q", promptlar[0])
	}
	conv, _ := h.st.BotConversation(context.Background(), h.bot)
	msgs, _ := h.st.Q.ListMessages(context.Background(), db.ListMessagesParams{ConversationID: conv.ID, Limit: 10})
	var kullanici string
	for _, m := range msgs {
		if m.Role == "user" {
			kullanici = m.Text
		}
	}
	if kullanici != "bu faturayı özetle" {
		t.Errorf("kullanıcı mesajı caption'ı taşımalı: %q", kullanici)
	}
}

// Albüm: üç fotoğraf üç update olarak gelir ama TEK koşu açılmalı (ADIM 13).
func TestTelegramAlbumTekKosu(t *testing.T) {
	h := setup(t, enginetest.PlainResponder("albümü aldım"), time.Second)

	h.sendFoto(21, "şu üç ekran görüntüsüne bak", "ALBUM1")
	h.sendFoto(22, "", "ALBUM1")
	h.sendFoto(23, "", "ALBUM1")

	waitFor(t, "yanıt gelmeli", func() bool {
		return strings.Contains(h.lastEditText(), "albümü aldım")
	})

	runs, _ := h.st.Q.ListRunsByBot(context.Background(), db.ListRunsByBotParams{BotID: h.bot.ID, Limit: 10})
	if len(runs) != 1 {
		t.Fatalf("albüm TEK koşu açmalı, açılan: %d", len(runs))
	}
	if !strings.Contains(runs[0].Prompt, "şu üç ekran görüntüsüne bak") {
		t.Errorf("albüm caption'ı kaybolmuş: %q", runs[0].Prompt)
	}
	if ekler := h.botEkleri(t); len(ekler) != 3 {
		t.Fatalf("üç ek bekleniyordu: %d", len(ekler))
	}
	// Tek akış mesajı: üç ayrı "düşünüyor…" balonu çıkmamalı.
	if n := len(h.fake.callsOf("sendMessage")); n != 1 {
		t.Errorf("albüm için tek akış mesajı beklenir, gönderilen: %d", n)
	}
}

// ADIM 13: botun ürettiği dosya sohbete düşer — hem ek satırı olarak hem
// Telegram'a yüklenerek.
func TestTelegramGidenDosya(t *testing.T) {
	h := setup(t, dosyaGonderen("rapor.pdf", "%PDF-1.7 rapor gövdesi", "rapor hazır"), time.Second)

	h.send("bana rapor çıkar")

	waitFor(t, "yanıt gelmeli", func() bool {
		return strings.Contains(h.lastEditText(), "rapor hazır")
	})
	waitFor(t, "dosya Telegram'a yüklenmeli", func() bool {
		return len(h.fake.callsOf("sendDocument")) == 1
	})

	body := h.fake.callsOf("sendDocument")[0].Body
	if body["file_name"] != "rapor.pdf" || body["file_body"] != "%PDF-1.7 rapor gövdesi" {
		t.Errorf("yüklenen dosya: %+v", body)
	}

	// Ek satırı BOT mesajına bağlanmalı: zaman tünelinde cevapla birlikte durur.
	ekler := h.botEkleri(t)
	if len(ekler) != 1 || ekler[0].Kind != "pdf" || !ekler[0].MessageID.Valid {
		t.Fatalf("giden ek mesaja bağlanmadı: %+v", ekler)
	}
	msg, err := h.st.Q.GetMessage(context.Background(), ekler[0].MessageID.UUID)
	if err != nil || msg.Role != "bot" {
		t.Fatalf("ek bot mesajında olmalı: %+v %v", msg, err)
	}
}

// dosyaGonderen — çalışma dizinine dosya yazıp send_file ile yollayan sahte
// motor (gerçek pi eklentisinin yaptığı iş).
func dosyaGonderen(ad, icerik, final string) enginetest.Func {
	return func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		emit(engine.Event{Kind: engine.KindSessionStarted, SessionID: "fake-oturum", Model: "fake-model"})
		if err := os.WriteFile(filepath.Join(in.CWD, ad), []byte(icerik), 0o600); err != nil {
			return nil, err
		}
		if in.OnAsk != nil {
			in.OnAsk(ctx, engine.Ask{Kind: "file", Path: ad})
		}
		res := &engine.TurnResult{Ok: true, StopReason: "success", NumTurns: 1, FinalText: final, ResumeCursor: "c"}
		emit(engine.Event{Kind: engine.KindTurnCompleted, Result: res})
		return res, nil
	}
}

func TestUnknownChatIgnoredSilently(t *testing.T) {
	h := setup(t, enginetest.PlainResponder("olmamalı"), time.Second)

	h.gw.handleUpdate(context.Background(), Update{
		UpdateID: 9,
		Message:  &Message{MessageID: 1, Chat: Chat{ID: 999999, Type: "private"}, Text: "merhaba"},
	})
	time.Sleep(300 * time.Millisecond)

	// Sessiz ret: hiç mesaj gönderilmemeli, koşu açılmamalı.
	if n := len(h.fake.callsOf("sendMessage")); n != 0 {
		t.Errorf("izinsiz sohbete cevap verildi (%d mesaj)", n)
	}
	runs, _ := h.st.Q.ListRunsByBot(context.Background(), db.ListRunsByBotParams{BotID: h.bot.ID, Limit: 5})
	if len(runs) != 0 {
		t.Errorf("izinsiz sohbet koşu başlattı: %d", len(runs))
	}
}

func TestApprovalCardAndCallback(t *testing.T) {
	h := setup(t, enginetest.AskingWriter("Dosya yazıldı.", "Onayın gerekiyor."), 200*time.Millisecond)

	h.send("not.txt yaz")

	// Kart açılmalı: buton taşıyan bir edit.
	var card db.TelegramCard
	waitFor(t, "onay kartı açılmalı", func() bool {
		runs, _ := h.st.Q.ListRunsByBot(context.Background(), db.ListRunsByBotParams{BotID: h.bot.ID, Limit: 5})
		if len(runs) == 0 {
			return false
		}
		c, err := h.st.Q.GetTelegramCard(context.Background(), runs[0].ID)
		if err != nil {
			return false
		}
		card = c
		return true
	})

	kb := lastKeyboard(t, h)
	if kb == nil || len(kb.InlineKeyboard) < 2 {
		t.Fatalf("onay + hep-izin-ver butonları bekleniyordu: %+v", kb)
	}
	allowTok := kb.InlineKeyboard[0][0].CallbackData
	if allowTok == "" || strings.Contains(allowTok, card.RunID.String()) {
		t.Errorf("callback_data tahmin edilebilir olmamalı: %q", allowTok)
	}

	// Butona bas → onay → koşu tamamlanır.
	h.click(t, "cb1", allowTok, card.MessageID)
	waitFor(t, "onay sonrası koşu tamamlanmalı", func() bool {
		r, err := h.st.Q.GetRun(context.Background(), card.RunID)
		return err == nil && r.Status == "completed"
	})

	// Token tek kullanımlık: aynı butona ikinci basış "geçerli değil".
	before := len(h.fake.callsOf("answerCallbackQuery"))
	h.click(t, "cb2", allowTok, card.MessageID)
	answers := h.fake.callsOf("answerCallbackQuery")
	if len(answers) != before+1 {
		t.Fatalf("ikinci tıklama cevaplanmadı")
	}
	if txt, _ := answers[len(answers)-1].Body["text"].(string); !strings.Contains(txt, "geçerli değil") {
		t.Errorf("ikinci tıklama zarifçe reddedilmeli: %q", txt)
	}
}

func TestOtherChannelWinsRace(t *testing.T) {
	h := setup(t, enginetest.AskingWriter("Dosya yazıldı.", "Onayın gerekiyor."), 200*time.Millisecond)

	h.send("not.txt yaz")

	var card db.TelegramCard
	waitFor(t, "onay kartı açılmalı", func() bool {
		runs, _ := h.st.Q.ListRunsByBot(context.Background(), db.ListRunsByBotParams{BotID: h.bot.ID, Limit: 5})
		if len(runs) == 0 {
			return false
		}
		c, err := h.st.Q.GetTelegramCard(context.Background(), runs[0].ID)
		card = c
		return err == nil
	})
	kb := lastKeyboard(t, h)
	allowTok := kb.InlineKeyboard[0][0].CallbackData

	// Mac/API kanalından onayla (Telegram'a değil).
	if err := h.orc.ResolveApproval(context.Background(), card.RunID, "allow", "", false); err != nil {
		t.Fatalf("API onayı: %v", err)
	}
	// Kanca Telegram kartını kapatmalı.
	waitFor(t, "kart başka kanaldan kapanmalı", func() bool {
		c, err := h.st.Q.GetTelegramCard(context.Background(), card.RunID)
		return err == nil && c.Resolved
	})

	// Şimdi Telegram butonuna basılırsa: token kullanılabilir ama koşu artık
	// onay beklemiyor → "zaten işlendi".
	h.click(t, "cb3", allowTok, card.MessageID)
	answers := h.fake.callsOf("answerCallbackQuery")
	txt, _ := answers[len(answers)-1].Body["text"].(string)
	if !strings.Contains(txt, "zaten işlendi") && !strings.Contains(txt, "geçerli değil") {
		t.Errorf("yarışı kaybeden tıklama zarifçe karşılanmalı: %q", txt)
	}
}

// asker — soru soran sahte motor; gerçek adaptördeki broker gibi
// request.opened/resolved olaylarını da kendisi yayınlar.
func asker(question string) enginetest.Func {
	return func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		emit(engine.Event{Kind: engine.KindSessionStarted, SessionID: "fake", Model: "fake"})
		ask := engine.Ask{Kind: "question", Question: question, Choices: []string{"Evet", "Hayır"}}
		emit(engine.Event{Kind: engine.KindRequestOpened, Request: &engine.RequestInfo{
			Kind: ask.Kind, Question: ask.Question, Choices: ask.Choices,
		}})
		ans := engine.Answer{}
		if in.OnAsk != nil {
			ans = in.OnAsk(ctx, ask)
		}
		emit(engine.Event{Kind: engine.KindRequestResolved, Request: &engine.RequestInfo{
			Kind: ask.Kind, Behavior: "answered", Source: ans.Source,
		}})

		final := "Cevabın: " + ans.Message
		if ans.Message == "" {
			final = "Cevap gelmedi, kendi kararımla ilerledim."
		}
		res := &engine.TurnResult{Ok: true, StopReason: "success", CostUSD: 0.001, NumTurns: 1, FinalText: final, ResumeCursor: "fake-imlec"}
		emit(engine.Event{Kind: engine.KindTurnCompleted, Result: res})
		return res, nil
	}
}

// Sorular PARK EDİLMEZ (pencere dolunca bot kendi kararını verir) — kart
// istek açılır açılmaz çıkar ve pencere içinde tek dokunuşla cevaplanır.
func TestQuestionCardChoiceButtons(t *testing.T) {
	h := setup(t, asker("Taslağı göndereyim mi?"), 10*time.Second)

	h.send("maili hallet")

	var card db.TelegramCard
	waitFor(t, "soru kartı açılmalı", func() bool {
		runs, _ := h.st.Q.ListRunsByBot(context.Background(), db.ListRunsByBotParams{BotID: h.bot.ID, Limit: 5})
		if len(runs) == 0 {
			return false
		}
		c, err := h.st.Q.GetTelegramCard(context.Background(), runs[0].ID)
		card = c
		return err == nil
	})

	kb := lastKeyboard(t, h)
	if kb == nil || len(kb.InlineKeyboard) != 2 {
		t.Fatalf("iki şık butonu bekleniyordu: %+v", kb)
	}
	if kb.InlineKeyboard[0][0].Text != "Evet" || kb.InlineKeyboard[1][0].Text != "Hayır" {
		t.Errorf("şıklar butona dönmemiş: %+v", kb.InlineKeyboard)
	}

	// "Evet" şıkkına bas → süreç içi kısa yol → koşu cevapla tamamlanır.
	h.click(t, "cb4", kb.InlineKeyboard[0][0].CallbackData, card.MessageID)
	waitFor(t, "cevaptan sonra koşu tamamlanmalı", func() bool {
		r, err := h.st.Q.GetRun(context.Background(), card.RunID)
		return err == nil && r.Status == "completed" && strings.Contains(r.FinalText, "Evet")
	})
}

// Pencere içinde cevaplanan izin isteği: kart açılır, tek dokunuşla onay
// verilir, koşu HİÇ park olmadan tamamlanır (K16 kısa yol).
func TestShortPathApprovalFromTelegram(t *testing.T) {
	h := setup(t, enginetest.AskingWriter("Dosya yazıldı.", "olmadı"), 10*time.Second)

	h.send("not.txt yaz")

	var card db.TelegramCard
	waitFor(t, "izin kartı açılmalı", func() bool {
		runs, _ := h.st.Q.ListRunsByBot(context.Background(), db.ListRunsByBotParams{BotID: h.bot.ID, Limit: 5})
		if len(runs) == 0 {
			return false
		}
		c, err := h.st.Q.GetTelegramCard(context.Background(), runs[0].ID)
		card = c
		return err == nil
	})
	kb := lastKeyboard(t, h)
	if kb == nil {
		t.Fatal("buton yok")
	}

	h.click(t, "cb5", kb.InlineKeyboard[0][0].CallbackData, card.MessageID)
	waitFor(t, "kısa yoldan tamamlanmalı", func() bool {
		r, err := h.st.Q.GetRun(context.Background(), card.RunID)
		return err == nil && r.Status == "completed" && r.FinalText == "Dosya yazıldı."
	})
	r, _ := h.st.Q.GetRun(context.Background(), card.RunID)
	if r.Attempt != 1 {
		t.Errorf("park olmamalıydı (kısa yol), attempt=%d", r.Attempt)
	}
}

// lastKeyboard — reply_markup taşıyan son çağrının klavyesi.
func lastKeyboard(t *testing.T, h *harness) *InlineKeyboard {
	t.Helper()
	h.fake.lock()
	defer h.fake.unlock()
	for i := len(h.fake.calls) - 1; i >= 0; i-- {
		rm, ok := h.fake.calls[i].Body["reply_markup"]
		if !ok || rm == nil {
			continue
		}
		raw, err := json.Marshal(rm)
		if err != nil {
			continue
		}
		var kb InlineKeyboard
		if json.Unmarshal(raw, &kb) == nil && len(kb.InlineKeyboard) > 0 {
			return &kb
		}
	}
	return nil
}
