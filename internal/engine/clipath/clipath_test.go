package clipath

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSurumKarsilastir(t *testing.T) {
	// Metin sıralaması v24.9.0'ı v24.18.0'ın üstüne koyardı ve eski nvm
	// sürümü kazanırdı — sıralama sayısal olmak zorunda.
	kucuktenBuyuge := []string{"v20.11.1", "v24.9.0", "v24.16.0", "v24.18.0", "v25.0.0"}
	for i := 0; i+1 < len(kucuktenBuyuge); i++ {
		a, b := kucuktenBuyuge[i], kucuktenBuyuge[i+1]
		if surumKarsilastir(a, b) >= 0 {
			t.Errorf("%s < %s olmalıydı", a, b)
		}
		if surumKarsilastir(b, a) <= 0 {
			t.Errorf("%s > %s olmalıydı", b, a)
		}
	}
	if surumKarsilastir("v24.18.0", "v24.18.0") != 0 {
		t.Error("aynı sürüm eşit olmalı")
	}
}

// TestSurumKarsilastirOnSurum — ön-sürüm kararlının ALTINDA. Ek ayrılmasaydı
// "0-rc" 0 sayılır, [25 0 0 1] dörtlüsü kararlı [25 0 0 0]'ı yener ve nightly
// kurmuş bir kullanıcı sessizce kararsız node'a düşerdi.
func TestSurumKarsilastirOnSurum(t *testing.T) {
	if surumKarsilastir("v25.0.0-rc.1", "v25.0.0") >= 0 {
		t.Error("v25.0.0-rc.1 < v25.0.0 olmalıydı")
	}
	if surumKarsilastir("v25.0.0", "v25.0.0-rc.1") <= 0 {
		t.Error("v25.0.0 > v25.0.0-rc.1 olmalıydı")
	}
	// Ön-sürüm de olsa çekirdek sürüm önce bakılır.
	if surumKarsilastir("v26.0.0-nightly1", "v25.0.0") <= 0 {
		t.Error("v26.0.0-nightly1 > v25.0.0 olmalıydı")
	}
}

// TestFindNvmEnYeniSurumu — servis PATH'i kullanıcının nvm sürümünü
// kaçırdığında ikili yine bulunmalı, hem de en yeni sürümden.
func TestFindNvmEnYeniSurumu(t *testing.T) {
	ev := bosEv(t)

	for _, s := range []string{"v24.9.0", "v24.18.0"} {
		nvmSurumu(t, ev, s, "sahtecli")
	}

	got := Find("sahtecli")
	istenen := filepath.Join(ev, ".nvm", "versions", "node", "v24.18.0", "bin", "sahtecli")
	if got != istenen {
		t.Errorf("en yeni nvm sürümü seçilmeliydi:\n got %q\nwant %q", got, istenen)
	}
}

// TestFindNvmVarsayilanTakmaAd — `nvm alias default` kullanıcının kabuğunun
// koştuğu sürümdür; en yeni sürüm başka bir sürümse bile O kazanmalı. Paketin
// sözü "terminaldeki ikili ile uygulamanınki aynı kalsın" — en yenisini
// seçmek tam da bu sözü bozardı.
func TestFindNvmVarsayilanTakmaAd(t *testing.T) {
	ev := bosEv(t)
	for _, s := range []string{"v24.16.0", "v25.0.0"} {
		nvmSurumu(t, ev, s, "sahtecli")
	}
	yaz(t, filepath.Join(ev, ".nvm", "alias", "default"), 0o644, "24\n")

	got := Find("sahtecli")
	istenen := filepath.Join(ev, ".nvm", "versions", "node", "v24.16.0", "bin", "sahtecli")
	if got != istenen {
		t.Errorf("varsayılan takma ad kazanmalıydı:\n got %q\nwant %q", got, istenen)
	}
}

// TestFindNvmCozulemeyenTakmaAd — "lts/*" bir sürüm dizinine çözülmez;
// sıralama en yeniye düşmeli, arama boşa çıkmamalı.
func TestFindNvmCozulemeyenTakmaAd(t *testing.T) {
	ev := bosEv(t)
	for _, s := range []string{"v24.16.0", "v25.0.0"} {
		nvmSurumu(t, ev, s, "sahtecli")
	}
	yaz(t, filepath.Join(ev, ".nvm", "alias", "default"), 0o644, "lts/*\n")

	got := Find("sahtecli")
	istenen := filepath.Join(ev, ".nvm", "versions", "node", "v25.0.0", "bin", "sahtecli")
	if got != istenen {
		t.Errorf("çözülemeyen takma adda en yeni seçilmeliydi:\n got %q\nwant %q", got, istenen)
	}
}

// TestFindUygulamaPaketiSonCare — kullanıcı kurulumu varken paket içindeki
// kopya KAZANMAMALI: terminalde çalışan ikili ile uygulamanınki aynı kalsın.
func TestFindUygulamaPaketiSonCare(t *testing.T) {
	ev := bosEv(t)

	yerel := filepath.Join(ev, ".local", "bin")
	yaz(t, filepath.Join(yerel, "sahtecli"), 0o755, "#!/bin/sh\n")

	paket := filepath.Join(ev, "Uygulama.app", "Contents", "Resources", "sahtecli")
	yaz(t, paket, 0o755, "#!/bin/sh\n")

	if got := Find("sahtecli", "~/Uygulama.app/Contents/Resources/sahtecli"); got != filepath.Join(yerel, "sahtecli") {
		t.Errorf("kullanıcı kurulumu öncelikli olmalı, %q döndü", got)
	}

	// Kullanıcı kurulumu yoksa paket içindeki kullanılır — kurulu tek kopya o.
	// (Aynı anahtarla ikinci çağrı: bellekteki bayat yol da tazelenmeli.)
	if err := os.Remove(filepath.Join(yerel, "sahtecli")); err != nil {
		t.Fatal(err)
	}
	if got := Find("sahtecli", "~/Uygulama.app/Contents/Resources/sahtecli"); got != paket {
		t.Errorf("paket içindeki kopya bulunmalıydı, %q döndü", got)
	}
}

// TestFindCalistirilamayaniAtlar — aynı adlı ama çalıştırılamayan dosya
// "kurulu" sayılmamalı; yoksa motor her turda exec hatası verirdi.
func TestFindCalistirilamayaniAtlar(t *testing.T) {
	ev := bosEv(t)
	yaz(t, filepath.Join(ev, ".local", "bin", "sahtecli"), 0o644, "#!/bin/sh\n") // çalıştırma biti yok

	if got := Find("sahtecli"); got != "" {
		t.Errorf("çalıştırılamayan dosya seçilmemeliydi: %q", got)
	}
}

// TestFindEvYokkenGoreliDonmez — HOME tanımsızken (kullanıcı oturumu dışında
// başlatılmış servis) ev-göreli adaylar ".local/bin/x" gibi GÖRELİ yollara
// dönüşürdü: burada kendi çalışma dizinimize göre stat'lanır ama exec botun
// çalışma dizinine göre çözer — deponun içindeki rastgele bir dosya motor
// sanılırdı.
func TestFindEvYokkenGoreliDonmez(t *testing.T) {
	is := t.TempDir()
	t.Chdir(is)
	t.Setenv("HOME", "")
	t.Setenv("PATH", filepath.Join(is, "bos"))

	yaz(t, filepath.Join(is, ".local", "bin", "sahtecli"), 0o755, "#!/bin/sh\n")

	got := Find("sahtecli")
	if got != "" {
		t.Errorf("HOME yokken aday üretilmemeliydi, %q döndü", got)
	}
	if got != "" && !filepath.IsAbs(got) {
		t.Errorf("Find asla göreli yol dönmemeli: %q", got)
	}
}

func TestFindBulunamazsaBosDoner(t *testing.T) {
	bosEv(t)
	if got := Find("hicbiryerdeyok"); got != "" {
		t.Errorf("boş dize beklenirdi: %q", got)
	}
}

// TestPATHIle — ikilinin dizini yalnız gerektiğinde ve yalnız bir kez.
func TestPATHIle(t *testing.T) {
	ev := t.TempDir()

	binDizin := filepath.Join(ev, ".nvm", "versions", "node", "v24.18.0", "bin")
	betik := filepath.Join(binDizin, "codex")
	yaz(t, betik, 0o755, "#!/usr/bin/env node\n")

	kaynak := filepath.Join(ev, "ChatGPT.app", "Contents", "Resources")
	yerliIkili := filepath.Join(kaynak, "codex")
	yaz(t, yerliIkili, 0o755, "\xcf\xfa\xed\xfe yerel ikili\n")
	paketBetigi := filepath.Join(kaynak, "betikcli")
	yaz(t, paketBetigi, 0o755, "#!/usr/bin/env node\n")

	for _, d := range []struct {
		ad, cli, yol, istenen string
	}{
		{"göreli cli dokunmaz", "codex", "/usr/bin", "/usr/bin"},
		{"bin dizini başa gelir", betik, "/usr/bin", binDizin + ":/usr/bin"},
		{"boş yolda tek başına", betik, "", binDizin},
		{"zaten varsa tekrarlanmaz", betik, "/usr/bin:" + binDizin, "/usr/bin:" + binDizin},
		// Paketteki YEREL ikili yorumlayıcı istemez; dizini eklenirse yanındaki
		// `rg` kullanıcının ripgrep'ini gölgelerdi.
		{"uygulama paketi ikilisi eklenmez", yerliIkili, "/usr/bin", "/usr/bin"},
		// Betikse yorumlayıcısı lazım — pakette olsa bile eklenir.
		{"uygulama paketi betiği eklenir", paketBetigi, "/usr/bin", kaynak + ":/usr/bin"},
	} {
		t.Run(d.ad, func(t *testing.T) {
			if got := PATHIle(d.cli, d.yol); got != d.istenen {
				t.Errorf("PATHIle(%q, %q)\n got %q\nwant %q", d.cli, d.yol, got, d.istenen)
			}
		})
	}
}

// TestBulunamadiDetay — clipath mutlak bir yol bulduğunda "PATH'te yok"
// demek yalan olurdu: yol GERÇEKTEN var, kullanıcı var olan dosyayı PATH'e
// eklemeye uğraşırdı. Sebep stderr'de.
func TestBulunamadiDetay(t *testing.T) {
	hata := &exec.ExitError{ProcessState: nil, Stderr: []byte("env: node: No such file or directory\n")}

	mutlak := BulunamadiDetay("/Applications/ChatGPT.app/Contents/Resources/codex", hata)
	if strings.Contains(mutlak, "PATH'te yok") {
		t.Errorf("mutlak yol için PATH mesajı verilmemeli: %q", mutlak)
	}
	if !strings.Contains(mutlak, "env: node") {
		t.Errorf("gerçek sebep (stderr) mesajda olmalı: %q", mutlak)
	}

	ham := BulunamadiDetay("codex", errors.New("executable file not found in $PATH"))
	if !strings.Contains(ham, "PATH'te yok") {
		t.Errorf("ham ad için PATH mesajı beklenirdi: %q", ham)
	}
}

// bosEv — HOME'u boş bir geçici dizine, PATH'i var olmayan bir dizine kurar:
// ikili PATH'te YOK, arama adaylara düşmek zorunda.
func bosEv(t *testing.T) string {
	t.Helper()
	ev := t.TempDir()
	t.Setenv("HOME", ev)
	t.Setenv("PATH", filepath.Join(ev, "bos"))
	return ev
}

func nvmSurumu(t *testing.T, ev, surum, ad string) {
	t.Helper()
	yaz(t, filepath.Join(ev, ".nvm", "versions", "node", surum, "bin", ad), 0o755, "#!/bin/sh\n")
}

func yaz(t *testing.T, yol string, mod os.FileMode, icerik string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(yol), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(yol, []byte(icerik), mod); err != nil {
		t.Fatal(err)
	}
}
