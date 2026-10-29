package pi

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/esmworks/OpenAssist/internal/engine"
)

// askMarker — OpenAssist eklentisinin onay kapısını üçüncü parti eklenti
// diyaloglarından ayıran işaret.
//
// pi'nin eklenti UI protokolünde diyalog metotlarının sabit alanları vardır
// (title/message/options/placeholder); yapısal yük taşıyacak alan yoktur.
// Onay kartı ise araç adı, ham girdi ve kural önerilerini ister. Bu yüzden
// KENDİ eklentimiz `input` metodunu zarf olarak kullanır: title işaret,
// placeholder yükün JSON'u, cevabımız da value içinde JSON. `input` seçildi
// çünkü serbest metin döndürür — `select`'in aksine pi'nin değeri bir seçenek
// listesine göre doğrulama ihtimali yoktur.
//
// Üçüncü parti eklentiler bu işareti bilmez; onlar aşağıdaki genel eşlemeden
// geçer ve yine de onay kapısına düşer.
const askMarker = "oa-ask"

// askEnvelope — eklentiden gelen yapısal istek (placeholder içinde JSON).
type askEnvelope struct {
	Kind        string          `json:"kind"` // permission | question | bot_message | file
	Tool        string          `json:"tool"`
	Input       json.RawMessage `json:"input"`
	Question    string          `json:"question"`
	Choices     []string        `json:"choices"`
	Suggestions json.RawMessage `json:"suggestions"`
	BotID       string          `json:"bot_id"`
	BotName     string          `json:"bot_name"`
	Message     string          `json:"message"`
	Path        string          `json:"path"`
	Name        string          `json:"name"`
}

// askReply — zarf cevabımız (value içinde JSON). Answer'ın tel karşılığı.
type askReply struct {
	Behavior string `json:"behavior"` // allow | deny
	Message  string `json:"message"`
	Always   bool   `json:"always"`
}

// handleUI — tek bir eklenti diyaloğunu karşılar. Kendi goroutine'inde koşar
// (rpcConn.dispatch böyle çağırır): onay dakikalarca bekleyebilir.
func (a *Adapter) handleUI(ctx context.Context, c *rpcConn, req uiRequest, in engine.TurnInput, emit func(engine.Event)) {
	switch req.Method {
	case "notify", "setStatus", "setWidget", "setTitle", "set_editor_text":
		// Tek yönlü: cevap beklenmiyor. Bunlar TUI süsü; başsız koşuda
		// karşılığı yok, sessizce yutulur.
		return
	}

	ask, structured := zarfCoz(req)
	if ask.Kind == "" {
		ask.Kind = "question"
	}

	// ÖN-İZİN: botun allowed_tools listesindeki araç sorulmadan geçer.
	//
	// Bu liste motora bayrak olarak VERİLMEZ (pi'nin --tools'u katalog
	// filtresidir, ön-izin değil — bkz. buildArgs), o yüzden ayrım burada
	// uygulanır. Kart da açılmaz: listedeki araç kullanıcının zaten bir kez
	// verdiği karardır, her çağrıda tekrar sormak onay yorgunluğu üretir.
	if ask.Kind == "permission" && izinliArac(in.AllowedTools, ask.Tool) {
		_ = c.answerUI(req.ID, cevapAlanlari(req.Method, structured,
			engine.Answer{Behavior: "allow", Source: "allowlist"}))
		return
	}

	// Devir (bot_message) ve dosya gönderimi (file) onay kartı DEĞİLDİR:
	// kullanıcıya istek düşmez, sonucu sohbette görür — claudecode
	// brokerıyla aynı kural.
	devirMi := ask.Kind == "bot_message" || ask.Kind == "file"
	if !devirMi {
		emit(engine.Event{Kind: engine.KindRequestOpened, Request: &engine.RequestInfo{
			Kind: ask.Kind, Tool: ask.Tool, Question: ask.Question, Choices: ask.Choices,
		}})
	}

	// İşleyici yoksa fail-closed: her istek reddedilir (engine.TurnInput
	// sözleşmesi).
	ans := engine.Answer{Behavior: "deny", Message: "OpenAssist: onay işleyicisi yok"}
	if in.OnAsk != nil {
		ans = in.OnAsk(ctx, ask)
	}

	if !devirMi {
		req := &engine.RequestInfo{
			Kind: ask.Kind, Tool: ask.Tool, Question: ask.Question, Choices: ask.Choices,
			Behavior: ans.Behavior, Source: ans.Source,
		}
		if ask.Kind == "question" {
			req.Behavior = "answered"
		}
		emit(engine.Event{Kind: engine.KindRequestResolved, Request: req})
	}

	_ = c.answerUI(req.ID, cevapAlanlari(req.Method, structured, ans))
}

// izinliArac — araç botun ön-izin listesinde mi. Boş araç adı (üçüncü parti
// eklentinin jenerik confirm'i) ASLA eşleşmez: adı olmayan bir isteği
// listeye dayanarak geçirmek, listeyi anlamsız kılardı.
func izinliArac(izinli []string, arac string) bool {
	if arac == "" {
		return false
	}
	for _, t := range izinli {
		if t == arac {
			return true
		}
	}
	return false
}

// zarfCoz — diyaloğu engine.Ask'e çevirir. İkinci dönüş: yapısal zarf mıydı.
func zarfCoz(req uiRequest) (engine.Ask, bool) {
	if req.Method == "input" && req.Title == askMarker {
		var env askEnvelope
		if err := json.Unmarshal([]byte(req.Placeholder), &env); err == nil {
			return engine.Ask{
				Kind: env.Kind, Tool: env.Tool, Input: env.Input,
				Question: env.Question, Choices: env.Choices, Suggestions: env.Suggestions,
				BotID: env.BotID, BotName: env.BotName, Message: env.Message,
				Path: env.Path, Name: env.Name,
			}, true
		}
		// Bozuk zarf: genel eşlemeye düş, istek yine de kullanıcıya gitsin.
	}

	// Üçüncü parti eklenti diyalogları.
	soru := strings.TrimSpace(strings.Join([]string{req.Title, req.Message}, "\n"))
	switch req.Method {
	case "confirm":
		// Evet/hayır kapısı — izin sorusu gibi davranır.
		return engine.Ask{Kind: "permission", Question: soru}, false
	case "select":
		return engine.Ask{Kind: "question", Question: soru, Choices: req.Options}, false
	default: // input | editor
		if soru == "" {
			soru = req.Placeholder
		}
		return engine.Ask{Kind: "question", Question: soru}, false
	}
}

// cevapAlanlari — Answer'ı method'un beklediği alanlara çevirir.
func cevapAlanlari(method string, structured bool, ans engine.Answer) map[string]any {
	if structured {
		reply, err := json.Marshal(askReply{
			Behavior: ans.Behavior, Message: ans.Message, Always: ans.Always,
		})
		if err != nil {
			return map[string]any{"cancelled": true}
		}
		return map[string]any{"value": string(reply)}
	}

	switch method {
	case "confirm":
		return map[string]any{"confirmed": ans.Behavior == "allow"}
	case "select":
		// Reddedilen seçim = iptal: eklenti kendi varsayılanına düşsün,
		// listede olmayan bir değeri yutmak zorunda kalmasın.
		if ans.Message == "" {
			return map[string]any{"cancelled": true}
		}
		return map[string]any{"value": ans.Message}
	default: // input | editor
		if ans.Behavior == "deny" && ans.Message == "" {
			return map[string]any{"cancelled": true}
		}
		return map[string]any{"value": ans.Message}
	}
}
