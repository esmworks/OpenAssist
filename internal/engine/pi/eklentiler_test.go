package pi

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/esmworks/OpenAssist/internal/engine"
)

// Gömülü eklenti HER ZAMAN listede olmalı: onay kapısı orada yaşıyor, hiç koşu
// yapılmamış taze kurulumda disk boşken de görünmesi gerekir.
func TestExtensionsGomuluyuDiskBoskendeVerir(t *testing.T) {
	a := &Adapter{ExtensionsDir: t.TempDir()}

	list, err := a.Extensions(context.Background())
	if err != nil {
		t.Fatalf("Extensions: %v", err)
	}

	var gateway bool
	for _, e := range list {
		if e.ID == OAGateway {
			gateway = true
			if e.Kind != "builtin" {
				t.Errorf("oa-gateway kind=%q, builtin olmalı", e.Kind)
			}
			if e.Description == "" {
				t.Error("açıklama boş: baş yorum bloğu okunmuyor")
			}
		}
	}
	if !gateway {
		t.Fatalf("gömülü %s listede yok: %+v", OAGateway, list)
	}
}

// Dizine düşen eklenti listeye girer, eklenti OLMAYAN dosya girmez. Girseydi
// kullanıcı onu bota takar, koşu "eklenti bulunamadı" ile düşerdi.
func TestExtensionsDiskteEklentiOlmayaniElemeli(t *testing.T) {
	dizin := t.TempDir()
	yaz := func(ad, icerik string) {
		if err := os.WriteFile(filepath.Join(dizin, ad), []byte(icerik), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	yaz("notlar.ts", "// notlar — koşu sonrası not tutar.\n//\n// gövde\nexport {}\n")
	yaz("README.md", "# eklenti değil")
	yaz("ayar.json", "{}")
	if err := os.Mkdir(filepath.Join(dizin, "altdizin"), 0o700); err != nil {
		t.Fatal(err)
	}

	list, err := (&Adapter{ExtensionsDir: dizin}).Extensions(context.Background())
	if err != nil {
		t.Fatalf("Extensions: %v", err)
	}

	gorulen := map[string]string{}
	for _, e := range list {
		gorulen[e.ID] = e.Kind
	}
	if gorulen["notlar.ts"] != "file" {
		t.Errorf("notlar.ts kind=%q, file olmalı", gorulen["notlar.ts"])
	}
	for _, ad := range []string{"README.md", "ayar.json", "altdizin"} {
		if _, var_ := gorulen[ad]; var_ {
			t.Errorf("%s listeye girmemeliydi", ad)
		}
	}
}

// Gömülü eklenti dizine yazıldıktan sonra da TEK satır kalmalı: iki kez
// görünseydi kullanıcı "kurulu kopyayı" kaldırılabilir sanırdı.
func TestExtensionsGomuluDiskeYazildigindaTekrarlanmaz(t *testing.T) {
	dizin := t.TempDir()
	a := &Adapter{ExtensionsDir: dizin}
	if err := a.eklentileriKur(); err != nil {
		t.Fatalf("eklentileriKur: %v", err)
	}

	list, err := a.Extensions(context.Background())
	if err != nil {
		t.Fatalf("Extensions: %v", err)
	}
	var sayac int
	for _, e := range list {
		if e.ID == OAGateway {
			sayac++
			if e.Kind != "builtin" {
				t.Errorf("diskteki kopya kind=%q, builtin kalmalı", e.Kind)
			}
		}
	}
	if sayac != 1 {
		t.Errorf("%s %d kez listelendi, 1 olmalı", OAGateway, sayac)
	}
}

// Sıralama: gömülüler başta. Onay kapısı listenin dibinde aranmamalı.
func TestExtensionsGomuluOnceGelir(t *testing.T) {
	dizin := t.TempDir()
	if err := os.WriteFile(filepath.Join(dizin, "aaa-once.ts"), []byte("// aaa\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	list, err := (&Adapter{ExtensionsDir: dizin}).Extensions(context.Background())
	if err != nil {
		t.Fatalf("Extensions: %v", err)
	}
	if len(list) < 2 {
		t.Fatalf("iki eklenti bekleniyordu: %+v", list)
	}
	if list[0].Kind != "builtin" {
		t.Errorf("ilk satır kind=%q, builtin olmalı (ada göre 'aaa-once.ts' öne geçmiş)", list[0].Kind)
	}
}

func TestEklentiBilgisiBasYorumdanOkur(t *testing.T) {
	testler := []struct {
		ad      string
		dosya   string
		kaynak  string
		istenen [2]string // ad, açıklama
	}{
		{
			ad:      "ad ve özet ayraçla",
			dosya:   "notlar.ts",
			kaynak:  "// notlar — koşu sonrası not tutar.\n//\n// Uzun gerekçe burada.\nexport {}",
			istenen: [2]string{"notlar", "koşu sonrası not tutar."},
		},
		{
			ad:      "çok satırlı özet birleşir",
			dosya:   "x.ts",
			kaynak:  "// x — ilk satır\n// ikinci satır\n\n// bu blok sayılmaz\n",
			istenen: [2]string{"x", "ilk satır ikinci satır"},
		},
		{
			ad:      "yorum yoksa ad dosya adı, açıklama boş",
			dosya:   "cıplak.ts",
			kaynak:  "export const x = 1\n",
			istenen: [2]string{"cıplak", ""},
		},
		{
			ad:      "ayraç yoksa satırın tamamı açıklamadır",
			dosya:   "y.ts",
			kaynak:  "// tek satırlık açıklama\n",
			istenen: [2]string{"y", "tek satırlık açıklama"},
		},
	}
	for _, tt := range testler {
		t.Run(tt.ad, func(t *testing.T) {
			got := eklentiBilgisi(tt.dosya, []byte(tt.kaynak), "file")
			if got.Name != tt.istenen[0] || got.Description != tt.istenen[1] {
				t.Errorf("ad=%q açıklama=%q; beklenen ad=%q açıklama=%q",
					got.Name, got.Description, tt.istenen[0], tt.istenen[1])
			}
			if got.ID != tt.dosya {
				t.Errorf("ID=%q, dosya adı olmalı (allowlist bunu yazar)", got.ID)
			}
		})
	}
}

// KEŞİF DÖRT TÜRDE DE KAPALI olmalı. Ölçüldü (0.84.2): yalnız
// --no-extensions verildiğinde ev dizinindeki beceriler her bota yükleniyor.
func TestBuildArgsButunKesifleriKapatir(t *testing.T) {
	a := &Adapter{ExtensionsDir: t.TempDir()}
	args, err := a.buildArgs(context.Background(), engine.TurnInput{}, "oturum")
	if err != nil {
		t.Fatalf("buildArgs: %v", err)
	}
	for _, bayrak := range []string{"--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes"} {
		if !bayrakVar(args, bayrak) {
			t.Errorf("%s yok: o türün keşfi açık kalır ve allowlist'ten geçmeden yüklenir", bayrak)
		}
	}
}

// Beceri --skill ile, eklenti -e ile verilmeli: pi'nin iki ayrı bayrağı var ve
// beceriyi -e ile vermek onu hiç yüklemezdi.
func TestBuildArgsBeceriyiSkillBayragiylaVerir(t *testing.T) {
	kok := t.TempDir()
	beceriKok := filepath.Join(kok, beceriDizini, "notlar")
	if err := os.MkdirAll(beceriKok, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beceriKok, "SKILL.md"),
		[]byte("---\nname: notlar\ndescription: Not tutar.\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	eklentiKok := filepath.Join(kok, "extensions")
	if err := os.MkdirAll(eklentiKok, 0o700); err != nil {
		t.Fatal(err)
	}
	a := &Adapter{ConfigDir: kok, ExtensionsDir: eklentiKok}
	if err := a.eklentileriKur(); err != nil {
		t.Fatal(err)
	}

	args, err := a.buildArgs(context.Background(),
		engine.TurnInput{Extensions: []string{OAGateway, beceriOneki + "notlar"}}, "oturum")
	if err != nil {
		t.Fatalf("buildArgs: %v", err)
	}
	if v, ok := bayrakDegeri(args, "-e"); !ok || v != filepath.Join(eklentiKok, OAGateway) {
		t.Errorf("eklenti -e ile verilmedi (%q): %v", v, args)
	}
	if v, ok := bayrakDegeri(args, "--skill"); !ok || v != beceriKok {
		t.Errorf("beceri --skill ile verilmedi (%q): %v", v, args)
	}
}

// Beceri katalogda görünür ve açıklamasını SKILL.md frontmatter'ından alır —
// modele gösterilen açıklama neyse kullanıcı da onu görsün.
func TestBecerilerKatalogaGirer(t *testing.T) {
	kok := t.TempDir()
	dizin := filepath.Join(kok, beceriDizini, "pdf-araclari")
	if err := os.MkdirAll(dizin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dizin, "SKILL.md"),
		[]byte("---\nname: pdf-araclari\ndescription: PDF'ten metin çıkarır.\n---\n# PDF\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// SKILL.md taşımayan dizin beceri DEĞİLDİR.
	if err := os.MkdirAll(filepath.Join(kok, beceriDizini, "bos"), 0o700); err != nil {
		t.Fatal(err)
	}

	list, err := (&Adapter{ConfigDir: kok, ExtensionsDir: t.TempDir()}).Extensions(context.Background())
	if err != nil {
		t.Fatalf("Extensions: %v", err)
	}
	var bulundu bool
	for _, e := range list {
		if e.ID == beceriOneki+"pdf-araclari" {
			bulundu = true
			if e.Kind != "skill" || len(e.Resources) != 1 || e.Resources[0] != "skill" {
				t.Errorf("beceri türü yanlış: %+v", e)
			}
			if e.Description != "PDF'ten metin çıkarır." {
				t.Errorf("frontmatter açıklaması okunmadı: %q", e.Description)
			}
		}
		if e.ID == beceriOneki+"bos" {
			t.Error("SKILL.md'siz dizin beceri sayıldı")
		}
	}
	if !bulundu {
		t.Errorf("beceri katalogda yok: %+v", list)
	}
}
