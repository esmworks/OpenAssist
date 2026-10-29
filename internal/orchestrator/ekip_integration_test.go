//go:build integration

// ADIM 10 — ekip sohbeti: ekipe gelen mesaj ekip kanalına yazılır, koşu
// @mention ile seçilen ya da varsayılan (ilk) üyeye açılır; üye cevabı ekip
// zaman tüneline gönderen damgalı düşer. Bilinmeyen mention fail-closed.
package orchestrator_test

import (
	"context"
	"strings"
	"testing"

	"github.com/esmworks/OpenAssist/internal/engine"
	"github.com/esmworks/OpenAssist/internal/engine/enginetest"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

func uyeCevapMotoru() enginetest.Func {
	return func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		emit(engine.Event{Kind: engine.KindSessionStarted, SessionID: "s", Model: "fake"})
		res := &engine.TurnResult{Ok: true, StopReason: "success", FinalText: "ekip yanıtı", NumTurns: 1, ResumeCursor: "c"}
		emit(engine.Event{Kind: engine.KindTurnCompleted, Result: res})
		return res, nil
	}
}

func TestEkipSohbeti(t *testing.T) {
	ctx := context.Background()
	st, o := setup(t, map[string]engine.Executor{"ekip-uye": uyeCevapMotoru()}, 0)

	ekip, err := st.Q.CreateBot(ctx, db.CreateBotParams{
		Kind: "team", Name: "Proje Ekip " + t.Name(), Engine: "ekip-uye", AllowedTools: []string{},
	})
	if err != nil {
		t.Fatalf("ekip: %v", err)
	}
	t.Cleanup(func() { _ = st.Q.DeleteBot(context.Background(), ekip.ID) })

	mkUye := func(ad string) db.Bot {
		t.Helper()
		b, err := st.Q.CreateBot(ctx, db.CreateBotParams{
			Kind: "bot", Name: ad, Engine: "ekip-uye", AllowedTools: []string{},
		})
		if err != nil {
			t.Fatalf("üye %s: %v", ad, err)
		}
		t.Cleanup(func() { _ = st.Q.DeleteBot(context.Background(), b.ID) })
		return b
	}
	ayse := mkUye("Ayşe " + t.Name())
	bora := mkUye("Bora " + t.Name())
	if err := st.Q.AddTeamMember(ctx, db.AddTeamMemberParams{TeamID: ekip.ID, MemberID: ayse.ID, Position: 0}); err != nil {
		t.Fatalf("üye ekle: %v", err)
	}
	if err := st.Q.AddTeamMember(ctx, db.AddTeamMemberParams{TeamID: ekip.ID, MemberID: bora.ID, Position: 1}); err != nil {
		t.Fatalf("üye ekle: %v", err)
	}

	// Varsayılan konuşmacı: ilk üye (Ayşe).
	r1, err := o.EnqueueChat(ctx, ekip, "ilk iş: rapor", "")
	if err != nil {
		t.Fatalf("ekibe mesaj: %v", err)
	}
	if r1.BotID != ayse.ID {
		t.Fatalf("varsayılan konuşmacı Ayşe olmalıydı, koşu %s'te", r1.BotID)
	}
	waitStatus(t, st, r1.ID, "completed")

	// Ekip kanalı grup sohbeti; mesaj ekip damgalı, cevap üye damgalı.
	ekipConv, err := st.BotConversation(ctx, ekip)
	if err != nil {
		t.Fatalf("ekip sohbeti: %v", err)
	}
	if ekipConv.Kind != "group" {
		t.Fatalf("ekip sohbeti group olmalı: %q", ekipConv.Kind)
	}
	if !r1.ConversationID.Valid || r1.ConversationID.UUID != ekipConv.ID {
		t.Fatalf("koşu ekip kanalına bağlı değil: %v", r1.ConversationID)
	}
	msgs, _ := st.Q.ListMessages(ctx, db.ListMessagesParams{ConversationID: ekipConv.ID, Limit: 10})
	var kullaniciEkipte, uyeCevabi bool
	for _, m := range msgs {
		switch {
		case m.Role == "user" && m.BotID == ekip.ID:
			kullaniciEkipte = true
		case m.Role == "bot" && m.BotID == ayse.ID && m.Text == "ekip yanıtı":
			uyeCevabi = true
		}
	}
	if !kullaniciEkipte || !uyeCevabi {
		t.Fatalf("ekip zaman tüneli eksik: %+v", msgs)
	}

	// @mention: Bora'ya yönlenir, AYNI kanala yazılır.
	r2, err := o.EnqueueChat(ctx, ekip, "@"+bora.Name+" ikinci iş", "")
	if err != nil {
		t.Fatalf("mention mesajı: %v", err)
	}
	if r2.BotID != bora.ID {
		t.Fatalf("mention Bora'ya gitmedi: %s", r2.BotID)
	}
	waitStatus(t, st, r2.ID, "completed")

	// Bilinmeyen mention fail-closed: koşu açılmamalı.
	if _, err := o.EnqueueChat(ctx, ekip, "@HayaletÜye selam", ""); err == nil ||
		!strings.Contains(err.Error(), "yok") {
		t.Fatalf("bilinmeyen mention hata dönmeliydi: %v", err)
	}

	// Üye imleçleri AYRI kanal anahtarlı: Ayşe'nin grup zinciri üstü boşta
	// üyelerin kendi sohbet zincirinden ayrı yaşar (göç 00014 backfill'i).
	cur, err := st.Q.GetBotCursor(ctx, db.GetBotCursorParams{
		BotID: ayse.ID, Engine: "ekip-uye", ConversationID: ekipConv.ID,
	})
	if err != nil || cur.Cursor != "c" {
		t.Fatalf("grup zinciri imleci yazılmadı: %q (%v)", cur.Cursor, err)
	}
}
