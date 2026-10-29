//go:build integration

// Sağlayıcı uçları (Modeller ekranı): gerçek Postgres + gerçek HTTP yığını.
//
// Buradaki asıl mesele ANAHTARIN TEK YÖNLÜ olması: bir kez yazıldıktan sonra
// hiçbir yanıtta geri dönmemeli. Dönseydi API belirtecini ele geçiren biri
// tüm sağlayıcı anahtarlarını da almış olurdu.
package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/esmworks/OpenAssist/internal/bus"
	"github.com/esmworks/OpenAssist/internal/config"
	"github.com/esmworks/OpenAssist/internal/crypto"
	"github.com/esmworks/OpenAssist/internal/httpapi"
	"github.com/esmworks/OpenAssist/internal/providers"
	"github.com/esmworks/OpenAssist/internal/store"
)

const testMuhurAnahtari = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func saglayiciSunucu(t *testing.T) (*store.Store, string) {
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
	if _, err := st.Pool().Exec(ctx, "DELETE FROM providers"); err != nil {
		t.Fatalf("temizlik: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool().Exec(context.Background(), "DELETE FROM providers")
	})

	sealer, err := crypto.New(testMuhurAnahtari)
	if err != nil {
		t.Fatalf("crypto.New: %v", err)
	}
	srv := httpapi.New(
		config.Config{HTTPAddr: ":0", APIToken: testBelirtec},
		httpapi.Deps{DB: st, Store: st, Bus: bus.New(), Providers: providers.New(st, sealer)})
	ts := httptest.NewServer(srv.Handler)
	belirtecliIstemci(ts)
	t.Cleanup(ts.Close)
	return st, ts.URL
}

func saglayiciYaz(t *testing.T, taban, id, govde string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, taban+"/api/providers/"+id, strings.NewReader(govde))
	if err != nil {
		t.Fatalf("istek: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+testBelirtec)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	defer res.Body.Close()
	ham, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(ham, &out)
	return res.StatusCode, out
}

func TestSaglayiciGidisDonusu(t *testing.T) {
	st, ts := saglayiciSunucu(t)
	ctx := context.Background()

	kod, kayit := saglayiciYaz(t, ts, "minimax", `{
		"id":"minimax","name":"MiniMax","base_url":"https://api.minimax.io/anthropic/v1",
		"api":"anthropic-messages","api_key":"sk-gizli",
		"models":[{"id":"MiniMax-M2.7","label":"M2.7","context_window":204800,
		           "max_tokens":131072,"reasoning":true,"input":["text"]}]}`)
	if kod != http.StatusOK {
		t.Fatalf("PUT durumu = %d (%v)", kod, kayit)
	}

	// Anahtar YANITTA YOK; yalnız "tanımlı mı" bilgisi var.
	if kayit["has_key"] != true {
		t.Errorf("has_key yanlış: %v", kayit["has_key"])
	}
	if govde, _ := json.Marshal(kayit); bytes.Contains(govde, []byte("sk-gizli")) {
		t.Fatal("anahtar yanıtta sızdı")
	}

	// Listede de sızmamalı.
	res, err := belirtecliGet(ts + "/api/providers")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	ham, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if bytes.Contains(ham, []byte("sk-gizli")) {
		t.Fatal("anahtar listede sızdı")
	}
	var liste []map[string]any
	if err := json.Unmarshal(ham, &liste); err != nil {
		t.Fatalf("liste çözülemedi: %v", err)
	}
	if len(liste) != 1 || liste[0]["id"] != "minimax" {
		t.Fatalf("liste beklenmedik: %v", liste)
	}

	// Motor tarafı aynı kaydı görüyor mu — anahtarın mührü açılıyor mu?
	sealer, _ := crypto.New(testMuhurAnahtari)
	tarif, err := providers.New(st, sealer).Tarifler(ctx)
	if err != nil {
		t.Fatalf("Tarifler: %v", err)
	}
	if len(tarif) != 1 || tarif[0].APIKey != "sk-gizli" {
		t.Fatalf("motor tarifi yanlış: %+v", tarif)
	}
}

// PUT semantiği: model listesi TAM DEĞİŞİR. Kısmi birleştirme, kullanıcının
// ekrandan sildiği bir modelin kayıtta kalmasına yol açardı.
func TestModelListesiTamDegisir(t *testing.T) {
	_, ts := saglayiciSunucu(t)

	if kod, g := saglayiciYaz(t, ts, "x", `{"id":"x","base_url":"https://ornek/v1",
		"models":[{"id":"a"},{"id":"b"}]}`); kod != http.StatusOK {
		t.Fatalf("ilk yazma: %d (%v)", kod, g)
	}
	kod, kayit := saglayiciYaz(t, ts, "x", `{"id":"x","base_url":"https://ornek/v1",
		"models":[{"id":"b"}]}`)
	if kod != http.StatusOK {
		t.Fatalf("ikinci yazma: %d (%v)", kod, kayit)
	}
	modeller, _ := kayit["models"].([]any)
	if len(modeller) != 1 {
		t.Fatalf("model listesi değişmedi: %v", kayit["models"])
	}
}

// Anahtar alanının üç durumu: gönderilmedi = dokunma, "" = sil, dolu =
// değiştir. "Dokunma" olmasaydı ekrandan yapılan her düzenleme anahtarı
// silerdi (ekran anahtarı bilmiyor, sunucu vermiyor).
func TestAnahtarUcDurum(t *testing.T) {
	_, ts := saglayiciSunucu(t)

	if kod, g := saglayiciYaz(t, ts, "x", `{"id":"x","base_url":"https://ornek/v1",
		"api_key":"sk-1","models":[{"id":"a"}]}`); kod != http.StatusOK {
		t.Fatalf("kurulum: %d (%v)", kod, g)
	}

	// Alan hiç yok: anahtar korunur.
	_, kayit := saglayiciYaz(t, ts, "x", `{"id":"x","base_url":"https://ornek/v1",
		"models":[{"id":"a"}]}`)
	if kayit["has_key"] != true {
		t.Fatal("anahtar alansız düzenlemede silindi")
	}

	// Boş dize: silinir.
	_, kayit = saglayiciYaz(t, ts, "x", `{"id":"x","base_url":"https://ornek/v1",
		"api_key":"","models":[{"id":"a"}]}`)
	if kayit["has_key"] != false {
		t.Fatal("boş anahtar silmedi")
	}
}

// compat ekrandan gönderilmiyor (uzman ayarı); düzenleme onu SİLMEMELİ.
func TestCompatKorunur(t *testing.T) {
	_, ts := saglayiciSunucu(t)

	if kod, g := saglayiciYaz(t, ts, "x", `{"id":"x","base_url":"https://ornek/v1",
		"compat":{"supportsEagerToolInputStreaming":false},"models":[{"id":"a"}]}`); kod != http.StatusOK {
		t.Fatalf("kurulum: %d (%v)", kod, g)
	}
	_, kayit := saglayiciYaz(t, ts, "x", `{"id":"x","name":"X",
		"base_url":"https://ornek/v1","models":[{"id":"a"}]}`)
	compat, _ := kayit["compat"].(map[string]any)
	if compat == nil {
		t.Fatalf("compat düzenlemede silindi: %v", kayit)
	}
}

func TestGecersizSaglayiciReddedilir(t *testing.T) {
	_, ts := saglayiciSunucu(t)

	// Gövde doğrulaması POST üzerinden sınanır: PUT'ta kimliği YOL belirler
	// (kaydın adresi odur), gövdedeki değer yok sayılır.
	durumlar := []struct{ ad, govde string }{
		{"şemasız adres", `{"id":"x","base_url":"ornek/v1"}`},
		{"adres yok", `{"id":"x"}`},
		{"geçersiz kimlik", `{"id":"X Y","base_url":"https://ornek/v1"}`},
		{"geçersiz biçim", `{"id":"x","base_url":"https://ornek/v1","api":"uydurma"}`},
		{"eğik çizgili model", `{"id":"x","base_url":"https://ornek/v1","models":[{"id":"a/b"}]}`},
		{"ikiz model", `{"id":"x","base_url":"https://ornek/v1","models":[{"id":"a"},{"id":"a"}]}`},
		{"tanınmayan alan", `{"id":"x","base_url":"https://ornek/v1","uydurma":1}`},
	}
	for _, d := range durumlar {
		res, err := belirtecliPost(ts+"/api/providers", "application/json", strings.NewReader(d.govde))
		if err != nil {
			t.Fatalf("%s: POST: %v", d.ad, err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: durum = %d, beklenen 400", d.ad, res.StatusCode)
		}
	}

	// Yoldaki kimlik de doğrulanır — yol geçersizse kayıt açılmamalı.
	if kod, _ := saglayiciYaz(t, ts, "X%20Y", `{"base_url":"https://ornek/v1"}`); kod != http.StatusBadRequest {
		t.Errorf("geçersiz yol kimliği: durum = %d, beklenen 400", kod)
	}
}

func TestSaglayiciSilinir(t *testing.T) {
	_, ts := saglayiciSunucu(t)

	if kod, g := saglayiciYaz(t, ts, "x", `{"id":"x","base_url":"https://ornek/v1",
		"models":[{"id":"a"}]}`); kod != http.StatusOK {
		t.Fatalf("kurulum: %d (%v)", kod, g)
	}

	req, _ := http.NewRequest(http.MethodDelete, ts+"/api/providers/x", nil)
	req.Header.Set("Authorization", "Bearer "+testBelirtec)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE durumu = %d", res.StatusCode)
	}

	// İkinci silme 404 — "sildim" demek yerine gerçeği söyler.
	req2, _ := http.NewRequest(http.MethodDelete, ts+"/api/providers/x", nil)
	req2.Header.Set("Authorization", "Bearer "+testBelirtec)
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("DELETE (ikinci): %v", err)
	}
	res2.Body.Close()
	if res2.StatusCode != http.StatusNotFound {
		t.Fatalf("ikinci DELETE durumu = %d, beklenen 404", res2.StatusCode)
	}
}

// Belirteçsiz istek geçmemeli: sağlayıcı uçları anahtar yazıyor.
func TestSaglayiciUclariBelirtecIster(t *testing.T) {
	_, ts := saglayiciSunucu(t)

	res, err := http.Get(ts + "/api/providers")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("belirteçsiz GET durumu = %d, beklenen 401", res.StatusCode)
	}
}
