// Package bus — süreç içi koşu olayı yayını. v1 tek core süreci olduğu için
// (K: tek servis) canlı SSE aboneliği in-memory pub/sub ile çözülür; kalıcı
// gerçek her zaman run_events tablosudur, bus yalnız "şu an bağlı" istemciler
// içindir.
package bus

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"
)

// KindRunSettled — koşunun durumu VERİTABANINA YAZILDIKTAN sonra yayınlanan
// kapanış işareti (payload: {"status":"completed"}). Motorun turn.completed
// olayı durum yazımından ÖNCE akar; canlı istemciler "akış bitti mi" kararını
// bu olaya bakarak verir. Kalıcı değildir — gerçek her zaman runs.status'tur.
const KindRunSettled = "run.settled"

type Event struct {
	// ID — run_events.id; kalıcılaştırılmayan olaylarda (content.delta) 0.
	ID      int64           `json:"id,omitempty"`
	RunID   uuid.UUID       `json:"run_id"`
	Kind    string          `json:"kind"`
	Payload json.RawMessage `json:"payload,omitempty"`
	At      time.Time       `json:"at"`
}

type Bus struct {
	mu   sync.RWMutex
	subs map[uuid.UUID]map[chan Event]struct{}
}

func New() *Bus {
	return &Bus{subs: map[uuid.UUID]map[chan Event]struct{}{}}
}

// Subscribe, koşunun canlı olaylarına abone olur. Kanal 256 tamponludur;
// yavaş istemcide olay DÜŞÜRÜLÜR (yayın asla bloklanmaz) — istemci SSE
// Last-Event-ID ile kalıcı olaylardan tamamlar.
func (b *Bus) Subscribe(runID uuid.UUID) (<-chan Event, func()) {
	ch := make(chan Event, 256)
	b.mu.Lock()
	if b.subs[runID] == nil {
		b.subs[runID] = map[chan Event]struct{}{}
	}
	b.subs[runID][ch] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs[runID], ch)
			if len(b.subs[runID]) == 0 {
				delete(b.subs, runID)
			}
			b.mu.Unlock()
			close(ch)
		})
	}
	return ch, cancel
}

func (b *Bus) Publish(ev Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs[ev.RunID] {
		select {
		case ch <- ev:
		default: // yavaş istemci: düşür, bloklama
		}
	}
}
