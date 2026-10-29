package pi

import (
	"strings"
	"testing"
)

func envHarita(env []string) map[string]string {
	out := map[string]string{}
	for _, e := range env {
		if k, v, ok := strings.Cut(e, "="); ok {
			out[k] = v
		}
	}
	return out
}

// Ortam SIFIRDAN kurulur (Ek B.3): kabuktan miras kalan sağlayıcı
// anahtarları koşuya sızmamalı.
func TestCleanEnvMirasAlmaz(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sizmamali")
	t.Setenv("NODE_OPTIONS", "--sizinti")

	a := &Adapter{ConfigDir: "/veri/pi"}
	env := envHarita(a.cleanEnv("/usr/bin/pi", nil))

	if _, ok := env["ANTHROPIC_API_KEY"]; ok {
		t.Error("kabuktan sağlayıcı anahtarı miras alındı")
	}
	if _, ok := env["NODE_OPTIONS"]; ok {
		t.Error("NODE_OPTIONS miras alındı")
	}
	if env["PI_CODING_AGENT_DIR"] != "/veri/pi" {
		t.Errorf("config dizini geçmedi: %q", env["PI_CODING_AGENT_DIR"])
	}
	if env["PI_TELEMETRY"] != "0" {
		t.Error("telemetri kapatılmadı")
	}
}

// Anahtar YALNIZ ortamda taşınır; models.json'da referansı durur.
func TestCleanEnvSaglayiciAnahtari(t *testing.T) {
	ps := []ProviderConfig{{ID: "oa", BaseURL: "http://x", APIKey: "sk-gizli"}}

	env := envHarita((&Adapter{}).cleanEnv("pi", ps))
	ad := anahtarEnvleri(ps)["oa"]
	if env[ad] != "sk-gizli" {
		t.Errorf("%s geçmedi: %q", ad, env[ad])
	}
}

// İki sağlayıcı, iki AYRI değişken; models.json ile ortam aynı adı görmeli.
func TestCokluSaglayiciAnahtarlariAyrisir(t *testing.T) {
	ps := []ProviderConfig{
		{ID: "oa", BaseURL: "http://a", APIKey: "sk-bir"},
		{ID: "z-ai", BaseURL: "http://b", APIKey: "sk-iki"},
	}
	env := envHarita((&Adapter{}).cleanEnv("pi", ps))
	adlar := anahtarEnvleri(ps)
	if adlar["oa"] == adlar["z-ai"] {
		t.Fatal("iki sağlayıcı aynı ortam değişkenine düştü")
	}
	if env[adlar["oa"]] != "sk-bir" || env[adlar["z-ai"]] != "sk-iki" {
		t.Errorf("anahtarlar karıştı: %q / %q", env[adlar["oa"]], env[adlar["z-ai"]])
	}
}

// Nokta ve tire aynı alt çizgiye düşer; çakışma sayıyla ayrılmalı yoksa
// iki sağlayıcı birbirinin anahtarını kullanırdı.
func TestAnahtarAdiCakismasiAyrilir(t *testing.T) {
	adlar := anahtarEnvleri([]ProviderConfig{
		{ID: "a-b", BaseURL: "http://a"},
		{ID: "a.b", BaseURL: "http://b"},
	})
	if adlar["a-b"] == adlar["a.b"] {
		t.Fatalf("çakışma ayrılmadı: %q", adlar["a-b"])
	}
}

func TestCleanEnvEkDegiskenler(t *testing.T) {
	a := &Adapter{Env: map[string]string{"ANTHROPIC_API_KEY": "sk-bilincli"}}
	env := envHarita(a.cleanEnv("pi", nil))
	if env["ANTHROPIC_API_KEY"] != "sk-bilincli" {
		t.Error("PI_ENV değişkeni sürece geçmedi")
	}
}

func TestGatewayCiftleri(t *testing.T) {
	got := gatewayCiftleri([]Gateway{
		{Name: "gw", URL: "http://127.0.0.1:8082/mcp"},
		{Name: "mac", URL: "http://127.0.0.1:8082/mcp/mac"},
	})
	if got != "gw=http://127.0.0.1:8082/mcp,mac=http://127.0.0.1:8082/mcp/mac" {
		t.Errorf("çiftler yanlış: %q", got)
	}
}

// Ayraç taşıyan yüzey ATLANIR: bozuk bir liste üretmek, eklentinin araç
// adlarını yanlış önekle kaydetmesine yol açardı.
func TestGatewayCiftleriBozukAtlanir(t *testing.T) {
	got := gatewayCiftleri([]Gateway{
		{Name: "kö,tü", URL: "http://a"},
		{Name: "e=şit", URL: "http://b"},
		{Name: "iyi", URL: "http://c"},
		{Name: "", URL: "http://d"},
		{Name: "adressiz", URL: ""},
	})
	if got != "iyi=http://c" {
		t.Errorf("yalnız geçerli yüzey kalmalıydı, geldi: %q", got)
	}
}
