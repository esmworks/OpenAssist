//go:build integration

package store_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esmworks/OpenAssist/internal/store"
	sqlcdb "github.com/esmworks/OpenAssist/internal/store/db"
)

// Sohbeti Temizle: mesajlar VE motor imleçleri birlikte gider — görünür
// tarih ile motor bağlamı birbirinden ayrı düşmemeli. Konuşma kanalı kalır
// (botun tek kanalı; sonraki mesaj aynı kanala düşer).
func TestClearChatMesajlariVeImleciSiler(t *testing.T) {
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
		Kind: "bot", Name: "sohbet-temiz-" + uuid.NewString()[:8],
		ColorPrimary: "#000000", ColorSecondary: "#111111", Engine: "pi",
		AllowedTools: []string{},
	})
	if err != nil {
		t.Fatalf("CreateBot: %v", err)
	}
	t.Cleanup(func() { _ = st.Q.DeleteBot(context.Background(), bot.ID) })

	// Mesaj + koşu (SaveChat ikisini tek transaction'da yazar) ve imleç.
	if _, _, err := st.SaveChat(ctx, bot, "merhaba", "merhaba", "mac", "", nil); err != nil {
		t.Fatalf("SaveChat: %v", err)
	}
	conv, err := st.BotConversation(ctx, bot)
	if err != nil {
		t.Fatalf("BotConversation: %v", err)
	}
	if err := st.Q.UpsertBotCursor(ctx, sqlcdb.UpsertBotCursorParams{
		BotID: bot.ID, Engine: "pi", ConversationID: conv.ID, Cursor: "oturum-1",
	}); err != nil {
		t.Fatalf("UpsertBotCursor: %v", err)
	}

	if _, err := st.ClearChat(ctx, bot); err != nil {
		t.Fatalf("ClearChat: %v", err)
	}

	rows, err := st.Q.ListMessages(ctx, sqlcdb.ListMessagesParams{ConversationID: conv.ID, Limit: 10})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("temizlikten sonra %d mesaj kaldı, 0 bekleniyordu", len(rows))
	}
	if _, err := st.Q.GetBotCursor(ctx, sqlcdb.GetBotCursorParams{
		BotID: bot.ID, Engine: "pi", ConversationID: conv.ID,
	}); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("imleç silinmeliydi, err = %v", err)
	}

	// İkinci temizlik (boş konuşma) hatasız döner — menü üst üste basılabilir.
	if _, err := st.ClearChat(ctx, bot); err != nil {
		t.Fatalf("ClearChat (boş): %v", err)
	}
}
