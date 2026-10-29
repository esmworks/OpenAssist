// Package config, ortam değişkenlerinden tipli ve doğrulanmış yapılandırma yükler.
//
// Sınır kuralı (docs/design/2026-08-13-faz0-tip-guvenligi.md): env bizim
// kontratımızdır, katı doğrulanır — eksik/bozuk değer açılışta hatadır,
// sessiz varsayılana düşülmez.
package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"

	"github.com/caarlos0/env/v11"
)

// apiTokenMinLen — API_TOKEN için asgari uzunluk. `openssl rand -hex 32`
// 64 karakter üretir; 32 alt sınırı "test" gibi tahmin edilebilir değerleri
// eler ama elle üretilmiş makul token'ları reddetmez.
const apiTokenMinLen = 32

type Config struct {
	// HTTPAddr — özel API + SSE yüzeyi. Dışa açılmaz (PRD: iki HTTP yüzeyi).
	HTTPAddr string `env:"HTTP_ADDR" envDefault:":8080"`
	// APIToken — özel API'nin bearer belirteci (PRD kararı).
	// ZORUNLU: token'sız açılış, bot yönetimini ve onayları korumasız
	// bırakırdı. Kurulum başına tek token (openssl rand -hex 32).
	APIToken string `env:"API_TOKEN"`
	// InternalHTTPAddr — MCP yüzeyleri (/mcp, /mcp/mac). AYRI dinleyici ve
	// yalnız loopback: motor bu makinedendir (K15), dışarıdan erişilmemeli.
	// Özel API bir alan adına bağlanabildiği için MCP onunla aynı dinleyicide
	// duramaz — orada bot yönetimi araçları var ve bearer'ı motorun MCP
	// yapılandırmasına taşımak token'ı gereksiz yere yayardı.
	InternalHTTPAddr string `env:"INTERNAL_HTTP_ADDR" envDefault:"127.0.0.1:8082"`
	// PublicHTTPAddr — yalnız webhook/OAuth yüzeyi (Faz 4'te devreye girer).
	PublicHTTPAddr string `env:"PUBLIC_HTTP_ADDR" envDefault:":8081"`
	// DatabaseURL — postgres://…; boşsa core iskelet modunda ayağa kalkar
	// (yalnız Faz 0-1 geliştirme kolaylığı; üretimde compose her zaman verir).
	DatabaseURL string `env:"DATABASE_URL"`
	// MigrateOnStart — açılışta gömülü göçleri uygula (Dokploy akışı).
	MigrateOnStart bool `env:"MIGRATE_ON_START" envDefault:"true"`
	// LogLevel — debug | info | warn | error.
	LogLevel string `env:"LOG_LEVEL" envDefault:"info"`
	// LogFormat — text | json. Sunucuda json: log toplayıcılar için
	// yapılandırılmış çıktı (Faz 9 işletme).
	LogFormat string `env:"LOG_FORMAT" envDefault:"text"`
	// Workers — eşzamanlı koşu sayısı (kuyruk paralelliği, K6).
	Workers int `env:"WORKERS" envDefault:"2"`
	// ApprovalWaitSeconds — onay kısa-bekleme penceresi; dolunca koşu park
	// edilir (K15).
	ApprovalWaitSeconds int `env:"APPROVAL_WAIT_SECONDS" envDefault:"60"`
	// MaxGroupHops — ekip kanalında üye→üye @mention zincirinin azami
	// derinliği (ADIM 10 ikinci aşama). 0 = devir kapalı: ekip sohbeti tek
	// konuşmacılı eski davranışına döner.
	MaxGroupHops int `env:"MAX_GROUP_HOPS" envDefault:"3"`
	// DataDir — çalışma alanları, olay tee dosyaları, ekler.
	DataDir string `env:"DATA_DIR" envDefault:"./data"`
	// PiCLI — pi binary yolu (konteynerde tam yol olabilir).
	//
	// BOŞ BIRAK: motor ikiliyi kendi bulur (clipath — PATH, sonra nvm/fnm/
	// volta/homebrew). Buraya "pi" gibi bir VARSAYILAN konmaz: adaptörün
	// cli() metodu dolu değeri olduğu gibi kullanır, keşif hiç çalışmaz ve
	// servis PATH'i kullanıcının nvm kurulumunu görmediğinde motor
	// "bulunamadı" olurdu. Değer verilirse keşif atlanır — bilinçli
	// sabitleme.
	PiCLI string `env:"PI_CLI"`
	// PiProviderID — pi'ye tanıtılacak özel sağlayıcının adı (ör. "oa").
	// Boşsa özel tarif yazılmaz: pi kendi yerleşik kataloğuyla çalışır ve
	// kimliği PiEnv'den okur.
	PiProviderID string `env:"PI_PROVIDER_ID"`
	// PiProviderBaseURL — sağlayıcının uç noktası. OpenAI uyumlu ya da
	// Anthropic uyumlu her adres olabilir; katalog bizim, ikilinin değil.
	PiProviderBaseURL string `env:"PI_PROVIDER_BASE_URL"`
	// PiProviderAPI — openai-completions | openai-responses |
	// anthropic-messages | google-generative-ai. Boşsa openai-completions.
	PiProviderAPI string `env:"PI_PROVIDER_API"`
	// PiProviderKey — sağlayıcı anahtarı. models.json'a DÜZ METİN yazılmaz;
	// dosyada yalnız değişken adı durur, değer sürece ortamdan geçer.
	PiProviderKey string `env:"PI_PROVIDER_KEY"`
	// PiModelsRaw — sağlayıcının modelleri: virgülle ayrılmış
	// "id[:bağlam:çıktı]" listesi (ör. "MiniMax-M3:400000:128000,glm-5").
	// Ayrıştırmayı Load() yapar.
	PiModelsRaw string `env:"PI_MODELS"`
	// PiModels — ayrıştırılmış model listesi.
	PiModels []PiModel `env:"-"`
	// PiDefaultModel — bot model seçmediğinde kullanılacak model.
	PiDefaultModel string `env:"PI_DEFAULT_MODEL"`
	// PiThinking — off|minimal|low|medium|high|xhigh|max; boşsa pi seçer.
	PiThinking string `env:"PI_THINKING"`
	// PiEnvRaw — pi sürecine geçecek ek değişkenler (ad=değer, virgül
	// ayrımlı). Özel sağlayıcı tarifi kullanılmadığında pi'nin yerleşik
	// sağlayıcı anahtarları buradan verilir.
	//
	// Doğrudan map'e BAĞLANMAZ: env kütüphanesi bozuk değerde hata metnine
	// değeri olduğu gibi koyuyor ve o hata log'a düşüyor — yani çıplak
	// yazılmış bir API anahtarı düz metin olarak sunucu log'una yazılırdı.
	// Kendimiz ayrıştırınca yalnız kaçıncı öğenin bozuk olduğunu söylüyoruz.
	PiEnvRaw string `env:"PI_ENV"`
	// PiEnv — ayrıştırılmış ek ortam değişkenleri.
	PiEnv map[string]string `env:"-"`
	// MasterKey — konektör token'larının durağan şifrelemesi için 64 hex
	// karakter (AES-256-GCM, §5). Google yapılandırılmışsa zorunludur.
	MasterKey string `env:"MASTER_KEY"`
	// GoogleClientID/Secret — Google Cloud Console OAuth istemcisi (Faz 4).
	// İkisi birlikte verilir; boşsa Google konektörü kapalıdır.
	GoogleClientID     string `env:"GOOGLE_CLIENT_ID"`
	GoogleClientSecret string `env:"GOOGLE_CLIENT_SECRET"`
	// PublicBaseURL — public yüzeyin dışarıdan görünen kökü; OAuth redirect
	// URI'si buradan türetilir (<kök>/oauth/google/callback). Console'daki
	// "Authorized redirect URIs" ile birebir aynı olmalı.
	PublicBaseURL string `env:"PUBLIC_BASE_URL" envDefault:"http://localhost:8081"`
	// TelegramBotToken — BotFather token'ı (Faz 5, K16). Boşsa Telegram
	// kanalı kapalıdır (özellik opsiyoneldir — gizlilik bedeli bilinçli).
	TelegramBotToken string `env:"TELEGRAM_BOT_TOKEN"`
	// TelegramAllowedChatIDs — izinli sohbetler; ilk açılışta allowlist'e
	// yazılır. Diğer herkese sessiz ret (K16 güvenlik).
	TelegramAllowedChatIDs []int64 `env:"TELEGRAM_ALLOWED_CHAT_IDS" envSeparator:","`
	// MemoryInferAfterRun — koşu sonrası hafıza çıkarımı (ADIM 8, K20):
	// biten her koşunun ardından ucuz bir tur transkriptten kalıcı olgu önerir.
	// Her koşuya bir motor çağrısı ekler; maliyet bilincine bağlı kapatılabilir.
	MemoryInferAfterRun bool `env:"MEMORY_INFER_AFTER_RUN" envDefault:"true"`
}

// TelegramEnabled — Telegram kanalı yapılandırılmış mı.
func (c Config) TelegramEnabled() bool { return c.TelegramBotToken != "" }

// GoogleEnabled — Google konektörü yapılandırılmış mı.
func (c Config) GoogleEnabled() bool { return c.GoogleClientID != "" }

func Load() (Config, error) {
	var c Config
	if err := env.Parse(&c); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	// Sondaki "/" budanır: değer redirect URI'sinin KÖKÜ olarak birleştirilir
	// ve "…com/" + "/oauth/…" çift eğik çizgi üretirdi. Google redirect_uri'yi
	// harfi harfine eşleştirir; konsolda kayıtlı olandan farkı reddedilir.
	c.PublicBaseURL = strings.TrimRight(c.PublicBaseURL, "/")
	keys, err := anahtarCiftleri(c.PiEnvRaw)
	if err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	c.PiEnv = keys
	models, err := piModelleri(c.PiModelsRaw)
	if err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	c.PiModels = models
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	return c, nil
}

// anahtarCiftleri — "ad=değer,ad=değer" listesini map'e çevirir.
//
// HATA MESAJINDA DEĞER GEÇMEZ: burada taşınan şey API anahtarı ve bu hata
// açılışta log'a yazılıyor. Bozuk öğenin sırasını ve varsa adını söylemek
// düzeltmek için yeter; anahtarın kendisini log'a düşürmek ise onu yakan
// bir sızıntı olurdu.
func anahtarCiftleri(ham string) (map[string]string, error) {
	ham = strings.TrimSpace(ham)
	if ham == "" {
		return nil, nil
	}
	out := map[string]string{}
	for i, parca := range strings.Split(ham, ",") {
		parca = strings.TrimSpace(parca)
		if parca == "" {
			continue
		}
		ad, deger, tamam := strings.Cut(parca, "=")
		ad = strings.TrimSpace(ad)
		if !tamam || ad == "" || strings.TrimSpace(deger) == "" {
			return nil, fmt.Errorf("PI_ENV %d. öğe %q biçiminde değil "+
				"(değer güvenlik için yazılmadı) — beklenen: virgülle ayrık "+
				"ad=değer çiftleri, ör. ANTHROPIC_API_KEY=sk-…,OPENAI_API_KEY=sk-…; "+
				"ek değişken gerekmiyorsa değişkeni tamamen sil", i+1, "ad=değer")
		}
		out[ad] = strings.TrimSpace(deger)
	}
	return out, nil
}

// PiModel — PI_MODELS listesinin tek öğesi.
type PiModel struct {
	ID            string
	ContextWindow int
	MaxTokens     int
}

// piModelleri — "id[:bağlam[:çıktı]]" listesini ayrıştırır.
//
// Model adları sır değildir; bozuk öğede adı yazmak düzeltmeyi kolaylaştırır.
// Sayı alanları isteğe bağlıdır: verilmezse pi kendi varsayılanını kullanır.
func piModelleri(ham string) ([]PiModel, error) {
	ham = strings.TrimSpace(ham)
	if ham == "" {
		return nil, nil
	}
	var out []PiModel
	for i, parca := range strings.Split(ham, ",") {
		parca = strings.TrimSpace(parca)
		if parca == "" {
			continue
		}
		alan := strings.Split(parca, ":")
		m := PiModel{ID: strings.TrimSpace(alan[0])}
		if m.ID == "" {
			return nil, fmt.Errorf("PI_MODELS %d. öğe boş model kimliği taşıyor", i+1)
		}
		if len(alan) > 1 {
			n, err := pozitifSayi(alan[1])
			if err != nil {
				return nil, fmt.Errorf("PI_MODELS %d. öğe (%q): bağlam penceresi %w", i+1, m.ID, err)
			}
			m.ContextWindow = n
		}
		if len(alan) > 2 {
			n, err := pozitifSayi(alan[2])
			if err != nil {
				return nil, fmt.Errorf("PI_MODELS %d. öğe (%q): çıktı sınırı %w", i+1, m.ID, err)
			}
			m.MaxTokens = n
		}
		out = append(out, m)
	}
	return out, nil
}

// pozitifSayi — boş alan "belirtilmedi" demektir (0 döner, pi kendi
// varsayılanını kullanır); dolu ama geçersiz alan hatadır.
func pozitifSayi(ham string) (int, error) {
	ham = strings.TrimSpace(ham)
	if ham == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(ham)
	if err != nil || n <= 0 {
		return 0, errors.New("pozitif tamsayı değil")
	}
	return n, nil
}

func (c Config) Validate() error {
	if c.HTTPAddr == "" {
		return errors.New("HTTP_ADDR boş olamaz")
	}
	if c.PublicHTTPAddr == "" {
		return errors.New("PUBLIC_HTTP_ADDR boş olamaz")
	}
	if c.InternalHTTPAddr == "" {
		return errors.New("INTERNAL_HTTP_ADDR boş olamaz")
	}
	// Token ZORUNLU ve kısa olamaz. Boş bırakılabilseydi özel API korumasız
	// açılırdı; "unuttum" ile "kapattım" ayırt edilemez, o yüzden fail-closed.
	if len(c.APIToken) < apiTokenMinLen {
		return fmt.Errorf("API_TOKEN en az %d karakter olmalı — özel API'yi "+
			"(bot yönetimi, onaylar, runner WS) bu belirteç korur; "+
			"üret: openssl rand -hex 32", apiTokenMinLen)
	}
	if _, err := c.SlogLevel(); err != nil {
		return err
	}
	if c.LogFormat != "text" && c.LogFormat != "json" {
		return fmt.Errorf("LOG_FORMAT text|json olmalı, geldi: %q", c.LogFormat)
	}
	if c.Workers < 1 || c.Workers > 32 {
		return fmt.Errorf("WORKERS 1-32 aralığında olmalı, geldi: %d", c.Workers)
	}
	if c.ApprovalWaitSeconds < 5 || c.ApprovalWaitSeconds > 3600 {
		return fmt.Errorf("APPROVAL_WAIT_SECONDS 5-3600 aralığında olmalı, geldi: %d", c.ApprovalWaitSeconds)
	}
	// Üst sınır sert: her devir gerçek bir LLM turudur ve zincir dallanabilir
	// (bir cevap birden çok üye çağırabilir). 8 devirlik bir zincir tek
	// mesajda onlarca tur demektir.
	if c.MaxGroupHops < 0 || c.MaxGroupHops > 8 {
		return fmt.Errorf("MAX_GROUP_HOPS 0-8 aralığında olmalı, geldi: %d", c.MaxGroupHops)
	}
	// Sağlayıcı tarifi ya EKSİKSİZ verilir ya hiç verilmez. Yarım tarif
	// açılışta sessizce geçer, sonra HER koşuyu düşürür: model kataloğu boş
	// bir sağlayıcıda pi seçilebilir model bulamaz ve yoklama da "anahtar
	// tanımlı mı?" diye yanlış yeri gösterir.
	if c.PiProviderID != "" || c.PiProviderBaseURL != "" {
		if c.PiProviderID == "" || c.PiProviderBaseURL == "" {
			return errors.New("PI_PROVIDER_ID ve PI_PROVIDER_BASE_URL birlikte verilmeli")
		}
		if len(c.PiModels) == 0 {
			return errors.New("PI_MODELS zorunlu: sağlayıcı tarifi verildiğinde model " +
				"kataloğunun sahibi biziz (pi ikilinin gömülü listesine bakmaz) — " +
				"ör. PI_MODELS=MiniMax-M3:400000:128000")
		}
	}
	if c.MasterKey != "" {
		if _, err := hex.DecodeString(c.MasterKey); err != nil || len(c.MasterKey) != 64 {
			return fmt.Errorf("MASTER_KEY 64 hex karakter olmalı (openssl rand -hex 32)")
		}
	}
	if (c.GoogleClientID == "") != (c.GoogleClientSecret == "") {
		return errors.New("GOOGLE_CLIENT_ID ve GOOGLE_CLIENT_SECRET birlikte verilmeli")
	}
	if c.GoogleEnabled() && c.MasterKey == "" {
		return errors.New("MASTER_KEY zorunlu: Google konektörü token'ları mühürleyerek saklar (openssl rand -hex 32)")
	}
	if c.PublicBaseURL == "" {
		return errors.New("PUBLIC_BASE_URL boş olamaz")
	}
	// ŞEMA ŞART. Değer OAuth redirect URI'sinin kökü olarak birleştirilir
	// (PublicBaseURL + "/oauth/google/callback"). "asistan.example.com" gibi
	// şemasız bir değer boş-değil kontrolünü geçer ama Google'a geçersiz bir
	// redirect_uri gider; hata aylar sonra, konektör bağlanırken ve
	// yapılandırmadan çok uzakta patlar. Açılışta söylensin.
	if u, err := url.Parse(c.PublicBaseURL); err != nil ||
		(u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("PUBLIC_BASE_URL http:// ya da https:// ile tam URL olmalı "+
			"(ör. https://asistan.example.com), geldi: %q", c.PublicBaseURL)
	}
	if c.TelegramEnabled() && len(c.TelegramAllowedChatIDs) == 0 {
		// Allowlist'siz bot herkese açık olurdu — fail-closed (K16).
		return errors.New("TELEGRAM_ALLOWED_CHAT_IDS zorunlu: izinli chat_id verilmeden Telegram açılmaz")
	}
	return nil
}

// SlogLevel, LogLevel'ı tipli karşılığına çevirir; bilinmeyen değer hatadır.
func (c Config) SlogLevel() (slog.Level, error) {
	switch c.LogLevel {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("LOG_LEVEL geçersiz: %q (debug|info|warn|error)", c.LogLevel)
	}
}
