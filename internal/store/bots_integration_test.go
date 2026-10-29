//go:build integration

// Entegrasyon testi: gerçek Postgres ister.
//
//	DATABASE_URL=postgres://… go test -tags integration ./internal/store
package store_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/esmworks/OpenAssist/internal/store"
	sqlcdb "github.com/esmworks/OpenAssist/internal/store/db"
)

// TestEngineChangeKeepsCursorsSeparate — ADIM 7 (Ek C.2): imleçler motor
// BAŞINA yaşar. Motor değişimi eski motorun zincirini SİLMEZ (yeni semantik);
// yeni motor kendi boş satırıyla başlar, bota geri dönünce kaldığı yerden
// sürer. Koşu yazımı run.Engine anahtarına gider.
func TestEngineChangeKeepsCursorsSeparate(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL tanımlı değil — entegrasyon testi atlandı")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, err := store.Open(ctx, url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	bot, err := st.Q.CreateBot(ctx, sqlcdb.CreateBotParams{
		Kind: "bot", Name: "motor-imlec-" + uuid.NewString()[:8],
		ColorPrimary: "#000000", ColorSecondary: "#111111", Engine: "claudecode",
		AllowedTools: []string{},
	})
	if err != nil {
		t.Fatalf("CreateBot: %v", err)
	}
	t.Cleanup(func() { _ = st.Q.DeleteBot(context.Background(), bot.ID) })

	conv, err := st.BotConversation(ctx, bot)
	if err != nil {
		t.Fatalf("BotConversation: %v", err)
	}
	if err := st.Q.UpsertBotCursor(ctx, sqlcdb.UpsertBotCursorParams{
		BotID: bot.ID, Engine: "claudecode", ConversationID: conv.ID,
		Cursor: "claude-oturum-abc",
	}); err != nil {
		t.Fatalf("UpsertBotCursor: %v", err)
	}

	guncelle := func(motor string) sqlcdb.Bot {
		t.Helper()
		b, err := st.Q.UpdateBot(ctx, sqlcdb.UpdateBotParams{
			ID: bot.ID, Name: bot.Name, ColorPrimary: bot.ColorPrimary,
			ColorSecondary: bot.ColorSecondary, Engine: motor, AllowedTools: []string{},
		})
		if err != nil {
			t.Fatalf("UpdateBot(%s): %v", motor, err)
		}
		return b
	}

	// Aynı motorla güncelleme claude imlecine dokunmamalı.
	guncelle("claudecode")
	c, err := st.Q.GetBotCursor(ctx, sqlcdb.GetBotCursorParams{
		BotID: bot.ID, Engine: "claudecode", ConversationID: conv.ID,
	})
	if err != nil || c.Cursor != "claude-oturum-abc" {
		t.Fatalf("claude imleci korunmalı: %q (%v)", c.Cursor, err)
	}

	// Motor değişince claude zinciri DURUR, codex'in satırı yoktur/bos.
	guncelle("codex")
	if c, err := st.Q.GetBotCursor(ctx, sqlcdb.GetBotCursorParams{
		BotID: bot.ID, Engine: "claudecode", ConversationID: conv.ID,
	}); err != nil || c.Cursor != "claude-oturum-abc" {
		t.Fatalf("motor değişimi eski zinciri sildi: %q (%v)", c.Cursor, err)
	}
	if c, err := st.Q.GetBotCursor(ctx, sqlcdb.GetBotCursorParams{
		BotID: bot.ID, Engine: "codex", ConversationID: conv.ID,
	}); err == nil && c.Cursor != "" {
		t.Fatalf("yeni motor imleçsiz başlamalı: %q", c.Cursor)
	}

	// Codex zinciri kendi anahtarına yazılır; claude zinciri yine durur.
	if err := st.Q.UpsertBotCursor(ctx, sqlcdb.UpsertBotCursorParams{
		BotID: bot.ID, Engine: "codex", ConversationID: conv.ID,
		Cursor: "codex-thread-xyz",
	}); err != nil {
		t.Fatalf("UpsertBotCursor(codex): %v", err)
	}
	if c, _ := st.Q.GetBotCursor(ctx, sqlcdb.GetBotCursorParams{
		BotID: bot.ID, Engine: "codex", ConversationID: conv.ID,
	}); c.Cursor != "codex-thread-xyz" {
		t.Fatalf("codex imleci yazılmadı: %q", c.Cursor)
	}
	if c, _ := st.Q.GetBotCursor(ctx, sqlcdb.GetBotCursorParams{
		BotID: bot.ID, Engine: "claudecode", ConversationID: conv.ID,
	}); c.Cursor != "claude-oturum-abc" {
		t.Fatalf("codex yazımı claude zincirini etkiledi: %q", c.Cursor)
	}
}
