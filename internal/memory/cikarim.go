package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/esmworks/OpenAssist/internal/engine"
	"github.com/esmworks/OpenAssist/internal/store"
	db "github.com/esmworks/OpenAssist/internal/store/db"
	"github.com/esmworks/OpenAssist/internal/workspace"
)

// Inferrer — koşu sonrası hafıza çıkarımı (K20, v1.5 maddesi — ADIM 8).
//
// Her biten koşunun ardından ucuz, imleçsiz ve araçsız bir tur koşar: motor
// tutanağı okur, YALNIZCA kalıcı olgu/tercih önerir — öneriler
// konsolidasyonla aynı yazma yolundan geçer (sır kapısı + dedup + revizyon,
// store.SaveMemory). Konsolidasyondan farkı yazma ZAMANI değil yönü: o var
// olanı birleştirir, bu yeniler. İkisi de aynı güvenlik kapısındadır.
type Inferrer struct {
	St      *store.Store
	Engines map[string]engine.Executor
	DataDir string
}

// NewInferrer — motora erişimi olmayan kurulumlarda da güvenle kurulur;
// AfterRun doğru motoru run.Engine'den çözer.
func NewInferrer(st *store.Store, engines map[string]engine.Executor, dataDir string) *Inferrer {
	return &Inferrer{St: st, Engines: engines, DataDir: dataDir}
}

// AfterRun — orchestrator'ın AfterRun kancası (ADIM 8). Kozmetik hatalar
// çıkarımı sessizce düşürür: sohbet koşusunun bittiği zaten bildirilmiştir.
func (in *Inferrer) AfterRun(ctx context.Context, bot db.Bot, run db.Run) {
	if run.Origin == "consolidation" || run.Origin == "inference" {
		return // arka plan turu arka plan turu doğurmasın
	}
	exec := in.Engines[run.Engine]
	if exec == nil {
		slog.Warn("çıkarım motoru yok", "engine", run.Engine)
		return
	}
	tutanak := in.tutanak(ctx, run)
	if len([]rune(tutanak)) < 80 {
		return // kısa koşuda öğrenilecek kalıcı bir şey yok
	}

	cwd, err := workspace.Dir(in.DataDir, bot.ID)
	if err != nil {
		slog.Warn("çıkarım çalışma dizini açılamadı", "err", err)
		return
	}
	basladi := time.Now()
	res, err := exec.SendTurn(ctx, engine.TurnInput{
		SystemPrompt: cikarimSistemPromptu,
		Prompt:       cikarimKullaniciPromptu(bot, tutanak),
		Model:        run.Model,
		CWD:          cwd,
		NoTools:      true, // bakım turu: katalog görünmez, sorular fail-closed
	}, func(engine.Event) {})
	maliyetKaydet(in.St, ctx, bot, run.Model, "inference", "koşu sonrası hafıza çıkarımı", basladi, res, err)
	if err != nil {
		slog.Warn("çıkarım turu başarısız", "bot", bot.Name, "err", err)
		return
	}
	if res == nil || !res.Ok {
		slog.Warn("çıkarım turu olumsuz döndü", "bot", bot.Name)
		return
	}

	oneriler, err := cikarimCoz(res.FinalText)
	if err != nil {
		slog.Warn("çıkarım çıktısı çözülemedi", "bot", bot.Name, "err", err)
		return
	}
	kaydedilen := in.kaydet(ctx, bot, run.ID, oneriler)
	if kaydedilen > 0 {
		slog.Info("çıkarım hafıza önerilerini kaydetti", "bot", bot.Name, "adet", kaydedilen)
	}
}

// tutanak — koşu olaylarını motorun okuyabileceği kompakt biçime indirir
// (ADIM 8). content.delta kalıcı bile değildir; kalıcı olaylar + koşunun
// kendi komutu (run.Prompt — kullanıcı mesajı) yeterlidir.
func (in *Inferrer) tutanak(ctx context.Context, run db.Run) string {
	evs, err := in.St.Q.ListRunEventsAfter(ctx, db.ListRunEventsAfterParams{RunID: run.ID, ID: 0})
	if err != nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("KULLANICI: " + run.Prompt + "\n")
	for _, ev := range evs {
		var p struct {
			Text string `json:"text"`
			Tool struct {
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			} `json:"tool"`
			Request struct {
				Kind     string `json:"kind"`
				Tool     string `json:"tool"`
				Question string `json:"question"`
			} `json:"request"`
		}
		if json.Unmarshal(ev.Payload, &p) != nil {
			continue
		}
		switch engine.Kind(ev.Kind) {
		case engine.KindAssistantText:
			b.WriteString("BOT: " + strings.TrimSpace(p.Text) + "\n")
		case engine.KindToolStarted:
			br := []rune(string(p.Tool.Input))
			if len(br) > 300 {
				p.Tool.Input = json.RawMessage(string(br[:300]) + "…")
			}
			b.WriteString("ARAÇ " + p.Tool.Name + ": " + string(p.Tool.Input) + "\n")
		case engine.KindRequestOpened:
			if p.Request.Kind == "question" {
				b.WriteString("SORU: " + p.Request.Question + "\n")
			} else {
				b.WriteString("ONAY İSTENDİ: " + p.Request.Tool + "\n")
			}
		case engine.KindRequestResolved:
			b.WriteString("ONAY SONUCU: " + p.Request.Tool + " → " + p.Request.Kind + "\n")
		}
	}
	// Koşunun kesin sonucu da tutanağın parçası — motor olayları seyrek
	// bile olsa (kimi adaptör assistant.text üretmez) içerik kaybolmaz.
	if sonuc := strings.TrimSpace(run.FinalText); sonuc != "" {
		b.WriteString("BOT (SONUÇ): " + sonuc + "\n")
	}
	satir := b.String()
	r := []rune(satir)
	if len(r) > 9000 {
		return string(r[:9000]) + "\n… (kırpıldı)"
	}
	return satir
}

// cikarimCoz — motor çıktısını öneri listesine çevirir. Hatalı öğe tüm
// listeyi düşürmez; ayıklanır.
func cikarimCoz(ham string) ([]cikarimOneri, error) {
	ham = strings.TrimSpace(ham)
	ham = strings.TrimPrefix(ham, "```json")
	ham = strings.TrimPrefix(ham, "```")
	ham = strings.TrimSuffix(ham, "```")
	if ham == "" || ham == "[]" || ham == "null" {
		return nil, nil
	}
	var hamListe []struct {
		Content    string  `json:"content"`
		Kind       string  `json:"kind"`
		Confidence float64 `json:"confidence"`
	}
	if err := json.Unmarshal([]byte(ham), &hamListe); err != nil {
		return nil, fmt.Errorf("çıkarım JSON'u: %w", err)
	}
	out := make([]cikarimOneri, 0, len(hamListe))
	for _, o := range hamListe {
		if len(out) >= 5 {
			break
		}
		if strings.TrimSpace(o.Content) == "" {
			continue
		}
		// Bilinmeyen kind 'fact'e düşer. 'instruction' da buraya düşer ve bu
		// BİLİNÇLİ: sabit talimat bloğuna yalnız kullanıcı yazar, bir bakım
		// turu kendi çıkardığı cümleyi oraya terfi ettiremez.
		if o.Kind != "fact" && o.Kind != "preference" && o.Kind != "correction" {
			o.Kind = "fact"
		}
		out = append(out, cikarimOneri{Content: strings.TrimSpace(o.Content), Kind: o.Kind, Confidence: o.Confidence})
	}
	return out, nil
}

// kaydet — önerileri normal yazma yolundan geçirir (sır kapısı + dedup +
// revizyon). No-op kapısı: güven < 0.5 olan öneri DÜŞER — "gelecekteki koşu
// bundan iyi davranacak mı?" sorusunun sayıya çevrilmiş hali; modelden bu
// eşiğe göre düşünmesi promptta istenir.
func (in *Inferrer) kaydet(ctx context.Context, bot db.Bot, runID uuid.UUID, oneriler []cikarimOneri) int {
	kaydedilen := 0
	for _, o := range oneriler {
		if o.Confidence < 0.5 {
			continue
		}
		err := in.St.InMemoryTx(ctx, func(w *store.MemoryWriter) error {
			_, err := w.Save(ctx, store.MemoryDraft{
				Scope: "bot", BotID: uuid.NullUUID{UUID: bot.ID, Valid: true},
				Kind: o.Kind, Content: o.Content,
				SourceRunID: uuid.NullUUID{UUID: runID, Valid: true},
				Confidence:  float32(o.Confidence),
			})
			return err
		})
		if err != nil {
			// Sır kapısı ya da başka yazım vetosu: bu öneri düşer, diğerleri sürer.
			slog.Debug("çıkarım önerisi reddedildi", "bot", bot.Name, "err", err)
			continue
		}
		kaydedilen++
	}
	return kaydedilen
}

// cikarimOneri — motordan beklenen tek öneri biçimi.
type cikarimOneri struct {
	Content    string
	Kind       string
	Confidence float64
}

// cikarimSistemPromptu — no-op kapısının prompt tarafı: model yalnız kalıcı
// değer taşıyan olguları önersin; rutin iş içeriği/geçmiş olaylar öneri değil.
const cikarimSistemPromptu = `Sen hafıza bakıcısısın. Sana bir bot koşusunun tutanağı verilir; görevin YALNIZCA kalıcı bilgiyi çıkarmak.

ÖNERİLECEK: kullanıcının sabit tercihleri, uzun ömürlü bağlam, tekrar edecek kurallar — gelecekteki bir koşuyu GERÇEKTEN daha iyi yapacak olgular.
YASAK: tek seferlik olay kayıtları, yapılan işin içeriği, sohbette zaten söylenmiş geçici detaylar, kısa vadeli durumlar.

ÖNCE DÜZELTMELERE BAK. Kullanıcı botu düzelttiyse ("hayır, öyle değil", "sana pnpm kullan demiştim", "bir daha sormadan yapma") bu tutanaktaki EN DEĞERLİ bilgidir: kind="correction" ile yaz, güveni yüksek ver. Düzeltme geçmiş bir olay değil, tekrarlanmaması gereken bir hatadır — kullanıcı onu bir kez söylediğini varsayar.
Ama her itiraz düzeltme DEĞİLDİR: "olsun", "boşver", "önemli değil" geçiştirmedir; arkasında gelecekte uyulacak bir kural yoksa yazma. Düzeltmeyi kuralın kendisi olarak yaz ("paket yöneticisi pnpm"), olayın anlatısı olarak değil ("kullanıcı npm kullandığım için uyardı").

Sorulacak kapı sorusu: "Gelecekteki bir koşu bunu bilseydi daha iyi davranır mıydı?" Cevap hayırsa ÖNERME.
Önerilecek hiçbir şey yoksa boş liste dön.

Yanıt: YALNIZCA JSON dizisi, en fazla 5 öğe:
[{"content":"kısa olgu","kind":"fact|preference|correction","confidence":0.0-1.0}]
confidence: kalıcılık ve önem ölçün (0.5 altı = değersiz, yazma).`

func cikarimKullaniciPromptu(bot db.Bot, tutanak string) string {
	return fmt.Sprintf("Bot: %s\n\nKOŞU TUTANAK:\n%s\n\nKalıcı hafıza önerilerini JSON olarak yaz.", bot.Name, tutanak)
}
