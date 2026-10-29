package orchestrator

import (
	"sync"

	"github.com/google/uuid"

	"github.com/esmworks/OpenAssist/internal/engine"
)

// pendingAsk — şu anda süreç içinde CEVAP BEKLEYEN istek (kısa-bekleme yolu).
// Park edilmiş istekler burada değil, runs.pending_ask'ta yaşar.
type pendingAsk struct {
	Ask engine.Ask
	ch  chan engine.Answer
}

// approvalHub, çalışan koşuların bekleyen isteklerini API'ye köprüler:
// worker isteği put ile asar, API ResolveApproval ile cevabı düşürür.
// İlk cevap kazanır — ikinci resolve false döner (çoklu kanal yarışı, K).
type approvalHub struct {
	mu      sync.Mutex
	waiting map[uuid.UUID]*pendingAsk
}

func newApprovalHub() *approvalHub {
	return &approvalHub{waiting: map[uuid.UUID]*pendingAsk{}}
}

// put, koşunun bekleyen isteğini kaydeder ve cevap kanalını döner.
func (h *approvalHub) put(runID uuid.UUID, ask engine.Ask) chan engine.Answer {
	h.mu.Lock()
	defer h.mu.Unlock()
	p := &pendingAsk{Ask: ask, ch: make(chan engine.Answer, 1)}
	h.waiting[runID] = p
	return p.ch
}

// drop, isteği (cevaplanmış ya da zaman aşımına uğramış) kaldırır.
func (h *approvalHub) drop(runID uuid.UUID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.waiting, runID)
}

// resolve, bekleyen isteğe cevabı ulaştırır; bekleyen yoksa false.
// Atomik: kaldır + gönder tek kilit altında — iki kanal aynı anda cevaplarsa
// yalnız biri true alır.
func (h *approvalHub) resolve(runID uuid.UUID, ans engine.Answer) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.waiting[runID]
	if !ok {
		return false
	}
	delete(h.waiting, runID)
	p.ch <- ans // tampon 1 — bloklamaz
	return true
}

// pending, koşunun süreç içi bekleyen isteğini döner (UI önizlemesi için).
func (h *approvalHub) pending(runID uuid.UUID) (engine.Ask, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	p, ok := h.waiting[runID]
	if !ok {
		return engine.Ask{}, false
	}
	return p.Ask, true
}
