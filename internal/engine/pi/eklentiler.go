package pi

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/esmworks/OpenAssist/internal/engine"
)

// eklentiUzantilari — eklenti dizininde eklenti SAYILAN dosyalar. pi bir TS/JS
// modülü yükler; dizine düşen README, .json ya da yedek dosyası eklenti değildir
// ve listede görünürse kullanıcı onu bota takmaya çalışır, koşu düşerdi.
var eklentiUzantilari = []string{".ts", ".js", ".mjs", ".tsx"}

// beceriOneki — beceri girdilerinin allowlist kimliği ("skill:ad"). Önek şart:
// beceriler pi'ye --skill ile, eklentiler -e ile veriliyor ve iki dizinde aynı
// ad bulunabilir.
const beceriOneki = "skill:"

// beceriDizini — veri dizinindeki beceri kökü. pi'nin kendi keşfi bu dizini de
// tarardı; biz keşfi kapattık (bkz. buildArgs) ve buradakileri KATALOĞA koyup
// bot başına seçilebilir yapıyoruz: eskiden buraya düşen her beceri her bota
// yükleniyordu.
const beceriDizini = "skills"

// Extensions — bu motorun yükleyebileceği eklentiler: ikiliye GÖMÜLÜ olanlar,
// eklenti dizinine sonradan düşenler ve kurulu pi paketleri.
//
// Gömülüler diskten DEĞİL embed FS'ten okunur: dizine ilk koşuda yazıldıkları
// için, hiç koşu yapılmamış taze bir kurulumda disk boştur ve ekran "hiç
// eklenti yok" derdi — oysa onay kapısı orada duruyor.
//
// Paket listesi alınamazsa (pi kurulu değil, `pi list` düştü) hata DÖNMEZ:
// gömülü eklentiler hâlâ doğru ve kullanışlı, üstelik onay kapısı da onların
// içinde — tüm listeyi bir alt komut yüzünden kaybetmek yanlış olurdu.
func (a *Adapter) Extensions(ctx context.Context) ([]engine.ExtensionInfo, error) {
	bulunan := map[string]engine.ExtensionInfo{}

	girdiler, err := fs.ReadDir(gomuluEklentiler, "extensions")
	if err != nil {
		return nil, err
	}
	for _, g := range girdiler {
		if g.IsDir() {
			continue
		}
		veri, err := gomuluEklentiler.ReadFile("extensions/" + g.Name())
		if err != nil {
			return nil, err
		}
		bulunan[g.Name()] = eklentiBilgisi(g.Name(), veri, "builtin")
	}

	if a.ExtensionsDir != "" {
		diskte, err := os.ReadDir(a.ExtensionsDir)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("eklenti dizini okunamadı (%s): %w", a.ExtensionsDir, err)
		}
		for _, g := range diskte {
			ad := g.Name()
			// Gömülünün diskteki kopyası AYRI bir eklenti değil: kaynağı
			// ikilidir, "file" diye ikinci kez listelenirse kullanıcı
			// kaldırılabilir sanır ve her koşuda geri gelmesine şaşardı.
			if _, gomulu := bulunan[ad]; gomulu {
				continue
			}
			if g.IsDir() || !eklentiDosyasi(ad) {
				continue
			}
			veri, err := os.ReadFile(filepath.Join(a.ExtensionsDir, ad))
			if err != nil {
				// Tek okunamayan dosya listeyi düşürmez: geri kalan
				// eklentiler hâlâ doğru ve kullanışlı.
				continue
			}
			bulunan[ad] = eklentiBilgisi(ad, veri, "file")
		}
	}

	out := make([]engine.ExtensionInfo, 0, len(bulunan))
	for _, e := range bulunan {
		out = append(out, e)
	}

	if list, err := a.paketler(ctx); err != nil {
		slog.Warn("pi paket listesi alınamadı — katalogda yalnız yerel eklentiler görünecek", "err", err)
	} else {
		for _, p := range list {
			out = append(out, paketBilgisi(p))
		}
	}
	out = append(out, a.beceriler()...)

	// Gömülüler başta: onay kapısı listenin dibinde aranmasın.
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Kind == "builtin") != (out[j].Kind == "builtin") {
			return out[i].Kind == "builtin"
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// beceriler — veri dizinindeki (<ConfigDir>/skills) beceriler. pi'nin keşif
// kuralı burada bilerek DARALTILDI: yalnız SKILL.md taşıyan üst düzey dizinler
// ve doğrudan kökteki .md dosyaları sayılır — özyinelemeli tarama yapmıyoruz,
// çünkü katalog kimliği (skill:<ad>) tek düzeyde okunur olmalı.
//
// Ad ve açıklama SKILL.md'nin frontmatter'ından okunur: modele gösterilen
// açıklama neyse kullanıcıya da o gösterilsin, ayrı bir metin uydurmayalım.
func (a *Adapter) beceriler() []engine.ExtensionInfo {
	if a.ConfigDir == "" {
		return nil
	}
	kok := filepath.Join(a.ConfigDir, beceriDizini)
	girdiler, err := os.ReadDir(kok)
	if err != nil {
		return nil // dizin yoksa beceri de yoktur
	}

	var out []engine.ExtensionInfo
	for _, g := range girdiler {
		ad := g.Name()
		var kaynak, yol string
		switch {
		case g.IsDir():
			yol = filepath.Join(kok, ad, "SKILL.md")
			if _, err := os.Stat(yol); err != nil {
				continue
			}
			kaynak = ad
		case strings.EqualFold(filepath.Ext(ad), ".md"):
			yol = filepath.Join(kok, ad)
			kaynak = strings.TrimSuffix(ad, filepath.Ext(ad))
		default:
			continue
		}

		bilgi := engine.ExtensionInfo{
			ID:        beceriOneki + kaynak,
			Name:      kaynak,
			Kind:      "skill",
			Resources: []string{"skill"},
		}
		if veri, err := os.ReadFile(yol); err == nil {
			if adi, aciklama := beceriBasligi(string(veri)); adi != "" || aciklama != "" {
				if adi != "" {
					bilgi.Name = adi
				}
				bilgi.Description = aciklama
			}
		}
		out = append(out, bilgi)
	}
	return out
}

// beceriYolu — "skill:<ad>" girdisini diskteki beceriye çevirir. Dizin ise
// dizinin kendisi verilir (pi SKILL.md'yi orada arar), tek dosyaysa dosya.
//
// Ad kontrolü eklentilerdekiyle AYNI sıkılıkta: allowlist bir isim listesidir,
// dosya sistemi gezintisi değil.
func (a *Adapter) beceriYolu(ad string) (string, error) {
	if a.ConfigDir == "" {
		return "", fmt.Errorf("pi becerisi %q istendi ama yapılandırma dizini ayarlı değil", ad)
	}
	kok := filepath.Join(a.ConfigDir, beceriDizini)
	yol := filepath.Join(kok, ad)
	if !strings.HasPrefix(filepath.Clean(yol)+string(os.PathSeparator),
		filepath.Clean(kok)+string(os.PathSeparator)) {
		return "", fmt.Errorf("pi becerisi %q beceri dizininin dışına çıkıyor", ad)
	}
	if bilgi, err := os.Stat(yol); err == nil && bilgi.IsDir() {
		return yol, nil
	}
	if _, err := os.Stat(yol + ".md"); err == nil {
		return yol + ".md", nil
	}
	return "", fmt.Errorf("pi becerisi %q bulunamadı (%s)", ad, kok)
}

// beceriBasligi — SKILL.md frontmatter'ından name/description. Ayrıştırıcı
// bilinçli olarak minimal: YAML kütüphanesi çekmek yerine iki alanı okuyoruz,
// çünkü hata durumunda kaybımız yalnız görünen ad.
func beceriBasligi(icerik string) (ad, aciklama string) {
	satirlar := strings.Split(strings.ReplaceAll(icerik, "\r\n", "\n"), "\n")
	if len(satirlar) == 0 || strings.TrimSpace(satirlar[0]) != "---" {
		return "", ""
	}
	for _, s := range satirlar[1:] {
		t := strings.TrimSpace(s)
		if t == "---" {
			break
		}
		anahtar, deger, ok := strings.Cut(t, ":")
		if !ok {
			continue
		}
		deger = strings.Trim(strings.TrimSpace(deger), `"'`)
		switch strings.ToLower(strings.TrimSpace(anahtar)) {
		case "name":
			ad = deger
		case "description":
			aciklama = deger
		}
	}
	return ad, aciklama
}

func eklentiDosyasi(ad string) bool {
	u := strings.ToLower(filepath.Ext(ad))
	for _, k := range eklentiUzantilari {
		if u == k {
			return true
		}
	}
	return false
}

// eklentiBilgisi — dosyanın BAŞ YORUM BLOĞUNDAN ad ve açıklama okur; ayrı bir
// manifest dosyası tutulmaz. Sebep: manifest ile kaynak ayrışır (eklenti
// değişir, açıklaması eski kalır), oysa baş yorum eklentiyi yazanın zaten
// yazdığı yerdir.
//
// Kural: ilk yorum satırı "// <ad> — <özet>"; sonraki yorum satırları özete
// eklenir, ilk BOŞ yorum satırında ya da yorum bitince durulur. Yorum yoksa ad
// dosya adıdır, açıklama boştur — uydurmaktansa boş bırakılır.
func eklentiBilgisi(dosya string, veri []byte, kind string) engine.ExtensionInfo {
	bilgi := engine.ExtensionInfo{
		ID:        dosya,
		Name:      strings.TrimSuffix(dosya, filepath.Ext(dosya)),
		Kind:      kind,
		Resources: []string{"extension"},
	}

	var satirlar []string
	for _, ham := range strings.Split(string(veri), "\n") {
		s := strings.TrimSpace(ham)
		if !strings.HasPrefix(s, "//") {
			break
		}
		s = strings.TrimSpace(strings.TrimPrefix(s, "//"))
		if s == "" {
			break
		}
		satirlar = append(satirlar, s)
	}
	if len(satirlar) == 0 {
		return bilgi
	}

	bas := satirlar[0]
	for _, ayrac := range []string{" — ", " – ", " - "} {
		if i := strings.Index(bas, ayrac); i > 0 {
			bilgi.Name = strings.TrimSpace(bas[:i])
			satirlar[0] = strings.TrimSpace(bas[i+len(ayrac):])
			break
		}
	}
	bilgi.Description = strings.Join(satirlar, " ")
	return bilgi
}
