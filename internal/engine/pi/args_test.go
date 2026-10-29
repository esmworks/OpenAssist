package pi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/esmworks/OpenAssist/internal/engine"
)

// eklentiliAdaptor — gerçek bir eklenti dosyası olan adaptör. Yol denetimi
// dosyanın VARLIĞINA baktığı için testler diske yazmak zorunda.
func eklentiliAdaptor(t *testing.T, adlar ...string) *Adapter {
	t.Helper()
	dizin := t.TempDir()
	for _, ad := range adlar {
		if err := os.WriteFile(filepath.Join(dizin, ad), []byte("export default () => {}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return &Adapter{ExtensionsDir: dizin}
}

func bayrakVar(args []string, bayrak string) bool {
	for _, a := range args {
		if a == bayrak {
			return true
		}
	}
	return false
}

func bayrakDegeri(args []string, bayrak string) (string, bool) {
	for i, a := range args {
		if a == bayrak && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func TestArgsTemelBayraklar(t *testing.T) {
	a := eklentiliAdaptor(t, OAGateway)
	a.ConfigDir = t.TempDir()

	args, err := a.buildArgs(context.Background(), engine.TurnInput{Extensions: []string{OAGateway}}, "oturum-1")
	if err != nil {
		t.Fatalf("buildArgs: %v", err)
	}

	if m, _ := bayrakDegeri(args, "--mode"); m != "rpc" {
		t.Errorf("--mode rpc bekleniyordu, geldi: %q", m)
	}
	if s, _ := bayrakDegeri(args, "--session-id"); s != "oturum-1" {
		t.Errorf("--session-id geçmedi: %q", s)
	}
	// Güvenlik sınırı: keşif kapalı, depo-yerel dosyalara güvenilmiyor.
	if !bayrakVar(args, "--no-extensions") {
		t.Error("--no-extensions yok: eklenti keşfi açık kalırdı")
	}
	if !bayrakVar(args, "--no-approve") {
		t.Error("--no-approve yok: çalışma alanındaki .pi/ dosyalarına güvenilirdi")
	}
	if e, ok := bayrakDegeri(args, "-e"); !ok || filepath.Base(e) != OAGateway {
		t.Errorf("-e ile oa-gateway yüklenmedi: %q", e)
	}
}

// AllowedTools bayrağa DÖNÜŞMEZ: pi'nin --tools'u katalog filtresidir,
// bot listesini oraya vermek read/bash/edit'i kapatırdı (ön-izin ask.go'da).
func TestArgsAllowedToolsBayraGaCevrilmez(t *testing.T) {
	a := eklentiliAdaptor(t, OAGateway)

	args, err := a.buildArgs(context.Background(), engine.TurnInput{
		Extensions:   []string{OAGateway},
		AllowedTools: []string{"mcp__gw__gmail_search", AskUserTool},
	}, "oturum")
	if err != nil {
		t.Fatalf("buildArgs: %v", err)
	}
	if bayrakVar(args, "--tools") {
		t.Fatal("--tools geçti: pi bunu katalog filtresi sayar ve yerleşik araçlar kapanırdı")
	}
	if strings.Contains(strings.Join(args, " "), "mcp__gw__gmail_search") {
		t.Error("allowlist argümanlara sızdı")
	}
}

func TestArgsNoTools(t *testing.T) {
	a := eklentiliAdaptor(t)
	args, err := a.buildArgs(context.Background(), engine.TurnInput{NoTools: true}, "oturum")
	if err != nil {
		t.Fatalf("buildArgs: %v", err)
	}
	if !bayrakVar(args, "--no-tools") {
		t.Error("bakım turunda --no-tools yok")
	}
}

// Eksik eklenti TURU DÜŞÜRÜR. Sessizce atlansaydı onay kapısı olmadan
// koşulurdu: pi'nin kendi izin penceresi yok.
func TestEksikEklentiTuruDusurur(t *testing.T) {
	a := eklentiliAdaptor(t) // dizin boş
	_, err := a.buildArgs(context.Background(), engine.TurnInput{Extensions: []string{OAGateway}}, "oturum")
	if err == nil {
		t.Fatal("eksik eklenti sessizce atlandı — onay kapısız koşu açılırdı")
	}
	if !strings.Contains(err.Error(), OAGateway) {
		t.Errorf("hata hangi eklentinin eksik olduğunu söylemiyor: %v", err)
	}
}

func TestEklentiYoluDizinDisinaCikamaz(t *testing.T) {
	a := eklentiliAdaptor(t, OAGateway)

	for _, kotu := range []string{
		"../oa-gateway.ts",
		"../../etc/passwd",
		"alt/../../kacak.ts",
		"/etc/passwd",
		"",
		"   ",
	} {
		if _, err := a.eklentiYolu(context.Background(), kotu); err == nil {
			t.Errorf("dizin dışına çıkan girdi kabul edildi: %q", kotu)
		}
	}
}

func TestEklentiDizinYoksaHata(t *testing.T) {
	a := &Adapter{}
	if _, err := a.eklentiYolu(context.Background(), OAGateway); err == nil {
		t.Fatal("eklenti dizini yokken hata beklenirdi")
	}
}

func TestKuyrukRuneSinirindaKeser(t *testing.T) {
	// Her karakter 2 bayt: 1 bayt geriden kesmek rune'u ikiye bölerdi.
	uzun := strings.Repeat("ş", 500)

	out := kuyruk(uzun, 101)
	if !utf8Gecerli(out) {
		t.Fatal("kuyruk geçersiz UTF-8 üretti — Postgres bunu 22021 ile reddeder")
	}
	if kisa := kuyruk("kısa metin", 800); kisa != "kısa metin" {
		t.Errorf("sınırın altındaki metin değişti: %q", kisa)
	}
}

func utf8Gecerli(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}
