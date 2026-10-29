//go:build integration

// Faz 10 — v1 kapanış senaryosu (uçtan uca, tüm sistemler birlikte):
//
//	Telegram'dan görev → server koşusu → onay kartı → tek dokunuş onay →
//	Mac runner'da dosya işlemi (WS) → local_audit → Aktivite akışı.
//
// Gerçek parçalar: Postgres, orkestratör, Telegram gateway (sahte Bot API
// sunucusuna karşı), runner hub + gerçek WebSocket istemcisi, HTTP API.
// Sahte parçalar: motor (Claude yerine hub'ı çağıran Func) ve Bot API.
package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"github.com/esmworks/OpenAssist/internal/bus"
	"github.com/esmworks/OpenAssist/internal/config"
	"github.com/esmworks/OpenAssist/internal/engine"
	"github.com/esmworks/OpenAssist/internal/engine/enginetest"
	"github.com/esmworks/OpenAssist/internal/httpapi"
	"github.com/esmworks/OpenAssist/internal/orchestrator"
	"github.com/esmworks/OpenAssist/internal/runner"
	"github.com/esmworks/OpenAssist/internal/store"
	db "github.com/esmworks/OpenAssist/internal/store/db"
	"github.com/esmworks/OpenAssist/internal/telegram"
)

const chatID = int64(424242)

// ── sahte Telegram Bot API ─────────────────────────────────────────────

type botCall struct {
	Method string
	Body   map[string]any
}

type fakeTelegram struct {
	srv *httptest.Server

	mu      sync.Mutex
	calls   []botCall
	updates []telegram.Update
	nextMsg int64
}

func newFakeTelegram() *fakeTelegram {
	f := &fakeTelegram{nextMsg: 100}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)

		respond := func(result any) {
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
		}
		switch method {
		case "getMe":
			respond(telegram.User{ID: 1, FirstName: "e2e", Username: "e2e_bot"})
		case "getUpdates":
			offset := int64(0)
			if o, ok := body["offset"].(float64); ok {
				offset = int64(o)
			}
			// Kısa long-poll: 150 ms içinde uygun update yoksa boş dön.
			deadline := time.Now().Add(150 * time.Millisecond)
			for {
				f.mu.Lock()
				var due []telegram.Update
				for _, u := range f.updates {
					if u.UpdateID >= offset {
						due = append(due, u)
					}
				}
				f.mu.Unlock()
				if len(due) > 0 || time.Now().After(deadline) {
					respond(due)
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		case "sendMessage":
			f.mu.Lock()
			f.nextMsg++
			id := f.nextMsg
			f.calls = append(f.calls, botCall{Method: method, Body: body})
			f.mu.Unlock()
			respond(telegram.Message{MessageID: id, Chat: telegram.Chat{ID: chatID}})
		default:
			f.mu.Lock()
			f.calls = append(f.calls, botCall{Method: method, Body: body})
			f.mu.Unlock()
			respond(true)
		}
	}))
	return f
}

func (f *fakeTelegram) inject(u telegram.Update) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, u)
}

func (f *fakeTelegram) callsOf(method string) []botCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []botCall
	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// keyboardToken — verilen etiketli butonun EN GÜNCEL callback token'ını
// döndürür. Kart akış mesajının düzenlenmesiyle gelir (editMessageText),
// yeni mesajla da gelebilir; her yeniden çizimde eski token'lar silindiği
// için son eşleşme alınır.
func (f *fakeTelegram) keyboardToken(label string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	token := ""
	for _, c := range f.calls {
		markup, ok := c.Body["reply_markup"].(map[string]any)
		if !ok {
			continue
		}
		rows, _ := markup["inline_keyboard"].([]any)
		for _, row := range rows {
			btns, _ := row.([]any)
			for _, btn := range btns {
				b, _ := btn.(map[string]any)
				if b["text"] == label {
					token, _ = b["callback_data"].(string)
				}
			}
		}
	}
	return token
}

// dump — sahte Bot API'ye gelen tüm çağrıları test günlüğüne yazar
// (başarısızlıkta teşhis için).
func (f *fakeTelegram) dump(t *testing.T) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		raw, _ := json.Marshal(c.Body)
		t.Logf("bot api çağrısı: %s %s", c.Method, raw)
	}
}

// ── sahte Swift runner (gerçek WebSocket) ──────────────────────────────

func connectFakeRunner(t *testing.T, wsURL string) func() {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("runner bağlanamadı: %v", err)
	}
	hello := map[string]any{
		"type": "hello", "name": "e2e-mac", "version": "0",
		"tools": []map[string]any{{
			"name": "write_file", "description": "dosya yazar",
			"input_schema": map[string]any{"type": "object"}, "class": "write",
		}},
	}
	raw, _ := json.Marshal(hello)
	if err := conn.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Fatalf("hello: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			var call struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Tool string `json:"tool"`
			}
			if json.Unmarshal(data, &call) != nil || call.Type != "call" {
				continue
			}
			res, _ := json.Marshal(map[string]any{
				"type": "result", "id": call.ID, "ok": true,
				"text": "yazıldı: ~/Desktop/not.txt (e2e)",
			})
			wctx, wcancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = conn.Write(wctx, websocket.MessageText, res)
			wcancel()
		}
	}()
	return func() {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		<-done
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("beklenen olmadı: %s", what)
}

// ── senaryo ────────────────────────────────────────────────────────────

func TestV1EndToEnd(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL tanımlı değil — entegrasyon testi atlandı")
	}
	ctx, cancel := context.WithCancel(context.Background())
	st, err := store.Open(ctx, url)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Runner hub + denetim günlüğü + gerçek WS istemcisi.
	hub := runner.NewHub()
	hub.Audit = func(actx context.Context, device, tool string, args json.RawMessage, ok bool, result string) {
		if len(args) == 0 {
			args = []byte("{}")
		}
		_ = st.Q.RecordLocalAudit(actx, db.RecordLocalAuditParams{
			Device: device, Tool: tool, Args: args, Ok: ok, Result: result,
		})
	}
	wsSrv := httptest.NewServer(hub.WSHandler())
	defer wsSrv.Close()
	closeRunner := connectFakeRunner(t, "ws"+strings.TrimPrefix(wsSrv.URL, "http"))
	defer closeRunner()
	waitFor(t, "runner bağlandı", hub.Connected)

	// Sahte motor: izin ister; onay gelirse Mac aracını hub üzerinden çalıştırır
	// (CLI→MCP gateway→hub zincirinin testteki karşılığı).
	macTool := "mcp__mac__write_file"
	eng := enginetest.Func(func(fctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		emit(engine.Event{Kind: engine.KindSessionStarted, SessionID: "e2e", Model: "fake"})
		emit(engine.Event{Kind: engine.KindRequestOpened, Request: &engine.RequestInfo{Kind: "permission", Tool: macTool}})
		ans := in.OnAsk(fctx, engine.Ask{Kind: "permission", Tool: macTool, Input: json.RawMessage(`{"path":"~/Desktop/not.txt","content":"e2e"}`)})
		emit(engine.Event{Kind: engine.KindRequestResolved, Request: &engine.RequestInfo{Kind: "permission", Tool: macTool, Behavior: ans.Behavior, Source: ans.Source}})
		res := &engine.TurnResult{Ok: true, StopReason: "success", NumTurns: 1, ResumeCursor: "e2e-c"}
		if ans.Behavior == "allow" {
			out, err := hub.Call(fctx, "write_file", json.RawMessage(`{"path":"~/Desktop/not.txt","content":"e2e"}`))
			if err != nil {
				out = "hata: " + err.Error()
			}
			res.FinalText = "Not oluşturuldu — " + out
		} else {
			res.FinalText = "İzin verilmedi, dosya yazılmadı."
		}
		emit(engine.Event{Kind: engine.KindTurnCompleted, Result: res})
		return res, nil
	})

	b := bus.New()
	ocfg := orchestrator.DefaultConfig()
	ocfg.Workers = 2
	ocfg.ApprovalWait = 5 * time.Second // kart tek dokunuşla kısa yoldan çözülür
	ocfg.PollEvery = 100 * time.Millisecond
	ocfg.DataDir = t.TempDir()
	ocfg.WorkerID = "e2e"
	ocfg.ToolClasses = runner.StaticClasses()

	// Telegram gateway (orkestratörden önce, kanca için — üretim sırası).
	fakeTG := newFakeTelegram()
	defer fakeTG.srv.Close()
	t.Cleanup(func() {
		if t.Failed() {
			fakeTG.dump(t)
		}
	})
	api := telegram.NewAPI("e2e-token")
	api.BaseURL = fakeTG.srv.URL
	tcfg := telegram.DefaultConfig()
	tcfg.PollTimeout = 1
	tcfg.EditEvery = 100 * time.Millisecond
	tcfg.RetryWait = 100 * time.Millisecond
	tcfg.ToolClasses = ocfg.ToolClasses
	tg := telegram.New(api, st, nil, b, tcfg)

	orch := orchestrator.New(st, map[string]engine.Executor{"fake": eng}, b, ocfg)
	orch.Start(ctx)
	tg.SetOrchestrator(orch)

	bot, err := st.Q.CreateBot(ctx, db.CreateBotParams{
		Kind: "bot", Name: "e2e-" + t.Name(), Engine: "fake", AllowedTools: []string{},
	})
	if err != nil {
		t.Fatalf("bot: %v", err)
	}
	t.Cleanup(func() { _ = st.Q.DeleteBot(context.Background(), bot.ID) })
	if _, err := st.Q.UpsertTelegramChat(ctx, db.UpsertTelegramChatParams{ChatID: chatID, Label: "e2e"}); err != nil {
		t.Fatalf("allowlist: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Q.DeleteTelegramChat(context.Background(), chatID) })
	if err := st.Q.SetTelegramActiveBot(ctx, db.SetTelegramActiveBotParams{
		ChatID: chatID, ActiveBotID: uuid.NullUUID{UUID: bot.ID, Valid: true},
	}); err != nil {
		t.Fatalf("aktif bot: %v", err)
	}

	go func() { _ = tg.Run(ctx) }()
	t.Cleanup(func() { cancel(); orch.Wait(); st.Close() })

	// HTTP API (Aktivite doğrulaması için). Özel API bearer ister; istemciye
	// başlığı taşıyıcı takılır ki çağrı yerleri sade kalsın.
	const e2eBelirtec = "e2e-token-0123456789abcdef0123456789abcdef"
	apiSrv := httptest.NewServer(httpapi.New(config.Config{HTTPAddr: ":0", APIToken: e2eBelirtec},
		httpapi.Deps{DB: st, Store: st, Orch: orch, Bus: b}).Handler)
	defer apiSrv.Close()
	apiSrv.Client().Transport = e2eBearer{alt: apiSrv.Client().Transport, token: e2eBelirtec}

	// 1) Telegram'dan görev gelir.
	fakeTG.inject(telegram.Update{UpdateID: 1, Message: &telegram.Message{
		MessageID: 11, Chat: telegram.Chat{ID: chatID}, Text: "Masaüstüne not.txt yaz",
	}})

	// 2) Onay kartı düşer (request.opened anında) — Onayla token'ını al.
	var allowToken string
	waitFor(t, "onay kartı geldi", func() bool {
		allowToken = fakeTG.keyboardToken("✅ Onayla")
		return allowToken != ""
	})

	// 3) Tek dokunuş onay (callback) — kısa yol.
	fakeTG.inject(telegram.Update{UpdateID: 2, CallbackQuery: &telegram.CallbackQuery{
		ID: "cb1", Data: allowToken,
		Message: &telegram.Message{MessageID: 101, Chat: telegram.Chat{ID: chatID}},
	}})

	// 4) Koşu tamamlanır, Mac işlemi denetim günlüğüne düşer.
	var runID uuid.UUID
	waitFor(t, "koşu tamamlandı", func() bool {
		runs, err := st.Q.ListRunsByBot(context.Background(), db.ListRunsByBotParams{BotID: bot.ID, Limit: 5})
		if err != nil || len(runs) == 0 {
			return false
		}
		runID = runs[0].ID
		return runs[0].Status == "completed" && strings.Contains(runs[0].FinalText, "yazıldı")
	})
	run, _ := st.Q.GetRun(ctx, runID)
	if run.Origin != "telegram" {
		t.Errorf("koşu kaynağı telegram olmalı: %s", run.Origin)
	}

	waitFor(t, "local_audit kaydı", func() bool {
		rows, err := st.Q.ListLocalAudit(context.Background(), 20)
		if err != nil {
			return false
		}
		for _, r := range rows {
			if r.Tool == "write_file" && r.Ok && r.Device == "e2e-mac" {
				return true
			}
		}
		return false
	})

	// 5) Aktivite akışı iki kaynağı da gösterir (K19).
	resp, err := apiSrv.Client().Get(apiSrv.URL + "/api/activity?limit=50")
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var feed []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&feed); err != nil {
		t.Fatalf("activity decode: %v", err)
	}
	var sawRun, sawLocal bool
	for _, it := range feed {
		if it.Type == "run" && strings.Contains(it.Text, "not.txt") {
			sawRun = true
		}
		if it.Type == "local" && strings.Contains(it.Text, "write_file") {
			sawLocal = true
		}
	}
	if !sawRun || !sawLocal {
		t.Errorf("aktivite akışı koşu+yerel işlemi göstermeli (run=%v local=%v)", sawRun, sawLocal)
	}

	// 6) Sonuç Telegram'a döner (yeni mesaj ya da düzenleme).
	waitFor(t, "final metin Telegram'da", func() bool {
		for _, c := range append(fakeTG.callsOf("sendMessage"), fakeTG.callsOf("editMessageText")...) {
			if s, ok := c.Body["text"].(string); ok && strings.Contains(s, "yazıldı") {
				return true
			}
		}
		return false
	})
}

// e2eBearer — özel API'nin Authorization başlığını her isteğe ekler.
type e2eBearer struct {
	alt   http.RoundTripper
	token string
}

func (b e2eBearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	alt := b.alt
	if alt == nil {
		alt = http.DefaultTransport
	}
	return alt.RoundTrip(r)
}
