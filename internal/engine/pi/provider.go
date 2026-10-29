package pi

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/esmworks/OpenAssist/internal/engine"
)

// Sağlayıcı anahtarları ortam değişkeniyle taşınır, models.json'a DÜZ METİN
// yazılmaz: dosya veri diskinde kalıcıdır ve yedeklere girer. pi `$VAR`
// biçimini çözdüğü için dosyada yalnız değişkenin adı durur. argv'ye de
// yazmıyoruz (--api-key olsaydı `ps` çıktısında görünürdü).
const anahtarEnvOnEki = "OA_PI_KEY_"

// ProviderConfig — pi'ye tanıtılacak bir sağlayıcı.
//
// pi'de sağlayıcı bir kod yolu değil VERİDİR; OpenAI uyumlu ya da Anthropic
// uyumlu her endpoint bu tarifle motora takılır. Model listesinin sahibi de
// biziz: ikilinin gömülü kataloğunu ölçmek zorunda kalmıyoruz.
//
// Tarifler artık veritabanında yaşar (göç 00016) — kullanıcı Modeller
// ekranından ekler, adaptör her turda okur.
type ProviderConfig struct {
	ID      string
	Name    string
	BaseURL string
	// API — openai-completions | openai-responses | anthropic-messages |
	// google-generative-ai. Boşsa openai-completions (en uyumlu).
	API    string
	APIKey string
	Models []ModelSpec
	// Compat — sağlayıcıya özel uyumluluk bayrakları (pi docs/models.md).
	// Örn. Anthropic uyumlu bir vekil eager tool streaming'i reddediyorsa
	// {"supportsEagerToolInputStreaming": false}.
	Compat map[string]any
}

type ModelSpec struct {
	ID            string
	Name          string
	Reasoning     bool
	Input         []string
	ContextWindow int
	MaxTokens     int
}

func (p ProviderConfig) tanimli() bool { return p.ID != "" && p.BaseURL != "" }

// anahtarEnvleri — sağlayıcı kimliğinden ortam değişkeni adına eşleme.
//
// TEK yerde üretilir çünkü iki taraf da aynı adı görmek zorunda: models.json
// referansı yazar, cleanEnv değeri koyar. İkisi ayrı hesaplansaydı bir
// yeniden adlandırma sessizce "anahtar yok" hatasına dönerdi.
//
// Kimlik alfabesi (a-z0-9._-) büyük harfe çevrilirken `.` ve `-` alt çizgi
// olur; bu iki farklı kimliği aynı ada düşürebilir ("a-b" ve "a.b"), o yüzden
// çakışma sayıyla ayrılır.
func anahtarEnvleri(ps []ProviderConfig) map[string]string {
	out := make(map[string]string, len(ps))
	alinmis := make(map[string]bool, len(ps))
	for _, p := range ps {
		if p.ID == "" {
			continue
		}
		var b strings.Builder
		for _, r := range strings.ToUpper(p.ID) {
			if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			} else {
				b.WriteByte('_')
			}
		}
		ad := anahtarEnvOnEki + b.String()
		if alinmis[ad] {
			for n := 2; ; n++ {
				aday := fmt.Sprintf("%s_%d", ad, n)
				if !alinmis[aday] {
					ad = aday
					break
				}
			}
		}
		alinmis[ad] = true
		out[p.ID] = ad
	}
	return out
}

// piGirdiKipleri — pi'nin models.json şemasının kabul ettiği `input` değerleri
// (0.84.2'de ikili üzerinde tek tek ölçüldü: text ve image geçiyor; audio,
// video, pdf, document, file, json reddediliyor).
var piGirdiKipleri = map[string]bool{"text": true, "image": true}

// GirdiKipleri — tanımadığımız kipleri ELER.
//
// Bu bir kozmetik temizlik değil, dosyanın tamamını kurtarma işi: pi
// models.json'ı JSON şemasıyla doğruluyor ve TEK bir geçersiz değer BÜTÜN
// dosyayı düşürüyor — bir modelin `audio` kipi yüzünden hiçbir sağlayıcının
// hiçbir modeli görünmez oluyor ("Model … not found"). Kaynak models.dev
// kataloğu: orada bir modelin `modalities.input` listesi rahatça
// ["text","image","audio","video"] olabiliyor.
//
// Beyaz liste bilinçli olarak DAR: pi yarın yeni bir kip desteklerse burayı
// büyütmek bir satır, ama tanımadığımız bir değeri geçirmenin bedeli tüm
// kurulumun modelsiz kalması. Kaybedilen tek şey pi'nin zaten kullanamadığı
// bir yetenek etiketi.
func GirdiKipleri(in []string) []string {
	out := make([]string, 0, len(in))
	for _, k := range in {
		if piGirdiKipleri[k] {
			out = append(out, k)
		}
	}
	return out
}

// saglayicilariYaz — tarifleri configDir/models.json'a koyar. Dosya her turda
// yeniden üretilir: kullanıcı Modeller ekranında bir şey değiştirince bir
// sonraki tur yeni tarifi görsün.
//
// Liste BOŞSA dosya silinir, boş bırakılmaz: bayat bir models.json, ekrandan
// kaldırılmış bir sağlayıcıyı motorda yaşatmaya devam ederdi.
func saglayicilariYaz(configDir string, ps []ProviderConfig) error {
	yol := filepath.Join(configDir, "models.json")

	envler := anahtarEnvleri(ps)
	saglayicilar := map[string]any{}
	for _, p := range ps {
		if !p.tanimli() {
			continue
		}
		api := p.API
		if api == "" {
			api = "openai-completions"
		}

		models := make([]map[string]any, 0, len(p.Models))
		for _, m := range p.Models {
			if m.ID == "" {
				continue
			}
			entry := map[string]any{"id": m.ID}
			if m.Name != "" {
				entry["name"] = m.Name
			}
			if m.Reasoning {
				entry["reasoning"] = true
			}
			if girdi := GirdiKipleri(m.Input); len(girdi) > 0 {
				entry["input"] = girdi
			}
			if m.ContextWindow > 0 {
				entry["contextWindow"] = m.ContextWindow
			}
			if m.MaxTokens > 0 {
				entry["maxTokens"] = m.MaxTokens
			}
			models = append(models, entry)
		}

		s := map[string]any{
			"baseUrl": p.BaseURL,
			"api":     api,
			// Anahtarsız yerel sunucularda bile bir değer gerekir: pi
			// kimliksiz modeli seçilebilir saymaz (docs/models.md).
			"apiKey": "$" + envler[p.ID],
			"models": models,
		}
		if p.Name != "" {
			s["name"] = p.Name
		}
		if len(p.Compat) > 0 {
			s["compat"] = p.Compat
		}
		saglayicilar[p.ID] = s
	}

	if len(saglayicilar) == 0 {
		if err := os.Remove(yol); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("models.json silinemedi: %w", err)
		}
		return nil
	}

	data, err := json.MarshalIndent(map[string]any{"providers": saglayicilar}, "", "  ")
	if err != nil {
		return err
	}

	// Geçici ad BENZERSİZ olmalı: hazırlık her turda ve her yoklamada koşar,
	// yani birden çok worker aynı anda buraya yazar. Sabit ".tmp" adında
	// biri yazarken diğeri truncate eder ve rename yarım dosyayı yerine
	// koyardı — pi o andan sonra hiç açılmazdı.
	tmp, err := os.CreateTemp(configDir, "models-*.json")
	if err != nil {
		return fmt.Errorf("models.json yazılamadı: %w", err)
	}
	tmpAd := tmp.Name()
	defer func() { _ = os.Remove(tmpAd) }() // başarılı rename'den sonra no-op

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("models.json izinleri kurulamadı: %w", err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("models.json yazılamadı: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("models.json kapatılamadı: %w", err)
	}
	if err := os.Rename(tmpAd, yol); err != nil {
		return fmt.Errorf("models.json yerine konamadı: %w", err)
	}
	return nil
}

// modelInfolar — tariflerden UI'ın göstereceği model listesi. Kimlik pi'nin
// beklediği "sağlayıcı/model" biçimindedir (--model bu deseni çözüyor).
func modelInfolar(ps []ProviderConfig) []engine.ModelInfo {
	var out []engine.ModelInfo
	for _, p := range ps {
		for _, m := range p.Models {
			if m.ID == "" {
				continue
			}
			ad := m.Name
			if ad == "" {
				ad = m.ID
			}
			kimlik := m.ID
			if p.ID != "" {
				kimlik = p.ID + "/" + m.ID
				if p.Name != "" {
					ad = p.Name + " · " + ad
				}
			}
			out = append(out, engine.ModelInfo{ID: kimlik, Label: ad})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
