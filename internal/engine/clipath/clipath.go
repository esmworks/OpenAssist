// Package clipath — motor ikilisini diskte bulur.
//
// Neden `exec.LookPath` yetmiyor: core bir servis olarak koşar ve PATH'ini
// başlatıldığı ortamdan miras alır. Kullanıcının etkileşimli kabuğunda
// çalışan `codex` çoğu zaman nvm'in O ANKİ node sürümünün altındadır; servis
// başka bir sürümle (ya da nvm hiç yüklenmemiş bir ortamda) başlatıldığında
// aynı ikili "PATH'te yok" olur. Kullanıcı terminalde çalıştırabildiği bir
// aracın uygulamada "bulunamadı" görünmesini haklı olarak hata sayar.
//
// İkinci sebep: bazı CLI'lar yalnız bir masaüstü uygulamasının içinde gelir —
// codex, ChatGPT.app'in Resources klasöründe. Kullanıcı ayrıca kurmamış
// olabilir; kurulu olan tek kopya odur.
//
// Arama sırası bilinçli: PATH önce gelir çünkü kullanıcının açık seçimidir,
// sonra kullanıcı kurulumları (nvm/fnm/volta/bun/homebrew/…), en sonda
// uygulama paketleri. Böylece terminaldeki `codex` ile uygulamanın kullandığı
// `codex` aynı kalır; paket içindekine ancak başka hiçbiri yoksa düşülür.
//
// İkiliyi bulmak yetmez: npm ile kurulan CLI bir node betiğidir ve kendi
// yorumlayıcısını PATH'ten arar. Bulduğumuz yolu çocuğun ortamına taşımak
// için PATHIle kullanılır.
package clipath

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// xOK — syscall.Access çalıştırma izni kipi (unistd.h X_OK).
const xOK = 0x01

// bellek — (ad, PATH, HOME) → bulunan mutlak yol. Find her turda çağrılır ve
// tarama onlarca stat eder; cevap yalnız kurulum değiştiğinde değişir.
// Yalnız POZİTİF sonuç saklanır: bulunamadıysa kullanıcı aracı yeni kurmuş
// olabilir, bir sonraki tur yeniden aransın. Saklanan yol da dönmeden önce
// hâlâ çalıştırılabilir mi diye bakılır — sürüm silinince kendini tazeler.
var bellek sync.Map

// Find — ikiliyi bulur ve MUTLAK yolunu döner; bulamazsa boş dize.
//
// paketler, aranacak uygulama paketi yollarıdır (ör.
// "/Applications/ChatGPT.app/Contents/Resources/codex"); "~/" ile başlayanlar
// ev dizinine göre çözülür.
//
// Çağıran boş dönüşte ham adı kullanmaya devam etmeli: hata mesajı "PATH'te
// yok" demeye devam etsin, burası sessizce başka bir davranış getirmesin.
func Find(ad string, paketler ...string) string {
	anahtar := ad + "\x00" + os.Getenv("PATH") + "\x00" + os.Getenv("HOME")
	if v, ok := bellek.Load(anahtar); ok {
		if yol, _ := v.(string); calistirilabilir(yol) {
			return yol
		}
		bellek.Delete(anahtar)
	}
	bulunan := ara(ad, paketler)
	if bulunan != "" {
		bellek.Store(anahtar, bulunan)
	}
	return bulunan
}

func ara(ad string, paketler []string) string {
	// LookPath göreli sonuçta (PATH'te "." gibi bir giriş) ErrDot döner ve
	// buraya girmez — dönen yol mutlaktır.
	if p, err := exec.LookPath(ad); err == nil {
		return p
	}
	for _, aday := range adaylar(ad, paketler) {
		if calistirilabilir(aday) {
			return aday
		}
	}
	return ""
}

func adaylar(ad string, paketler []string) []string {
	ev, _ := os.UserHomeDir()
	var out []string

	// 1) Sürüm yöneticileri — node tabanlı CLI'ların en sık yeri ve servis
	// PATH'inin en sık kaçırdığı yer.
	for _, dizin := range surumYoneticiDizinleri(ev) {
		out = append(out, filepath.Join(dizin, ad))
	}

	// 2) Diğer kullanıcı kurulumları.
	//
	// ev boşsa (HOME tanımsız — kullanıcı oturumu dışında başlatılmış
	// servis) ev-göreli adaylar ATLANIR: filepath.Join("", ".local", "bin")
	// boş değil ".local/bin" verir. Göreli yol burada bizim çalışma dizinimize
	// göre stat'lanır ama exec cmd.Dir'e (botun çalışma dizinine) göre
	// çözülür — deponun içindeki rastgele bir dosya motor sanılırdı.
	var dizinler []string
	if ev != "" {
		dizinler = append(dizinler,
			filepath.Join(ev, ".local", "bin"),
			filepath.Join(ev, ".bun", "bin"),
			filepath.Join(ev, ".volta", "bin"),
			filepath.Join(ev, ".asdf", "shims"),
			filepath.Join(ev, ".npm-global", "bin"), // npm config set prefix
			filepath.Join(ev, "."+ad, "bin"),        // ör. ~/.codex/bin
		)
	}
	dizinler = append(dizinler, "/opt/homebrew/bin", "/usr/local/bin")
	for _, dizin := range dizinler {
		out = append(out, filepath.Join(dizin, ad))
	}

	// 3) Masaüstü uygulaması paketleri — en son çare.
	for _, p := range paketler {
		if q := evGenislet(p, ev); filepath.IsAbs(q) {
			out = append(out, q)
		}
	}
	return out
}

// surumYoneticiDizinleri — nvm ve fnm'in kurulu sürüm bin dizinleri, yeniden
// eskiye. nvm'de kullanıcının `default` takma adı (kabuğunun kullandığı
// sürüm) en öne alınır: paket başlığındaki söz, terminaldeki ikili ile
// uygulamanınkinin AYNI kalması — en yenisini seçmek o sözü bozardı.
func surumYoneticiDizinleri(ev string) []string {
	if ev == "" {
		return nil
	}
	out := surumluBinDizinleri(
		filepath.Join(ev, ".nvm", "versions", "node"), "bin", nvmVarsayilan(ev))
	fnmAlt := filepath.Join("installation", "bin")
	for _, kok := range []string{
		filepath.Join(ev, ".local", "share", "fnm", "node-versions"),
		filepath.Join(ev, "Library", "Application Support", "fnm", "node-versions"),
	} {
		out = append(out, surumluBinDizinleri(kok, fnmAlt, "")...)
	}
	return out
}

// surumluBinDizinleri — <kok>/<sürüm>/<alt>, sürüm sırasıyla yeniden eskiye.
// Sıralama sayısal: metin sıralaması v24.9.0'ı v24.18.0'ın üstüne koyardı ve
// eski sürüm kazanırdı. oncelikli boş değilse o takma ada uyan sürüm başa
// alınır.
func surumluBinDizinleri(kok, alt, oncelikli string) []string {
	girisler, err := os.ReadDir(kok)
	if err != nil {
		return nil
	}
	var surumler []string
	for _, g := range girisler {
		if g.IsDir() {
			surumler = append(surumler, g.Name())
		}
	}
	sort.Slice(surumler, func(i, j int) bool {
		return surumKarsilastir(surumler[i], surumler[j]) > 0
	})
	surumler = oncelikliyiOneAl(surumler, oncelikli)
	out := make([]string, 0, len(surumler))
	for _, s := range surumler {
		out = append(out, filepath.Join(kok, s, alt))
	}
	return out
}

// nvmVarsayilan — ~/.nvm/alias/default içeriği ("24", "v24.16.0", "lts/*"…).
// "lts/*" ya da "node" gibi dolaylı adlar bir sürüme çözülemez; boş dönülür
// ve en-yeni sırası korunur.
func nvmVarsayilan(ev string) string {
	b, err := os.ReadFile(filepath.Join(ev, ".nvm", "alias", "default"))
	if err != nil {
		return ""
	}
	ad := strings.TrimSpace(string(b))
	if ad == "" || strings.ContainsAny(ad, "/*") {
		return ""
	}
	if !strings.HasPrefix(ad, "v") {
		ad = "v" + ad
	}
	return ad
}

// oncelikliyiOneAl — takma addan ("v24" ya da "v24.16.0") çözülen sürümü
// listenin başına alır. Liste yeniden eskiye sıralı olduğundan ilk eşleşen,
// takma adın gösterdiği en yüksek sürümdür. Eşleşme yoksa liste değişmez.
func oncelikliyiOneAl(surumler []string, ad string) []string {
	if ad == "" {
		return surumler
	}
	for i, v := range surumler {
		if v != ad && !strings.HasPrefix(v, ad+".") {
			continue
		}
		out := make([]string, 0, len(surumler))
		out = append(out, v)
		out = append(out, surumler[:i]...)
		return append(out, surumler[i+1:]...)
	}
	return surumler
}

// surumKarsilastir — "v24.18.0" biçimini parça parça sayısal karşılaştırır.
// Ön-sürüm eki (v25.0.0-rc.1) aynı çekirdeğin kararlı sürümünün ALTINDA
// sıralanır; eşitlikte metin sırasına düşer.
func surumKarsilastir(a, b string) int {
	ca, ona := surumParcala(a)
	cb, onb := surumParcala(b)
	for i := 0; i < len(ca) || i < len(cb); i++ {
		na, nb := parcaSayi(ca, i), parcaSayi(cb, i)
		if na != nb {
			if na > nb {
				return 1
			}
			return -1
		}
	}
	switch {
	case ona == "" && onb != "":
		return 1
	case ona != "" && onb == "":
		return -1
	}
	return strings.Compare(a, b)
}

// surumParcala — "v25.0.0-rc.1" → ["25","0","0"] + "rc.1". Ek ayrılmasa
// parcaSayi "0-rc"yi 0 sayar ve dörtlü [25 0 0 1] kararlı [25 0 0 0]'ın
// ÜSTÜNE çıkardı: aday sürüm kararlıyı yenerdi.
func surumParcala(s string) ([]string, string) {
	cekirdek, on, _ := strings.Cut(strings.TrimPrefix(s, "v"), "-")
	return strings.Split(cekirdek, "."), on
}

func parcaSayi(parcalar []string, i int) int {
	if i >= len(parcalar) {
		return 0
	}
	n, _ := strconv.Atoi(parcalar[i])
	return n
}

func evGenislet(yol, ev string) string {
	if ev != "" && strings.HasPrefix(yol, "~/") {
		return filepath.Join(ev, yol[2:])
	}
	return yol
}

// calistirilabilir — dosya var, dizin değil ve BİZ çalıştırabiliyor muyuz.
// İzin bitine bakmak yetmezdi: başkasına ait 0700 bir dosya da "çalıştırma
// biti açık" görünür, Find onu seçer ve sonraki adaylar hiç denenmezdi —
// motor her turda EACCES ile düşerdi.
func calistirilabilir(yol string) bool {
	if yol == "" {
		return false
	}
	bilgi, err := os.Stat(yol)
	if err != nil || bilgi.IsDir() {
		return false
	}
	return syscall.Access(yol, xOK) == nil
}

// binDizinAdlari — "burası çalıştırılabilirler klasörü" sayılan son bileşen.
// clipath'in aradığı kullanıcı kurulumlarının hepsi bunlardan biriyle biter;
// uygulama paketleri (…/Contents/Resources) bitmez.
var binDizinAdlari = map[string]bool{"bin": true, "sbin": true, "shims": true}

// PATHIle — cli'nin dizinini yolun BAŞINA ekler (zaten varsa dokunmaz), ama
// yalnız o dizin bir çalıştırılabilirler klasörüyse ya da cli bir yorumlayıcı
// betiğiyse. Aksi halde yol olduğu gibi döner.
//
// Sebep: npm ile kurulan CLI'lar node betiğidir (`#!/usr/bin/env node`) ve
// yorumlayıcısını çocuğun PATH'inden arar; node'un kendisi de köprünün
// doğurduğu alt süreçlere lazım. İkiliyi PATH DIŞINDA bulduysak (Find)
// yanındaki node da PATH dışındadır ve süreç "node: command not found" ile
// düşerdi. Başa eklemek şart: PATH'te BAŞKA (eski) bir node varsa betik
// onunla değil, kurulduğu sürümle koşmalı.
//
// Koşul neden var: uygulama paketinden gelen YEREL ikili yorumlayıcı istemez
// ve dizinini başa almak o klasördeki başka araçların kullanıcınınkileri
// gölgelemesi olurdu — ChatGPT.app/Contents/Resources altında codex'in
// yanında `rg` de var, eklenseydi motorun çalıştırdığı her `rg` kullanıcının
// kendi ripgrep'i yerine uygulamanın kopyasına giderdi.
func PATHIle(cli, yol string) string {
	if !filepath.IsAbs(cli) {
		return yol
	}
	dizin := filepath.Dir(cli)
	if !binDizinAdlari[filepath.Base(dizin)] && !betikMi(cli) {
		return yol
	}
	for _, p := range strings.Split(yol, string(os.PathListSeparator)) {
		if p == dizin {
			return yol
		}
	}
	if yol == "" {
		return dizin
	}
	return dizin + string(os.PathListSeparator) + yol
}

// BulunamadiDetay — yoklama başarısız olduğunda kullanıcıya gösterilecek
// açıklama.
//
// "PATH'te yok" yalnız HAM AD için doğrudur. clipath ikiliyi PATH dışında
// bulduysa cli mutlak bir yoldur ve o yol gerçekten vardır; kullanıcıya
// "PATH'te yok" demek onu var olan bir dosyayı PATH'e eklemeye uğraştırır,
// asıl sebebi (yorumlayıcı yok, karantina bayrağı, sıfırdan farklı çıkış)
// gizlerdi. Sebep stderr'de olur, oraya bakılır.
func BulunamadiDetay(cli string, err error) string {
	ayrinti := err.Error()
	var cikis *exec.ExitError
	if errors.As(err, &cikis) && len(cikis.Stderr) > 0 {
		ayrinti = strings.TrimSpace(string(cikis.Stderr))
	}
	if r := []rune(ayrinti); len(r) > 200 {
		ayrinti = "…" + string(r[len(r)-200:])
	}
	if filepath.IsAbs(cli) {
		return fmt.Sprintf("%q çalıştırılamadı — %s", cli, ayrinti)
	}
	return fmt.Sprintf("bulunamadı — %q PATH'te yok ya da çalışmıyor (%s)", cli, ayrinti)
}

// betikMi — dosya "#!" ile mi başlıyor.
func betikMi(yol string) bool {
	f, err := os.Open(yol)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }() // salt okuma; kapanış hatası anlamsız
	var bas [2]byte
	if _, err := io.ReadFull(f, bas[:]); err != nil {
		return false
	}
	return bas[0] == '#' && bas[1] == '!'
}
