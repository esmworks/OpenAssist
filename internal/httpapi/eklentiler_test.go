package httpapi

import (
	"context"
	"errors"
	"testing"

	"github.com/esmworks/OpenAssist/internal/engine"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

// eklentiliMotor — Extender uygulayan test motoru. Eklentisiz motor yolu
// (Extender UYGULAMAYAN adaptör) için sessizMotor kullanılıyor: her motorun
// eklenti kavramı olmak zorunda değil.
type eklentiliMotor struct {
	sessizMotor
	list []engine.ExtensionInfo
	err  error
}

func (m eklentiliMotor) Extensions(context.Context) ([]engine.ExtensionInfo, error) {
	return m.list, m.err
}

func (m eklentiliMotor) InstallExtension(context.Context, string) error { return m.err }
func (m eklentiliMotor) RemoveExtension(context.Context, string) error  { return m.err }

func TestEklentiDTOlariKullananBotlariEsler(t *testing.T) {
	list := []engine.ExtensionInfo{
		{ID: "oa-gateway.ts", Name: "oa-gateway", Kind: "builtin"},
		{ID: "notlar.ts", Name: "notlar", Kind: "file"},
	}
	bots := []db.Bot{
		{Name: "Zeynep", Engine: "pi", Extensions: []string{"oa-gateway.ts"}},
		{Name: "Ahmet", Engine: "pi", Extensions: []string{"oa-gateway.ts", "notlar.ts"}},
		// Başka motora bağlı bot bu kataloğu ilgilendirmez.
		{Name: "Başka", Engine: "codex", Extensions: []string{"oa-gateway.ts"}},
	}

	out := eklentiDTOlari("pi", list, bots)
	if len(out) != 2 {
		t.Fatalf("iki satır bekleniyordu: %+v", out)
	}
	if got := out[0].UsedBy; len(got) != 2 || got[0] != "Ahmet" || got[1] != "Zeynep" {
		t.Errorf("oa-gateway kullananlar=%v; ada göre sıralı [Ahmet Zeynep] olmalı", got)
	}
	if got := out[1].UsedBy; len(got) != 1 || got[0] != "Ahmet" {
		t.Errorf("notlar kullananlar=%v; [Ahmet] olmalı", got)
	}
	if out[0].Engine != "pi" {
		t.Errorf("engine=%q, motor kimliği taşınmalı", out[0].Engine)
	}
}

// Kullanılmayan eklentinin used_by'ı BOŞ DİZİ olmalı, null değil: istemci
// tarafında null bir listeyi çözmeye çalışmak kartı düşürürdü.
func TestEklentiDTOlariKullanilmayanBosDiziVerir(t *testing.T) {
	out := eklentiDTOlari("pi", []engine.ExtensionInfo{{ID: "x.ts", Kind: "file"}}, nil)
	if len(out) != 1 {
		t.Fatalf("tek satır bekleniyordu: %+v", out)
	}
	if out[0].UsedBy == nil {
		t.Error("used_by nil: JSON'da null'a düşer")
	}
	if len(out[0].UsedBy) != 0 {
		t.Errorf("used_by=%v, boş olmalı", out[0].UsedBy)
	}
}

func TestBilinmeyenEklentiKapisi(t *testing.T) {
	kurulu := eklentiliMotor{list: []engine.ExtensionInfo{{ID: "oa-gateway.ts", Kind: "builtin"}}}
	a := &api{engines: map[string]engine.Executor{
		"pi":       kurulu,
		"eksiksiz": sessizMotor{},
		"bozuk":    eklentiliMotor{err: errors.New("dizin okunamadı")},
	}}

	if ad, yok := a.bilinmeyenEklenti(context.Background(), "pi", []string{"oa-gateway.ts"}); yok {
		t.Errorf("kurulu eklenti reddedildi: %q", ad)
	}
	ad, yok := a.bilinmeyenEklenti(context.Background(), "pi", []string{"oa-gateway.ts", "hayalet.ts"})
	if !yok || ad != "hayalet.ts" {
		t.Errorf("bilinmeyen eklenti geçti (ad=%q yok=%v) — koşu anında düşerdi", ad, yok)
	}

	// Eklenti kavramı olmayan motorda, katalog okunamadığında ve motor hiç
	// tanınmıyorken doğrulama yapılmaz: bilmediğimiz için reddetmek çalışan
	// bir kurulumu ayar ekranından kilitlerdi.
	for _, motor := range []string{"eksiksiz", "bozuk", "hiçyok"} {
		if ad, yok := a.bilinmeyenEklenti(context.Background(), motor, []string{"her-neyse.ts"}); yok {
			t.Errorf("%s: doğrulanamayan katalogda reddedildi (%q)", motor, ad)
		}
	}
}

func TestMotorAdlariSirali(t *testing.T) {
	got := motorAdlari(map[string]engine.Executor{
		"zeta": sessizMotor{}, "alfa": sessizMotor{}, "beta": sessizMotor{},
	})
	if len(got) != 3 || got[0] != "alfa" || got[1] != "beta" || got[2] != "zeta" {
		t.Errorf("sıra=%v; alfabetik olmalı (liste her istekte zıplamasın)", got)
	}
}

// Katalog satırı: sürüm SABİTLENİR ve OpenAssist'te karşılığı olan paketler
// sebebiyle işaretlenir (gizlenmez — kullanıcının kendi seçimi).
func TestNpmSonuclariCevir(t *testing.T) {
	var ham npmAramaYanit
	ham.Objects = make([]struct {
		Package struct {
			Name        string `json:"name"`
			Version     string `json:"version"`
			Description string `json:"description"`
			Publisher   struct {
				Username string `json:"username"`
			} `json:"publisher"`
			Links struct {
				NPM string `json:"npm"`
			} `json:"links"`
		} `json:"package"`
		Downloads struct {
			Monthly int `json:"monthly"`
		} `json:"downloads"`
	}, 3)
	ham.Objects[0].Package.Name = "pi-web-access"
	ham.Objects[0].Package.Version = "0.23.0"
	ham.Objects[0].Downloads.Monthly = 257972
	ham.Objects[1].Package.Name = "pi-sarki-sozu"
	ham.Objects[1].Package.Version = "1.0.0"
	// Sürümsüz kayıt: kurulum kaynağı üretilemez, elenir.
	ham.Objects[2].Package.Name = "bozuk"

	got := npmSonuclariCevir(ham)
	if len(got) != 2 {
		t.Fatalf("sürümsüz kayıt elenmedi: %+v", got)
	}
	if got[0].Source != "npm:pi-web-access@0.23.0" {
		t.Errorf("kaynak=%q; sürüm sabitlenmeli", got[0].Source)
	}
	if got[0].ConflictNote == "" {
		t.Error("pi-web-access çakışma notu taşımalı: web okuma bizde kendi aracımızda")
	}
	if got[1].ConflictNote != "" {
		t.Errorf("çakışmayan pakete not düştü: %q", got[1].ConflictNote)
	}
}

// Kullanan bot varken kaldırma isteği önce kimin bozulacağını söyler.
func TestEklentiyiKullananlar(t *testing.T) {
	bots := []db.Bot{
		{Name: "Zeynep", Engine: "pi", Extensions: []string{"npm:pi-x"}},
		{Name: "Ahmet", Engine: "pi", Extensions: []string{"oa-gateway.ts"}},
		{Name: "Başka", Engine: "codex", Extensions: []string{"npm:pi-x"}},
	}
	got := eklentiyiKullananlar("pi", "npm:pi-x", bots)
	if len(got) != 1 || got[0] != "Zeynep" {
		t.Errorf("kullananlar=%v; yalnız [Zeynep] olmalı", got)
	}
	if k := eklentiyiKullananlar("pi", "npm:kimse-yok", bots); len(k) != 0 {
		t.Errorf("kullanılmayan eklentiye bot atandı: %v", k)
	}
}

// Motor seçimi: eklenti yönetebilen tek motor varsa o seçilir; iki taneyse
// istemci hangisini kastettiğini söylemek zorundadır (paket yanlış motora
// kurulmasın).
func TestEklentiYoneticisiSecimi(t *testing.T) {
	tek := &api{engines: map[string]engine.Executor{
		"pi": eklentiliMotor{}, "sessiz": sessizMotor{},
	}}
	if _, id, ok := tek.eklentiYoneticisi(""); !ok || id != "pi" {
		t.Errorf("tek yönetici seçilmedi: id=%q ok=%v", id, ok)
	}
	if _, _, ok := tek.eklentiYoneticisi("sessiz"); ok {
		t.Error("eklenti yönetemeyen motor yönetici sayıldı")
	}

	cift := &api{engines: map[string]engine.Executor{
		"pi": eklentiliMotor{}, "pi2": eklentiliMotor{},
	}}
	if _, _, ok := cift.eklentiYoneticisi(""); ok {
		t.Error("iki yönetici varken motor tahmin edildi")
	}
}
