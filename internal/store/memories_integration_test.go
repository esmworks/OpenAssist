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

	"github.com/esmworks/OpenAssist/internal/store"
	sqlcdb "github.com/esmworks/OpenAssist/internal/store/db"
	"github.com/google/uuid"
)

// TestMemoryPromptTakesNewestNotOldest — limit dolduğunda EN YENİ kayıtlar
// seçilmeli, en eskiler değil (K20). Eski sorgu `ORDER BY … created_at` +
// `LIMIT` ile en eskileri alıyordu: hafıza sınırı geçtiği gün sonradan
// yazılan hiçbir kayıt sistem promptuna girmiyordu — hata vermeden, sessizce.
//
// Çıktı sırası ayrı bir iddia: seçim yeniden eskiye, yazım eskiden yeniye.
func TestMemoryPromptTakesNewestNotOldest(t *testing.T) {
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
		Kind: "bot", Name: "hafiza-sira-" + uuid.NewString()[:8],
		ColorPrimary: "#000000", ColorSecondary: "#111111", Engine: "claudecode",
		AllowedTools: []string{},
	})
	if err != nil {
		t.Fatalf("CreateBot: %v", err)
	}
	t.Cleanup(func() { _ = st.Q.DeleteBot(context.Background(), bot.ID) })
	botID := uuid.NullUUID{UUID: bot.ID, Valid: true}

	// Üç bot hafızası, yazım sırasıyla. created_at DEFAULT now() =
	// transaction_timestamp: ayrı ifadeler ayrı damgalar alır, ama aynı
	// mikrosaniyeye düşmesinler diye araya nefes koyuyoruz.
	sira := []string{"eski", "orta", "yeni"}
	for _, icerik := range sira {
		if _, err := st.Q.UpsertMemory(ctx, sqlcdb.UpsertMemoryParams{
			Scope: "bot", BotID: botID, Kind: "fact",
			Content: icerik, ContentHash: uuid.NewString(), Confidence: 1,
		}); err != nil {
			t.Fatalf("UpsertMemory(%s): %v", icerik, err)
		}
		time.Sleep(2 * time.Millisecond)
	}

	// user scope kayıtları GLOBAL — paylaşılan test veritabanında başka
	// testlerin bıraktıkları da bu bota görünür ve seçimde önceliklidir.
	// Limiti onların üstüne kuruyoruz ki iddia yalnız bot kayıtlarını ölçsün.
	tumu, err := st.Q.ListMemoriesForPrompt(ctx, sqlcdb.ListMemoriesForPromptParams{
		BotID: botID, Limit: 1000,
	})
	if err != nil {
		t.Fatalf("ListMemoriesForPrompt (sayım): %v", err)
	}
	var userSayisi int32
	for _, m := range tumu {
		if m.Scope == "user" {
			userSayisi++
		}
	}

	got, err := st.Q.ListMemoriesForPrompt(ctx, sqlcdb.ListMemoriesForPromptParams{
		BotID: botID, Limit: userSayisi + 2,
	})
	if err != nil {
		t.Fatalf("ListMemoriesForPrompt: %v", err)
	}

	var botIcerik []string
	for _, m := range got {
		if m.Scope == "bot" {
			botIcerik = append(botIcerik, m.Content)
		}
	}

	// İki iddia birden: "eski" elendi (en yeni 2 seçildi) VE kalanlar
	// kronolojik sırada yazıldı.
	want := []string{"orta", "yeni"}
	if len(botIcerik) != len(want) {
		t.Fatalf("2 bot hafızası beklenir, %d geldi: %v", len(botIcerik), botIcerik)
	}
	for i := range want {
		if botIcerik[i] != want[i] {
			t.Fatalf("seçim/sıra yanlış: %v (beklenen %v) — en eski kayıt eleniyor mu?", botIcerik, want)
		}
	}

	// user scope hâlâ bot scope'tan önce (PRD §8) — bu davranış korundu.
	for i, m := range got {
		if m.Scope == "user" && i > 0 && got[i-1].Scope == "bot" {
			t.Fatalf("user scope bot scope'tan ÖNCE gelmeli (PRD §8): %d. sırada user var", i)
		}
	}
}

// TestMemoryRevisionChain — K20'nin ön koşulu: her içerik değişikliği geçmişe
// yazılır ve geri alınabilir. Bu olmadan konsolidasyon turu hafızayı geri
// alınamaz şekilde ezerdi.
func TestMemoryRevisionChain(t *testing.T) {
	st, bot := hafizaKurulum(t)
	ctx := context.Background()
	botID := uuid.NullUUID{UUID: bot.ID, Valid: true}

	mem, err := st.SaveMemory(ctx, store.MemoryDraft{
		Scope: "bot", BotID: botID, Kind: "fact", Content: "Kullanıcı sabah 7'de kalkar",
	})
	if err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}
	if mem.Revision != 1 {
		t.Fatalf("ilk kayıt r1 olmalı, r%d geldi", mem.Revision)
	}

	// Aynı içerik tekrar önerilirse yeni satır DA yeni revizyon DA doğmaz.
	tekrar, err := st.SaveMemory(ctx, store.MemoryDraft{
		Scope: "bot", BotID: botID, Kind: "fact", Content: "Kullanıcı sabah 7'de kalkar",
	})
	if err != nil {
		t.Fatalf("SaveMemory (tekrar): %v", err)
	}
	if tekrar.ID != mem.ID || tekrar.Revision != 1 {
		t.Fatalf("dedup bozuldu: id=%s r%d", tekrar.ID, tekrar.Revision)
	}

	// Konsolidasyon içeriği değiştirir → r2 ve geçmişte iki satır.
	birlesik, err := st.ReviseMemory(ctx, mem.ID,
		"Kullanıcı sabah 7'de kalkar ve ilk işi gelen kutusudur",
		store.YazarKonsolidasyon, "iki olgu birleştirildi", uuid.NullUUID{})
	if err != nil {
		t.Fatalf("ReviseMemory: %v", err)
	}
	if birlesik.Revision != 2 {
		t.Fatalf("revizyon 2 olmalı, r%d geldi", birlesik.Revision)
	}
	// content_hash içerikle birlikte değişmeli — yoksa dedup anahtarı yalan söyler.
	if birlesik.ContentHash == mem.ContentHash {
		t.Error("içerik değişti ama content_hash aynı kaldı")
	}

	gecmis, err := st.Q.ListMemoryRevisions(ctx, mem.ID)
	if err != nil {
		t.Fatalf("ListMemoryRevisions: %v", err)
	}
	if len(gecmis) != 2 {
		t.Fatalf("2 revizyon beklenir, %d geldi", len(gecmis))
	}
	if gecmis[0].Author != store.YazarKonsolidasyon || gecmis[1].Author != store.YazarAgent {
		t.Errorf("yazarlar yanlış: %s, %s", gecmis[0].Author, gecmis[1].Author)
	}

	// Geri alma: eski içeriğe dön, ama sayaç geri sarılmasın.
	geri, err := st.RevertMemory(ctx, mem.ID, 1)
	if err != nil {
		t.Fatalf("RevertMemory: %v", err)
	}
	if geri.Content != "Kullanıcı sabah 7'de kalkar" {
		t.Errorf("r1 içeriğine dönmeliydi: %q", geri.Content)
	}
	if geri.Revision != 3 {
		t.Errorf("geri alma yeni revizyon yazmalı (r3), r%d geldi", geri.Revision)
	}
}

// TestSupersededMemoryLeavesPrompt — katılan kayıt prompta girmez ama silinmez;
// birleştiği kayıt sonradan silinse bile DİRİLMEZ (K20: sessiz geri gelme yok).
func TestSupersededMemoryLeavesPrompt(t *testing.T) {
	st, bot := hafizaKurulum(t)
	ctx := context.Background()
	botID := uuid.NullUUID{UUID: bot.ID, Valid: true}

	kaynak, err := st.SaveMemory(ctx, store.MemoryDraft{
		Scope: "bot", BotID: botID, Kind: "fact", Content: "Toplantı notları kısa olsun",
	})
	if err != nil {
		t.Fatalf("SaveMemory kaynak: %v", err)
	}
	hedef, err := st.SaveMemory(ctx, store.MemoryDraft{
		Scope: "bot", BotID: botID, Kind: "fact", Content: "Toplantı notları kısa ve maddeli olsun",
	})
	if err != nil {
		t.Fatalf("SaveMemory hedef: %v", err)
	}

	if err := st.Q.SupersedeMemory(ctx, sqlcdb.SupersedeMemoryParams{
		ID: kaynak.ID, SupersededBy: uuid.NullUUID{UUID: hedef.ID, Valid: true},
	}); err != nil {
		t.Fatalf("SupersedeMemory: %v", err)
	}

	promptta := func() []string {
		t.Helper()
		rows, err := st.Q.ListMemoriesForPrompt(ctx, sqlcdb.ListMemoriesForPromptParams{
			BotID: botID, Limit: 1000,
		})
		if err != nil {
			t.Fatalf("ListMemoriesForPrompt: %v", err)
		}
		var out []string
		for _, m := range rows {
			if m.Scope == "bot" {
				out = append(out, m.Content)
			}
		}
		return out
	}

	got := promptta()
	if len(got) != 1 || got[0] != hedef.Content {
		t.Fatalf("yalnız birleştirilmiş kayıt promptta olmalı: %v", got)
	}

	// Hedef silinince kaynak geri GELMEMELİ: superseded_by NULL'a düşer ama
	// yaşayıp yaşamama kararı superseded_at'te.
	if _, err := st.Q.DeleteMemory(ctx, hedef.ID); err != nil {
		t.Fatalf("DeleteMemory: %v", err)
	}
	if got := promptta(); len(got) != 0 {
		t.Fatalf("katılmış kayıt hedefi silinince dirilmemeli: %v", got)
	}
}

// hafizaKurulum — bağlantı + temiz bir bot; testler arası çakışma olmasın diye
// bot adı benzersiz ve sonunda silinir (hafızalar CASCADE ile gider).
func hafizaKurulum(t *testing.T) (*store.Store, sqlcdb.Bot) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL tanımlı değil — entegrasyon testi atlandı")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	st, err := store.Open(ctx, url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	bot, err := st.Q.CreateBot(ctx, sqlcdb.CreateBotParams{
		Kind: "bot", Name: "hafiza-" + uuid.NewString()[:8],
		ColorPrimary: "#000000", ColorSecondary: "#111111", Engine: "claudecode",
		AllowedTools: []string{},
	})
	if err != nil {
		t.Fatalf("CreateBot: %v", err)
	}
	t.Cleanup(func() { _ = st.Q.DeleteBot(context.Background(), bot.ID) })
	return st, bot
}
