//go:build integration

// Entegrasyon testi: gerçek Postgres ister.
//
//	DATABASE_URL=postgres://… go test -tags integration ./internal/memory
package memory_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/esmworks/OpenAssist/internal/engine"
	"github.com/esmworks/OpenAssist/internal/engine/enginetest"
	"github.com/esmworks/OpenAssist/internal/memory"
	"github.com/esmworks/OpenAssist/internal/store"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

func kurulum(t *testing.T) (*store.Store, db.Bot) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL tanımlı değil — entegrasyon testi atlandı")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)

	st, err := store.Open(ctx, url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	bot, err := st.Q.CreateBot(ctx, db.CreateBotParams{
		Kind: "bot", Name: "konsolidasyon-" + uuid.NewString()[:8],
		ColorPrimary: "#000000", ColorSecondary: "#111111",
		Engine: "fake", AllowedTools: []string{},
	})
	if err != nil {
		t.Fatalf("CreateBot: %v", err)
	}
	t.Cleanup(func() { _ = st.Q.DeleteBot(context.Background(), bot.ID) })
	return st, bot
}

// yaz — bot kapsamında n adet hafıza yazar ve id'lerini döner.
func yaz(t *testing.T, st *store.Store, bot db.Bot, icerikler ...string) []db.Memory {
	t.Helper()
	ctx := context.Background()
	var out []db.Memory
	for _, ic := range icerikler {
		m, err := st.SaveMemory(ctx, store.MemoryDraft{
			Scope: "bot", BotID: uuid.NullUUID{UUID: bot.ID, Valid: true},
			Kind: "fact", Content: ic,
		})
		if err != nil {
			t.Fatalf("SaveMemory(%q): %v", ic, err)
		}
		out = append(out, m)
	}
	return out
}

// yeniConsolidator — testlerin standart kurulumu. DataDir mutlaka verilir:
// boş bırakılsa çalışma dizini testin kendi klasörüne açılırdı.
func yeniConsolidator(t *testing.T, st *store.Store, exec engine.Executor) *memory.Consolidator {
	t.Helper()
	return memory.New(st, map[string]engine.Executor{"fake": exec}, memory.Config{DataDir: t.TempDir()})
}

// planlayan — verdiği JSON planını döndüren sahte motor; ayrıca aldığı
// TurnInput'u yakalar (imleç/araç iddiaları için).
func planlayan(plan any, yakala *engine.TurnInput) enginetest.Func {
	return func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		if yakala != nil {
			*yakala = in
		}
		gövde, _ := json.Marshal(plan)
		res := &engine.TurnResult{
			Ok: true, StopReason: "success", NumTurns: 1,
			FinalText: string(gövde), ResumeCursor: "konsolidasyon-imleci",
		}
		emit(engine.Event{Kind: engine.KindTurnCompleted, Result: res})
		return res, nil
	}
}

// TestConsolidationMergesAndSummarizes — turun mutlu yolu: birleştirme
// yazılır, katılan kayıt prompttan düşer, özet üretilir, geçmiş korunur.
func TestConsolidationMergesAndSummarizes(t *testing.T) {
	st, bot := kurulum(t)
	ctx := context.Background()

	m := yaz(t, st, bot,
		"Kullanıcı toplantı notlarını kısa ister",
		"Kullanıcı toplantı notlarında madde işareti sever",
		"Kullanıcı sabah 7'de kalkar",
		"Kullanıcının saat dilimi Europe/Istanbul",
		"Eski proje adı Codezal'dı",
	)

	plan := map[string]any{
		"birlestir": []map[string]any{{
			"tut":     m[0].ID.String(),
			"kat":     []string{m[1].ID.String()},
			"icerik":  "Kullanıcı toplantı notlarını kısa ve maddeli ister",
			"gerekce": "aynı tercihin iki parçası",
		}},
		"ele":  []map[string]any{{"id": m[4].ID.String(), "gerekce": "artık geçerli değil"}},
		"ozet": "- Notlar kısa ve maddeli\n- Sabah 7, Europe/Istanbul",
	}

	var girdi engine.TurnInput
	c := yeniConsolidator(t, st, planlayan(plan, &girdi))
	rapor, err := c.ConsolidateBot(ctx, bot)
	if err != nil {
		t.Fatalf("ConsolidateBot: %v", err)
	}
	if rapor.Atlandi != "" {
		t.Fatalf("tur atlanmamalıydı: %s", rapor.Atlandi)
	}
	if rapor.Birlestime != 1 || rapor.Elenen != 1 {
		t.Errorf("1 birleştirme + 1 eleme beklenir: %+v", rapor)
	}

	// Konsolidasyon botun SOHBETİNE girmemeli: imleçsiz ve araçsız koşar.
	if girdi.ResumeCursor != "" {
		t.Errorf("konsolidasyon imleçle koşmamalı: %q", girdi.ResumeCursor)
	}
	if len(girdi.AllowedTools) != 0 {
		t.Errorf("konsolidasyon araçsız koşmalı: %v", girdi.AllowedTools)
	}
	// Boş AllowedTools yetmez: adaptör o durumda bayrağı hiç eklemez ve tüm
	// MCP kataloğu modele görünür kalır. NoTools bunu açıkça kapatır.
	if !girdi.NoTools {
		t.Error("konsolidasyon NoTools ile koşmalı — yoksa araç kataloğu görünür kalır")
	}
	// CWD boş bırakılırsa motor süreci sunucunun kendi dizininde koşar; CLI
	// oturum/önbellek yazımları konteynerde sessizce düşer.
	if girdi.CWD == "" {
		t.Error("konsolidasyon turunda CWD boş — motor sunucunun dizininde koşar")
	}
	// Konsolidasyon botun imleç SATIRINI bile açmamalı: motor başına imleçlere
	// dokunmaz, botun sohbet zinciri el değmeden kalır (ADIM 7).
	if _, err := st.Q.GetBotCursor(ctx, db.GetBotCursorParams{BotID: bot.ID, Engine: bot.Engine}); err == nil {
		t.Error("konsolidasyon bot imleç satırı açtı — sohbet zincirine dokunmamalı")
	}

	// Birleşik içerik yazıldı ve geçmişte iki revizyon var.
	tut, err := st.Q.GetMemory(ctx, m[0].ID)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if tut.Content != "Kullanıcı toplantı notlarını kısa ve maddeli ister" || tut.Revision != 2 {
		t.Errorf("birleşik kayıt yanlış: %q r%d", tut.Content, tut.Revision)
	}
	gecmis, _ := st.Q.ListMemoryRevisions(ctx, m[0].ID)
	if len(gecmis) != 2 || gecmis[0].Author != store.YazarKonsolidasyon {
		t.Errorf("geçmiş eksik: %d revizyon", len(gecmis))
	}

	// Katılan ve elenen kayıtlar prompttan düştü, ama SİLİNMEDİ.
	kalan := promptIcerikleri(t, st, bot)
	for _, olmamali := range []string{"madde işareti", "Codezal"} {
		if strings.Contains(strings.Join(kalan, "\n"), olmamali) {
			t.Errorf("%q prompttan düşmeliydi: %v", olmamali, kalan)
		}
	}
	if _, err := st.Q.GetMemory(ctx, m[1].ID); err != nil {
		t.Errorf("katılan kayıt SİLİNMEMELİ (geri alınabilir olmalı): %v", err)
	}

	// Özet yazıldı ve artık prompta o giriyor.
	ozet, err := st.Q.GetMemorySummary(ctx, bot.ID)
	if err != nil {
		t.Fatalf("GetMemorySummary: %v", err)
	}
	if !strings.Contains(ozet.Content, "maddeli") || ozet.SourceCount != 5 {
		t.Errorf("özet yanlış: %q (kaynak %d)", ozet.Content, ozet.SourceCount)
	}
}

// TestConsolidationRejectsMassDeletion — bozuk bir tur hafızayı boşaltamaz.
// Plan tümden reddedilir: yarısı uygulanmış hafıza, hiç uygulanmamıştan kötü.
func TestConsolidationRejectsMassDeletion(t *testing.T) {
	st, bot := kurulum(t)
	ctx := context.Background()

	var icerikler []string
	for i := 0; i < 6; i++ {
		icerikler = append(icerikler, fmt.Sprintf("Korunması gereken olgu %d", i))
	}
	m := yaz(t, st, bot, icerikler...)

	var ele []map[string]any
	for _, mem := range m[:5] { // 5/6 — yarıdan fazla
		ele = append(ele, map[string]any{"id": mem.ID.String(), "gerekce": "gereksiz"})
	}
	plan := map[string]any{"birlestir": []any{}, "ele": ele, "ozet": "boş hafıza"}

	c := yeniConsolidator(t, st, planlayan(plan, nil))
	if _, err := c.ConsolidateBot(ctx, bot); err == nil {
		t.Fatal("kitle eleme planı reddedilmeliydi")
	}

	// Hiçbir kayıt düşmemeli ve özet YAZILMAMALI (plan hiç uygulanmadı).
	if kalan := promptIcerikleri(t, st, bot); len(kalan) != 6 {
		t.Errorf("6 kayıt da durmalıydı, %d kaldı", len(kalan))
	}
	if _, err := st.Q.GetMemorySummary(ctx, bot.ID); err == nil {
		t.Error("reddedilen plan özet yazmamalı")
	}
}

// TestConsolidationSkipsSmallMemory — üç satır için model çağırmaya değmez.
func TestConsolidationSkipsSmallMemory(t *testing.T) {
	st, bot := kurulum(t)
	ctx := context.Background()
	yaz(t, st, bot, "tek olgu", "ikinci olgu")

	cagrildi := false
	exec := enginetest.Func(func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		cagrildi = true
		return &engine.TurnResult{Ok: true}, nil
	})
	c := yeniConsolidator(t, st, exec)
	rapor, err := c.ConsolidateBot(ctx, bot)
	if err != nil {
		t.Fatalf("ConsolidateBot: %v", err)
	}
	if rapor.Atlandi == "" {
		t.Error("az kayıtlı bot atlanmalıydı")
	}
	if cagrildi {
		t.Error("atlanacak bot için motor ÇAĞRILMAMALI (boşa maliyet)")
	}
}

// TestConsolidationSecretGate — konsolidasyonun ürettiği metin de sır
// kapısından geçer (store.SirTara her yazma yolunda).
func TestConsolidationSecretGate(t *testing.T) {
	st, bot := kurulum(t)
	ctx := context.Background()
	m := yaz(t, st, bot, "olgu bir", "olgu iki", "olgu üç", "olgu dört", "olgu beş")

	plan := map[string]any{
		"birlestir": []map[string]any{{
			"tut":     m[0].ID.String(),
			"kat":     []string{m[1].ID.String()},
			"icerik":  "Kullanıcının anahtarı sk-proj-AbCdEf0123456789xyzQ",
			"gerekce": "birleştirme",
		}},
		"ele":  []any{},
		"ozet": "özet",
	}
	c := yeniConsolidator(t, st, planlayan(plan, nil))
	if _, err := c.ConsolidateBot(ctx, bot); err == nil {
		t.Fatal("sır içeren birleştirme reddedilmeliydi")
	}
	if tut, err := st.Q.GetMemory(ctx, m[0].ID); err == nil && strings.Contains(tut.Content, "sk-proj") {
		t.Errorf("sır hafızaya yazılmış: %q", tut.Content)
	}
}

// TestConsolidationIsAtomic — plan YA HEP YA HİÇ uygulanır.
//
// İkinci birleştirme, birincinin ürettiği içerikle aynı content_hash'e düşer
// (aynı kapsamda dedup çakışması) ve reddedilir. Birinci birleştirme tek
// başına yazılı kalsaydı kullanıcı hem birleşmiş hem birleşmemiş kayıtların
// karışımıyla, üstelik özetsiz kalırdı.
func TestConsolidationIsAtomic(t *testing.T) {
	st, bot := kurulum(t)
	ctx := context.Background()

	m := yaz(t, st, bot, "olgu A", "olgu B", "olgu C", "olgu D", "olgu E", "olgu F")
	ayniIcerik := "Birleşik olgu " + uuid.NewString()[:8]

	plan := map[string]any{
		"birlestir": []map[string]any{
			{"tut": m[0].ID.String(), "kat": []string{m[1].ID.String()}, "icerik": ayniIcerik, "gerekce": "ilk"},
			// İkincisi aynı metni yazmaya çalışıyor → dedup çakışması.
			{"tut": m[2].ID.String(), "kat": []string{m[3].ID.String()}, "icerik": ayniIcerik, "gerekce": "ikinci"},
		},
		"ele":  []any{},
		"ozet": "özet",
	}

	c := yeniConsolidator(t, st, planlayan(plan, nil))
	if _, err := c.ConsolidateBot(ctx, bot); err == nil {
		t.Fatal("çakışan plan hata döndürmeliydi")
	}

	// İlk birleştirme de geri sarılmalı: içerik değişmemiş, kaynak kayıt
	// prompttan düşmemiş, özet yazılmamış olmalı.
	ilk, err := st.Q.GetMemory(ctx, m[0].ID)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if ilk.Content != "olgu A" || ilk.Revision != 1 {
		t.Errorf("ilk birleştirme geri sarılmadı: %q r%d", ilk.Content, ilk.Revision)
	}
	if kalan := promptIcerikleri(t, st, bot); len(kalan) != 6 {
		t.Errorf("6 kayıt da yaşamalıydı, %d kaldı: %v", len(kalan), kalan)
	}
	if _, err := st.Q.GetMemorySummary(ctx, bot.ID); err == nil {
		t.Error("yarım kalan plan özet yazmamalı")
	}
}

// TestConsolidationRecordsCost — bakım turu kuyruğa girmez ama para harcar;
// maliyet defterine (runs) yazılmazsa /api/stats faturayla uzlaşmaz.
// Aktivite akışında ise GÖRÜNMEMELİ: her gece her bot için bir satır düşseydi
// kullanıcının kendi işleri kaybolurdu.
func TestConsolidationRecordsCost(t *testing.T) {
	st, bot := kurulum(t)
	ctx := context.Background()

	m := yaz(t, st, bot, "olgu 1", "olgu 2", "olgu 3", "olgu 4", "olgu 5")
	plan := map[string]any{
		"birlestir": []map[string]any{{
			"tut": m[0].ID.String(), "kat": []string{m[1].ID.String()},
			"icerik": "Birleşik olgu", "gerekce": "aynı şey",
		}},
		"ele":  []any{},
		"ozet": "özet",
	}

	pahali := enginetest.Func(func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		gövde, _ := json.Marshal(plan)
		res := &engine.TurnResult{
			Ok: true, StopReason: "success", NumTurns: 2, FinalText: string(gövde),
			CostUSD: 0.0042, InputTokens: 1200, OutputTokens: 340,
		}
		emit(engine.Event{Kind: engine.KindTurnCompleted, Result: res})
		return res, nil
	})
	if _, err := yeniConsolidator(t, st, pahali).ConsolidateBot(ctx, bot); err != nil {
		t.Fatalf("ConsolidateBot: %v", err)
	}

	kosular, err := st.Q.ListRunsByBot(ctx, db.ListRunsByBotParams{BotID: bot.ID, Limit: 10})
	if err != nil {
		t.Fatalf("ListRunsByBot: %v", err)
	}
	var kayit *db.Run
	for i := range kosular {
		if kosular[i].Origin == "consolidation" {
			kayit = &kosular[i]
		}
	}
	if kayit == nil {
		t.Fatalf("konsolidasyon turu maliyet defterine yazılmadı: %+v", kosular)
	}
	if kayit.CostUsd != 0.0042 || kayit.InputTokens != 1200 || kayit.OutputTokens != 340 {
		t.Errorf("maliyet eksik yazıldı: %+v", kayit)
	}
	if kayit.Status != "completed" {
		t.Errorf("durum completed olmalı: %q", kayit.Status)
	}

	akis, err := st.Q.ListRecentRuns(ctx, 200)
	if err != nil {
		t.Fatalf("ListRecentRuns: %v", err)
	}
	for _, r := range akis {
		if r.ID == kayit.ID {
			t.Error("bakım turu Aktivite akışına düşmemeli")
		}
	}
}

// TestConsolidationSingleCopy — iki kopya aynı botu aynı anda konsolide
// edemez. Zamanlayıcı tek kopya varsayar ama deploy sırasında eski ve yeni kap
// kısa süre birlikte koşar; ikinci tur artık geçersiz bir plan uygulardı.
func TestConsolidationSingleCopy(t *testing.T) {
	st, bot := kurulum(t)
	ctx := context.Background()
	yaz(t, st, bot, "olgu 1", "olgu 2", "olgu 3", "olgu 4", "olgu 5")

	// İlk tur motorun içinde beklerken ikinci tur devreye girer.
	girdi := make(chan struct{})
	devam := make(chan struct{})
	yavas := enginetest.Func(func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		close(girdi)
		<-devam
		return &engine.TurnResult{Ok: true, StopReason: "success", NumTurns: 1,
			FinalText: `{"birlestir":[],"ele":[],"ozet":"özet"}`}, nil
	})

	birinci := yeniConsolidator(t, st, yavas)
	bitti := make(chan error, 1)
	go func() {
		_, err := birinci.ConsolidateBot(ctx, bot)
		bitti <- err
	}()
	<-girdi

	cagrildi := false
	ikinciExec := enginetest.Func(func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		cagrildi = true
		return &engine.TurnResult{Ok: true}, nil
	})
	rapor, err := yeniConsolidator(t, st, ikinciExec).ConsolidateBot(ctx, bot)
	if err != nil {
		t.Fatalf("ikinci kopya hata döndürmemeli, atlamalı: %v", err)
	}
	if rapor.Atlandi == "" {
		t.Error("ikinci kopya atlamalıydı — kilit tutulmuyor")
	}
	if cagrildi {
		t.Error("ikinci kopya motoru çağırmamalı (çift maliyet + çift plan)")
	}

	close(devam)
	if err := <-bitti; err != nil {
		t.Fatalf("birinci tur: %v", err)
	}
}

func promptIcerikleri(t *testing.T, st *store.Store, bot db.Bot) []string {
	t.Helper()
	rows, err := st.Q.ListMemoriesForPrompt(context.Background(), db.ListMemoriesForPromptParams{
		BotID: uuid.NullUUID{UUID: bot.ID, Valid: true}, Limit: 1000,
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

// TestConsolidationCannotTouchStandingInstructions — sabit talimatlar (K20+,
// kind='instruction') konsolidasyon turuna HİÇ GİRMEZ.
//
// Neden ayrı bir test: konsolidasyon bir damıtma turudur — benzer kayıtları
// birleştirir, bayat olanı eler. Bir kural ("onaysız e-posta gönderme")
// damıtılacak bir olgu değil, uyulacak bir emirdir; "yakın" bir olguyla
// birleşirse anlamı bozulur, uzun süre tetiklenmedi diye elenirse kullanıcı
// koyduğu yasağın kalktığını fark bile etmez. Bu testin gerçekten ölçtüğü
// şey, modele VERİLEN listede talimatın olmaması: model göremediğine
// dokunamaz, plan doğrulaması da bilmediği bir id'yi reddeder.
func TestConsolidationCannotTouchStandingInstructions(t *testing.T) {
	st, bot := kurulum(t)
	ctx := context.Background()

	m := yaz(t, st, bot,
		"Kullanıcı toplantı notlarını kısa ister",
		"Kullanıcı toplantı notlarında madde işareti sever",
		"Kullanıcı sabah 7'de kalkar",
		"Kullanıcının saat dilimi Europe/Istanbul",
		"Eski proje adı Codezal'dı",
	)
	talimat, err := st.SaveMemory(ctx, store.MemoryDraft{
		Scope: "bot", BotID: uuid.NullUUID{UUID: bot.ID, Valid: true},
		Kind: "instruction", Content: "Onaysız e-posta gönderme",
	})
	if err != nil {
		t.Fatalf("sabit talimat yazılamadı: %v", err)
	}

	// Plan, gördüğü kayıtlardan birini eler — tur gerçekten koşsun.
	plan := map[string]any{
		"birlestir": []map[string]any{},
		"ele":       []map[string]any{{"id": m[4].ID.String(), "gerekce": "artık geçerli değil"}},
		"ozet":      "- Notlar kısa\n- Sabah 7",
	}
	var girdi engine.TurnInput
	c := yeniConsolidator(t, st, planlayan(plan, &girdi))
	if _, err := c.ConsolidateBot(ctx, bot); err != nil {
		t.Fatalf("ConsolidateBot: %v", err)
	}

	// 1) Talimat modele hiç gösterilmedi.
	if strings.Contains(girdi.Prompt, "Onaysız e-posta gönderme") ||
		strings.Contains(girdi.Prompt, talimat.ID.String()) {
		t.Errorf("sabit talimat konsolidasyon promptuna girmemeliydi:\n%s", girdi.Prompt)
	}
	// 2) Kayıt yaşıyor: emekliye ayrılmadı, içeriği değişmedi.
	sonra, err := st.Q.GetMemory(ctx, talimat.ID)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if sonra.SupersededAt != nil {
		t.Error("sabit talimat emekliye ayrılmış")
	}
	if sonra.Content != "Onaysız e-posta gönderme" || sonra.Revision != talimat.Revision {
		t.Errorf("sabit talimat değiştirilmiş: %+v", sonra)
	}
}
