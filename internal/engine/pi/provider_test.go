package pi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const testAnahtari = "sk-test-COK-GIZLI-DEGER"

func ornekSaglayici() []ProviderConfig {
	return []ProviderConfig{{
		ID:      "oa",
		BaseURL: "https://ornek.invalid/v1",
		APIKey:  testAnahtari,
		Models:  []ModelSpec{{ID: "m-1", ContextWindow: 400000, MaxTokens: 128000}},
	}}
}

// Sağlayıcı kimliğinden türeyen ortam değişkeni adı — models.json referansı
// ile cleanEnv'in koyduğu değer AYNI adı görmek zorunda.
func ornekAnahtarEnv() string { return anahtarEnvleri(ornekSaglayici())["oa"] }

// Sırların diske düşmemesi bu adaptörün EN SERT kuralı: models.json veri
// diskinde kalıcıdır ve yedeklere girer.
func TestModelsJsonAnahtariDuzMetinYAZMAZ(t *testing.T) {
	dizin := t.TempDir()
	if err := saglayicilariYaz(dizin, ornekSaglayici()); err != nil {
		t.Fatalf("yaz: %v", err)
	}

	ham, err := os.ReadFile(filepath.Join(dizin, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ham), testAnahtari) {
		t.Fatal("API anahtarı models.json'a DÜZ METİN yazıldı")
	}
	if !strings.Contains(string(ham), "$"+ornekAnahtarEnv()) {
		t.Errorf("anahtar ortam referansı yok, dosya: %s", ham)
	}
}

func TestModelsJsonIzinleriDar(t *testing.T) {
	dizin := t.TempDir()
	if err := saglayicilariYaz(dizin, ornekSaglayici()); err != nil {
		t.Fatalf("yaz: %v", err)
	}
	st, err := os.Stat(filepath.Join(dizin, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("models.json izinleri %v — 0600 olmalı", st.Mode().Perm())
	}
}

func TestModelsJsonYapisi(t *testing.T) {
	dizin := t.TempDir()
	p := ornekSaglayici()
	p[0].API = "" // varsayılana düşmeli
	if err := saglayicilariYaz(dizin, p); err != nil {
		t.Fatalf("yaz: %v", err)
	}

	var doc struct {
		Providers map[string]struct {
			BaseURL string `json:"baseUrl"`
			API     string `json:"api"`
			Models  []struct {
				ID            string `json:"id"`
				ContextWindow int    `json:"contextWindow"`
			} `json:"models"`
		} `json:"providers"`
	}
	ham, _ := os.ReadFile(filepath.Join(dizin, "models.json"))
	if err := json.Unmarshal(ham, &doc); err != nil {
		t.Fatalf("models.json ayrıştırılamadı: %v", err)
	}
	s, ok := doc.Providers["oa"]
	if !ok {
		t.Fatalf("sağlayıcı kendi kimliğiyle yazılmadı: %s", ham)
	}
	if s.API != "openai-completions" {
		t.Errorf("varsayılan api yanlış: %q", s.API)
	}
	if len(s.Models) != 1 || s.Models[0].ID != "m-1" || s.Models[0].ContextWindow != 400000 {
		t.Errorf("model kataloğu yanlış yazıldı: %+v", s.Models)
	}
}

// Tarif yoksa dosyaya HİÇ dokunulmaz: pi kendi yerleşik kataloğuyla çalışır.
func TestTarifsizSaglayiciDosyaYazmaz(t *testing.T) {
	dizin := t.TempDir()
	if err := saglayicilariYaz(dizin, nil); err != nil {
		t.Fatalf("yaz: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dizin, "models.json")); !os.IsNotExist(err) {
		t.Error("tarif yokken models.json yazıldı")
	}
}

// Her tur ve her yoklama hazırlığı koşar; eşzamanlı yazımlar dosyayı
// bozmamalı (sabit .tmp adı kullanılsaydı yarım dosya rename edilirdi).
func TestEszamanliYazimBozmaz(t *testing.T) {
	dizin := t.TempDir()
	p := ornekSaglayici()

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := saglayicilariYaz(dizin, p); err != nil {
				t.Errorf("yaz: %v", err)
			}
		}()
	}
	wg.Wait()

	ham, err := os.ReadFile(filepath.Join(dizin, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(ham, &doc); err != nil {
		t.Fatalf("eşzamanlı yazımdan sonra models.json bozuk: %v\n%s", err, ham)
	}

	// Geçici dosyalar arkada kalmamalı: pi config dizinini tarar.
	girdiler, _ := os.ReadDir(dizin)
	for _, g := range girdiler {
		if g.Name() != "models.json" {
			t.Errorf("artık dosya kaldı: %s", g.Name())
		}
	}
}

func TestModelInfolarSaglayiciOnekliDoner(t *testing.T) {
	infolar := modelInfolar(ornekSaglayici())
	if len(infolar) != 1 {
		t.Fatalf("model sayısı: %d", len(infolar))
	}
	// pi --model "provider/id" desenini kabul ediyor (0.84.2 --help).
	if infolar[0].ID != "oa/m-1" {
		t.Errorf("model kimliği %q — sağlayıcı öneki bekleniyordu", infolar[0].ID)
	}
}
