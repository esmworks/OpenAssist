// Package pi — `pi --mode rpc` sürecini süren Executor adaptörü (K18).
//
// pi çekirdeği bilinçli olarak küçüktür; kendi dokümanı "intentionally does
// not include built-in MCP, sub-agents, permission popups, plan mode, to-dos,
// or background bash" der. OpenAssist'in gateway araçları ve onay kapısı bu
// yüzden motora EKLENTİ olarak girer (extensions/oa-gateway.ts), onay da
// eklenti UI protokolünden geri döner (ask.go).
//
// Süreç tur başınadır: her tur yeni bir `pi --mode rpc` doğar, promptu alır,
// agent_settled'ı bekler ve kapanır. Süreklilik pi'nin KENDİ oturum
// dosyasındadır — imleç oturum kimliğidir, oturumlar --session-dir ile veri
// diskinde yaşar (kap içinde kalsaydı her dağıtım tüm sohbetlerin imlecini
// geçersiz kılardı).
package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/esmworks/OpenAssist/internal/engine"
	"github.com/esmworks/OpenAssist/internal/engine/clipath"
)

type Adapter struct {
	// CLI — pi binary yolu; boşsa clipath arar.
	CLI string
	// ConfigDir — PI_CODING_AGENT_DIR. Eklentiler, ayarlar ve models.json
	// burada yaşar. Boşsa pi'nin varsayılanı (~/.pi/agent) kullanılır.
	ConfigDir string
	// SessionDir — --session-dir; boşsa ConfigDir/sessions.
	SessionDir string
	// Providers — sağlayıcı tariflerini VERİTABANINDAN okuyan yükleyici
	// (göç 00016). Her turda çağrılır: kullanıcı Modeller ekranında bir
	// adres ya da anahtar değiştirdiğinde bir sonraki tur onu görsün,
	// yeniden başlatma gerekmesin.
	//
	// nil ya da boş liste = tarif yok: pi kendi yerleşik kataloğuyla
	// çalışır ve kimliği ortamdan okur (Env).
	Providers func(context.Context) ([]ProviderConfig, error)
	// DefaultModel — TurnInput.Model boşsa kullanılacak model.
	DefaultModel string
	// Thinking — off|minimal|low|medium|high|xhigh|max; boşsa pi seçer.
	Thinking string
	// ExtensionsDir — eklenti kaynağı. Bot allowlist'i bu dizindeki
	// dosyaları adıyla seçer; keşif KAPALIDIR (--no-extensions), yalnız
	// seçilenler -e ile açıkça yüklenir.
	ExtensionsDir string
	// Gateways — oa-gateway eklentisinin bağlanacağı MCP yüzeyleri
	// (çekirdek gateway + Mac runner köprüsü). Eklentiye ortam
	// değişkeniyle geçer; argv'ye yazılsaydı `ps` çıktısında görünürdü.
	//
	// Yüzey adı araç kimliğinin parçasıdır: araçlar modele
	// `mcp__<ad>__<araç>` olarak görünür ve politika motoru, araç
	// sınıfları ve bot allowlist'leri bu adla eşleşir.
	Gateways []Gateway
	// GatewayToken — yüzeylere giden bearer belirteci (iç dinleyici
	// belirteçsizse boş kalır).
	GatewayToken string
	// Env — pi sürecine geçecek ek değişkenler. Özel sağlayıcı tarifi
	// kullanılmadığında pi'nin yerleşik sağlayıcı anahtarları buradan
	// verilir (ANTHROPIC_API_KEY, OPENAI_API_KEY, …).
	Env map[string]string

	// paketOnbellegi — `pi list` sonucunun kısa ömürlü önbelleği. Liste hem
	// katalogda hem her turun eklenti çözümünde okunuyor; her seferinde
	// Node süreci açmak turu yavaşlatırdı.
	paketOnbellegi paketOnbellek
}

// Gateway — tek bir MCP yüzeyi. Name araç önekine girer (mcp__<Name>__…).
type Gateway struct {
	Name string
	URL  string
}

var _ engine.Executor = (*Adapter)(nil)

// turnTimeout — agent_settled beklerken üst sınır yoktur (uzun araç zincirleri
// meşrudur); yalnız kapanış beklemesi sınırlıdır.
const kapanisBeklemesi = 10 * time.Second

func (a *Adapter) SendTurn(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
	bin := a.cli()

	ps, err := a.hazirla(ctx)
	if err != nil {
		return nil, err
	}
	if err := a.modelKontrol(in, ps); err != nil {
		return nil, err
	}

	sessionID := strings.TrimSpace(in.ResumeCursor)
	if sessionID == "" {
		sessionID = uuid.NewString()
	}

	var emitMu sync.Mutex
	syncEmit := func(e engine.Event) {
		emitMu.Lock()
		defer emitMu.Unlock()
		emit(e)
	}

	args, err := a.buildArgs(ctx, in, sessionID)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(bin, args...)
	cmd.Dir = in.CWD
	cmd.Env = a.cleanEnv(bin, ps)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("pi başlatılamadı: %w", err)
	}
	killTree := func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }

	// ctx iptali süreç ağacını indirir; done kaçağı kapatır.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			killTree()
		case <-done:
		}
	}()

	p := &parser{}
	settled := make(chan struct{})
	var settleOnce sync.Once

	conn := newRPCConn(stdin)
	conn.onEvent = func(typ string, line []byte) {
		for _, ev := range p.parseLine(typ, line) {
			syncEmit(ev)
		}
		if p.settled {
			settleOnce.Do(func() { close(settled) })
		}
	}
	conn.onUIRequest = func(req uiRequest) {
		a.handleUI(ctx, conn, req, in, syncEmit)
	}

	// Okuma döngüsü bitince (süreç kapandı) bekleyenler çözülür.
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		conn.readLoop(stdout)
	}()

	// bitir — süreci indirir, REAP EDER ve stderr'i güvenle okur.
	//
	// cmd.Wait() şart: Stderr bir strings.Builder olduğu için os/exec boruyu
	// AYRI bir goroutine'de kopyalar ve o goroutine yalnız Wait ile
	// buluşturulur. Wait'siz okumak strings.Builder üzerinde veri yarışıdır
	// (yarış dedektörü yakalar, üretimde yarım metin döndürür) ve süreç de
	// reap edilmediği için zombi kalırdı.
	bitir := func() string {
		_ = stdin.Close()
		_ = waitWithTimeout(cmd, kapanisBeklemesi, killTree)
		<-readDone
		return stderr.String()
	}

	syncEmit(engine.Event{
		Kind:      engine.KindSessionStarted,
		SessionID: sessionID,
		Model:     a.model(in),
	})

	// Kullanım ölçümünün TABANI. pi'nin sayaçları oturum toplamıdır
	// (docs/rpc.md: "across the full session"); sürdürülen bir oturumda ham
	// toplamı yazmak turun maliyetine tüm geçmişi eklerdi. İki uçtan farkı
	// alınca ölçüm turun kendisi olur. message_update.usage'a güvenmiyoruz:
	// o yalnız SON LLM çağrısının anlık görüntüsü, araç zincirli bir turda
	// aradaki çağrıları saymazdı.
	taban := a.oturumIstatistigi(ctx, conn)

	if _, err := conn.command(ctx, map[string]any{"type": "prompt", "message": in.Prompt}); err != nil {
		killTree()
		msg := fmt.Sprintf("pi promptu kabul etmedi: %s (stderr: %s)",
			piKimlikHatasi(err.Error()), sirlariGizle(kuyruk(bitir(), 800), ps))
		syncEmit(engine.Event{Kind: engine.KindError, Err: msg})
		return nil, errors.New(msg)
	}

	// Turun bitişi: agent_settled. Süreç önce ölürse readDone kapanır ve
	// aşağıdaki dal hatayı üretir.
	select {
	case <-settled:
	case <-readDone:
		msg := fmt.Sprintf("pi tur bitmeden kapandı (stderr: %s)", sirlariGizle(kuyruk(bitir(), 800), ps))
		syncEmit(engine.Event{Kind: engine.KindError, Err: msg})
		return nil, errors.New(msg)
	case <-ctx.Done():
		killTree()
		bitir()
		return nil, ctx.Err()
	}

	finalText := a.sonMetin(ctx, conn)
	kullanim := a.oturumIstatistigi(ctx, conn).eksi(taban)
	bitir()

	res := &engine.TurnResult{
		Ok:           p.errMsg == "",
		StopReason:   "success",
		CostUSD:      kullanim.Cost,
		NumTurns:     p.turns,
		InputTokens:  kullanim.Tokens.Input + kullanim.Tokens.CacheRead + kullanim.Tokens.CacheWrite,
		OutputTokens: kullanim.Tokens.Output,
		FinalText:    finalText,
		ResumeCursor: sessionID,
	}
	if !kullanim.ok {
		// İstatistik alınamadı: akıştan biriken anlık görüntüye düş. Eksik
		// sayar ama sıfır yazmaktan iyidir.
		res.CostUSD = p.usage.Cost.Total
		res.InputTokens = p.usage.Input + p.usage.CacheRead + p.usage.CacheWrite
		res.OutputTokens = p.usage.Output
	}
	if p.errMsg != "" {
		// StopReason kullanıcıya GÖSTERİLEN metindir (orchestrator koşuyu
		// bununla düşürür). Sabit "error" yazmak, elimizde sağlayıcının
		// gerçek mesajı varken kullanıcıya hiçbir şey söylememek olurdu.
		res.StopReason = piKimlikHatasi(p.errMsg)
	}
	return res, nil
}

// oturumIstatistik — get_session_stats yanıtının bize lazım olan kısmı.
type oturumIstatistik struct {
	Tokens struct {
		Input      int64 `json:"input"`
		Output     int64 `json:"output"`
		CacheRead  int64 `json:"cacheRead"`
		CacheWrite int64 `json:"cacheWrite"`
	} `json:"tokens"`
	Cost float64 `json:"cost"`
	// ok — ölçüm gerçekten alındı mı (sıfır ile "bilinmiyor" ayrı şeyler).
	ok bool
}

// eksi — iki ölçümün farkı, yani BU turun tüketimi. Negatife düşmez:
// oturum sıfırlanırsa (compaction, dal değişimi) fark eksi çıkabilir ve
// maliyet defterine eksi satır yazmak sayaçları bozardı.
func (s oturumIstatistik) eksi(taban oturumIstatistik) oturumIstatistik {
	if !s.ok || !taban.ok {
		return oturumIstatistik{}
	}
	out := oturumIstatistik{ok: true}
	out.Tokens.Input = enAzSifir(s.Tokens.Input - taban.Tokens.Input)
	out.Tokens.Output = enAzSifir(s.Tokens.Output - taban.Tokens.Output)
	out.Tokens.CacheRead = enAzSifir(s.Tokens.CacheRead - taban.Tokens.CacheRead)
	out.Tokens.CacheWrite = enAzSifir(s.Tokens.CacheWrite - taban.Tokens.CacheWrite)
	if d := s.Cost - taban.Cost; d > 0 {
		out.Cost = d
	}
	return out
}

func enAzSifir(n int64) int64 {
	if n < 0 {
		return 0
	}
	return n
}

func (a *Adapter) oturumIstatistigi(ctx context.Context, conn *rpcConn) oturumIstatistik {
	data, err := conn.command(ctx, map[string]any{"type": "get_session_stats"})
	if err != nil {
		return oturumIstatistik{}
	}
	var out oturumIstatistik
	if err := json.Unmarshal(data, &out); err != nil {
		return oturumIstatistik{}
	}
	out.ok = true
	return out
}

// sonMetin — turun son asistan metni. Akıştaki delta'ları biriktirmek yerine
// pi'ye soruyoruz: message_end yetkili kaynaktır ve çok bloklu yanıtlarda
// birleştirmeyi pi zaten doğru yapar.
func (a *Adapter) sonMetin(ctx context.Context, conn *rpcConn) string {
	data, err := conn.command(ctx, map[string]any{"type": "get_last_assistant_text"})
	if err != nil {
		return ""
	}
	var out struct {
		Text *string `json:"text"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.Text == nil {
		return ""
	}
	return *out.Text
}

func (a *Adapter) buildArgs(ctx context.Context, in engine.TurnInput, sessionID string) ([]string, error) {
	args := []string{"--mode", "rpc"}

	args = append(args, "--session-dir", a.sessionDir(), "--session-id", sessionID)

	// --model "sağlayıcı/model" desenini kabul ediyor ve sağlayıcıyı kendisi
	// çözüyor (0.84.2'de ölçüldü), o yüzden --provider'a gerek yok: birden
	// çok sağlayıcı tanımlıyken tek bir tanesini sabitlemek yanlış olurdu.
	if m := a.model(in); m != "" {
		args = append(args, "--model", m)
	}
	if a.Thinking != "" {
		args = append(args, "--thinking", a.Thinking)
	}

	// Bot açıklaması + hafıza özeti pi'nin kendi araç-farkında sistem
	// promptunun ÜSTÜNE eklenir. --system-prompt olsaydı taban promptu
	// silinir, model araçlarını tanımaz hale gelirdi.
	if s := strings.TrimSpace(in.SystemPrompt); s != "" {
		args = append(args, "--append-system-prompt", s)
	}

	// AllowedTools BURAYA GİRMEZ. pi'nin --tools bayrağı bir KATALOG
	// filtresidir ("allowlist of tool names to enable", yerleşik araçlara da
	// uygulanır): bot listesini oraya vermek read/bash/edit'i tamamen
	// kapatırdı. Bizde allowlist ÖN-İZİN listesidir — listedeki araç
	// sorulmadan geçer, listede olmayan onay kapısına düşer. Bu ayrım
	// ask.go'da uygulanır.
	if in.NoTools {
		args = append(args, "--no-tools")
	}

	// KEŞİF DÖRT TÜRDE DE KAPALI: bot ne yükleyeceğini açıkça söyler.
	//
	// Yalnız --no-extensions yetmiyor. 0.84.2'de ölçüldü: eklenti keşfi
	// kapalıyken bile ev dizinindeki `~/.agents/skills` altındaki beceriler
	// (bu makinede 69 tane) HER bota yükleniyordu — allowlist'ten geçmeden,
	// hiçbir yere yazılmadan. Beceri "sadece metin" değildir: sistem
	// promptuna girer ve modele istediği eylemi yaptırabilir (pi
	// docs/skills.md kendi güvenlik notunda söylüyor). Şablon ve tema de
	// aynı keşif yolundan geliyor.
	//
	// --skill/-e ile AÇIKÇA verilenler bu bayraklara rağmen yüklenir
	// (ölçüldü) — yani kapatmak seçme hakkını elimizden almıyor.
	//
	// AGENTS.md/CLAUDE.md keşfi (--no-context-files) BİLEREK açık: o,
	// kullanıcının kendi çalışma alanındaki dokümantasyonu ve kodlama
	// ajanının normal davranışı. Kapatmak çalışan kurulumları sessizce
	// değiştirirdi.
	args = append(args, "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes")
	ekArgs, err := a.eklentiArgumanlari(ctx, in.Extensions)
	if err != nil {
		return nil, err
	}
	args = append(args, ekArgs...)

	// Depodaki .pi/ dosyalarına GÜVENİLMEZ: bir çalışma alanı kendi
	// eklentisini enjekte edip botun yetkileriyle kod çalıştırabilirdi.
	// (`--no-approve, -na  Ignore project-local files for this run` — pi
	// 0.84.2 `--help` çıktısından doğrulandı; `--approve` tersidir.)
	args = append(args, "--no-approve")

	return args, nil
}

// eklentiArgumanlari — allowlist girdilerini pi bayraklarına çevirir. Tür
// bayrağı belirler: kod olan her şey -e ile, beceri --skill ile yüklenir.
//
// Paketler -e ile verilir ve pi paketin TAMAMINI (eklenti + beceri + şablon)
// yükler (0.84.2'de ölçüldü) — bu yüzden paket için ayrıca --skill vermek
// gerekmez, verirsek beceri iki kez kaydolurdu.
func (a *Adapter) eklentiArgumanlari(ctx context.Context, list []string) ([]string, error) {
	var args []string
	for _, ad := range list {
		yol, err := a.eklentiYolu(ctx, ad)
		if err != nil {
			return nil, err
		}
		bayrak := "-e"
		if strings.HasPrefix(strings.TrimSpace(ad), beceriOneki) {
			bayrak = "--skill"
		}
		args = append(args, bayrak, yol)
	}
	return args, nil
}

// eklentiYolu — allowlist girdisini mutlak yola çevirir. Dizin dışına çıkan
// girdi (../ ya da mutlak yol) reddedilir: allowlist bir isim listesidir,
// dosya sistemi gezintisi değil.
//
// "npm:<paket>" girdileri KURULU PAKETLERDEN çözülür. Paket yolu tahmin
// edilmez, pi'nin kendi listesinden okunur; `-e npm:<paket>` biçimi de
// kullanılmaz, çünkü pi onu her koşu için GEÇİCİ dizine yeniden indiriyor
// (docs/packages.md) — koşu ortamı çevrimdışı olduğu için bu her turda düşerdi.
//
// Bulunamayan eklenti TURU DÜŞÜRÜR, sessizce atlanmaz. Sebebi güvenlik:
// pi'nin kendi izin penceresi yoktur, onay kapısı oa-gateway eklentisinin
// içindedir. Eksik eklentiyi atlasaydık bot yerleşik bash/write araçlarını
// HİÇ ONAY SORMADAN koşturur ve bu hiçbir yere yazılmazdı.
func (a *Adapter) eklentiYolu(ctx context.Context, ad string) (string, error) {
	ad = strings.TrimSpace(ad)
	if ad == "" {
		return "", errors.New("pi eklenti listesinde boş ad var")
	}
	if strings.HasPrefix(ad, "npm:") {
		return a.paketYolu(ctx, ad)
	}
	if strings.HasPrefix(ad, beceriOneki) {
		return a.beceriYolu(strings.TrimPrefix(ad, beceriOneki))
	}
	if a.ExtensionsDir == "" {
		return "", fmt.Errorf("pi eklentisi %q istendi ama eklenti dizini yapılandırılmamış", ad)
	}
	yol := filepath.Join(a.ExtensionsDir, ad)
	kok := filepath.Clean(a.ExtensionsDir) + string(os.PathSeparator)
	if !strings.HasPrefix(filepath.Clean(yol)+string(os.PathSeparator), kok) {
		return "", fmt.Errorf("pi eklentisi %q eklenti dizininin dışına çıkıyor", ad)
	}
	if _, err := os.Stat(yol); err != nil {
		return "", fmt.Errorf("pi eklentisi %q bulunamadı (%s) — onay kapısı bu eklentide, onsuz koşu açılmaz", ad, a.ExtensionsDir)
	}
	return yol, nil
}

func (a *Adapter) model(in engine.TurnInput) string {
	if in.Model != "" {
		return in.Model
	}
	return a.DefaultModel
}

// modelYok — kullanıcının yapması gereken şeyi söyleyen mesaj. pi'nin kendi
// metni ("No API key found for the selected model. Use /login to log into a
// provider…") bu üründe YANLIŞ YOL GÖSTERİYOR: /login diye bir yer yok,
// gösterdiği docs yolları kabın içinde ve anahtar Modeller ekranından
// giriliyor. Kullanıcının gördüğü ilk cümle doğru yeri göstermeli.
const modelYok = "Ayarlar › Modeller'den bir sağlayıcı (adres + API anahtarı) ve en az bir model ekle"

// modelKontrol — pi'yi hiç açmadan "seçilebilir model yok" hâlini yakalar.
//
// Sebebi teşhis değil, MESAJ: bu durumda pi ne yaparsak yapalım kendi
// kimlik metnini basıyor ve kullanıcı ekranda /login arıyor. Emin
// olamadığımız hiçbir durumda araya girmiyoruz — yalnız model bulunmasının
// kanıtlanabilir şekilde imkânsız olduğu iki hâlde.
func (a *Adapter) modelKontrol(in engine.TurnInput, ps []ProviderConfig) error {
	// (1) Ne tarif var, ne pi'nin yerleşik sağlayıcıları için anahtar
	// (PI_ENV), ne de pi'nin kendi kimlik dosyası (`/login` ile yazılır,
	// yerel Mac kurulumunda olabilir). Motorun seçebileceği tek bir model
	// bile olamaz.
	if modelSayisi(ps) == 0 && len(a.Env) == 0 && !a.piKimlikDosyasiVar() {
		if len(ps) == 0 {
			return errors.New("tanımlı sağlayıcı yok: " + modelYok)
		}
		return errors.New("sağlayıcı tanımlı ama hiç modeli yok: " + modelYok)
	}

	// (2) İstenen model BİZİM bir sağlayıcımıza ait ama o sağlayıcının model
	// listesinde yok. Sağlayıcı bizde değilse karışmıyoruz: pi'nin yerleşik
	// kataloğundan geliyor olabilir.
	m := a.model(in)
	sag, mid, bolundu := strings.Cut(m, "/")
	if !bolundu || sag == "" || mid == "" {
		return nil
	}
	for _, p := range ps {
		if p.ID != sag {
			continue
		}
		for _, mm := range p.Models {
			if mm.ID == mid {
				return nil
			}
		}
		return fmt.Errorf("%q modeli %q sağlayıcısında tanımlı değil: "+
			"Ayarlar › Modeller'den ekle ya da botun modelini değiştir", mid, sag)
	}
	return nil
}

// piKimlikDosyasiVar — pi'nin kendi auth.json'ı (config.js: agentDir/auth.json).
// Konteynerde yoktur ama yerel Mac'te `/login` yapılmış olabilir; o kurulumda
// tarif olmadan da model bulunur, ön kontrol araya girmemeli.
func (a *Adapter) piKimlikDosyasiVar() bool {
	if a.ConfigDir == "" {
		return true // pi varsayılan dizinini kullanıyor: bilemeyiz, karışma
	}
	_, err := os.Stat(filepath.Join(a.ConfigDir, "auth.json"))
	return err == nil
}

func modelSayisi(ps []ProviderConfig) int {
	n := 0
	for _, p := range ps {
		if !p.tanimli() {
			continue
		}
		for _, m := range p.Models {
			if m.ID != "" {
				n++
			}
		}
	}
	return n
}

// sirlariGizle — pi'nin çıktısında sağlayıcı anahtarı GEÇMEMELİ.
//
// Bugün geçmiyor ama bu metin kullanıcıya gösteriliyor VE mesaj olarak
// veritabanına yazılıyor: motorun bir gün anahtarı hata metnine koyması
// (bozuk models.json'ı olduğu gibi basmak, upstream'in isteği yankılaması)
// sessiz ve kalıcı bir sızıntı olurdu. Kısa değerler atlanır — anahtarsız
// sağlayıcıya koyduğumuz "-" her tireyi maskelerdi.
func sirlariGizle(s string, ps []ProviderConfig) string {
	for _, p := range ps {
		if len(p.APIKey) >= 8 {
			s = strings.ReplaceAll(s, p.APIKey, "«gizlendi»")
		}
	}
	return s
}

// piKimlikHatasi — pi'nin kimlik mesajını ürünün diline çevirir.
//
// Ön kontrol her hâli yakalayamaz (anahtarın mührü açılamamış, sağlayıcı
// pi'nin yerleşik kataloğundan geliyor…). O zaman da kullanıcı ilk satırda
// ne yapacağını görsün; pi'nin özgün metni arkada kalır, teşhis kaybolmaz.
func piKimlikHatasi(msg string) string {
	if !strings.Contains(msg, "No API key found") &&
		!strings.Contains(msg, "No model selected") &&
		!strings.Contains(msg, "No models available") {
		return msg
	}
	return "motor kimlikli bir model bulamadı — " + modelYok + ". (pi: " + msg + ")"
}

func (a *Adapter) cli() string {
	if a.CLI != "" {
		return a.CLI
	}
	if p := clipath.Find("pi"); p != "" {
		return p
	}
	return "pi" // bulunamadı — hata mesajı "PATH'te yok" desin
}

func (a *Adapter) sessionDir() string {
	if a.SessionDir != "" {
		return a.SessionDir
	}
	return filepath.Join(a.ConfigDir, "sessions")
}

// hazirla — config dizinini, eklentileri ve sağlayıcı tariflerini yerine
// koyar. Her turun başında koşar.
//
// Tarifleri DÖNDÜRÜR: aynı liste cleanEnv'e verilir. Turda TEK okuma olması
// şart — models.json ile ortam iki ayrı okumadan üretilseydi aralarında
// değişen bir kayıt "dosyada referans var, ortamda değişken yok" hâline
// düşer ve pi "No API key found" derdi.
func (a *Adapter) hazirla(ctx context.Context) ([]ProviderConfig, error) {
	ps, err := a.saglayicilar(ctx)
	if err != nil {
		// models.json'a DOKUNMUYORUZ: oradaki tarif son bilinen iyi hâl.
		// Silmek ya da bayat dosyayla devam etmek, sebebi hiç görünmeyen bir
		// "model yok" hatası üretirdi; sebebi söyleyip durmak dürüst olan.
		return nil, err
	}
	if a.ConfigDir == "" {
		return ps, nil // pi kendi varsayılanıyla çalışır
	}
	for _, d := range []string{a.ConfigDir, a.sessionDir()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, fmt.Errorf("pi dizini kurulamadı (%s): %w", d, err)
		}
	}
	if err := a.eklentileriKur(); err != nil {
		return nil, err
	}
	if err := saglayicilariYaz(a.ConfigDir, ps); err != nil {
		return nil, err
	}
	return ps, nil
}

// saglayicilar — yükleyiciyi çağırır.
//
// Hata YUTULMAZ. Eskiden boş liste dönülüyordu ("pi yerleşik kataloğuyla
// devam etsin") ama sonucu bunun tersiydi: boş liste models.json'ı SİLDİRİR,
// pi'nin elinde kimlikli tek bir model kalmaz ve koşu, DB'nin anlık bir
// hıçkırığından bahsetmeyen bir mesajla düşerdi. Sebep artık yukarı çıkıyor.
func (a *Adapter) saglayicilar(ctx context.Context) ([]ProviderConfig, error) {
	if a.Providers == nil {
		return nil, nil
	}
	ps, err := a.Providers(ctx)
	if err != nil {
		return nil, fmt.Errorf("sağlayıcı tarifleri okunamadı: %w", err)
	}
	return ps, nil
}

func waitWithTimeout(cmd *exec.Cmd, d time.Duration, kill func()) error {
	errc := make(chan error, 1)
	go func() { errc <- cmd.Wait() }()
	select {
	case err := <-errc:
		return err
	case <-time.After(d):
		kill()
		return <-errc
	}
}

// kuyruk — metnin son n baytı. Kesim RUNE SINIRINA çekilir: bayt ortasından
// kesilmiş UTF-8'i Postgres reddediyor (22021) ve o metin koşunun hata
// mesajıdır — kullanıcı sebebi hiç göremezdi (bkz. cmd/core clip).
func kuyruk(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	bas := len(s) - n
	for bas < len(s) && !utf8.RuneStart(s[bas]) {
		bas++
	}
	return "…" + s[bas:]
}

// satirlar — kısa çıktıları satırlara böler (Probe yardımcıları).
func satirlar(s string) []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(s))
	for sc.Scan() {
		if t := strings.TrimSpace(sc.Text()); t != "" {
			out = append(out, t)
		}
	}
	return out
}
