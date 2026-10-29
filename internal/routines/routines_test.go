package routines

import (
	"strings"
	"testing"
	"time"

	db "github.com/esmworks/OpenAssist/internal/store/db"
)

func TestNextRunHonorsTimezone(t *testing.T) {
	// §6 saat dilimi tuzağı: "her gün 07:00" Europe/Istanbul'da (UTC+3)
	// 04:00 UTC'dir.
	after := time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)
	next, err := NextRun("0 7 * * *", "Europe/Istanbul", after)
	if err != nil {
		t.Fatalf("NextRun: %v", err)
	}
	want := time.Date(2026, 8, 14, 4, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("Istanbul 07:00 = 04:00 UTC olmalı, geldi: %s", next)
	}
}

func TestNextRunWeekdays(t *testing.T) {
	// Cumartesi (2026-08-15) sonrası "hafta içi 09:00" → Pazartesi.
	after := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	next, err := NextRun("0 9 * * 1-5", "UTC", after)
	if err != nil {
		t.Fatalf("NextRun: %v", err)
	}
	if next.Weekday() != time.Monday || next.Hour() != 9 {
		t.Fatalf("Pazartesi 09:00 beklenirdi, geldi: %s (%s)", next, next.Weekday())
	}
}

func TestValidateRejectsBadInput(t *testing.T) {
	cases := []struct {
		name, kind, cron, tz string
	}{
		{"bozuk cron", "cron", "bu cron değil", "UTC"},
		{"bozuk tz", "cron", "0 7 * * *", "Mars/Olympus"},
		{"interval v1'de yok", "interval", "", "UTC"},
	}
	for _, c := range cases {
		if err := Validate(c.kind, c.cron, c.tz); err == nil {
			t.Errorf("%s: hata beklenirdi", c.name)
		}
	}
	if err := Validate("cron", "*/15 * * * *", "Europe/Istanbul"); err != nil {
		t.Errorf("geçerli tanım reddedildi: %v", err)
	}
}

func TestBuildPromptCarriesContract(t *testing.T) {
	r := db.Routine{
		Name: "Sabah taraması", InputSource: "Gmail gelen kutusu",
		ExpectedOutput: "cevap bekleyenlere taslak", ApprovalNote: "gönderim her zaman onaylı",
		StalePolicy: "fail",
	}
	p := BuildPrompt(r)
	for _, want := range []string{"Sabah taraması", "Gmail gelen kutusu", "taslak", "onaylı", "DURDUR", "hazırla, gönderme"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt %q içermeli:\n%s", want, p)
		}
	}
	if strings.Contains(p, "eski veriyle devam") {
		t.Error("fail politikasında warn_stale metni olmamalı")
	}

	r.StalePolicy = "warn_stale"
	if p := BuildPrompt(r); !strings.Contains(p, "AÇIKÇA belirt") {
		t.Error("warn_stale politikası açık uyarı talimatı taşımalı")
	}
}
