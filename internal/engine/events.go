package engine

import "encoding/json"

// Kind — normalize olay türleri (PRD K15/K18, Ek B.2-1'den zenginleştirildi).
type Kind string

const (
	KindSessionStarted  Kind = "session.started"
	KindAssistantDelta  Kind = "content.delta"  // akan metin parçası
	KindAssistantText   Kind = "assistant.text" // tamamlanmış metin bloğu
	KindToolStarted     Kind = "tool.started"
	KindToolCompleted   Kind = "tool.completed"
	KindRequestOpened   Kind = "request.opened"   // permission | question
	KindRequestResolved Kind = "request.resolved" // behavior + kaynak kanal
	KindTurnCompleted   Kind = "turn.completed"
	KindError           Kind = "runtime.error"
	// KindUnknown — tanınmayan motor çıktısı. Motor çıktısı BİZİM kontratımız
	// değildir: bilinmeyen satır koşuyu kırmaz, ham haliyle kayda geçer
	// (docs/design sınır kuralı).
	KindUnknown Kind = "unknown"
)

// Event — tek normalize olay. Alanlar türe göre dolar; Raw her zaman motorun
// ham satırını taşır (kayıp yaşatmayan geçiş, Ek B.2-1c).
type Event struct {
	Kind      Kind
	SessionID string
	Model     string          // session.started
	Text      string          // content.delta / assistant.text
	Tool      *ToolInfo       // tool.started / tool.completed
	Request   *RequestInfo    // request.opened / request.resolved
	Result    *TurnResult     // turn.completed
	Err       string          // runtime.error
	Raw       json.RawMessage // motorun ham satırı
}

type ToolInfo struct {
	ID      string
	Name    string
	Input   json.RawMessage // tool.started
	IsError bool            // tool.completed
}

type RequestInfo struct {
	Kind     string // "permission" | "question"
	Tool     string
	Question string
	Choices  []string
	// Çözümde:
	Behavior string // "allow" | "deny" | "answered"
	Source   string // cevabın geldiği kanal (mac | telegram | timeout | …) — Ek B.2-1c
}
