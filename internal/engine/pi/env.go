package pi

import (
	"os"
	"sort"
	"strings"

	"github.com/esmworks/OpenAssist/internal/engine/clipath"
)

// cleanEnv — pi sürecinin ortamını SIFIRDAN kurar (Ek B.3): miras alınan
// değişkenler (NODE_OPTIONS, başka sağlayıcı anahtarları, kullanıcı kabuğunun
// artıkları) koşuya sızmaz.
//
// bin — pi ikilisinin yolu; PATH DIŞINDA bulunduysa dizini PATH'in başına
// eklenir ki pi'nin doğurduğu alt süreçler (node, ripgrep) aynı kurulumu
// bulsun.
//
// ps — TARİFLER DIŞARIDAN GELİR, burada yeniden okunmaz. models.json'ı yazan
// hazirla ile bu fonksiyon ayrı ayrı okusaydı ikisi ayrışabilirdi: dosyada
// `$OA_PI_KEY_X` referansı durur, ortamda o değişken hiç olmaz ve pi tam da
// kullanıcının gördüğü hatayı verirdi — "No API key found for the selected
// model". Aynı listeden üretmek bu sınıfı bütünüyle kapatıyor.
func (a *Adapter) cleanEnv(bin string, ps []ProviderConfig) []string {
	env := []string{
		"PATH=" + clipath.PATHIle(bin, os.Getenv("PATH")),
		"HOME=" + os.Getenv("HOME"),
		"USER=" + os.Getenv("USER"),
		"LANG=en_US.UTF-8",
		// Sunucu hijyeni: açılışta sürüm yoklaması, paket güncellemesi ve
		// telemetri yok. Kapalı ağdaki bir dağıtımda bunlar turu geciktirir.
		"PI_OFFLINE=1",
		"PI_SKIP_VERSION_CHECK=1",
		"PI_TELEMETRY=0",
	}
	if tmp := os.Getenv("TMPDIR"); tmp != "" {
		env = append(env, "TMPDIR="+tmp)
	}
	if a.ConfigDir != "" {
		env = append(env, "PI_CODING_AGENT_DIR="+a.ConfigDir)
	}
	// Sağlayıcı anahtarları: models.json'da yalnız `$AD` referansı durur,
	// değer BURADAN geçer. Ad eşlemesi tek yerde üretilir (anahtarEnvleri)
	// ki dosya ile ortam asla ayrışmasın.
	envAdlari := anahtarEnvleri(ps)
	for _, p := range ps {
		ad := envAdlari[p.ID]
		if ad == "" {
			continue
		}
		// Anahtarsız yerel sunucu da geçerli: pi bir DEĞER görmek istiyor,
		// boş bırakmak "kimlik yok" sayılırdı.
		deger := p.APIKey
		if deger == "" {
			deger = "-"
		}
		env = append(env, ad+"="+deger)
	}
	// oa-gateway eklentisinin bağlanacağı yerler. Eklenti bunu ortamdan
	// okur; argv'ye yazılsaydı belirteç `ps` çıktısında görünürdü.
	if ciftler := gatewayCiftleri(a.Gateways); ciftler != "" {
		env = append(env, "OA_GATEWAY_URLS="+ciftler)
	}
	if a.GatewayToken != "" {
		env = append(env, "OA_GATEWAY_TOKEN="+a.GatewayToken)
	}
	for _, k := range sirali(a.Env) {
		env = append(env, k+"="+a.Env[k])
	}
	return env
}

// gatewayCiftleri — yüzeyleri "ad=url,ad=url" biçimine çevirir. Adında ya da
// adresinde ayraç (virgül/eşittir) taşıyan yüzey ATLANIR: sessizce bozuk bir
// liste üretmektense o yüzey hiç görünmesin.
func gatewayCiftleri(gws []Gateway) string {
	var parcalar []string
	for _, g := range gws {
		if g.Name == "" || g.URL == "" {
			continue
		}
		if strings.ContainsAny(g.Name, ",=") || strings.Contains(g.URL, ",") {
			continue
		}
		parcalar = append(parcalar, g.Name+"="+g.URL)
	}
	return strings.Join(parcalar, ",")
}

// sirali — harita anahtarlarını sabit sırada verir: ortam listesi turdan tura
// değişmesin (testler ve log karşılaştırmaları için).
func sirali(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
