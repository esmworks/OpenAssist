package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/esmworks/OpenAssist/internal/engine"
)

// sessizMotor — Describer/Prober uygulamayan adaptör; "tanıtmayan motor"
// yolunu kapsar.
type sessizMotor struct{}

func (sessizMotor) SendTurn(context.Context, engine.TurnInput, func(engine.Event)) (*engine.TurnResult, error) {
	return &engine.TurnResult{}, nil
}

// sahteMotor — gerçek CLI'ı çağırmadan tanıtma + yoklama. Testler süreç
// açmaz: CI'da `claude` yok, olsa bile yoklama ağa çıkardı.
type sahteMotor struct {
	sessizMotor
	st    engine.Status
	calls int
}

func (m *sahteMotor) DisplayName() string { return "Sahte Motor" }
func (m *sahteMotor) Models() []engine.ModelInfo {
	return []engine.ModelInfo{{ID: "", Label: "Varsayılan"}, {ID: "hizli", Label: "Hızlı"}}
}

func (m *sahteMotor) Probe(context.Context) engine.Status {
	m.calls++
	return m.st
}

func getEngines(t *testing.T, a *api, query string) []engineDTO {
	t.Helper()
	rec := httptest.NewRecorder()
	a.listEngines(rec, httptest.NewRequest(http.MethodGet, "/api/engines"+query, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("beklenen 200, geldi %d", rec.Code)
	}
	var got []engineDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("çözülemedi: %v — gövde: %s", err, rec.Body)
	}
	return got
}

func TestListEnginesTanitiyor(t *testing.T) {
	m := &sahteMotor{st: engine.Status{
		Available: true, Version: "2.1.232", LoggedIn: true,
		Account: "biri@ornek.com", Plan: "Claude Max aboneliği",
		Detail: "biri@ornek.com · Claude Max aboneliği",
	}}
	a := &api{engines: map[string]engine.Executor{"sahte": m}}

	got := getEngines(t, a, "")
	if len(got) != 1 {
		t.Fatalf("bir motor bekleniyordu, geldi: %+v", got)
	}
	e := got[0]
	if e.ID != "sahte" || e.Name != "Sahte Motor" {
		t.Errorf("kimlik/ad yanlış: %+v", e)
	}
	if !e.Available || !e.LoggedIn || !e.Probed || e.Version != "2.1.232" {
		t.Errorf("durum taşınmadı: %+v", e)
	}
	if e.Detail == "" {
		t.Error("durum satırı boş — ekran neden çalışmadığını söyleyemez")
	}
	// Boş kimlikli "varsayılan" seçeneği şart: bayrak verilmeyen hali de
	// seçilebilir olmalı (motor kendi seçer).
	var varsayilan bool
	for _, mi := range e.Models {
		if mi.ID == "" {
			varsayilan = true
		}
		if mi.Label == "" {
			t.Errorf("etiketsiz model: %+v", mi)
		}
	}
	if !varsayilan {
		t.Error("boş kimlikli varsayılan seçeneği yok")
	}
}

// Motor kurulu değilse ekran bunu koşu denenmeden söylemeli.
func TestListEnginesKurulmamisMotor(t *testing.T) {
	m := &sahteMotor{st: engine.Status{Detail: `bulunamadı — "claude" PATH'te yok`}}
	a := &api{engines: map[string]engine.Executor{"sahte": m}}

	e := getEngines(t, a, "")[0]
	if e.Available || e.LoggedIn {
		t.Errorf("kurulu olmayan motor kullanılabilir görünüyor: %+v", e)
	}
	if e.Detail == "" {
		t.Error("neden bulunamadığı yazılmalı")
	}
	// Model listesi yine dolu: motor kurulunca seçim hazır olsun.
	if len(e.Models) == 0 {
		t.Error("model listesi yoklamadan bağımsızdır")
	}
}

// Yoklama süreç açar: her istekte değil, yalnız ?refresh=1 deyince koşar.
func TestListEnginesYoklamaOnbellegi(t *testing.T) {
	m := &sahteMotor{st: engine.Status{Available: true}}
	a := &api{engines: map[string]engine.Executor{"sahte": m}}

	getEngines(t, a, "")
	getEngines(t, a, "")
	if m.calls != 1 {
		t.Errorf("önbellek tutmadı, yoklama %d kez koştu", m.calls)
	}
	getEngines(t, a, "?refresh=1")
	if m.calls != 2 {
		t.Errorf("refresh=1 yoklamayı zorlamalıydı, toplam: %d", m.calls)
	}
}

func TestProbeCacheTTLDolunca(t *testing.T) {
	m := &sahteMotor{st: engine.Status{Available: true}}
	var c probeCache
	t0 := time.Now()

	c.status(context.Background(), "sahte", m, false, t0)
	c.status(context.Background(), "sahte", m, false, t0.Add(probeTTL-time.Second))
	if m.calls != 1 {
		t.Fatalf("TTL içinde yeniden yoklandı: %d", m.calls)
	}
	_, at := c.status(context.Background(), "sahte", m, false, t0.Add(probeTTL+time.Second))
	if m.calls != 2 {
		t.Errorf("TTL dolunca yeniden yoklanmalıydı: %d", m.calls)
	}
	if at.Equal(t0) {
		t.Error("yoklama zamanı tazelenmedi — ekran bayat zaman gösterir")
	}
}

// Tanıtmayan motor da listede görünmeli — eksik söylemek, gizlemekten iyidir.
func TestListEnginesTanitmayanMotor(t *testing.T) {
	a := &api{engines: map[string]engine.Executor{"suskun": sessizMotor{}}}

	got := getEngines(t, a, "")
	if len(got) != 1 || got[0].ID != "suskun" || got[0].Name != "suskun" {
		t.Fatalf("kimlik ada düşmeliydi: %+v", got)
	}
	if got[0].Probed {
		t.Error("yoklanmayan motor yoklandı görünüyor")
	}
	// null değil boş dizi: istemci tarafında nil kontrolü gerekmesin.
	if got[0].Models == nil || len(got[0].Models) != 0 {
		t.Errorf("boş model listesi bekleniyordu, geldi: %+v", got[0].Models)
	}
}

func TestKnownEngine(t *testing.T) {
	a := &api{engines: map[string]engine.Executor{"claudecode": sessizMotor{}}}
	if !a.knownEngine("claudecode") {
		t.Error("kayıtlı motor tanınmalı")
	}
	if a.knownEngine("opencode") {
		t.Error("kayıtsız motor reddedilmeli — yoksa hata koşu anına ertelenir")
	}

	// Motor haritası verilmemişse (iskelet/test kurulumu) doğrulama yapılmaz;
	// aksi halde her bot yazımı 400 döner.
	bos := &api{}
	if !bos.knownEngine("her ne olursa") {
		t.Error("motor haritası boşken doğrulama yapılmamalı")
	}
}
