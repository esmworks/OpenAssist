//go:build integration

// ADIM 13 — ekler iki yönlü:
//
//	GELEN: motorun okuyamayacağı görüntü (HEIC) promptta AÇIKÇA uyarılır —
//	       yoksa model çöp okuyup uydurur.
//	GİDEN: send_file botun kendi kutusundaki dosyayı cevabına ekler; kutu
//	       dışı yol, sembolik bağ kaçışı ve sayı sınırı fail-closed.
package orchestrator_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/esmworks/OpenAssist/internal/engine"
	"github.com/esmworks/OpenAssist/internal/engine/enginetest"
	"github.com/esmworks/OpenAssist/internal/orchestrator"
	"github.com/esmworks/OpenAssist/internal/store"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

// dosyaMotoru — verilen dosyaları çalışma dizinine yazıp send_file ile yollar;
// her çağrının cevabını kanala bildirir (kabul/ret metni ölçülebilsin).
func dosyaMotoru(yazilacak map[string]string, yollar []string, cevaplar chan string) enginetest.Func {
	return func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		emit(engine.Event{Kind: engine.KindSessionStarted, SessionID: "s", Model: "fake"})
		for ad, icerik := range yazilacak {
			if err := os.WriteFile(filepath.Join(in.CWD, ad), []byte(icerik), 0o600); err != nil {
				return nil, err
			}
		}
		for _, y := range yollar {
			ans := in.OnAsk(ctx, engine.Ask{Kind: "file", Path: y})
			cevaplar <- ans.Behavior + ": " + ans.Message
		}
		res := &engine.TurnResult{Ok: true, StopReason: "success", FinalText: "hazır", NumTurns: 1, ResumeCursor: "c"}
		emit(engine.Event{Kind: engine.KindTurnCompleted, Result: res})
		return res, nil
	}
}

func TestSendFileCevabaEklenir(t *testing.T) {
	ctx := context.Background()
	cevaplar := make(chan string, 4)
	st, o := setup(t, map[string]engine.Executor{
		"dosya": dosyaMotoru(map[string]string{"rapor.md": "# Aylık rapor\n"}, []string{"rapor.md"}, cevaplar),
	}, 0)
	bot := mkBot(t, st, "dosya")
	t.Cleanup(func() { _ = st.Q.DeleteBot(context.Background(), bot.ID) })

	run, err := o.EnqueueChat(ctx, bot, "rapor çıkar", "api")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if c := <-cevaplar; !strings.HasPrefix(c, "allow") || !strings.Contains(c, "rapor.md") {
		t.Fatalf("send_file kabul edilmeliydi: %q", c)
	}
	waitStatus(t, st, run.ID, "completed")

	ekler, err := st.Q.ListAttachmentsByRun(ctx, uuid.NullUUID{UUID: run.ID, Valid: true})
	if err != nil || len(ekler) != 1 {
		t.Fatalf("koşunun eki yok: %+v %v", ekler, err)
	}
	ek := ekler[0]
	if ek.Name != "rapor.md" || ek.Size == 0 || ek.BotID != bot.ID {
		t.Errorf("ek satırı: %+v", ek)
	}
	// Dosya KOPYALANMIŞ olmalı: çalışma dizini sonraki koşuda değişebilir.
	if !ek.MessageID.Valid {
		t.Fatal("ek bot mesajına bağlanmadı")
	}
	msg, err := st.Q.GetMessage(ctx, ek.MessageID.UUID)
	if err != nil || msg.Role != "bot" || msg.RunID.UUID != run.ID {
		t.Fatalf("ek yanlış mesajda: %+v %v", msg, err)
	}
}

// Metin üretmeyen ama dosya gönderen tur da mesaj yazmalı: yoksa ek hiçbir
// yüzeyde görünmez (mesajı olmayan ek zaman tünelinde yoktur).
func TestSendFileSessizTurdaDaMesajYazar(t *testing.T) {
	ctx := context.Background()
	cevaplar := make(chan string, 2)
	sessiz := func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		if err := os.WriteFile(filepath.Join(in.CWD, "grafik.png"), []byte("\x89PNG\r\n\x1a\n"), 0o600); err != nil {
			return nil, err
		}
		cevaplar <- in.OnAsk(ctx, engine.Ask{Kind: "file", Path: "grafik.png"}).Behavior
		res := &engine.TurnResult{Ok: true, StopReason: "success", FinalText: "", NumTurns: 1, ResumeCursor: "c"}
		emit(engine.Event{Kind: engine.KindTurnCompleted, Result: res})
		return res, nil
	}
	st, o := setup(t, map[string]engine.Executor{"sessiz": enginetest.Func(sessiz)}, 0)
	bot := mkBot(t, st, "sessiz")
	t.Cleanup(func() { _ = st.Q.DeleteBot(context.Background(), bot.ID) })

	run, err := o.EnqueueChat(ctx, bot, "grafik çiz", "api")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if b := <-cevaplar; b != "allow" {
		t.Fatalf("send_file reddedildi: %q", b)
	}
	waitStatus(t, st, run.ID, "completed")

	ekler, _ := st.Q.ListAttachmentsByRun(ctx, uuid.NullUUID{UUID: run.ID, Valid: true})
	if len(ekler) != 1 || !ekler[0].MessageID.Valid {
		t.Fatalf("sessiz turda ek bağlanmadı: %+v", ekler)
	}
	msg, err := st.Q.GetMessage(ctx, ekler[0].MessageID.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg.Text, "grafik.png") {
		t.Errorf("boş metin yerine dosya adı yazılmalı: %q", msg.Text)
	}
	if ekler[0].Kind != "image" {
		t.Errorf("PNG imzası görüntü olarak saptanmalı: %q", ekler[0].Kind)
	}
}

// Kutu dışı yol, sembolik bağ kaçışı, dizin ve olmayan dosya: hepsi ret.
func TestSendFileKutuDisiRed(t *testing.T) {
	ctx := context.Background()
	disari := filepath.Join(t.TempDir(), "sir.txt")
	if err := os.WriteFile(disari, []byte("gizli"), 0o600); err != nil {
		t.Fatal(err)
	}

	cevaplar := make(chan string, 8)
	kacisMotoru := func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
		// Kutunun İÇİNE dışarıyı gösteren bir bağ kur: yol kontrolü bağı
		// çözmezse bu kapıdan geçerdi (Faz 6 Swift kaçışının aynısı).
		_ = os.Symlink(disari, filepath.Join(in.CWD, "kisayol.txt"))
		_ = os.MkdirAll(filepath.Join(in.CWD, "klasor"), 0o755)
		for _, y := range []string{disari, "kisayol.txt", "../../etc/hosts", "klasor", "yok.txt"} {
			ans := in.OnAsk(ctx, engine.Ask{Kind: "file", Path: y})
			cevaplar <- ans.Behavior + ": " + ans.Message
		}
		res := &engine.TurnResult{Ok: true, StopReason: "success", FinalText: "bitti", NumTurns: 1, ResumeCursor: "c"}
		emit(engine.Event{Kind: engine.KindTurnCompleted, Result: res})
		return res, nil
	}
	st, o := setup(t, map[string]engine.Executor{"kacis": enginetest.Func(kacisMotoru)}, 0)
	bot := mkBot(t, st, "kacis")
	t.Cleanup(func() { _ = st.Q.DeleteBot(context.Background(), bot.ID) })

	run, err := o.EnqueueChat(ctx, bot, "dosyaları yolla", "api")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	for i := 0; i < 5; i++ {
		if c := <-cevaplar; !strings.HasPrefix(c, "deny") {
			t.Errorf("%d. yol reddedilmeliydi: %q", i, c)
		}
	}
	waitStatus(t, st, run.ID, "completed")
	if ekler, _ := st.Q.ListAttachmentsByRun(ctx, uuid.NullUUID{UUID: run.ID, Valid: true}); len(ekler) != 0 {
		t.Fatalf("reddedilen yollar ek yazmamalı: %+v", ekler)
	}
}

// Sayı sınırı: bir koşu sohbeti dosyayla dolduramaz.
func TestSendFileSayiSiniri(t *testing.T) {
	ctx := context.Background()
	yazilacak := map[string]string{}
	var yollar []string
	for i := 0; i <= orchestrator.GidenEkAzami; i++ {
		ad := "dosya" + string(rune('a'+i)) + ".txt"
		yazilacak[ad] = "içerik"
		yollar = append(yollar, ad)
	}
	cevaplar := make(chan string, len(yollar))
	st, o := setup(t, map[string]engine.Executor{
		"cok": dosyaMotoru(yazilacak, yollar, cevaplar),
	}, 0)
	bot := mkBot(t, st, "cok")
	t.Cleanup(func() { _ = st.Q.DeleteBot(context.Background(), bot.ID) })

	run, err := o.EnqueueChat(ctx, bot, "hepsini yolla", "api")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	var kabul, ret int
	for range yollar {
		if strings.HasPrefix(<-cevaplar, "allow") {
			kabul++
		} else {
			ret++
		}
	}
	if kabul != orchestrator.GidenEkAzami || ret != 1 {
		t.Fatalf("sınır tutmadı: %d kabul, %d ret", kabul, ret)
	}
	waitStatus(t, st, run.ID, "completed")
}

// Gelen HEIC: türü "image" ama motor göremez — prompt bunu SÖYLEMELİ.
func TestHeicEkiPromptaUyariDuser(t *testing.T) {
	ctx := context.Background()
	promptlar := make(chan string, 2)
	var dataDir string
	st, o := setup(t, map[string]engine.Executor{
		"prompt": enginetest.Func(func(ctx context.Context, in engine.TurnInput, emit func(engine.Event)) (*engine.TurnResult, error) {
			promptlar <- in.Prompt
			res := &engine.TurnResult{Ok: true, StopReason: "success", FinalText: "ok", NumTurns: 1, ResumeCursor: "c"}
			emit(engine.Event{Kind: engine.KindTurnCompleted, Result: res})
			return res, nil
		}),
	}, 0, func(c *orchestrator.Config) { dataDir = c.DataDir })
	bot := mkBot(t, st, "prompt")
	t.Cleanup(func() { _ = st.Q.DeleteBot(context.Background(), bot.ID) })

	heic := append([]byte{0x00, 0x00, 0x00, 0x18}, []byte("ftypheic")...)
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 'J', 'F', 'I', 'F'}
	heicEk := ekYaz(t, st, dataDir, bot, "iphone.heic", heic)
	jpegEk := ekYaz(t, st, dataDir, bot, "tarayici.jpg", jpeg)

	if _, err := o.EnqueueChatWithKey(ctx, bot, "bunlarda ne yazıyor?", "api", "",
		[]uuid.UUID{heicEk.ID, jpegEk.ID}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	select {
	case p := <-promptlar:
		heicSatiri, jpegSatiri := "", ""
		for _, s := range strings.Split(p, "\n") {
			switch {
			case strings.Contains(s, "iphone.heic"):
				heicSatiri = s
			case strings.Contains(s, "tarayici.jpg"):
				jpegSatiri = s
			}
		}
		if !strings.Contains(heicSatiri, "UYARI") {
			t.Errorf("HEIC satırında uyarı yok: %q", heicSatiri)
		}
		if strings.Contains(jpegSatiri, "UYARI") {
			t.Errorf("okunabilir JPEG uyarı almamalı: %q", jpegSatiri)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("motor turu başlamadı")
	}
}

func ekYaz(t *testing.T, st *store.Store, dataDir string, bot db.Bot, ad string, icerik []byte) db.Attachment {
	t.Helper()
	att, err := st.SaveAttachmentFile(context.Background(), dataDir, bot, ad, bytes.NewReader(icerik))
	if err != nil {
		t.Fatalf("ek yazılamadı (%s): %v", ad, err)
	}
	return att
}
