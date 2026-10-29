package pi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/esmworks/OpenAssist/internal/engine"
)

// Pi paketleri — npm'e yayınlanmış eklenti/beceri demetleri (pi.dev/packages).
//
// ÜÇ KURAL, üçü de güvenlik gerekçeli:
//
//  1. Kurmak SUNUCUDA KOD İNDİRİR. `pi install` npm'i çağırır, npm de paketin
//     kurulum betiklerini çalıştırır (kataloğun popüler paketlerinden birinin
//     `postinstall`u var). Bu yüzden kaynak dizesi katı bir desenden geçer ve
//     yalnız `npm:` kabul edilir: git/https kaynağı keyfi bir depoyu çalıştırmak
//     olurdu ve tel üstünde doğrulanabilir hiçbir kimlik taşımaz.
//  2. Kurulan paket HİÇBİR BOTA kendiliğinden bulaşmaz. Koşu keşif KAPALI
//     açılır (--no-extensions, 0.84.2'de ölçüldü: settings.json'daki paketler
//     bu bayrakla yüklenmiyor); paketi kullanmak için botun allowlist'inde
//     açıkça yazması gerekir.
//  3. Kimlik SÜRÜMSÜZDÜR ("npm:pi-subagents"). Allowlist'e sürümlü ad yazsaydık
//     paketi güncelleyen kullanıcı, güncellediği anda tüm botların koşusunu
//     düşürürdü — pi'nin kendi tekilleştirme kimliği de paket adıdır.
const (
	// paketTTL — `pi list` bir Node süreci açar; Mac 15 sn'de bir yavaş tur
	// atıyor, her turda süreç açmak israf olurdu.
	paketTTL = time.Minute
	// paketListeTimeout — yerel bir ayar okuması; uzarsa bir şey tıkanmıştır.
	paketListeTimeout = 30 * time.Second
	// kurulumTimeout — npm ağdan indirir ve bağımlılık kurar; dakikalar sürebilir.
	kurulumTimeout = 5 * time.Minute
)

var (
	_ engine.Extender         = (*Adapter)(nil)
	_ engine.ExtensionManager = (*Adapter)(nil)
)

// npmKaynakDeseni — "npm:paket", "npm:paket@1.2.3", "npm:@kapsam/paket@1.2.3".
// Kabuk metakarakteri, boşluk ve yol gezintisi geçmez: kaynak dizesi argv'ye
// gidiyor ve kullanıcıdan geliyor.
var npmKaynakDeseni = regexp.MustCompile(`^npm:(@[a-z0-9][a-z0-9._-]*/)?[a-z0-9][a-z0-9._-]*(@[a-zA-Z0-9][a-zA-Z0-9._-]*)?$`)

// Paket — settings.json'da kayıtlı bir paket ve diskteki çözülmüş yeri.
type Paket struct {
	Source string // "npm:pi-subagents@0.50.0"
	Path   string // "/veri/engines/pi/npm/node_modules/pi-subagents"
}

type paketOnbellek struct {
	mu    sync.Mutex
	kayit []Paket
	at    time.Time
	// tekSefer — eşzamanlı istekler tek süreç açsın.
	tekSefer sync.Mutex
}

func (c *paketOnbellek) temizle() {
	c.mu.Lock()
	c.kayit, c.at = nil, time.Time{}
	c.mu.Unlock()
}

// paketler — kurulu paketler. Liste pi'nin KENDİSİNDEN alınır (`pi list`),
// dizin düzeni tahmin edilmez: npm paketlerinin nereye indiği pi'nin işidir ve
// sürümle değişebilir — tahmin etseydik bir gün sessizce yanlış yola bakardık.
func (a *Adapter) paketler(ctx context.Context) ([]Paket, error) {
	// ConfigDir yoksa paket yönetimi KAPALIDIR. Aksi halde pi kendi
	// varsayılanına (~/.pi/agent) düşer ve sunucu, üstünde koştuğu kullanıcının
	// KİŞİSEL pi kurulumunu listeler, kurar, kaldırırdı. Sunucu bu dizini her
	// zaman verir; boş olduğu tek yer testler ve iskelet kurulum.
	if a.ConfigDir == "" {
		return nil, nil
	}
	c := &a.paketOnbellegi
	c.mu.Lock()
	kayit, at := c.kayit, c.at
	c.mu.Unlock()
	if kayit != nil && time.Since(at) < paketTTL {
		return kayit, nil
	}

	c.tekSefer.Lock()
	defer c.tekSefer.Unlock()
	c.mu.Lock()
	kayit, at = c.kayit, c.at
	c.mu.Unlock()
	if kayit != nil && time.Since(at) < paketTTL {
		return kayit, nil
	}

	out, err := a.paketKomutu(ctx, paketListeTimeout, "list")
	if err != nil {
		return nil, err
	}
	yeni := paketleriAyristir(out)
	c.mu.Lock()
	c.kayit, c.at = yeni, time.Now()
	c.mu.Unlock()
	return yeni, nil
}

// InstallExtension — paketi sunucuya kurar. Kurulum turu ETKİLEMEZ: hiçbir bot
// bu paketi allowlist'ine yazmadıkça yüklenmez.
func (a *Adapter) InstallExtension(ctx context.Context, source string) error {
	if a.ConfigDir == "" {
		return errors.New("pi yapılandırma dizini ayarlı değil — paket kurulumu kapalı")
	}
	source = strings.TrimSpace(source)
	if !npmKaynakDeseni.MatchString(source) {
		return fmt.Errorf("geçersiz paket kaynağı %q — yalnız npm kabul ediliyor (örn. npm:pi-subagents@0.50.0)", source)
	}
	slog.Warn("pi paketi kuruluyor — sunucuda üçüncü parti kod çalışacak", "kaynak", source)
	if _, err := a.paketKomutu(ctx, kurulumTimeout, "install", source); err != nil {
		return err
	}
	a.paketOnbellegi.temizle()
	return nil
}

// RemoveExtension — paketi settings'ten düşürür. Diskteki dosyaları pi'nin
// kendi `remove`u ne yapıyorsa o olur; biz dosya silmeyiz.
//
// ad, katalogdaki kimliktir ("npm:pi-subagents"): settings'te sürümlü kayıtlı
// olabilir, bu yüzden gerçek kaynak dizesi listeden bulunur.
func (a *Adapter) RemoveExtension(ctx context.Context, ad string) error {
	if a.ConfigDir == "" {
		return errors.New("pi yapılandırma dizini ayarlı değil — paket yönetimi kapalı")
	}
	if !strings.HasPrefix(ad, "npm:") {
		return fmt.Errorf("%q bir paket değil — yalnız kurulan paketler kaldırılabilir", ad)
	}
	list, err := a.paketler(ctx)
	if err != nil {
		return err
	}
	for _, p := range list {
		if paketKimligi(p.Source) != ad {
			continue
		}
		if _, err := a.paketKomutu(ctx, paketListeTimeout, "remove", p.Source); err != nil {
			return err
		}
		a.paketOnbellegi.temizle()
		return nil
	}
	return fmt.Errorf("paket kurulu değil: %s", ad)
}

// paketKomutu — pi'nin paket alt komutlarını koşturur.
//
// Ortam koşununkiyle AYNI kurulur (cleanEnv): paketler koşunun göreceği
// PI_CODING_AGENT_DIR'e inmeli, yoksa kurduğumuz paketi motor bulamazdı.
// Fark tek: PI_OFFLINE düşer — kurulum ağa çıkmak zorunda.
func (a *Adapter) paketKomutu(ctx context.Context, sure time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, sure)
	defer cancel()

	bin := a.cli()
	cmd := exec.CommandContext(ctx, bin, append(args, "--no-approve")...)
	cmd.Env = agliOrtam(a.cleanEnv(bin, nil))
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	if err := cmd.Run(); err != nil {
		// pi'nin kendi cümlesi bizimkinden iyidir ("Error: Path does not
		// exist", "404 Not Found") — kullanıcıya onu gösteriyoruz.
		sebep := strings.TrimSpace(errBuf.String())
		if sebep == "" {
			sebep = strings.TrimSpace(out.String())
		}
		if sebep == "" {
			sebep = err.Error()
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			sebep = "süre doldu (" + sure.String() + ") — " + sebep
		}
		return "", fmt.Errorf("pi %s: %s", strings.Join(args, " "), kuyruk(sebep, 500))
	}
	return out.String(), nil
}

// agliOrtam — PI_OFFLINE'ı düşürür. Koşu ortamı bilinçli olarak çevrimdışıdır
// (açılışta sürüm yoklaması/paket güncellemesi turu geciktirmesin); kurulum ise
// tam da ağ işidir.
func agliOrtam(env []string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, "PI_OFFLINE=") {
			continue
		}
		out = append(out, e)
	}
	return out
}

// paketleriAyristir — `pi list` çıktısını okur. Biçim (0.84.2):
//
//	User packages:
//	  npm:pi-subagents@0.50.0
//	    /veri/engines/pi/npm/node_modules/pi-subagents
//
// YALNIZ "User packages" bölümü okunur: proje-yerel paketler (.pi/settings.json)
// bizim yönetimimizde değil ve koşuda zaten yok sayılıyor (--no-approve).
// Tanınmayan bir çıktı biçiminde liste BOŞ döner — yanlış ayrıştırılmış bir
// satırı paket sanıp allowlist'e sunmaktansa hiç göstermemek doğru.
func paketleriAyristir(out string) []Paket {
	var list []Paket
	kullanici := false
	// GİRİNTİ ANLAM TAŞIYOR (kaynak mı yol mu), o yüzden ortak satirlar()
	// yardımcısı kullanılmaz: o her satırı kırpıyor.
	for _, satir := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(satir) == "" {
			continue
		}
		if !strings.HasPrefix(satir, " ") {
			// Bölüm başlığı: "User packages:" / "Project packages:"
			kullanici = strings.HasPrefix(strings.ToLower(strings.TrimSpace(satir)), "user packages")
			continue
		}
		if !kullanici {
			continue
		}
		girinti := len(satir) - len(strings.TrimLeft(satir, " "))
		deger := strings.TrimSpace(satir)
		if deger == "" {
			continue
		}
		switch {
		case girinti <= 2:
			list = append(list, Paket{Source: deger})
		case len(list) > 0 && list[len(list)-1].Path == "":
			list[len(list)-1].Path = deger
		}
	}
	// Yolu çözülememiş kayıt kullanılamaz: -e ile verilecek bir şey yok.
	out2 := list[:0]
	for _, p := range list {
		if p.Path != "" {
			out2 = append(out2, p)
		}
	}
	return out2
}

// paketKimligi — allowlist'e yazılan sürümSÜZ ad: "npm:@kapsam/x@1.2.3" →
// "npm:@kapsam/x". Kapsam öneki de '@' ile başladığı için sürüm SON '@'ten
// kesilir; baştaki '@' korunur.
func paketKimligi(source string) string {
	s := strings.TrimPrefix(source, "npm:")
	if i := strings.LastIndex(s, "@"); i > 0 {
		s = s[:i]
	}
	return "npm:" + s
}

// paketYolu — allowlist'teki "npm:<paket>" girdisini diskteki dizine çevirir.
// Kurulu değilse TUR DÜŞER: eksik eklentiyi atlamak, botun beklediği aracı
// sessizce yok saymak olurdu.
func (a *Adapter) paketYolu(ctx context.Context, ad string) (string, error) {
	list, err := a.paketler(ctx)
	if err != nil {
		return "", fmt.Errorf("pi paketi %q çözülemedi: %w", ad, err)
	}
	for _, p := range list {
		if paketKimligi(p.Source) == ad {
			if _, err := os.Stat(p.Path); err != nil {
				return "", fmt.Errorf("pi paketi %q kayıtlı ama diski boş (%s)", ad, p.Path)
			}
			return p.Path, nil
		}
	}
	return "", fmt.Errorf("pi paketi %q kurulu değil — Eklentiler ekranından kur", ad)
}

// paketManifesti — paketin package.json'ı; ad/sürüm/açıklama buradan gelir.
// `pi` anahtarı paketin hangi kaynakları taşıdığını bildirir; yoksa pi
// gelenek dizinlerine bakar (docs/packages.md).
type paketManifesti struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	Pi          *struct {
		Extensions []string `json:"extensions"`
		Skills     []string `json:"skills"`
		Prompts    []string `json:"prompts"`
		Themes     []string `json:"themes"`
	} `json:"pi"`
}

// paketKaynaklari — paketin NE taşıdığı. Kullanıcı bunu görmeli: "eklenti
// kuruyorum" sanıp modele talimat veren bir beceri demeti açmak başka bir şey.
//
// Manifest varsa o esastır (glob'lar açılmaz — varlık sorusuna cevap yeter),
// yoksa gelenek dizinlerine bakılır.
func paketKaynaklari(kok string, m paketManifesti) []string {
	var out []string
	ekle := func(tur string, varsa bool) {
		if varsa {
			out = append(out, tur)
		}
	}
	if m.Pi != nil {
		ekle("extension", len(m.Pi.Extensions) > 0)
		ekle("skill", len(m.Pi.Skills) > 0)
		ekle("prompt", len(m.Pi.Prompts) > 0)
		ekle("theme", len(m.Pi.Themes) > 0)
		return out
	}
	for _, t := range []struct{ tur, dizin string }{
		{"extension", "extensions"},
		{"skill", "skills"},
		{"prompt", "prompts"},
		{"theme", "themes"},
	} {
		bilgi, err := os.Stat(filepath.Join(kok, t.dizin))
		ekle(t.tur, err == nil && bilgi.IsDir())
	}
	return out
}

// paketBilgisi — kurulu paketi katalog satırına çevirir. package.json
// okunamazsa kayıt YİNE görünür (kaynak dizesiyle): görünmeseydi kullanıcı
// kurduğu paketi kaldıramazdı.
func paketBilgisi(p Paket) engine.ExtensionInfo {
	bilgi := engine.ExtensionInfo{
		ID:   paketKimligi(p.Source),
		Name: strings.TrimPrefix(paketKimligi(p.Source), "npm:"),
		Kind: "npm",
	}
	veri, err := os.ReadFile(filepath.Join(p.Path, "package.json"))
	if err != nil {
		return bilgi
	}
	var m paketManifesti
	if err := json.Unmarshal(veri, &m); err != nil {
		return bilgi
	}
	if m.Name != "" {
		bilgi.Name = m.Name
	}
	bilgi.Version = m.Version
	bilgi.Description = m.Description
	bilgi.Resources = paketKaynaklari(p.Path, m)
	return bilgi
}
