package httpapi

import "testing"

// Katalogda gömme/ses/görsel modelleri de var; bir botta hiçbiri koşmaz ve
// listeyi kirletirler.
func TestSohbetModeliSuzgeci(t *testing.T) {
	metin := []string{"text"}

	gecmeli := []struct{ id, ad string }{
		{"claude-opus-4-7", "Claude Opus 4.7"},
		{"gpt-5", "GPT-5"},
		{"deepseek-v4-flash", ""},
		{"qwen3-coder", "Qwen3 Coder"},
	}
	for _, m := range gecmeli {
		if !sohbetModeliMi(m.id, m.ad, false, metin) {
			t.Errorf("sohbet modeli elendi: %s", m.id)
		}
	}

	elenmeli := []struct{ id, ad string }{
		{"text-embedding-3-large", ""},
		{"whisper-1", ""},
		{"dall-e-3", ""},
		{"gpt-image-1", ""},
		{"gpt-4o-realtime-preview", ""},
		{"omni-moderation-latest", "Moderation"},
		{"rerank-v3", ""},
	}
	for _, m := range elenmeli {
		if sohbetModeliMi(m.id, m.ad, false, metin) {
			t.Errorf("sohbet dışı model listeye girdi: %s", m.id)
		}
	}
}

func TestDeprecatedModelElenir(t *testing.T) {
	if sohbetModeliMi("gpt-4-0314", "GPT-4", true, []string{"text"}) {
		t.Error("deprecated model listeye girdi")
	}
}

// Çıktısı metin olmayan model sohbet motoru değildir (modaliteye bak,
// yalnız ada değil).
func TestMetinCiktisiOlmayanElenir(t *testing.T) {
	if sohbetModeliMi("bir-model", "Bir Model", false, []string{"image"}) {
		t.Error("metin üretmeyen model listeye girdi")
	}
	// Modalite bilgisi yoksa eleme yapılmaz: katalog eksik olabilir.
	if !sohbetModeliMi("bir-model", "Bir Model", false, nil) {
		t.Error("modalitesiz model gereksiz yere elendi")
	}
}

// models.dev tel biçimini hiç söylemiyor; biçim SDK paketinden çıkarılır ki
// kullanıcı katalogdan seçince alan doğru ön-dolsun.
func TestTelBicimiNPMdenCikar(t *testing.T) {
	durumlar := []struct{ npm, bekleyen string }{
		{"@ai-sdk/anthropic", "anthropic-messages"},
		{"@ai-sdk/google", "google-generative-ai"},
		{"@ai-sdk/openai-compatible", "openai-completions"},
		{"@ai-sdk/openai", "openai-responses"},
		// Vertex üstünden Anthropic: tel yine Anthropic'tir.
		{"@ai-sdk/google-vertex/anthropic", "anthropic-messages"},
		// Katalogdaki 147 kaydın paketi openai-compatible; tanımadığımız
		// paket de en olası biçime düşer.
		{"@openrouter/ai-sdk-provider", "openai-completions"},
		{"", "openai-completions"},
	}
	for _, d := range durumlar {
		if got := telBicimi(d.npm); got != d.bekleyen {
			t.Errorf("npm %q → %q, beklenen %q", d.npm, got, d.bekleyen)
		}
	}
	// Üretilen her biçim DB CHECK'inden geçmeli, yoksa katalogdan seçilen
	// sağlayıcı kaydedilemezdi.
	for _, npm := range []string{"@ai-sdk/anthropic", "@ai-sdk/google", "", "@ai-sdk/openai"} {
		if !gecerliAPIler[telBicimi(npm)] {
			t.Errorf("npm %q geçersiz biçim üretti: %q", npm, telBicimi(npm))
		}
	}
}

// models.dev'in `api` alanı TABAN ADRESTİR; forma ön-dolar.
func TestTemelURLKatalogdanGelir(t *testing.T) {
	if got := temelURL("https://openrouter.ai/api/v1"); got != "https://openrouter.ai/api/v1" {
		t.Errorf("adres geçmedi: %q", got)
	}
	// Çözülmemiş kabuk değişkeni adres değildir (katalogdaki "neon" kaydı):
	// forma dolsaydı kullanıcı fark etmeden kaydeder, koşular düşerdi.
	if got := temelURL("${NEON_AI_GATEWAY_BASE_URL}/v1"); got != "" {
		t.Errorf("adres olmayan değer geçti: %q", got)
	}
	if got := temelURL(""); got != "" {
		t.Errorf("boş değer geçti: %q", got)
	}
}
