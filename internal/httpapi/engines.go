package httpapi

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/esmworks/OpenAssist/internal/engine"
)

// probeTTL — yoklama süreç açar; Mac 15 sn'de bir yavaş tur atıyor, her
// turda motoru koşturmak israf olur. Kullanıcı tazelemek isterse
// ?refresh=1 ile bekletmeden yeniler.
const probeTTL = 5 * time.Minute

// engineDTO — kayıtlı bir motor: durumu, sürümü, kimliği ve sunduğu
// modeller. İstemci model listesini buradan alır; kendi listesini gömerse
// motora geçmeyen bir ad seçilebilir ve her koşu düşer (K18).
type engineDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// CLI/durum alanları — Motorlar ekranı bunları gösterir.
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	LoggedIn  bool   `json:"logged_in"`
	Account   string `json:"account,omitempty"`
	Plan      string `json:"plan,omitempty"`
	Detail    string `json:"detail"`
	Probed    bool   `json:"probed"` // false = motor yoklama desteklemiyor

	Models    []engine.ModelInfo `json:"models"`
	CheckedAt time.Time          `json:"checked_at"`
}

// probeCache — motor başına son yoklama. Yoklama süreç açtığı için
// eşzamanlı isteklerde tek seferlik çalışması yeter.
type probeCache struct {
	mu   sync.Mutex
	seen map[string]probeEntry
}

type probeEntry struct {
	status engine.Status
	at     time.Time
}

// status — önbellekten okur, bayatsa (ya da force ise) yeniden yoklar.
func (c *probeCache) status(ctx context.Context, id string, p engine.Prober, force bool, now time.Time) (engine.Status, time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen == nil {
		c.seen = map[string]probeEntry{}
	}
	if e, ok := c.seen[id]; ok && !force && now.Sub(e.at) < probeTTL {
		return e.status, e.at
	}
	e := probeEntry{status: p.Probe(ctx), at: now}
	c.seen[id] = e
	return e.status, e.at
}

func (a *api) listEngines(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("refresh") == "1"
	now := time.Now()

	out := make([]engineDTO, 0, len(a.engines))
	for id, ex := range a.engines {
		d := engineDTO{ID: id, Name: id, Models: []engine.ModelInfo{}, CheckedAt: now}
		// Tanıtmayan motor kimliğiyle ve boş listeyle görünür — yalan
		// söylemektense eksik söyler.
		if desc, ok := ex.(engine.Describer); ok {
			d.Name = desc.DisplayName()
			if m := desc.Models(); m != nil {
				d.Models = m
			}
		}
		if p, ok := ex.(engine.Prober); ok {
			st, at := a.probes.status(r.Context(), id, p, force, now)
			d.Available, d.Version = st.Available, st.Version
			d.LoggedIn, d.Account, d.Plan = st.LoggedIn, st.Account, st.Plan
			d.Detail, d.Probed, d.CheckedAt = st.Detail, true, at
			// Yoklamada ikiliden liste geldiyse elle tutulanın YERİNE geçer:
			// katalog sürümle ve kimlikle değişiyor, gömülü liste bayatlıyor.
			if len(st.Models) > 0 {
				d.Models = st.Models
			}
		} else {
			// Kayıtlı ama yoklanamıyor: kullanılabilir sayılır, durumu
			// bilinmediği açıkça yazılır.
			d.Available = true
			d.Detail = "durum yoklaması desteklenmiyor"
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	writeJSON(w, http.StatusOK, out)
}

// knownEngine — bot'a yazılmadan önceki kapı. Tanınmayan motor DB'ye
// girerse hata koşu anına ertelenir ("motor %q bu sürümde tanınmıyor"):
// kullanıcı ayarda başarı görür, sohbette sessiz düşme yaşar. Motor
// haritası boşsa (iskelet/test kurulumu) doğrulama yapılmaz.
func (a *api) knownEngine(id string) bool {
	if len(a.engines) == 0 {
		return true
	}
	_, ok := a.engines[id]
	return ok
}

// temizle — önbelleği boşaltır. Sağlayıcı/model değişince çağrılır: yoklama
// 5 dk önbellekli olduğu için kullanıcı kaydettiği modeli listede göremezdi.
func (c *probeCache) temizle() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = nil
}
