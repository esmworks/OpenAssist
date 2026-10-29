package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/esmworks/OpenAssist/internal/engine/pi"
)

// models.dev kataloğu — topluluk tarafından beslenen sağlayıcı/model listesi
// (186 sağlayıcı, binlerce model): taban adres, bağlam penceresi, çıktı
// sınırı, akıl yürütme ve modalite bilgisi.
//
// Neden SUNUCUDAN çekiliyor da Mac'ten değil: (1) Mac uygulaması yalnız
// OpenAssist'e bağlanır, dışa doğrudan çıkmasını istemiyoruz; (2) önbellek
// tek yerde tutulunca her istemci aynı listeyi görür; (3) kapalı ağdaki bir
// kurulumda tek bir yerde kapanabilir.
//
// Katalog SEÇİM KOLAYLIĞIDIR, otorite değil: kullanıcı modeli elle de
// ekleyebilir. Bu yüzden çekme hatası ekranı kilitlemez, boş liste döner.
const (
	katalogURL    = "https://models.dev/api.json"
	katalogTTL    = 24 * time.Hour
	katalogTimout = 20 * time.Second
	// models.dev yanıtı ~2 MB; tavan bir sürüm patlamasında belleği
	// korur (istemci de bunu ayrıştırmak zorunda).
	katalogMaxBayt = 32 << 20
)

type katalogModel struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Reasoning     bool     `json:"reasoning"`
	ToolCall      bool     `json:"tool_call"`
	ContextWindow int      `json:"context_window"`
	MaxTokens     int      `json:"max_tokens"`
	Input         []string `json:"input"`
	ReleaseDate   string   `json:"release_date"`
}

type katalogSaglayici struct {
	ID string `json:"id"`
	// BaseURL — katalogdan gelen taban adres; sağlayıcı seçilince forma
	// ön-dolar. Boş olabilir (kataloğun 26 kaydında adres yok).
	BaseURL string `json:"base_url,omitempty"`
	Name    string `json:"name"`
	// API — pi'nin tel biçimi (npm paketinden çıkarıldı, bkz. telBicimi).
	API    string         `json:"api,omitempty"`
	Doc    string         `json:"doc,omitempty"`
	Models []katalogModel `json:"models"`
}

// modelsDevYanit — yalnız kullandığımız alanlar.
type modelsDevYanit map[string]struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	NPM  string `json:"npm"`
	// API — adı yanıltıcı: models.dev burada TABAN ADRESİ tutuyor
	// ("https://openrouter.ai/api/v1"), tel biçimini değil. 186 sağlayıcının
	// 160'ında var; biçim yalnız `npm` paketinden çıkarılabiliyor.
	API    string `json:"api"`
	Doc    string `json:"doc"`
	Models map[string]struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Reasoning   bool   `json:"reasoning"`
		ToolCall    bool   `json:"tool_call"`
		ReleaseDate string `json:"release_date"`
		Deprecated  bool   `json:"deprecated"`
		Modalities  struct {
			Input  []string `json:"input"`
			Output []string `json:"output"`
		} `json:"modalities"`
		Limit struct {
			Context int `json:"context"`
			Output  int `json:"output"`
		} `json:"limit"`
	} `json:"models"`
}

type katalogOnbellek struct {
	mu      sync.Mutex
	veri    []katalogSaglayici
	cekilen time.Time
	// tekSefer — eşzamanlı istekler tek çekim yapsın; bayat önbellekte on
	// istemci aynı anda models.dev'e gitmesin.
	tekSefer sync.Mutex
}

// Sohbet/kodlama dışı modeller (gömme, ses, görsel üretimi, moderasyon).
// Bunlar listeyi kirletir ve hiçbiri bir botta çalışmaz.
var sohbetDisiDesenler = []string{
	"embed", "embedding", "whisper", "tts", "text-to-speech", "speech-to-text",
	"-stt-", "moderation", "dall-e", "dalle", "-image", "image-", "gpt-image",
	"-search-", "-realtime-", "-audio", "-transcribe", "sora", "imagen", "veo",
	"rerank", "guard",
}

func sohbetModeliMi(id, ad string, deprecated bool, cikti []string) bool {
	if deprecated {
		return false
	}
	// Çıktısı metin olmayan model bir sohbet motoru değildir.
	if len(cikti) > 0 {
		metin := false
		for _, o := range cikti {
			if o == "text" {
				metin = true
			}
		}
		if !metin {
			return false
		}
	}
	dusuk := strings.ToLower(id + " " + ad)
	for _, d := range sohbetDisiDesenler {
		if strings.Contains(dusuk, d) {
			return false
		}
	}
	return true
}

// telBicimi — katalogdaki SDK paketinden pi'nin tel biçimini çıkarır.
//
// models.dev tel biçimini HİÇ söylemiyor; yalnız hangi Vercel AI SDK
// paketiyle konuşulduğunu (`npm`) veriyor. Elimizdeki tek ipucu bu —
// kullanıcı sağlayıcıyı katalogdan seçtiğinde biçim alanı doğru ön-dolsun
// diye çeviriyoruz; tutmazsa ekrandan değiştirir.
//
// Tanımadığımız pakette openai-completions'a düşülür: kataloğun 186
// sağlayıcısının 147'si zaten openai uyumlu, ve boş bırakmanın sonucu da
// aynı (Validate boş biçimi buna çeviriyor) — açık olan yeğdir.
func telBicimi(npm string) string {
	switch {
	case strings.Contains(npm, "anthropic"):
		return "anthropic-messages"
	// google-vertex/anthropic yukarıda yakalandı: orada tel Anthropic'tir.
	case strings.Contains(npm, "google"):
		return "google-generative-ai"
	case strings.Contains(npm, "openai-compatible"):
		return "openai-completions"
	case strings.Contains(npm, "openai"):
		return "openai-responses"
	}
	return "openai-completions"
}

// temelURL — katalogdaki adresi yalnız gerçekten adresse geçirir.
//
// Bir kayıt ("neon") adres yerine `${NEON_AI_GATEWAY_BASE_URL}/v1` yazıyor:
// çözülmemiş bir kabuk değişkenini forma doldurmak, kullanıcının fark etmeden
// kaydedip koşuların düşmesini seyretmesi demekti.
func temelURL(api string) string {
	if strings.HasPrefix(api, "http://") || strings.HasPrefix(api, "https://") {
		return api
	}
	return ""
}

func (c *katalogOnbellek) al(ctx context.Context, zorla bool) ([]katalogSaglayici, time.Time, error) {
	c.mu.Lock()
	veri, cekilen := c.veri, c.cekilen
	c.mu.Unlock()
	if !zorla && veri != nil && time.Since(cekilen) < katalogTTL {
		return veri, cekilen, nil
	}

	c.tekSefer.Lock()
	defer c.tekSefer.Unlock()
	// Sırada beklerken başkası tazelemiş olabilir.
	c.mu.Lock()
	veri, cekilen = c.veri, c.cekilen
	c.mu.Unlock()
	if !zorla && veri != nil && time.Since(cekilen) < katalogTTL {
		return veri, cekilen, nil
	}

	yeni, err := katalogCek(ctx)
	if err != nil {
		// Bayat veri hiç veriden iyidir: models.dev düştüğünde ekran
		// çalışmaya devam etsin.
		if veri != nil {
			return veri, cekilen, nil
		}
		return nil, time.Time{}, err
	}
	simdi := time.Now()
	c.mu.Lock()
	c.veri, c.cekilen = yeni, simdi
	c.mu.Unlock()
	return yeni, simdi, nil
}

func katalogCek(ctx context.Context) ([]katalogSaglayici, error) {
	ctx, cancel := context.WithTimeout(ctx, katalogTimout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, katalogURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, &katalogHata{"models.dev HTTP " + res.Status}
	}

	var ham modelsDevYanit
	if err := json.NewDecoder(http.MaxBytesReader(nil, res.Body, katalogMaxBayt)).Decode(&ham); err != nil {
		return nil, err
	}

	out := make([]katalogSaglayici, 0, len(ham))
	for id, p := range ham {
		s := katalogSaglayici{
			ID: id, Name: p.Name, BaseURL: temelURL(p.API),
			API: telBicimi(p.NPM), Doc: p.Doc,
		}
		if s.Name == "" {
			s.Name = id
		}
		for mid, m := range p.Models {
			kimlik := m.ID
			if kimlik == "" {
				kimlik = mid
			}
			if !sohbetModeliMi(kimlik, m.Name, m.Deprecated, m.Modalities.Output) {
				continue
			}
			s.Models = append(s.Models, katalogModel{
				ID: kimlik, Name: m.Name, Reasoning: m.Reasoning, ToolCall: m.ToolCall,
				ContextWindow: m.Limit.Context, MaxTokens: m.Limit.Output,
				// Kip listesi motorun kabul ettiğine indirgenir: models.dev
				// rahatça ["text","image","audio","video"] diyor, pi ise
				// yalnız text/image tanıyor ve TEK geçersiz değer models.json'ın
				// TAMAMINI düşürüyor. Kaynakta elemek, kullanıcının hiç
				// kullanılamayacak bir etiketi kaydetmesini de önler.
				Input: pi.GirdiKipleri(m.Modalities.Input), ReleaseDate: m.ReleaseDate,
			})
		}
		if len(s.Models) == 0 {
			continue
		}
		// En yeni önce: kullanıcı genelde son çıkanı arıyor.
		sort.Slice(s.Models, func(i, j int) bool {
			a, b := s.Models[i], s.Models[j]
			if a.ReleaseDate != b.ReleaseDate {
				return a.ReleaseDate > b.ReleaseDate
			}
			return a.ID < b.ID
		})
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

type katalogHata struct{ msg string }

func (e *katalogHata) Error() string { return e.msg }

// GET /api/models/catalog[?refresh=1]
func (a *api) modelCatalog(w http.ResponseWriter, r *http.Request) {
	veri, cekilen, err := a.katalog.al(r.Context(), r.URL.Query().Get("refresh") == "1")
	if err != nil {
		// 200 + boş liste DEĞİL: ekranın "katalog yok" ile "katalog boş"
		// ayrımını yapabilmesi lazım.
		writeErr(w, http.StatusBadGateway, "models.dev kataloğu alınamadı: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"fetched_at": cekilen,
		"providers":  veri,
	})
}
