//go:build integration

// ADIM 9 — düzenle + yeniden üret: eski sürüm görünmez olur (superseded),
// yeni dal taze oturum + satır içi geçmiş tekrarıyla koşar. İdem konusunda
// düzenlemenin key'i düzenlemeye aittir; aynı mesajın ikinci farklı
// düzenlemesi yarışı 409 ile düşer.
package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/esmworks/OpenAssist/internal/bus"
	"github.com/esmworks/OpenAssist/internal/config"
	"github.com/esmworks/OpenAssist/internal/engine"
	"github.com/esmworks/OpenAssist/internal/httpapi"
	"github.com/esmworks/OpenAssist/internal/orchestrator"
	"github.com/esmworks/OpenAssist/internal/store"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

func TestMesajDuzenleYenidenUret(t *testing.T) {
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

	cfg := orchestrator.DefaultConfig()
	cfg.Workers = 2
	cfg.PollEvery = 50 * time.Millisecond
	cfg.DataDir = t.TempDir()
	cfg.WorkerID = "test-" + t.Name()
	orch := orchestrator.New(st, map[string]engine.Executor{"instant": instantEngine()}, bus.New(), cfg)
	orch.Start(ctx)
	t.Cleanup(func() {
		cancel()
		orch.Wait()
		st.Close()
	})

	srv := httpapi.New(config.Config{HTTPAddr: ":0", APIToken: testBelirtec}, httpapi.Deps{
		DB: st, Store: st, Orch: orch, Bus: bus.New(), DataDir: t.TempDir(),
	})
	ts := httptest.NewServer(srv.Handler)
	belirtecliIstemci(ts)
	defer ts.Close()

	bot, err := st.Q.CreateBot(ctx, db.CreateBotParams{
		Kind: "bot", Name: "duz-" + t.Name(), Engine: "instant", AllowedTools: []string{},
	})
	if err != nil {
		t.Fatalf("bot: %v", err)
	}
	t.Cleanup(func() { _ = st.Q.DeleteBot(context.Background(), bot.ID) })
	botID := bot.ID.String()

	post := func(text, key string) string {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"text": text, "origin": "api", "key": key})
		resp, err := ts.Client().Post(ts.URL+"/api/bots/"+botID+"/messages",
			"application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		var cikti struct {
			RunID string `json:"run_id"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&cikti); err != nil {
			t.Fatalf("gövde: %v", err)
		}
		return cikti.RunID
	}

	// İlk tur: geçmişi oluşturan soru. İkinci tur: düzenleyeceğimiz soru.
	post("Birinci soru: giriş bilgisi", "")
	waitRunsSettled(t, st, ctx, botID)
	post("İkinci soru: rapor isterim", "")
	waitRunsSettled(t, st, ctx, botID)

	conv, err := st.BotConversation(ctx, bot)
	if err != nil {
		t.Fatalf("sohbet: %v", err)
	}
	msgs, err := st.Q.ListMessages(ctx, db.ListMessagesParams{ConversationID: conv.ID, Limit: 10})
	if err != nil {
		t.Fatalf("mesajlar: %v", err)
	}
	var ilkSoru db.Message
	for _, m := range msgs {
		if m.Role == "user" {
			ilkSoru = m // DESC: ilk görülen = en yeni kullanıcı mesajı
			break
		}
	}
	if ilkSoru.ID == uuid.Nil {
		t.Fatal("kullanıcı mesajı yok")
	}
	if ilkSoru.Text != "İkinci soru: rapor isterim" {
		t.Fatalf("beklenmedik mesaj seçildi: %q", ilkSoru.Text)
	}

	// Düzenle: yeni sürüm + taze koşu.
	patch := func(mid, text, key string) *http.Response {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"text": text, "origin": "api", "key": key})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPatch,
			ts.URL+"/api/messages/"+mid, bytes.NewReader(body))
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("patch: %v", err)
		}
		return resp
	}
	resp := patch(ilkSoru.ID.String(), "Düzenlenmiş soru: PPT raporu", "duz-1")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("düzenleme → %d (202 beklenirdi)", resp.StatusCode)
	}
	waitRunsSettled(t, st, ctx, botID)

	// Eski sürüm superseded: GetMessage'te işaretli, ListMessages'te yok.
	eski, err := st.Q.GetMessage(ctx, ilkSoru.ID)
	if err != nil || !eski.SupersededBy.Valid {
		t.Fatalf("eski sürüm işaretlenmedi: %+v (%v)", eski, err)
	}
	msgs, _ = st.Q.ListMessages(ctx, db.ListMessagesParams{ConversationID: conv.ID, Limit: 10})
	var gorunen []string
	for _, m := range msgs {
		if m.Role == "user" {
			gorunen = append(gorunen, m.Text)
		}
	}
	if len(gorunen) != 2 ||
		strings.Contains(strings.Join(gorunen, "|"), "İkinci soru: rapor isterim") {
		t.Fatalf("görünür akış bozuk: %v", gorunen)
	}
	if gorunen[0] != "Düzenlenmiş soru: PPT raporu" {
		t.Fatalf("düzenlenmiş sürüm görünmüyor: %v", gorunen)
	}

	// Yeni koşunun promptu satır içi geçmiş tekrarını içermeli (rewind):
	// birinci tur orada, DÜZENLENEN metnin eski hali değil.
	runs, err := st.Q.ListRunsByBot(ctx, db.ListRunsByBotParams{BotID: bot.ID, Limit: 5})
	if err != nil {
		t.Fatalf("runs: %v", err)
	}
	var yeniRun *db.Run
	for i := range runs {
		if strings.HasPrefix(runs[i].Prompt, "ÖNCEKİ KONUŞMA") {
			yeniRun = &runs[i]
		}
	}
	if yeniRun == nil {
		t.Fatalf("yeniden üretme koşusu bulunamadı: %+v", runs)
	}
	if !strings.Contains(yeniRun.Prompt, "Birinci soru") || strings.Contains(yeniRun.Prompt, "İkinci soru: rapor isterim") {
		t.Fatalf("rewind promptu yanlış: %q", yeniRun.Prompt)
	}

	// Aynı anahtarla ikinci PATCH → yeni dal AÇMAZ (idem).
	r2 := patch(ilkSoru.ID.String(), "Düzenlenmiş soru: PPT raporu", "duz-1")
	var cikti2 struct {
		RunID string `json:"run_id"`
	}
	_ = json.NewDecoder(r2.Body).Decode(&cikti2)
	_ = r2.Body.Close()
	if r2.StatusCode != http.StatusAccepted || cikti2.RunID != yeniRun.ID.String() {
		t.Fatalf("idem eden düzenleme yeni dal açtı: %d %q", r2.StatusCode, cikti2.RunID)
	}

	// Eski (superseded) sürümü DÜZENLEMEK 409 döner.
	waitRunsSettled(t, st, ctx, botID)
	resp409 := patch(ilkSoru.ID.String(), "eski sürümden bir daha", "duz-2")
	_ = resp409.Body.Close()
	if resp409.StatusCode != http.StatusConflict {
		t.Fatalf("superseded sürüme düzenleme 409 dönmeli, geldi: %d", resp409.StatusCode)
	}
}
