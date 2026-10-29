package pi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `pi list` çıktısının ayrıştırılması — 0.84.2'de ölçülen biçim. Yanlış
// ayrıştırma, allowlist'e var olmayan bir eklenti sunmak demek olurdu.
func TestPaketleriAyristir(t *testing.T) {
	cikti := `User packages:
  npm:pi-subagents@0.50.0
    /veri/engines/pi/npm/node_modules/pi-subagents
  ../yerel-paket
    /veri/yerel-paket
Project packages:
  npm:proje-paketi@1.0.0
    /repo/.pi/npm/node_modules/proje-paketi
`
	got := paketleriAyristir(cikti)
	if len(got) != 2 {
		t.Fatalf("iki kullanıcı paketi bekleniyordu, geldi %d: %+v", len(got), got)
	}
	if got[0].Source != "npm:pi-subagents@0.50.0" ||
		got[0].Path != "/veri/engines/pi/npm/node_modules/pi-subagents" {
		t.Errorf("ilk kayıt yanlış: %+v", got[0])
	}
	for _, p := range got {
		if strings.Contains(p.Path, "/repo/") {
			t.Errorf("proje paketi listeye girdi: %+v — koşuda --no-approve ile yok sayılıyor", p)
		}
	}
}

// Yolu çözülmemiş kayıt ELENİR: -e ile verilecek bir şey yok, listede
// görünseydi bota takılır ve koşu düşerdi.
func TestPaketleriAyristirYolsuzKaydiEler(t *testing.T) {
	got := paketleriAyristir("User packages:\n  npm:yarim@1.0.0\n")
	if len(got) != 0 {
		t.Errorf("yolsuz kayıt elenmedi: %+v", got)
	}
}

// Tanınmayan çıktı (pi biçim değiştirirse, ya da hiç paket yoksa) BOŞ liste
// verir; rastgele satırlar paket sanılmaz.
func TestPaketleriAyristirTanimayanCikti(t *testing.T) {
	for _, cikti := range []string{"", "No packages installed.\n", "Merhaba dünya\n"} {
		if got := paketleriAyristir(cikti); len(got) != 0 {
			t.Errorf("%q → %+v; boş olmalı", cikti, got)
		}
	}
}

// Allowlist kimliği SÜRÜMSÜZDÜR: paketi güncelleyen kullanıcı tüm botların
// koşusunu düşürmemeli.
func TestPaketKimligiSurumuAtar(t *testing.T) {
	testler := map[string]string{
		"npm:pi-subagents@0.50.0":   "npm:pi-subagents",
		"npm:pi-subagents":          "npm:pi-subagents",
		"npm:@kapsam/paket@1.2.3":   "npm:@kapsam/paket",
		"npm:@kapsam/paket":         "npm:@kapsam/paket",
		"npm:paket@beta-2026.01.01": "npm:paket",
	}
	for kaynak, istenen := range testler {
		if got := paketKimligi(kaynak); got != istenen {
			t.Errorf("%s → %s; beklenen %s", kaynak, got, istenen)
		}
	}
}

// Kaynak deseni: kabuk metakarakteri ve yol gezintisi geçmemeli — bu dize
// argv'ye gidiyor ve kullanıcıdan geliyor.
func TestKurulumKaynagiDogrulanir(t *testing.T) {
	a := &Adapter{ConfigDir: t.TempDir()}
	gecerli := []string{"npm:pi-subagents", "npm:pi-subagents@0.50.0", "npm:@kapsam/paket@1.0.0"}
	for _, s := range gecerli {
		if !npmKaynakDeseni.MatchString(s) {
			t.Errorf("geçerli kaynak reddedildi: %q", s)
		}
	}
	gecersiz := []string{
		"npm:paket; rm -rf /", "npm:../../etc/passwd", "git:github.com/biri/depo",
		"https://ornek.com/paket.tgz", "/etc/passwd", "npm:paket ekstra", "",
		"npm:paket$(whoami)", "npm:paket\npi install baska",
	}
	for _, s := range gecersiz {
		if npmKaynakDeseni.MatchString(s) {
			t.Errorf("geçersiz kaynak desene uydu: %q", s)
		}
		if err := a.InstallExtension(context.Background(), s); err == nil {
			t.Errorf("geçersiz kaynak kabul edildi: %q", s)
		}
	}
}

// ConfigDir yokken paket yönetimi KAPALI: sunucu, üstünde koştuğu kullanıcının
// kişisel ~/.pi kurulumunu yönetmeye kalkmamalı.
func TestConfigDirYokkenPaketYonetimiKapali(t *testing.T) {
	a := &Adapter{}
	if err := a.InstallExtension(context.Background(), "npm:pi-subagents@0.50.0"); err == nil {
		t.Error("ConfigDir yokken kurulum kabul edildi")
	}
	if err := a.RemoveExtension(context.Background(), "npm:pi-subagents"); err == nil {
		t.Error("ConfigDir yokken kaldırma kabul edildi")
	}
	list, err := a.paketler(context.Background())
	if err != nil || len(list) != 0 {
		t.Errorf("ConfigDir yokken liste=%v hata=%v; boş ve hatasız olmalı", list, err)
	}
}

// Paket olmayan bir ad kaldırılamaz: gömülü eklenti ikilinin parçası.
func TestGomuluEklentiKaldirilamaz(t *testing.T) {
	a := &Adapter{ConfigDir: t.TempDir()}
	if err := a.RemoveExtension(context.Background(), OAGateway); err == nil {
		t.Error("gömülü eklenti kaldırılabildi — onay kapısı orada")
	}
}

// package.json'dan ad/sürüm/açıklama okunur; okunamazsa kayıt YİNE görünür
// (yoksa kullanıcı kurduğu paketi kaldıramazdı).
func TestPaketBilgisiManifesttenOkur(t *testing.T) {
	dizin := t.TempDir()
	m := map[string]string{
		"name": "pi-subagents", "version": "0.50.0",
		"description": "Tek ajan devri ve betikli çoklu ajan akışı",
	}
	veri, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dizin, "package.json"), veri, 0o600); err != nil {
		t.Fatal(err)
	}

	got := paketBilgisi(Paket{Source: "npm:pi-subagents@0.50.0", Path: dizin})
	if got.ID != "npm:pi-subagents" {
		t.Errorf("ID=%q; sürümsüz kimlik olmalı", got.ID)
	}
	if got.Name != "pi-subagents" || got.Version != "0.50.0" || got.Description == "" {
		t.Errorf("manifest okunmadı: %+v", got)
	}
	if got.Kind != "npm" {
		t.Errorf("kind=%q, npm olmalı", got.Kind)
	}

	bos := paketBilgisi(Paket{Source: "npm:hayalet@1.0.0", Path: filepath.Join(dizin, "yok")})
	if bos.ID != "npm:hayalet" || bos.Kind != "npm" {
		t.Errorf("manifestsiz paket kayboldu: %+v", bos)
	}
}

// Kurulum ağa çıkmak zorunda: koşu ortamının çevrimdışı bayrağı düşmeli.
func TestAgliOrtamOfflineBayraginiDusurur(t *testing.T) {
	got := agliOrtam([]string{"PATH=/bin", "PI_OFFLINE=1", "HOME=/kok", "PI_TELEMETRY=0"})
	for _, e := range got {
		if strings.HasPrefix(e, "PI_OFFLINE=") {
			t.Fatalf("PI_OFFLINE kaldı: %v", got)
		}
	}
	if len(got) != 3 {
		t.Errorf("başka değişkenler de düştü: %v", got)
	}
}

// Allowlist'teki "npm:" girdisi kurulu paket yoksa TUR DÜŞÜRÜR: sessizce
// atlamak, botun beklediği aracı yok saymak olurdu.
func TestPaketYoluKuruluDegilseHataVerir(t *testing.T) {
	a := &Adapter{ConfigDir: t.TempDir(), ExtensionsDir: t.TempDir()}
	_, err := a.eklentiYolu(context.Background(), "npm:hayalet")
	if err == nil {
		t.Fatal("kurulu olmayan paket için hata beklenirdi")
	}
	if !strings.Contains(err.Error(), "npm:hayalet") {
		t.Errorf("hata paketi söylemiyor: %v", err)
	}
}
