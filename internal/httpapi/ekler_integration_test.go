//go:build integration

// ADIM 5: ek yükleme → mesaja bağlama → listeleme → indirme zinciri gerçek
// Postgres + diske yazılan dosyayla test edilir. Motor promptunun ek yollarını
// içerdiği de doğrulanır (bot dosyaya sunucuda erişir).
package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
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

// waitRunsSettled — botun koşuları kuyruk/çalışma durumundan çıkana kadar
// bekler; cleanup silme yarışını (FK ihlali gürültüsü) önler.
func waitRunsSettled(t *testing.T, st *store.Store, ctx context.Context, botID string) {
	t.Helper()
	id, err := uuid.Parse(botID)
	if err != nil {
		t.Fatalf("bot id: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		rs, err := st.Q.ListRunsByBot(ctx, db.ListRunsByBotParams{BotID: id, Limit: 100})
		if err != nil {
			t.Fatalf("runs: %v", err)
		}
		bekliyor := false
		for _, r := range rs {
			if r.Status == "queued" || r.Status == "running" {
				bekliyor = true
			}
		}
		if !bekliyor {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("koşular 10 sn içinde oturmadı")
}

func TestAttachmentRoundTrip(t *testing.T) {
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

	dataDir := t.TempDir()
	srv := httpapi.New(config.Config{HTTPAddr: ":0", APIToken: testBelirtec}, httpapi.Deps{
		DB: st, Store: st, Orch: orch, Bus: bus.New(), DataDir: dataDir,
	})
	ts := httptest.NewServer(srv.Handler)
	belirtecliIstemci(ts)
	defer ts.Close()

	bot, err := st.Q.CreateBot(ctx, db.CreateBotParams{
		Kind: "bot", Name: "ek-" + t.Name(), Engine: "instant", AllowedTools: []string{},
	})
	if err != nil {
		t.Fatalf("bot: %v", err)
	}
	t.Cleanup(func() { _ = st.Q.DeleteBot(context.Background(), bot.ID) })
	botID := bot.ID.String()

	// 1) Yükle: multipart "file" — Türkçe dosya adıyla (Content-Disposition
	// kodlaması gidiş-dönüşte bozulmamalı).
	icerik := []byte("%PDF-1.4 sahte fatura içeriği")
	upload := func(name string) map[string]any {
		t.Helper()
		var gövde bytes.Buffer
		mw := multipart.NewWriter(&gövde)
		part, err := mw.CreateFormFile("file", name)
		if err != nil {
			t.Fatalf("multipart: %v", err)
		}
		if _, err := part.Write(icerik); err != nil {
			t.Fatalf("yazım: %v", err)
		}
		if err := mw.Close(); err != nil {
			t.Fatalf("kapat: %v", err)
		}
		resp, err := ts.Client().Post(ts.URL+"/api/bots/"+botID+"/attachments",
			mw.FormDataContentType(), &gövde)
		if err != nil {
			t.Fatalf("upload: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("yükleme → %d: %s", resp.StatusCode, body)
		}
		var out map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("gövde: %v", err)
		}
		return out
	}
	ek := upload("fatura_şablon.pdf")
	ekID, _ := ek["id"].(string)
	if ek["name"] != "fatura_şablon.pdf" || ek["kind"] != "pdf" {
		t.Fatalf("ek metadası yanlış: %+v", ek)
	}

	// 2) Mesajı ek id'siyle gönder (metin boş — yalnız ek kabul edilmeli).
	body, _ := json.Marshal(map[string]any{
		"origin": "api", "attachment_ids": []string{ekID},
	})
	resp, err := ts.Client().Post(ts.URL+"/api/bots/"+botID+"/messages",
		"application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("ekli mesaj → %d (202 beklenirdi)", resp.StatusCode)
	}

	// 3) Koşu otursun; motor promptu ek yolunu içermeli.
	waitRunsSettled(t, st, ctx, bot.ID.String())
	rs, err := st.Q.ListRunsByBot(ctx, db.ListRunsByBotParams{BotID: bot.ID, Limit: 5})
	if err != nil || len(rs) == 0 {
		t.Fatalf("koşu yok: %v", err)
	}
	if !strings.Contains(rs[0].Prompt, "Ekli dosyalar") {
		t.Fatalf("motor promptu ek blokunu içermiyor: %q", rs[0].Prompt)
	}

	// 4) Mesaj listeleme ekleri taşımalı.
	conv, err := st.BotConversation(ctx, bot)
	if err != nil {
		t.Fatalf("sohbet: %v", err)
	}
	msgs, err := st.Q.ListMessages(ctx, db.ListMessagesParams{ConversationID: conv.ID, Limit: 10})
	if err != nil {
		t.Fatalf("mesajlar: %v", err)
	}
	var userDB db.Message
	for _, m := range msgs {
		if m.Role == "user" {
			userDB = m
		}
	}
	var got []map[string]any
	gresp, err := ts.Client().Get(ts.URL + "/api/bots/" + botID + "/messages")
	if err != nil {
		t.Fatalf("listele: %v", err)
	}
	defer func() { _ = gresp.Body.Close() }()
	if err := json.NewDecoder(gresp.Body).Decode(&got); err != nil {
		t.Fatalf("liste gövdesi: %v", err)
	}
	var ats []any
	for _, item := range got {
		if item["id"] == userDB.ID.String() {
			ats, _ = item["attachments"].([]any)
		}
	}
	if len(ats) != 1 {
		t.Fatalf("mesajda 1 ek bekleniyordu: %v", ats)
	}

	// 5) İndirme gidiş-dönüş: içerik birebir.
	dresp, err := ts.Client().Get(ts.URL + "/api/attachments/" + ekID + "/file")
	if err != nil {
		t.Fatalf("indir: %v", err)
	}
	defer func() { _ = dresp.Body.Close() }()
	indirilen, err := io.ReadAll(dresp.Body)
	if err != nil {
		t.Fatalf("indirme okuma: %v", err)
	}
	if !bytes.Equal(indirilen, icerik) {
		t.Fatal("indirilen içerik yüklenenle aynı değil")
	}
}
