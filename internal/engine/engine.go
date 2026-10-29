// Package engine, motor-bağımsız Executor kontratını ve normalize olay
// sözlüğünü tanımlar (PRD K18). Adaptör (bugün yalnız pi) motorun ham
// çıktısını bu sözlüğe çevirir; UI, kayıt katmanı ve Telegram motor adı
// bilmez. Kontrat tek adaptörle de duruyor: dört farklı ham şemayı taşıdığı
// için kanıtlanmış durumda, motorun şeması değişse dışarısı kırılmaz.
package engine

import (
	"context"
	"encoding/json"
)

// TurnInput, bir koşu turunun motor-bağımsız girdisi.
type TurnInput struct {
	Prompt       string
	SystemPrompt string // bot açıklaması + hafıza özeti (K15)
	Model        string
	// ResumeCursor opaktır — anlamını yalnız adaptör bilir (pi: oturum
	// kimliği). Boş = yeni oturum. (Ek B.2-3)
	ResumeCursor string
	// AllowedTools ÖN-İZİN listesidir, katalog filtresi DEĞİL: listedeki
	// araç sorulmadan geçer, listede olmayan onay kapısına düşer (kullanıcı
	// hâlâ izin verebilir). Aracı büsbütün görünmez kılmak için NoTools var.
	//
	// Ayrım önemli: pi'nin --tools bayrağı katalog filtresidir ve bot
	// listesini oraya vermek yerleşik read/bash/edit'i de kapatırdı; ön-izin
	// bu yüzden adaptörün onay yolunda uygulanır.
	AllowedTools []string
	// NoTools — tur ARAÇSIZ koşar: motora hiçbir MCP yüzeyi takılmaz ve
	// gömülü araçlar kapatılır. Boş AllowedTools ile aynı şey DEĞİL: boş liste
	// yalnız ön-izin listesini boşaltır, katalog yine görünür kalır ve model
	// her denemede bir tur harcar. Damıtma turları (gece hafıza
	// konsolidasyonu) için: girdi promptta, çıktı metinde.
	NoTools        bool
	PermissionMode string // default | acceptEdits | plan
	CWD            string
	// Extensions — bu tur yüklenecek pi eklentileri (global eklenti
	// dizinindeki dosya/dizin adları). Boş = yalnız çekirdek araçlar.
	//
	// Keşif bilinçli olarak KAPALIDIR: global dizine düşen bir eklenti
	// kendiliğinden tüm botlara bulaşmaz, her bot ne yükleyeceğini
	// açıkça söyler. Eklentiler botun tüm yetkileriyle kod çalıştırır.
	Extensions []string
	// OnAsk, onay/soru isteklerini karşılar; nil ise her istek reddedilir
	// (fail-closed).
	OnAsk AskHandler
}

// Ask — motordan gelen izin veya soru isteği (request.opened karşılığı).
// JSON etiketleri: runs.pending_ask'ta bu haliyle saklanır, API'den bu
// haliyle okunur.
type Ask struct {
	Kind     string          `json:"kind"` // "permission" | "question" | "bot_message" | "file"
	Tool     string          `json:"tool,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"` // önizleme/denetim için
	Question string          `json:"question,omitempty"`
	Choices  []string        `json:"choices,omitempty"` // Telegram inline keyboard / Swift kart
	// Suggestions — CLI'ın önerdiği izin kuralları; "hep izin ver" cevabında
	// aynen geri verilir, kural sözdizimi icat edilmez (Ek B.3).
	Suggestions json.RawMessage `json:"suggestions,omitempty"`
	// AllowAlways — bu istek için kullanıcıya "hep izin ver" SUNULABİLİR mi.
	// policy.Decide'ın kararıdır (AllowAlwaysRule): yüksek risk sınıfı,
	// tainted koşu, kota ve sessiz saat vetoları bunu kapatır. İstemci kendi
	// hesaplamasın diye ask ile taşınıyor — aksi hâlde kural mantığı iki
	// yerde yaşar ve sessizce ayrışır.
	AllowAlways bool `json:"allow_always,omitempty"`
	// Devir alanları (ADIM 6, kind="bot_message"): hedef bot ve mesaj.
	// Yalnız run-bağlamlı kanaldan gelir (pi oa-gateway eklentisi) — bu yüzden
	// kaynak bot/koşu bilgisi güvenilirdir; istemci bu yolu taklit edemez.
	BotID   string `json:"bot_id,omitempty"`
	BotName string `json:"bot_name,omitempty"`
	Message string `json:"message,omitempty"`
	// Giden dosya alanları (ADIM 13, kind="file"): motorun kendi çalışma
	// dizininde ürettiği dosyanın yolu ve kullanıcıya görünecek adı. Aynı
	// güven gerekçesi: koşu bağlamı bu kanaldan gelir, dosya da o koşunun
	// botunun kutusuyla sınırlanır.
	Path string `json:"path,omitempty"`
	Name string `json:"name,omitempty"`
}

// Answer — Ask'ın cevabı.
type Answer struct {
	Behavior string // "allow" | "deny"; sorularda boş bırakılır, Message cevaptır
	Message  string // deny gerekçesi ya da soru cevabı
	Always   bool   // "hep izin ver" — Suggestions updatedPermissions olarak döner
	// Source — kararın kanalı: rule | api | grant | timeout | policy.
	// request.resolved olayına işlenir (Ek B.2-1c, çoklu kanal onay kaydı).
	Source string
}

// AskHandler bloklayıcıdır: kısa-bekleme penceresi burada işletilir; pencere
// dolarsa park kararı deny("park") olarak döner ve koşu approval_pending'e
// geçer (K15 park/resume).
type AskHandler func(ctx context.Context, ask Ask) Answer

// TurnResult, turn.completed olayının gövdesi.
type TurnResult struct {
	Ok           bool
	StopReason   string // success | error_max_turns | …
	CostUSD      float64
	NumTurns     int
	InputTokens  int64
	OutputTokens int64
	FinalText    string
	// ResumeCursor — bir sonraki tur için saklanacak opak imleç.
	ResumeCursor string
}

// Executor — her motor adaptörünün uyduğu kontrat (K18). Olaylar emit ile
// sırayla akar; dönüş değeri turun özeti. emit çağrıları tek goroutine'den
// gelir, sıra garantilidir.
type Executor interface {
	SendTurn(ctx context.Context, in TurnInput, emit func(Event)) (*TurnResult, error)
}

// ModelInfo — motorun kabul ettiği bir model seçeneği. ID motora AYNEN
// geçer (Claude Code'da --model değeri), Label kullanıcıya görünür.
// Boş ID = "varsayılan": bayrak hiç verilmez, seçimi motor yapar.
type ModelInfo struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Describer — motorun kendini tanıtması: görünen ad + kabul ettiği model
// listesi. UI bu listeyi gösterir, kendi listesini UYDURMAZ — motora
// geçmeyen bir model adı (ör. başka sağlayıcının modeli) seçilirse her koşu
// düşer. Executor'a eklenmedi: zorunlu olsaydı test sahteleri de doldurmak
// zorunda kalırdı; uygulamayan motor ad olarak kimliğiyle, boş model
// listesiyle görünür (K18 — yetenek matrisi dürüst tutulur).
type Describer interface {
	DisplayName() string
	Models() []ModelInfo
}

// Status — motorun o anki hali: kurulu mu, hangi sürüm, kimlik var mı.
// Motorlar ekranı bunu gösterir; amaç koşu düşmeden ÖNCE görmek ("bulunamadı"
// / "kimlik yok" satırı, ilk mesajı yazmadan okunur).
type Status struct {
	Available bool   `json:"available"`         // binary çalışıyor mu
	Version   string `json:"version,omitempty"` // motorun bildirdiği sürüm
	LoggedIn  bool   `json:"logged_in"`
	Account   string `json:"account,omitempty"` // e-posta / hesap
	Plan      string `json:"plan,omitempty"`    // "Claude Max aboneliği" gibi
	// Detail — kullanıcıya gösterilecek tek satır. Metni motor kurar:
	// nedenini en iyi o bilir, istemci sözlük taşımaz.
	Detail string `json:"detail"`
	// Models — yoklama sırasında İKİLİDEN öğrenilen model listesi; doluysa
	// Describer.Models()'in yerine geçer.
	//
	// Neden burada: elle tutulan liste bayatlıyor ve bayatladığı fark
	// edilmiyor — bir motorun kataloğu değişince ekrandaki altı seçeneğin
	// altısı da geçersizdi, her seçim koşuyu düşürürdü (opencode'da ölçüldü).
	// Üstelik liste kimliğe bağlı (aynı ikili anahtarsız 8, anahtarla 79
	// model veriyordu),
	// yani yalnız çalışma anında bilinebilir. Yoklama zaten 5 dk
	// önbelleklendiği için ek maliyet yok.
	Models []ModelInfo `json:"models,omitempty"`
}

// Prober — motorun kendini yoklaması. Opsiyonel: uygulamayan motor "durum
// yoklaması desteklenmiyor" satırıyla görünür. Yoklama süreç açar; çağıran
// ÖNBELLEKLEMELİDİR (her istekte koşturulmaz).
type Prober interface {
	Probe(ctx context.Context) Status
}

// ExtensionInfo — motorun yükleyebileceği bir eklenti.
//
// ID, bots.extensions allowlist'ine yazılan addır ve KİMLİKTİR: görünen ad
// değişse de o sabit kalır, çünkü koşu anında motora verilen şey odur.
type ExtensionInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Kind — eklentinin kaynağı: "builtin" (ikiliye gömülü, kaldırılamaz),
	// "file" (eklenti dizinine düşmüş dosya), "npm" (paket kaydından gelen).
	// Kaldırılabilirliği istemci buradan bilir; ayrı bir bayrak eklemek
	// aynı gerçeği iki yerde tutmak olurdu.
	Kind    string `json:"kind"`
	Version string `json:"version,omitempty"`
	// Resources — bu girdinin motora GERÇEKTEN ne taşıdığı:
	// "extension" (kod), "skill" (modele talimat), "prompt", "theme".
	// Bir paket birden fazlasını taşıyabilir ve hepsi tek allowlist
	// girdisiyle yüklenir; kullanıcı neyi açtığını görebilmeli çünkü
	// beceri de en az eklenti kadar iş yaptırır.
	Resources []string `json:"resources"`
}

// Extender — motorun KURULU eklenti kataloğunu tanıtması. Describer/Prober
// ile aynı sebeple opsiyoneldir: Executor'a eklenseydi eklenti kavramı
// olmayan motorlar da boş bir uygulama yazmak zorunda kalırdı.
//
// Liste "kurulu olanı" söyler, "botun kullandığını" değil — o seçim bot
// başına allowlist'tedir (TurnInput.Extensions).
type Extender interface {
	Extensions(ctx context.Context) ([]ExtensionInfo, error)
}

// ExtensionManager — eklenti kurma/kaldırma. Extender'dan AYRI arayüz:
// listelemek okuma, kurmak ise sunucuda üçüncü parti kod indirip
// çalıştırmaktır. Aynı arayüzde dursalardı katalog gösterebilen her motor
// istemeden kurulum yetkisi de tanımış olurdu.
type ExtensionManager interface {
	// InstallExtension — kaynak dizesi motorun kendi sözdizimidir (pi:
	// "npm:paket@sürüm"). Doğrulama motorda yapılır: kabul edilebilir
	// kaynağın ne olduğunu en iyi o bilir.
	InstallExtension(ctx context.Context, source string) error
	// RemoveExtension — ExtensionInfo.ID ile kaldırır.
	RemoveExtension(ctx context.Context, id string) error
}
