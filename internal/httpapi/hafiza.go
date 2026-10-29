package httpapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/esmworks/OpenAssist/internal/memory"
	"github.com/esmworks/OpenAssist/internal/store"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

// Hafıza dışa/içe aktarma + arama uçları (K20).

// exportMemories — GET /api/memories/export → insan-okur markdown.
// Yedek değil (o `pg_dump`): kullanıcının hafızasını gözle denetleyip elle
// düzeltebilmesi için.
func (a *api) exportMemories(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := a.st.Q.ListMemories(ctx, 10000)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	bots, err := a.st.Q.ListBots(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	metin := memory.DisaAktar(rows, bots, time.Now())

	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="openassist-hafiza.md"`)
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, metin)
}

// iceAktarimAzami — kabul edilen gövde sınırı. Aşılırsa 413: sessizce kırpmak
// (io.LimitReader) yarım bir olgu yazabilir ("- [fact] Kullanıcı toplantı
// notlarını") ya da dosyanın kalanını kaybettirir, kullanıcı ise 200 görür.
const iceAktarimAzami = 4 << 20

// importMemories — POST /api/memories/import, gövde markdown.
//
// Her satır normal yazma yolundan geçer (dedup + sır kapısı + revizyon):
// id/revizyon geri YÜKLENMEZ, satırlar yeni öneri gibi işlenir. Bu yüzden
// elle düzenlenmiş bir dosyayı geri vermek güvenlidir.
//
// Aktarım YA HEP YA HİÇ: ayrıştırma hatası da, 30. satırdaki bir sır da tüm
// aktarımı düşürür ve hiçbir satır yazılmaz. Yarısı yüklenmiş bir dosya,
// kullanıcının neyin girdiğini bilememesi demektir.
func (a *api) importMemories(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	r.Body = http.MaxBytesReader(w, r.Body, iceAktarimAzami)
	gövde, err := io.ReadAll(r.Body)
	if err != nil {
		var büyük *http.MaxBytesError
		if errors.As(err, &büyük) {
			writeErr(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("dosya çok büyük (sınır %d MB) — parçalara bölüp aktar", iceAktarimAzami>>20))
			return
		}
		writeErr(w, http.StatusBadRequest, "gövde okunamadı")
		return
	}
	satirlar, err := memory.IceAktarCoz(string(gövde))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(satirlar) == 0 {
		writeErr(w, http.StatusBadRequest, "dosyada hafıza satırı yok")
		return
	}

	// Bot adları önce çözülür: tanınmayan bir ad varsa yazma hiç başlamaz.
	bots, err := a.st.Q.ListBots(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	botID := map[string]uuid.UUID{}
	for _, b := range bots {
		botID[strings.ToLower(b.Name)] = b.ID
	}
	taslaklar := make([]store.MemoryDraft, 0, len(satirlar))
	for _, s := range satirlar {
		taslak := store.MemoryDraft{
			Scope: s.Scope, Kind: s.Kind, Content: s.Content,
			Confidence: s.Confidence, ExpiresAt: s.ExpiresAt,
		}
		if s.Scope == "bot" {
			id, ok := botID[strings.ToLower(s.BotAdi)]
			if !ok {
				writeErr(w, http.StatusBadRequest, "bilinmeyen bot: "+s.BotAdi)
				return
			}
			taslak.BotID = uuid.NullUUID{UUID: id, Valid: true}
		}
		taslaklar = append(taslaklar, taslak)
	}

	// Tek transaction: bir satır reddedilirse (en sık sır kapısı) hiçbiri
	// yazılmaz. Hata metni hangi satırın düşürdüğünü söyler.
	yazilan, err := a.st.SaveMemories(ctx, taslaklar)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"okunan": len(satirlar), "yazilan": len(yazilan)})
}

// listStandingInstructions — GET /api/memories/instructions?bot=<id>
//
// Prompta giren listenin AYNISI: orkestratör de aynı sorguyu çağırır. Ayrı
// bir uç olmasının sebebi bu — kullanıcı "modelin gördüğü kurallar" listesini
// görmeli, ona benzeyen bir listeyi değil. Sıra, tavan ve kapsam kuralları
// (önce user, sonra bot) tek yerde duruyor.
//
// bot verilmezse yalnız user kapsamı döner: bütün botları bağlayan kurallar.
func (a *api) listStandingInstructions(w http.ResponseWriter, r *http.Request) {
	var bot uuid.NullUUID
	if s := r.URL.Query().Get("bot"); s != "" {
		id, err := uuid.Parse(s)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "geçersiz bot id")
			return
		}
		bot = uuid.NullUUID{UUID: id, Valid: true}
	}
	rows, err := a.st.Q.ListStandingInstructions(r.Context(), db.ListStandingInstructionsParams{
		BotID: bot, Limit: queryLimit(r, sabitTalimatTavani),
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, memoryDTOs(rows))
}

// sabitTalimatTavani — orkestratörün prompt tavanıyla AYNI sayı olmalı; ekran
// modelin görmediği bir kuralı "geçerli" diye göstermesin.
const sabitTalimatTavani = 20

// createMemory — POST /api/memories
//
// Kullanıcının KENDİ eliyle kayıt yazması. Bugüne kadar tek yol içe aktarımdı
// (bütün bir markdown dosyası); sabit talimatlar bunu zorunlu kıldı:
// kind='instruction' bloğuna motor yazamaz — yazabilen tek taraf kullanıcıdır
// ve o tarafın bir kapısı olmalı, yoksa özellik yalnız kağıt üzerinde kalır.
//
// Sır kapısı burada da geçerli (store.SaveMemory): kullanıcı da olsa hafızaya
// API anahtarı yazılmaz — o metin her koşuda motora gider.
func (a *api) createMemory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Scope         string `json:"scope"`
		BotID         string `json:"bot_id"`
		Kind          string `json:"kind"`
		Content       string `json:"content"`
		ExpiresInDays int    `json:"expires_in_days"`
	}
	if err := decodeStrict(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Content = strings.TrimSpace(req.Content)
	if req.Content == "" {
		writeErr(w, http.StatusBadRequest, "content boş olamaz")
		return
	}
	switch req.Kind {
	case "fact", "preference", "instruction", "correction":
	default:
		writeErr(w, http.StatusBadRequest, "geçersiz kind (fact|preference|instruction|correction)")
		return
	}

	taslak := store.MemoryDraft{Scope: req.Scope, Kind: req.Kind, Content: req.Content}
	switch req.Scope {
	case "user":
		if req.BotID != "" {
			writeErr(w, http.StatusBadRequest, "scope=user kaydı bota bağlanamaz")
			return
		}
	case "bot":
		id, err := uuid.Parse(req.BotID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "scope=bot için geçerli bot_id gerekli")
			return
		}
		if _, err := a.st.Q.GetBot(r.Context(), id); err != nil {
			writeErr(w, http.StatusBadRequest, "bilinmeyen bot")
			return
		}
		taslak.BotID = uuid.NullUUID{UUID: id, Valid: true}
	default:
		writeErr(w, http.StatusBadRequest, "geçersiz scope (user|bot)")
		return
	}
	if req.ExpiresInDays > 0 {
		t := time.Now().UTC().AddDate(0, 0, req.ExpiresInDays)
		taslak.ExpiresAt = &t
	}

	mem, err := a.st.SaveMemory(r.Context(), taslak)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, memoryDTOs([]db.Memory{mem})[0])
}

// searchMemories — GET /api/memories/search?q=…&bot=<id>&arsiv=1
// Basit içerik araması (K20: gömme/vektör yok). arsiv=1 emekliye ayrılmış
// kayıtları arar — kötü bir konsolidasyon turundan sonra katılan kaydı bulup
// geri getirmenin (memory_restore) tek yolu.
func (a *api) searchMemories(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeErr(w, http.StatusBadRequest, "q parametresi gerekli")
		return
	}
	var bot uuid.NullUUID
	if s := r.URL.Query().Get("bot"); s != "" {
		id, err := uuid.Parse(s)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "geçersiz bot id")
			return
		}
		bot = uuid.NullUUID{UUID: id, Valid: true}
	}
	rows, err := a.st.Q.SearchMemories(r.Context(), db.SearchMemoriesParams{
		BotID: bot, Q: q, Arsiv: boolQuery(r, "arsiv"), Lim: queryLimit(r, 50),
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, memoryDTOs(rows))
}

// restoreMemory — POST /api/memories/{id}/restore
// Emekliye ayrılmış kaydı yaşayanlar arasına geri alır.
func (a *api) restoreMemory(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "geçersiz id")
		return
	}
	mem, err := a.st.RestoreMemory(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, memoryDTOs([]db.Memory{mem})[0])
}

// boolQuery — "1"/"true"/"evet" gibi değerleri bayrak sayar.
func boolQuery(r *http.Request, ad string) bool {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get(ad))) {
	case "1", "true", "evet", "yes":
		return true
	}
	return false
}
