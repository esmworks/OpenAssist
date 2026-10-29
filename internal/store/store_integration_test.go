//go:build integration

// Entegrasyon testi: gerçek Postgres ister.
//
//	DATABASE_URL=postgres://… go test -tags integration ./internal/store
//
// CI'da postgres:17 service container ile koşar (.github/workflows/ci.yml).
package store_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/esmworks/OpenAssist/internal/store"
	sqlcdb "github.com/esmworks/OpenAssist/internal/store/db"
)

func TestMigrateAndRoundtrip(t *testing.T) {
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
	// İdempotentlik: ikinci çağrı hatasız dönmeli.
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate (tekrar): %v", err)
	}

	if err := st.Q.SetSystemInfo(ctx, sqlcdb.SetSystemInfoParams{Key: "faz", Value: "0"}); err != nil {
		t.Fatalf("SetSystemInfo: %v", err)
	}
	// Upsert: aynı anahtara ikinci yazış güncellemeli.
	if err := st.Q.SetSystemInfo(ctx, sqlcdb.SetSystemInfoParams{Key: "faz", Value: "0-guncel"}); err != nil {
		t.Fatalf("SetSystemInfo (upsert): %v", err)
	}

	got, err := st.Q.GetSystemInfo(ctx, "faz")
	if err != nil {
		t.Fatalf("GetSystemInfo: %v", err)
	}
	if got.Value != "0-guncel" {
		t.Errorf("value = %q, beklenen %q", got.Value, "0-guncel")
	}
	if got.UpdatedAt.IsZero() {
		t.Error("updated_at boş — DEFAULT now() çalışmıyor")
	}
}

// Webhook idempotency (Faz 3): aynı (source, event_id) ikinci kez 0 satır
// döner — Telegram/Google tekrar teslimatı çift koşu üretmez.
func TestWebhookEventDedup(t *testing.T) {
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

	// Test tekrar koşularında çakışmasın diye event_id'yi benzersizleştir.
	eventID := "upd-" + time.Now().Format("20060102150405.000000000")
	p := sqlcdb.RecordWebhookEventParams{Source: "telegram", EventID: eventID}

	n, err := st.Q.RecordWebhookEvent(ctx, p)
	if err != nil || n != 1 {
		t.Fatalf("ilk kayıt 1 satır olmalı: n=%d err=%v", n, err)
	}
	n, err = st.Q.RecordWebhookEvent(ctx, p)
	if err != nil || n != 0 {
		t.Fatalf("tekrar teslim 0 satır olmalı (dedup): n=%d err=%v", n, err)
	}
	// Aynı event_id farklı kaynaktan gelirse ayrı olaydır.
	n, err = st.Q.RecordWebhookEvent(ctx, sqlcdb.RecordWebhookEventParams{Source: "google", EventID: eventID})
	if err != nil || n != 1 {
		t.Fatalf("farklı kaynak ayrı olay olmalı: n=%d err=%v", n, err)
	}
}
