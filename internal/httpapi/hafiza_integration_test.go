//go:build integration

// Hafıza uçları (K20): dışa aktarma → elle düzenleme → geri yükleme
// gidiş-dönüşü ve arama, gerçek Postgres + gerçek HTTP yığını üzerinden.
package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/esmworks/OpenAssist/internal/bus"
	"github.com/esmworks/OpenAssist/internal/config"
	"github.com/esmworks/OpenAssist/internal/httpapi"
	"github.com/esmworks/OpenAssist/internal/store"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

func hafizaSunucu(t *testing.T) (*store.Store, string, db.Bot) {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL tanımlı değil — entegrasyon testi atlandı")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, url)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(st.Close)

	bot, err := st.Q.CreateBot(ctx, db.CreateBotParams{
		Kind: "bot", Name: "hafiza-api-" + uuid.NewString()[:8],
		Engine: "claudecode", AllowedTools: []string{},
	})
	if err != nil {
		t.Fatalf("bot: %v", err)
	}
	t.Cleanup(func() { _ = st.Q.DeleteBot(context.Background(), bot.ID) })

	srv := httpapi.New(config.Config{HTTPAddr: ":0", APIToken: testBelirtec}, httpapi.Deps{DB: st, Store: st, Bus: bus.New()})
	ts := httptest.NewServer(srv.Handler)
	belirtecliIstemci(ts)
	t.Cleanup(ts.Close)
	return st, ts.URL, bot
}

func TestMemoryExportImportRoundTrip(t *testing.T) {
	st, ts, bot := hafizaSunucu(t)
	ctx := context.Background()

	m, err := st.SaveMemory(ctx, store.MemoryDraft{
		Scope: "bot", BotID: uuid.NullUUID{UUID: bot.ID, Valid: true},
		Kind: "instruction", Content: "Dışa aktarma kanıtı " + bot.Name,
	})
	if err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Q.DeleteMemory(ctx, m.ID) })

	// Dışa aktar.
	res, err := belirtecliGet(ts + "/api/memories/export")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("export durumu: %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("markdown beklenirdi: %q", ct)
	}
	gövde, _ := io.ReadAll(res.Body)
	metin := string(gövde)
	if !strings.Contains(metin, "Dışa aktarma kanıtı") || !strings.Contains(metin, bot.Name) {
		t.Fatalf("dışa aktarımda kayıt yok:\n%s", metin)
	}

	// Elle düzenle: aynı bota yeni bir satır ekle (gerçek kullanım şekli).
	yeni := "Elle eklenmiş olgu " + bot.Name
	duzenli := strings.Replace(metin,
		"## bot: "+bot.Name+"\n\n",
		"## bot: "+bot.Name+"\n\n- [fact] "+yeni+"\n", 1)

	// Geri yükle.
	res2, err := belirtecliPost(ts+"/api/memories/import", "text/markdown", strings.NewReader(duzenli))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res2.Body)
		t.Fatalf("import durumu %d: %s", res2.StatusCode, b)
	}

	// Yeni satır yazıldı; var olan satır dedup'a takıldı (kopya doğmadı).
	bulundu, kopya := 0, 0
	rows, err := st.Q.ListMemories(ctx, 1000)
	if err != nil {
		t.Fatalf("ListMemories: %v", err)
	}
	for _, r := range rows {
		switch r.Content {
		case yeni:
			bulundu++
		case m.Content:
			kopya++
		}
	}
	if bulundu != 1 {
		t.Errorf("elle eklenen satır yazılmalıydı (%d bulundu)", bulundu)
	}
	if kopya != 1 {
		t.Errorf("var olan kayıt kopyalanmamalıydı (%d kopya)", kopya)
	}
}

// TestMemoryExportKeepsExpiryAndConfidence — dışa aktarma expires_at ve
// confidence'ı taşımazsa geçici bir olgu gidiş-dönüşte KALICILAŞIR: içerik
// aynı olduğu için upsert var olan kaydın expires_at'ini NULL ile ezer ve
// "kullanıcı bu hafta İzmir'de" haftalar sonra hâlâ promptta durur.
func TestMemoryExportKeepsExpiryAndConfidence(t *testing.T) {
	st, ts, bot := hafizaSunucu(t)
	ctx := context.Background()

	sonEr := time.Now().UTC().AddDate(0, 0, 7).Truncate(time.Second)
	m, err := st.SaveMemory(ctx, store.MemoryDraft{
		Scope: "bot", BotID: uuid.NullUUID{UUID: bot.ID, Valid: true},
		Kind: "fact", Content: "Kullanıcı bu hafta İzmir'de " + bot.Name,
		Confidence: 0.7, ExpiresAt: &sonEr,
	})
	if err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Q.DeleteMemory(ctx, m.ID) })

	res, err := belirtecliGet(ts + "/api/memories/export")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	defer res.Body.Close()
	gövde, _ := io.ReadAll(res.Body)
	metin := string(gövde)
	if !strings.Contains(metin, "expires=") || !strings.Contains(metin, "conf=0.70") {
		t.Fatalf("dışa aktarım expires/conf taşımıyor:\n%s", metin)
	}

	// Geri yükle: alanlar korunmalı.
	res2, err := belirtecliPost(ts+"/api/memories/import", "text/markdown", strings.NewReader(metin))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(res2.Body)
		t.Fatalf("import durumu %d: %s", res2.StatusCode, b)
	}

	geri, err := st.Q.GetMemory(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMemory: %v", err)
	}
	if geri.ExpiresAt == nil {
		t.Fatal("gidiş-dönüşte expires_at kayboldu — geçici olgu kalıcılaştı")
	}
	if !geri.ExpiresAt.UTC().Equal(sonEr) {
		t.Errorf("expires_at kaydı: %v ≠ %v", geri.ExpiresAt.UTC(), sonEr)
	}
	if geri.Confidence < 0.69 || geri.Confidence > 0.71 {
		t.Errorf("confidence gidiş-dönüşte değişti: %v", geri.Confidence)
	}
}

// TestMemoryImportIsAllOrNothing — ortadaki bir satır reddedilirse ÖNCEKİ
// satırlar da yazılmamış olmalı. Aksi halde kullanıcı "aktarım başarısız"
// görür ama veritabanında yarım bir aktarım durur ve dosyayı düzeltip
// yeniden gönderdiğinde neyin girdiğini bilemez.
func TestMemoryImportIsAllOrNothing(t *testing.T) {
	st, ts, bot := hafizaSunucu(t)
	ctx := context.Background()

	damga := uuid.NewString()[:8]
	dosya := "# OpenAssist hafıza\n\n## bot: " + bot.Name + "\n\n" +
		"- [fact] Önceki satır " + damga + "\n" +
		"- [fact] Anahtar sk-proj-AbCdEf0123456789xyzQ\n" +
		"- [fact] Sonraki satır " + damga + "\n"

	res, err := belirtecliPost(ts+"/api/memories/import", "text/markdown", strings.NewReader(dosya))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("400 beklenirdi, %d geldi", res.StatusCode)
	}

	rows, err := st.Q.ListMemories(ctx, 1000)
	if err != nil {
		t.Fatalf("ListMemories: %v", err)
	}
	for _, r := range rows {
		if strings.Contains(r.Content, damga) {
			t.Fatalf("reddedilen aktarımdan satır yazılmış: %q", r.Content)
		}
	}
}

// TestMemoryImportRejectsOversizedBody — sınır aşımı sessizce kırpılmaz.
// Kırpma bir içerik satırının ortasına denk gelseydi yarım bir olgu geçerli
// görünüp hafızaya yazılırdı; kullanıcı ise 200 OK görürdü.
func TestMemoryImportRejectsOversizedBody(t *testing.T) {
	_, ts, bot := hafizaSunucu(t)

	var b strings.Builder
	b.WriteString("# OpenAssist hafıza\n\n## bot: " + bot.Name + "\n\n")
	for b.Len() < 5<<20 {
		b.WriteString("- [fact] Dolgu satırı " + uuid.NewString() + "\n")
	}
	res, err := belirtecliPost(ts+"/api/memories/import", "text/markdown", strings.NewReader(b.String()))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		gövde, _ := io.ReadAll(res.Body)
		t.Errorf("413 beklenirdi, %d geldi: %s", res.StatusCode, gövde)
	}
}

func TestMemoryImportRejectsSecretsAndBadFormat(t *testing.T) {
	_, ts, _ := hafizaSunucu(t)

	kotu := map[string]string{
		"biçim":     "## user\n- [fact] başlıksız dosya\n",
		"sır":       "# OpenAssist hafıza\n## user\n- [fact] anahtar sk-proj-AbCdEf0123456789xyzQ\n",
		"tür":       "# OpenAssist hafıza\n## user\n- [gizli] tanınmayan tür\n",
		"boş dosya": "# OpenAssist hafıza\n",
	}
	for ad, gövde := range kotu {
		t.Run(ad, func(t *testing.T) {
			res, err := belirtecliPost(ts+"/api/memories/import", "text/markdown", strings.NewReader(gövde))
			if err != nil {
				t.Fatalf("import: %v", err)
			}
			defer res.Body.Close()
			if res.StatusCode != http.StatusBadRequest {
				b, _ := io.ReadAll(res.Body)
				t.Errorf("400 beklenirdi, %d geldi: %s", res.StatusCode, b)
			}
		})
	}
}

func TestMemorySearchEndpoint(t *testing.T) {
	st, ts, bot := hafizaSunucu(t)
	ctx := context.Background()

	damga := uuid.NewString()[:8]
	for _, ic := range []string{"Kırmızı bisiklet " + damga, "Mavi araba " + damga} {
		m, err := st.SaveMemory(ctx, store.MemoryDraft{
			Scope: "bot", BotID: uuid.NullUUID{UUID: bot.ID, Valid: true},
			Kind: "fact", Content: ic,
		})
		if err != nil {
			t.Fatalf("SaveMemory: %v", err)
		}
		t.Cleanup(func() { _, _ = st.Q.DeleteMemory(ctx, m.ID) })
	}

	ara := func(q string) []map[string]any {
		t.Helper()
		res, err := belirtecliGet(ts + "/api/memories/search?q=" + q + "&bot=" + bot.ID.String())
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("search durumu: %d", res.StatusCode)
		}
		var out []map[string]any
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatalf("çözümleme: %v", err)
		}
		return out
	}

	if got := ara("bisiklet"); len(got) != 1 || !strings.Contains(got[0]["content"].(string), "Kırmızı") {
		t.Errorf("bisiklet araması: %+v", got)
	}
	// ILIKE büyük/küçük harf ayırmaz. Türkçe noktalı İ de eşleşir — bu
	// veritabanı harmanlamasına (collation) bağlıdır: postgres:17 varsayılanı
	// lower('MAVİ')='mavi' verir. Harmanlama değişirse burası önce kırılır.
	if got := ara("MAVİ"); len(got) != 1 {
		t.Errorf("büyük harfli arama da eşleşmeli: %+v", got)
	}
	if got := ara(damga); len(got) != 2 {
		t.Errorf("ortak damga iki kaydı da bulmalı: %+v", got)
	}

	// ILIKE jokerleri KAÇIRILIR: '%' tek başına her şeyi getirseydi ajan bunu
	// "bu konuda çok şey biliyorum" diye okuyup alakasız 50 kaydı bağlama
	// alırdı. '_' de tek karakter jokeri olmamalı.
	if got := ara(url.QueryEscape("%")); len(got) != 0 {
		t.Errorf("'%%' joker gibi davranmamalı, %d kayıt döndü", len(got))
	}
	if got := ara(url.QueryEscape("Mav_")); len(got) != 0 {
		t.Errorf("'_' joker gibi davranmamalı, %d kayıt döndü", len(got))
	}

	// q zorunlu.
	res, err := belirtecliGet(ts + "/api/memories/search")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("q'suz arama 400 dönmeli, %d geldi", res.StatusCode)
	}
}

// TestMemoryRestoreEndpoint — konsolidasyonun emekliye ayırdığı kayıt geri
// getirilebilmeli. K20'nin "katılan kayıt silinmez, geri alınabilir" vaadi
// arşiv araması + geri getirme olmadan pratikte tutmuyordu: kullanıcı kaydın
// id'sini bile bulamıyordu.
func TestMemoryRestoreEndpoint(t *testing.T) {
	st, ts, bot := hafizaSunucu(t)
	ctx := context.Background()

	damga := uuid.NewString()[:8]
	m, err := st.SaveMemory(ctx, store.MemoryDraft{
		Scope: "bot", BotID: uuid.NullUUID{UUID: bot.ID, Valid: true},
		Kind: "fact", Content: "Emekliye ayrılacak olgu " + damga,
	})
	if err != nil {
		t.Fatalf("SaveMemory: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Q.DeleteMemory(ctx, m.ID) })

	if err := st.Q.SupersedeMemory(ctx, db.SupersedeMemoryParams{ID: m.ID}); err != nil {
		t.Fatalf("SupersedeMemory: %v", err)
	}

	ara := func(qs string) []map[string]any {
		t.Helper()
		res, err := belirtecliGet(ts + "/api/memories/search?q=" + damga + qs)
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		defer res.Body.Close()
		var out []map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		return out
	}
	if got := ara(""); len(got) != 0 {
		t.Errorf("emekli kayıt normal aramada çıkmamalı: %+v", got)
	}
	if got := ara("&arsiv=1"); len(got) != 1 {
		t.Fatalf("arşiv aramasında çıkmalı: %+v", got)
	}

	res, err := belirtecliPost(ts+"/api/memories/"+m.ID.String()+"/restore", "", nil)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		gövde, _ := io.ReadAll(res.Body)
		t.Fatalf("restore durumu %d: %s", res.StatusCode, gövde)
	}
	if got := ara(""); len(got) != 1 {
		t.Errorf("geri getirilen kayıt normal aramada çıkmalı: %+v", got)
	}
}

// TestKullaniciSabitTalimatYazar — POST /api/memories, kullanıcının kendi
// eliyle kayıt yazma yolu.
//
// Bu uç sabit talimatlar yüzünden var: kind='instruction' bloğuna motor
// yazamıyor (memory_save reddeder), konsolidasyon dokunamıyor. Yazabilen tek
// taraf kullanıcı — o tarafın kapısı olmasaydı özellik yalnız kağıt üzerinde
// kalırdı. Sır kapısı burada da geçerli: kullanıcı da olsa hafızaya anahtar
// yazılmaz, çünkü o metin her koşuda motora gider.
func TestKullaniciSabitTalimatYazar(t *testing.T) {
	st, ts, bot := hafizaSunucu(t)
	ctx := context.Background()

	gövde := `{"scope":"bot","bot_id":"` + bot.ID.String() + `","kind":"instruction",` +
		`"content":"Onaysız e-posta gönderme"}`
	res, err := belirtecliPost(ts+"/api/memories", "application/json", strings.NewReader(gövde))
	if err != nil {
		t.Fatalf("istek: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusCreated {
		g, _ := io.ReadAll(res.Body)
		t.Fatalf("201 beklenirdi: %d — %s", res.StatusCode, g)
	}
	var olusan struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	}
	if err := json.NewDecoder(res.Body).Decode(&olusan); err != nil {
		t.Fatalf("çözüm: %v", err)
	}
	if olusan.Kind != "instruction" {
		t.Errorf("kind korunmalıydı: %q", olusan.Kind)
	}
	id, err := uuid.Parse(olusan.ID)
	if err != nil {
		t.Fatalf("id: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Q.DeleteMemory(ctx, id) })

	// Kayıt gerçekten sabit talimat listesine düştü mü — prompta girecek yol
	// burası (orkestratör aynı sorguyu çağırır).
	tal, err := st.Q.ListStandingInstructions(ctx, db.ListStandingInstructionsParams{
		BotID: uuid.NullUUID{UUID: bot.ID, Valid: true}, Limit: 20,
	})
	if err != nil {
		t.Fatalf("ListStandingInstructions: %v", err)
	}
	var bulundu bool
	for _, m := range tal {
		if m.ID == id {
			bulundu = true
		}
	}
	if !bulundu {
		t.Errorf("yazılan talimat listede yok: %+v", tal)
	}

	// Reddedilmesi gerekenler.
	for _, d := range []struct {
		ad, gövde string
	}{
		{"geçersiz kind", `{"scope":"user","kind":"emir","content":"x"}`},
		{"boş içerik", `{"scope":"user","kind":"fact","content":"   "}`},
		{"user kaydı bota bağlanamaz", `{"scope":"user","bot_id":"` + bot.ID.String() + `","kind":"fact","content":"x"}`},
		{"bilinmeyen alan", `{"scope":"user","kind":"fact","content":"x","gizli":1}`},
		{"sır", `{"scope":"user","kind":"fact","content":"Anahtarım sk-ant-api03-` +
			`AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`},
	} {
		res, err := belirtecliPost(ts+"/api/memories", "application/json", strings.NewReader(d.gövde))
		if err != nil {
			t.Fatalf("%s: istek: %v", d.ad, err)
		}
		kod := res.StatusCode
		_ = res.Body.Close()
		if kod != http.StatusBadRequest {
			t.Errorf("%s: 400 beklenirdi, %d geldi", d.ad, kod)
		}
	}
}
