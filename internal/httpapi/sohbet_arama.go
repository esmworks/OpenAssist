package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	db "github.com/esmworks/OpenAssist/internal/store/db"
)

// Sohbet geçmişi araması — "bunu daha önce konuşmuş muyduk?".
//
// Hafıza aramasından (K20) ayrı bir uç, çünkü ayrı bir soru: hafıza koşulardan
// damıtılmış kalıcı olguları tutar, sohbet ise söylenenin kendisini. Bir
// kullanıcı "geçen ay şu hatayı nasıl çözmüştük" diye sorduğunda cevap
// hafızada olmayabilir ve olmamalı da — her konuşmayı kalıcı olguya çevirmek
// hafızayı çöpe çevirir.

// sohbetArananDTO — arama sonucu satırı. Mesaj akışının DTO'sundan DAR: ek
// dosyaları, dallanma alanları ve idempotency anahtarı burada gürültü.
type sohbetArananDTO struct {
	ID      string `json:"id"`
	BotID   string `json:"bot_id"`
	BotName string `json:"bot_name"`
	Role    string `json:"role"`
	Text    string `json:"text"`
	// CreatedAt time.Time olarak gider (hafıza DTO'larıyla aynı kalıp):
	// istemci biçimi kendi yerelinde kurar.
	CreatedAt time.Time `json:"created_at"`
}

// searchMessages — GET /api/messages/search?q=…&bot=<id>&limit=…
//
// Boş `q` 400 döner (hafıza aramasıyla aynı kural): boş sorguyla "her şey"
// dönmesi arama değil listelemedir, üstelik en pahalı halidir.
func (a *api) searchMessages(w http.ResponseWriter, r *http.Request) {
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
	rows, err := a.st.Q.SearchMessages(r.Context(), db.SearchMessagesParams{
		BotID: bot, Q: q, Lim: queryLimit(r, 50),
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Bot adları tek sorguda: satır başına GetBot çağırmak 50 sonuç için 50
	// gidiş-dönüş demekti. Ad çözülemezse satır yine döner — arama sonucunu
	// silmek, adı eksik göstermekten kötü.
	adlar := map[uuid.UUID]string{}
	if bots, err := a.st.Q.ListBots(r.Context()); err == nil {
		for _, b := range bots {
			adlar[b.ID] = b.Name
		}
	}
	writeJSON(w, http.StatusOK, sohbetAramaDTOlari(rows, adlar))
}

// sohbetAramaDTOlari — saf dönüşüm (testten doğrudan çağrılır).
func sohbetAramaDTOlari(rows []db.SearchMessagesRow, adlar map[uuid.UUID]string) []sohbetArananDTO {
	out := make([]sohbetArananDTO, 0, len(rows))
	for _, m := range rows {
		out = append(out, sohbetArananDTO{
			ID:        m.ID.String(),
			BotID:     m.BotID.String(),
			BotName:   adlar[m.BotID],
			Role:      m.Role,
			Text:      m.Text,
			CreatedAt: m.CreatedAt,
		})
	}
	return out
}
