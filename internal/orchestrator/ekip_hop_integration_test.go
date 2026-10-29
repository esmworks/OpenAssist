//go:build integration

// ADIM 10 ikinci aşama — koordinatör dağıtımı: grup kanalında biten koşunun
// cevabındaki @Ad çağrıları SIRADAKİ üyeyi aynı kanalda uyandırır; zincir
// MAX_GROUP_HOPS'ta durur ve sohbete sebebini yazan bir çip düşer.
package orchestrator_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/esmworks/OpenAssist/internal/engine"
	"github.com/esmworks/OpenAssist/internal/engine/enginetest"
	"github.com/esmworks/OpenAssist/internal/orchestrator"
	"github.com/esmworks/OpenAssist/internal/store"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

// ayarlanabilirMotor — davranışı SONRADAN kurulabilen motor. Gerek şu: sahte
// cevap gerçek üye adını içermeli (@Ad çözümü ada bakar), üyeler ise
// orkestratör ayağa kalktıktan sonra yaratılıyor. Üretim koduna test kancası
// eklemek yerine yönlendirmeyi motorun kendisi yapıyor.
type ayarlanabilirMotor struct {
	mu sync.Mutex
	f  enginetest.Func
}

func (m *ayarlanabilirMotor) set(f enginetest.Func) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.f = f
}

func (m *ayarlanabilirMotor) SendTurn(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
	m.mu.Lock()
	f := m.f
	m.mu.Unlock()
	if f == nil {
		return nil, errors.New("test motoru henüz kurulmadı")
	}
	return f(ctx, in, emit)
}

// cagiranMotor — cevabında verilen metni döndüren sahte motor. Hedef adı
// koşunun promptuna göre değişir: kullanıcının ilk mesajı zinciri başlatır,
// devir turları (prompt'ta "ekip sohbeti" geçer) sabit cevap verir.
func cagiranMotor(ilkCevap, devirCevabi string) enginetest.Func {
	return func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		emit(engine.Event{Kind: engine.KindSessionStarted, SessionID: "s", Model: "fake"})
		metin := ilkCevap
		if strings.Contains(in.Prompt, "ekip sohbeti]") {
			metin = devirCevabi
		}
		res := &engine.TurnResult{Ok: true, StopReason: "success", FinalText: metin, NumTurns: 1, ResumeCursor: "c"}
		emit(engine.Event{Kind: engine.KindTurnCompleted, Result: res})
		return res, nil
	}
}

// ekipKur — ekip + sıralı üyeler (ilki koordinatör).
func ekipKur(t *testing.T, st *store.Store, motor string, adlar ...string) (db.Bot, []db.Bot) {
	t.Helper()
	ctx := context.Background()
	ekip, err := st.Q.CreateBot(ctx, db.CreateBotParams{
		Kind: "team", Name: "Ekip " + t.Name(), Engine: motor, AllowedTools: []string{},
	})
	if err != nil {
		t.Fatalf("ekip: %v", err)
	}
	t.Cleanup(func() { _ = st.Q.DeleteBot(context.Background(), ekip.ID) })

	uyeler := make([]db.Bot, 0, len(adlar))
	for i, ad := range adlar {
		b, err := st.Q.CreateBot(ctx, db.CreateBotParams{
			Kind: "bot", Name: ad, Engine: motor, AllowedTools: []string{},
		})
		if err != nil {
			t.Fatalf("üye %s: %v", ad, err)
		}
		t.Cleanup(func() { _ = st.Q.DeleteBot(context.Background(), b.ID) })
		if err := st.Q.AddTeamMember(ctx, db.AddTeamMemberParams{
			TeamID: ekip.ID, MemberID: b.ID, Position: int32(i),
		}); err != nil {
			t.Fatalf("üye ekle: %v", err)
		}
		uyeler = append(uyeler, b)
	}
	return ekip, uyeler
}

// bekleKosu — bot için koşu doğana kadar bekler (devir koşusunu kullanıcı
// açmaz; orkestratör açar, o yüzden id'sini önceden bilmiyoruz).
func bekleKosu(t *testing.T, st *store.Store, botID uuid.UUID) db.Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runs, err := st.Q.ListRunsByBot(context.Background(), db.ListRunsByBotParams{BotID: botID, Limit: 5})
		if err != nil {
			t.Fatalf("ListRunsByBot: %v", err)
		}
		if len(runs) > 0 {
			return runs[0]
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("bot %s için devir koşusu açılmadı", botID)
	return db.Run{}
}

func TestEkipDevriZinciri(t *testing.T) {
	ctx := context.Background()
	motor := &ayarlanabilirMotor{}
	st, o := setup(t, map[string]engine.Executor{"devir": motor}, 0)

	ekip, uyeler := ekipKur(t, st, "devir", "Koordinatör "+t.Name(), "Teklifçi "+t.Name())
	koordinator, teklifci := uyeler[0], uyeler[1]
	motor.set(cagiranMotor("Fiyat kısmı için @"+teklifci.Name+" devralsın.", "Teklif hazır."))

	r1, err := o.EnqueueChat(ctx, ekip, "3E için teklif çıkaralım", "")
	if err != nil {
		t.Fatalf("ekibe mesaj: %v", err)
	}
	if r1.BotID != koordinator.ID {
		t.Fatalf("ilk koşu koordinatöre açılmalıydı: %s", r1.BotID)
	}
	if r1.GroupHop != 0 {
		t.Fatalf("kullanıcı mesajı hop=0 olmalı: %d", r1.GroupHop)
	}
	waitStatus(t, st, r1.ID, "completed")

	// Devir: çağrılan üyeye AYNI kanalda hop=1 koşusu açılır.
	r2 := bekleKosu(t, st, teklifci.ID)
	if r2.GroupHop != 1 {
		t.Fatalf("devir koşusu hop=1 olmalı: %d", r2.GroupHop)
	}
	if !r2.ConversationID.Valid || r2.ConversationID.UUID != r1.ConversationID.UUID {
		t.Fatalf("devir koşusu ekip kanalında değil: %v", r2.ConversationID)
	}
	if r2.Origin != r1.Origin {
		t.Fatalf("devir koşusu kaynak kanalı korumalı: %q ≠ %q", r2.Origin, r1.Origin)
	}
	if !strings.Contains(r2.Prompt, koordinator.Name) || !strings.Contains(r2.Prompt, "devralsın") {
		t.Fatalf("devir promptu çağıranın cevabını taşımalı: %q", r2.Prompt)
	}
	waitStatus(t, st, r2.ID, "completed")

	// Zaman tüneli: iki cevap da EKİP kanalında, kendi bot damgalarıyla.
	conv, err := st.BotConversation(ctx, ekip)
	if err != nil {
		t.Fatalf("ekip sohbeti: %v", err)
	}
	msgs, err := st.Q.ListMessages(ctx, db.ListMessagesParams{ConversationID: conv.ID, Limit: 20})
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	var koordinatorCevabi, teklifciCevabi, fazladanKullanici int
	for _, m := range msgs {
		switch {
		case m.Role == "bot" && m.BotID == koordinator.ID:
			koordinatorCevabi++
		case m.Role == "bot" && m.BotID == teklifci.ID && m.Text == "Teklif hazır.":
			teklifciCevabi++
		case m.Role == "user":
			fazladanKullanici++
		}
	}
	if koordinatorCevabi != 1 || teklifciCevabi != 1 {
		t.Fatalf("ekip tünelinde iki cevap beklenir: koordinatör=%d teklifçi=%d (%+v)",
			koordinatorCevabi, teklifciCevabi, msgs)
	}
	// Devir çağrısı kullanıcı mesajı olarak İKİNCİ kez yazılmamalı: metin
	// zaten koordinatörün balonunda duruyor.
	if fazladanKullanici != 1 {
		t.Fatalf("kanalda tek kullanıcı mesajı olmalı, %d var", fazladanKullanici)
	}
}

func TestEkipDevriSinirdaDurur(t *testing.T) {
	ctx := context.Background()
	motor := &ayarlanabilirMotor{}
	st, o := setup(t, map[string]engine.Executor{"devir-sinir": motor}, 0,
		func(c *orchestrator.Config) { c.MaxGroupHops = 1 })

	ekip, uyeler := ekipKur(t, st, "devir-sinir", "Birinci "+t.Name(), "İkinci "+t.Name())
	birinci, ikinci := uyeler[0], uyeler[1]

	// İki üye birbirini durmadan çağırır: sınır olmasa zincir sonsuza giderdi.
	motor.set(enginetest.Func(
		func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
			emit(engine.Event{Kind: engine.KindSessionStarted, SessionID: "s", Model: "fake"})
			hedef := ikinci.Name
			if strings.Contains(in.Prompt, "ekip sohbeti]") {
				hedef = birinci.Name
			}
			res := &engine.TurnResult{
				Ok: true, StopReason: "success",
				FinalText: "sıra sende @" + hedef, NumTurns: 1, ResumeCursor: "c",
			}
			emit(engine.Event{Kind: engine.KindTurnCompleted, Result: res})
			return res, nil
		}))

	r1, err := o.EnqueueChat(ctx, ekip, "başla", "")
	if err != nil {
		t.Fatalf("ekibe mesaj: %v", err)
	}
	waitStatus(t, st, r1.ID, "completed")
	r2 := bekleKosu(t, st, ikinci.ID)
	waitStatus(t, st, r2.ID, "completed")

	// İkinci üye de çağırıyor ama hop=1 sınırda: üçüncü koşu AÇILMAZ,
	// yerine kanala sebep çipi düşer.
	conv, err := st.BotConversation(ctx, ekip)
	if err != nil {
		t.Fatalf("ekip sohbeti: %v", err)
	}
	var cip string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && cip == "" {
		msgs, err := st.Q.ListMessages(ctx, db.ListMessagesParams{ConversationID: conv.ID, Limit: 20})
		if err != nil {
			t.Fatalf("ListMessages: %v", err)
		}
		for _, m := range msgs {
			if m.Role == "event" && strings.HasPrefix(m.Text, orchestrator.GrupSinirCipi) {
				cip = m.Text
			}
		}
		if cip == "" {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if cip == "" {
		t.Fatal("sınır çipi sohbete düşmedi — kullanıcı zincirin neden durduğunu göremez")
	}
	if !strings.Contains(cip, birinci.Name) {
		t.Fatalf("çip çağrılmayan üyeyi söylemeli: %q", cip)
	}

	// Birinci üyede yalnız ilk koşu olmalı (sınır ikinci turu engelledi).
	runs, err := st.Q.ListRunsByBot(ctx, db.ListRunsByBotParams{BotID: birinci.ID, Limit: 10})
	if err != nil {
		t.Fatalf("ListRunsByBot: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("sınır aşıldı: birinci üyede %d koşu var", len(runs))
	}
}

// Genişlik sınırı: derinlik sınırı tek başına yetmez — bir cevap ekibin
// TAMAMINI çağırabilseydi yedi kişilik ekipte üç devirlik zincir yüzlerce
// koşuya açılırdı. Tek cevap en çok grupHedefAzami üyeyi uyandırır, geri
// kalanı çiple duyurulur.
func TestEkipDevriGenislikSiniri(t *testing.T) {
	ctx := context.Background()
	motor := &ayarlanabilirMotor{}
	st, o := setup(t, map[string]engine.Executor{"devir-genislik": motor}, 0)

	ekip, uyeler := ekipKur(t, st, "devir-genislik",
		"Koor "+t.Name(), "Bir "+t.Name(), "İki "+t.Name(), "Üç "+t.Name(), "Dört "+t.Name())
	cagri := ""
	for _, u := range uyeler[1:] {
		cagri += " @" + u.Name
	}
	motor.set(cagiranMotor("hepiniz bakın:"+cagri, "tamam"))

	r1, err := o.EnqueueChat(ctx, ekip, "dağıt", "")
	if err != nil {
		t.Fatalf("ekibe mesaj: %v", err)
	}
	waitStatus(t, st, r1.ID, "completed")

	conv, err := st.BotConversation(ctx, ekip)
	if err != nil {
		t.Fatalf("ekip sohbeti: %v", err)
	}
	// Çip düşene kadar bekle: devir koşuları koşu bitiminden SONRA açılır.
	var cip string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && cip == "" {
		msgs, err := st.Q.ListMessages(ctx, db.ListMessagesParams{ConversationID: conv.ID, Limit: 20})
		if err != nil {
			t.Fatalf("ListMessages: %v", err)
		}
		for _, m := range msgs {
			if m.Role == "event" && strings.HasPrefix(m.Text, orchestrator.GrupSinirCipi) {
				cip = m.Text
			}
		}
		if cip == "" {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if cip == "" {
		t.Fatal("genişlik sınırı çipi düşmedi")
	}
	if !strings.Contains(cip, uyeler[4].Name) {
		t.Fatalf("çip çağrılmayan üyeyi söylemeli: %q", cip)
	}

	// Dördüncü üyeye koşu AÇILMAMALI; ilk üçüne açılmalı.
	for _, u := range uyeler[1:4] {
		if r := bekleKosu(t, st, u.ID); r.GroupHop != 1 {
			t.Fatalf("%s hop=1 beklenir: %d", u.Name, r.GroupHop)
		}
	}
	runs, err := st.Q.ListRunsByBot(ctx, db.ListRunsByBotParams{BotID: uyeler[4].ID, Limit: 5})
	if err != nil {
		t.Fatalf("ListRunsByBot: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("sınır aşıldı: dördüncü üyede %d koşu var", len(runs))
	}
}
