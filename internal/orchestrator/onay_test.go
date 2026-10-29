package orchestrator

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/esmworks/OpenAssist/internal/engine"
)

func komut(c string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"command": c})
	return b
}

// Park edildikten SONRAKİ istekler pencereyi yeniden harcamamalı.
//
// Sahada ölçülen hata buydu: motor reddedilen komuttan sonra alternatif
// deniyor (curl → node -e fetch → command -v node wget busybox), her deneme
// 60 saniyelik kısa-bekleme penceresini baştan harcıyordu. Kullanıcı on
// dakika boyunca ekranda hiçbir şey olmadığını izledi. Park kararı TURUN
// TAMAMI içindir — cevap geldiğinde koşu yeni bir turda devam eder.
func TestParktanSonraBeklenmez(t *testing.T) {
	const pencere = 400 * time.Millisecond
	o := &Orchestrator{hub: newApprovalHub(), cfg: Config{ApprovalWait: pencere}}
	runID := uuid.New()
	park := &parkKutusu{}

	ilk := engine.Ask{Kind: "permission", Tool: "bash", Input: komut("curl …")}
	bas := time.Now()
	ans := o.waitForHuman(context.Background(), runID, ilk, park)
	if gecen := time.Since(bas); gecen < pencere {
		t.Fatalf("ilk istek beklemedi (%s) — kısa-bekleme yolu kırılmış", gecen)
	}
	if ans.Behavior != "deny" || ans.Message != engine.DenyParkNote {
		t.Fatalf("ilk istekte park notu yok: %+v", ans)
	}
	if p := park.al(); p == nil || string(p.Input) != string(komut("curl …")) {
		t.Fatalf("park edilen istek yanlış: %+v", park.al())
	}

	// İkinci ve üçüncü istek: ANINDA park notu.
	for _, ask := range []engine.Ask{
		{Kind: "permission", Tool: "bash", Input: komut("node -e fetch(…)")},
		{Kind: "permission", Tool: "bash", Input: komut("command -v node wget busybox")},
	} {
		bas = time.Now()
		ans = o.waitForHuman(context.Background(), runID, ask, park)
		if gecen := time.Since(bas); gecen > pencere/4 {
			t.Fatalf("%s için yeniden beklendi (%s) — ölü zaman geri geldi", ask.Input, gecen)
		}
		if ans.Behavior != "deny" || ans.Message != engine.DenyParkNote {
			t.Fatalf("%s için park notu yok: %+v", ask.Input, ans)
		}
	}

	// İlk park KORUNUR: kullanıcının ekranda gördüğü ve cevaplayacağı kart odur.
	if string(park.al().Input) != string(komut("curl …")) {
		t.Fatalf("park üzerine yazıldı: %+v — kullanıcı başka bir kart cevaplar", park.al())
	}
}

// parkKutusu iki goroutine'den kullanılır (motorun ui goroutine'i yazar,
// execute okur) ve aralarında buluşma noktası yok — kilit gerçekten şart.
// `go test -race` ile koşunca kilit kalkarsa bu test bağırır.
func TestParkKutusuYarisSiz(t *testing.T) {
	park := &parkKutusu{}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); park.koy(engine.Ask{Kind: "permission", Tool: "bash"}) }()
		go func() { defer wg.Done(); _ = park.al() }()
	}
	wg.Wait()
	if park.al() == nil {
		t.Fatal("hiç park edilmemiş")
	}
}

// Soru park kısa devresine GİRMEZ: onay değil, konuşmanın kendisidir.
// "Hangi takım?" diye soran bot, park yüzünden cevapsız bırakılırsa
// kullanıcı soruyu ekranda görür ama bot cevabı asla almaz.
func TestParktanSonraSoruYineSorulur(t *testing.T) {
	o := &Orchestrator{hub: newApprovalHub(), cfg: Config{ApprovalWait: 5 * time.Second}}
	runID := uuid.New()
	park := &parkKutusu{}
	park.koy(engine.Ask{Kind: "permission", Tool: "bash"})

	sonuc := make(chan engine.Answer, 1)
	go func() {
		sonuc <- o.waitForHuman(context.Background(), runID,
			engine.Ask{Kind: "question", Question: "Hangi takım?"}, park)
	}()

	// Soru hub'a asılmalı ki API cevabı düşürebilsin.
	var asildi bool
	for i := 0; i < 100; i++ {
		if _, ok := o.hub.pending(runID); ok {
			asildi = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !asildi {
		t.Fatal("soru hub'a asılmadı — park kısa devresi soruyu da yuttu")
	}
	if !o.hub.resolve(runID, engine.Answer{Message: "Fenerbahçe"}) {
		t.Fatal("soru cevaplanamadı")
	}
	select {
	case ans := <-sonuc:
		if ans.Message != "Fenerbahçe" {
			t.Fatalf("cevap motora ulaşmadı: %+v", ans)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cevap zamanında dönmedi")
	}
}
