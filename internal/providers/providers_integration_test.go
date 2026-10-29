//go:build integration

// Entegrasyon testi: gerçek Postgres ister.
//
//	DATABASE_URL=postgres://… go test -tags integration ./internal/providers
//
// Sağlayıcı kaydının kısıtları (kimlik alfabesi, eğik çizgisiz model adı,
// silmede cascade) VERİTABANINDA yaşıyor; yalnız Go tarafını sınamak onları
// hiç çalıştırmazdı — ilk kırılma üretimde görünürdü.
package providers_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/esmworks/OpenAssist/internal/crypto"
	"github.com/esmworks/OpenAssist/internal/providers"
	"github.com/esmworks/OpenAssist/internal/store"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

func kur(t *testing.T) (context.Context, *store.Store) {
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
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	temizle(ctx, t, st)
	t.Cleanup(func() { temizle(ctx, t, st) })
	return ctx, st
}

// Tablo testler arası paylaşılıyor (tek veritabanı): her test kendi
// başlangıcını kurar, yoksa Tohumla "tablo boş" koşulunu asla göremezdi.
func temizle(ctx context.Context, t *testing.T, st *store.Store) {
	t.Helper()
	if _, err := st.Pool().Exec(ctx, "DELETE FROM providers"); err != nil {
		t.Fatalf("temizlik: %v", err)
	}
}

func TestSaglayiciTuruEksiksiz(t *testing.T) {
	ctx, st := kur(t)
	sealer, err := crypto.New("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("crypto.New: %v", err)
	}
	svc := providers.New(st, sealer)

	if _, err := st.Q.UpsertProvider(ctx, db.UpsertProviderParams{
		ID: "minimax", Name: "MiniMax", BaseUrl: "https://api.minimax.io/anthropic/v1",
		Api: "anthropic-messages", Enabled: true, Compat: []byte(`{"a":1}`),
	}); err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}
	if err := st.Q.UpsertProviderModel(ctx, db.UpsertProviderModelParams{
		ProviderID: "minimax", ModelID: "MiniMax-M2.7", Label: "MiniMax-M2.7",
		ContextWindow: 204800, MaxTokens: 131072, Reasoning: true,
		Input: []string{"text"},
	}); err != nil {
		t.Fatalf("UpsertProviderModel: %v", err)
	}
	if err := svc.AnahtarYaz(ctx, "minimax", "sk-gizli"); err != nil {
		t.Fatalf("AnahtarYaz: %v", err)
	}

	// Anahtar DURAĞAN HALDE MÜHÜRLÜ: satırda düz metin durmamalı.
	kayit, err := st.Q.GetProvider(ctx, "minimax")
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if kayit.SealedApiKey == "" || kayit.SealedApiKey == "sk-gizli" {
		t.Fatalf("anahtar mühürlenmemiş: %q", kayit.SealedApiKey)
	}

	tarif, err := svc.Tarifler(ctx)
	if err != nil {
		t.Fatalf("Tarifler: %v", err)
	}
	if len(tarif) != 1 {
		t.Fatalf("tarif sayısı = %d, beklenen 1", len(tarif))
	}
	p := tarif[0]
	if p.APIKey != "sk-gizli" {
		t.Errorf("anahtarın mührü açılmadı: %q", p.APIKey)
	}
	if p.API != "anthropic-messages" || p.BaseURL == "" {
		t.Errorf("tarif eksik: %+v", p)
	}
	if len(p.Models) != 1 || p.Models[0].ContextWindow != 204800 {
		t.Errorf("model sınırları taşınmadı: %+v", p.Models)
	}
	if p.Compat["a"] == nil {
		t.Errorf("compat çözülmedi: %+v", p.Compat)
	}
}

// Kapalı sağlayıcı motora HİÇ verilmemeli: "kapalı" ile "silinmiş" arasındaki
// fark, anahtarı kaybetmeden devre dışı bırakabilmektir.
func TestKapaliSaglayiciTariflerdeYok(t *testing.T) {
	ctx, st := kur(t)
	svc := providers.New(st, nil)

	if _, err := st.Q.UpsertProvider(ctx, db.UpsertProviderParams{
		ID: "kapali", BaseUrl: "https://ornek/v1", Api: "openai-completions",
		Enabled: false, Compat: []byte("{}"),
	}); err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}
	tarif, err := svc.Tarifler(ctx)
	if err != nil {
		t.Fatalf("Tarifler: %v", err)
	}
	if len(tarif) != 0 {
		t.Errorf("kapalı sağlayıcı motora verildi: %+v", tarif)
	}
}

// Kısıtlar veritabanında: kimlik alfabesi, eğik çizgisiz model adı, geçerli
// tel biçimi. Bunlar API doğrulaması atlansa bile tutmalı.
func TestKisitlarVeritabanindaTutuyor(t *testing.T) {
	ctx, st := kur(t)

	if _, err := st.Q.UpsertProvider(ctx, db.UpsertProviderParams{
		ID: "Büyük Harf", BaseUrl: "https://ornek/v1", Api: "openai-completions",
		Enabled: true, Compat: []byte("{}"),
	}); err == nil {
		t.Error("geçersiz kimlik kabul edildi")
	}
	if _, err := st.Q.UpsertProvider(ctx, db.UpsertProviderParams{
		ID: "x", BaseUrl: "https://ornek/v1", Api: "uydurma-bicim",
		Enabled: true, Compat: []byte("{}"),
	}); err == nil {
		t.Error("geçersiz tel biçimi kabul edildi")
	}

	if _, err := st.Q.UpsertProvider(ctx, db.UpsertProviderParams{
		ID: "x", BaseUrl: "https://ornek/v1", Api: "openai-completions",
		Enabled: true, Compat: []byte("{}"),
	}); err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}
	// Eğik çizgi kimliği ikiye bölerdi: önek sağlayıcının kendisidir.
	if err := st.Q.UpsertProviderModel(ctx, db.UpsertProviderModelParams{
		ProviderID: "x", ModelID: "openai/gpt-5", Input: []string{},
	}); err == nil {
		t.Error("eğik çizgili model kimliği kabul edildi")
	}
	// Kayıtsız sağlayıcıya model bağlanamaz (FK).
	if err := st.Q.UpsertProviderModel(ctx, db.UpsertProviderModelParams{
		ProviderID: "yok", ModelID: "m", Input: []string{},
	}); err == nil {
		t.Error("olmayan sağlayıcıya model eklendi")
	}
}

// Sağlayıcı silinince modelleri de gitmeli (ON DELETE CASCADE): yetim
// satırlar aynı kimlikle yeni bir kayıt açıldığında geri dirilirdi.
func TestSilmeModelleriDeSiler(t *testing.T) {
	ctx, st := kur(t)

	if _, err := st.Q.UpsertProvider(ctx, db.UpsertProviderParams{
		ID: "x", BaseUrl: "https://ornek/v1", Api: "openai-completions",
		Enabled: true, Compat: []byte("{}"),
	}); err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}
	if err := st.Q.UpsertProviderModel(ctx, db.UpsertProviderModelParams{
		ProviderID: "x", ModelID: "m", Input: []string{},
	}); err != nil {
		t.Fatalf("UpsertProviderModel: %v", err)
	}
	if err := st.Q.DeleteProvider(ctx, "x"); err != nil {
		t.Fatalf("DeleteProvider: %v", err)
	}
	kalan, err := st.Q.ListProviderModels(ctx)
	if err != nil {
		t.Fatalf("ListProviderModels: %v", err)
	}
	for _, m := range kalan {
		if m.ProviderID == "x" {
			t.Fatalf("model yetim kaldı: %+v", m)
		}
	}
}

// Tohumlama YALNIZ tablo boşken çalışır: kullanıcı bir kez düzenledikten
// sonra ortam değişkeni söz sahibi olsaydı, her yeniden başlatma
// değişikliği geri alırdı.
func TestTohumlamaBirKezCalisir(t *testing.T) {
	ctx, st := kur(t)
	svc := providers.New(st, nil)

	tohum := providers.Tohum{
		ID: "ortam", Name: "Ortam", BaseURL: "https://ornek/v1",
		API:    "openai-completions",
		Models: []providers.TohumModel{{ID: "m1", Label: "M1", ContextWindow: 128000}},
	}
	if err := svc.Tohumla(ctx, tohum); err != nil {
		t.Fatalf("Tohumla: %v", err)
	}
	sayi, err := st.Q.CountProviders(ctx)
	if err != nil {
		t.Fatalf("CountProviders: %v", err)
	}
	if sayi != 1 {
		t.Fatalf("tohumlama sonrası kayıt = %d, beklenen 1", sayi)
	}

	// Kullanıcı adresi değiştirdi; ikinci tohumlama ona dokunmamalı.
	if _, err := st.Q.UpsertProvider(ctx, db.UpsertProviderParams{
		ID: "ortam", Name: "Ortam", BaseUrl: "https://kullanici/v1",
		Api: "openai-completions", Enabled: true, Compat: []byte("{}"),
	}); err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}
	if err := svc.Tohumla(ctx, tohum); err != nil {
		t.Fatalf("Tohumla (ikinci): %v", err)
	}
	kayit, err := st.Q.GetProvider(ctx, "ortam")
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if kayit.BaseUrl != "https://kullanici/v1" {
		t.Errorf("tohumlama kullanıcının düzenlemesini ezdi: %q", kayit.BaseUrl)
	}
}

// MASTER_KEY yokken anahtar yazma REDDEDİLİR — sessizce düz metin saklamak
// yerine özelliği kapatmak doğru.
func TestMuhursuzAnahtarYazilmaz(t *testing.T) {
	ctx, st := kur(t)
	svc := providers.New(st, nil)

	if _, err := st.Q.UpsertProvider(ctx, db.UpsertProviderParams{
		ID: "x", BaseUrl: "https://ornek/v1", Api: "openai-completions",
		Enabled: true, Compat: []byte("{}"),
	}); err != nil {
		t.Fatalf("UpsertProvider: %v", err)
	}
	if err := svc.AnahtarYaz(ctx, "x", "sk-gizli"); err == nil {
		t.Fatal("mühürsüz kurulumda anahtar yazıldı")
	}
	// Silme mühürsüz de çalışmalı: yanlış anahtarı kaldırmak için
	// MASTER_KEY şartı, kullanıcıyı bozuk kayıtla baş başa bırakırdı.
	if err := svc.AnahtarYaz(ctx, "x", ""); err != nil {
		t.Errorf("mühürsüz silme reddedildi: %v", err)
	}
}

// Anahtarlı tohum + MASTER_KEY yok: çekirdek AÇILMALI ve tablo BOŞ kalmalı.
//
// İki ayrı tuzağın testi. (1) Hata döndürmek açılışı düşürüyordu: yükseltilen
// eski bir kurulum (PI_PROVIDER_KEY var, MASTER_KEY yok) hiç açılmaz, kullanıcı
// sebebi görebileceği ekrana bile ulaşamazdı. (2) Anahtarsız yarım tohumlamak
// tabloyu doldurur ve MASTER_KEY sonradan tanımlandığında tohum bir daha ASLA
// koşmazdı — sağlayıcı kalıcı olarak anahtarsız kalırdı.
func TestMuhursuzAnahtarliTohumYarimKalmaz(t *testing.T) {
	ctx, st := kur(t)
	tohum := providers.Tohum{
		ID: "ortam", Name: "Ortam", BaseURL: "https://ornek/v1",
		API: "openai-completions", APIKey: "sk-gizli",
		Models: []providers.TohumModel{{ID: "m1", Label: "M1", ContextWindow: 128000}},
	}

	if err := providers.New(st, nil).Tohumla(ctx, tohum); err != nil {
		t.Fatalf("mühürsüz tohumlama açılışı düşürdü: %v", err)
	}
	sayi, err := st.Q.CountProviders(ctx)
	if err != nil {
		t.Fatalf("CountProviders: %v", err)
	}
	if sayi != 0 {
		t.Fatalf("anahtarsız yarım kayıt açıldı (%d) — MASTER_KEY sonradan gelince tohum bir daha koşmaz", sayi)
	}

	// MASTER_KEY sonradan tanımlandı: tohum ŞİMDİ koşmalı, anahtarıyla.
	sealer, err := crypto.New("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("crypto.New: %v", err)
	}
	svc := providers.New(st, sealer)
	if err := svc.Tohumla(ctx, tohum); err != nil {
		t.Fatalf("mühür geldikten sonra tohumlama: %v", err)
	}
	tarifler, err := svc.Tarifler(ctx)
	if err != nil {
		t.Fatalf("Tarifler: %v", err)
	}
	if len(tarifler) != 1 || tarifler[0].APIKey != "sk-gizli" {
		t.Fatalf("tohum anahtarıyla gelmedi: %+v", tarifler)
	}
}
