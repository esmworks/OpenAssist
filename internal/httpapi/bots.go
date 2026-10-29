package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/esmworks/OpenAssist/internal/attachments"
	"github.com/esmworks/OpenAssist/internal/engine/pi"
	"github.com/esmworks/OpenAssist/internal/orchestrator"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

// ── DTO'lar ────────────────────────────────────────────────────────────

type botDTO struct {
	ID             uuid.UUID   `json:"id"`
	Kind           string      `json:"kind"`
	Name           string      `json:"name"`
	Description    string      `json:"description"`
	SystemPrompt   string      `json:"system_prompt"`
	ColorPrimary   string      `json:"color_primary"`
	ColorSecondary string      `json:"color_secondary"`
	Section        string      `json:"section"`
	Engine         string      `json:"engine"`
	Model          string      `json:"model"`
	Temperature    *float64    `json:"temperature,omitempty"`
	AllowedTools   []string    `json:"allowed_tools"`
	Extensions     []string    `json:"extensions"`
	Pinned         bool        `json:"pinned"`
	Members        []uuid.UUID `json:"members,omitempty"` // yalnız ekiplerde
	CreatedAt      string      `json:"created_at"`
	UpdatedAt      string      `json:"updated_at"`
}

func toBotDTO(b db.Bot, members []uuid.UUID) botDTO {
	d := botDTO{
		ID: b.ID, Kind: b.Kind, Name: b.Name, Description: b.Description,
		SystemPrompt: b.SystemPrompt, ColorPrimary: b.ColorPrimary,
		ColorSecondary: b.ColorSecondary, Section: b.Section, Engine: b.Engine,
		Model: b.Model, AllowedTools: b.AllowedTools, Extensions: b.Extensions,
		Pinned:    b.Pinned,
		Members:   members,
		CreatedAt: b.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt: b.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
	if b.Temperature.Valid {
		v := b.Temperature.Float64
		d.Temperature = &v
	}
	return d
}

type botCreateReq struct {
	Kind           string      `json:"kind"`
	Name           string      `json:"name"`
	Description    string      `json:"description"`
	SystemPrompt   string      `json:"system_prompt"`
	ColorPrimary   string      `json:"color_primary"`
	ColorSecondary string      `json:"color_secondary"`
	Section        string      `json:"section"`
	Engine         string      `json:"engine"`
	Model          string      `json:"model"`
	Temperature    *float64    `json:"temperature"`
	AllowedTools   []string    `json:"allowed_tools"`
	Extensions     []string    `json:"extensions"`
	Members        []uuid.UUID `json:"members"`
}

func (r *botCreateReq) Validate() error {
	if r.Kind == "" {
		r.Kind = "bot"
	}
	if r.Kind != "bot" && r.Kind != "team" {
		return errors.New("kind bot|team olmalı")
	}
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" || len(r.Name) > 120 {
		return errors.New("name zorunlu ve en çok 120 karakter")
	}
	if r.Engine == "" {
		r.Engine = "pi"
	}
	if r.Temperature != nil && (*r.Temperature < 0 || *r.Temperature > 1) {
		return errors.New("temperature 0-1 aralığında olmalı")
	}
	if r.Kind == "bot" && len(r.Members) > 0 {
		return errors.New("members yalnız ekiplerde verilebilir")
	}
	if r.ColorPrimary == "" {
		r.ColorPrimary = "#b18cf0"
	}
	if r.ColorSecondary == "" {
		r.ColorSecondary = "#7b4fd6"
	}
	if r.AllowedTools == nil {
		r.AllowedTools = []string{}
	}
	// Eklenti listesi VERİLMEDİYSE varsayılan gelir: onay kapısı ve
	// OpenAssist araçları oa-gateway'de yaşıyor, onsuz bot ne izin sorar
	// ne de gateway'i görür. Açıkça boş liste göndermek ise geçerlidir:
	// "çıplak pi" bilinçli bir seçimdir.
	if r.Extensions == nil {
		r.Extensions = []string{pi.OAGateway}
	}
	return nil
}

// ── işleyiciler ────────────────────────────────────────────────────────

func (a *api) createBot(w http.ResponseWriter, r *http.Request) {
	var req botCreateReq
	if err := decodeStrict(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "geçersiz gövde: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if !a.knownEngine(req.Engine) {
		writeErr(w, http.StatusBadRequest, "motor tanınmıyor: "+req.Engine)
		return
	}
	if ad, yok := a.bilinmeyenEklenti(r.Context(), req.Engine, req.Extensions); yok {
		writeErr(w, http.StatusBadRequest, "eklenti kurulu değil: "+ad)
		return
	}
	// Ekip üyeleri var olan tekil botlar olmalı.
	for _, m := range req.Members {
		mb, err := a.st.Q.GetBot(r.Context(), m)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && mb.Kind != "bot") {
			writeErr(w, http.StatusBadRequest, "üye bulunamadı ya da bot değil: "+m.String())
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	bot, err := a.st.Q.CreateBot(r.Context(), db.CreateBotParams{
		Kind: req.Kind, Name: req.Name, Description: req.Description,
		SystemPrompt: req.SystemPrompt, ColorPrimary: req.ColorPrimary,
		ColorSecondary: req.ColorSecondary, Section: req.Section,
		Engine: req.Engine, Model: req.Model,
		Temperature:  toFloat8(req.Temperature),
		AllowedTools: req.AllowedTools,
		Extensions:   req.Extensions,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i, m := range req.Members {
		if err := a.st.Q.AddTeamMember(r.Context(), db.AddTeamMemberParams{
			TeamID: bot.ID, MemberID: m, Position: int32(i),
		}); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusCreated, toBotDTO(bot, req.Members))
}

func (a *api) listBots(w http.ResponseWriter, r *http.Request) {
	bots, err := a.st.Q.ListBots(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	memberships, err := a.st.Q.ListAllTeamMemberships(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	byTeam := map[uuid.UUID][]uuid.UUID{}
	for _, m := range memberships {
		byTeam[m.TeamID] = append(byTeam[m.TeamID], m.MemberID)
	}
	out := make([]botDTO, 0, len(bots))
	for _, b := range bots {
		out = append(out, toBotDTO(b, byTeam[b.ID]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *api) getBot(w http.ResponseWriter, r *http.Request) {
	bot, ok := a.botFromPath(w, r)
	if !ok {
		return
	}
	var members []uuid.UUID
	if bot.Kind == "team" {
		rows, err := a.st.Q.ListTeamMembers(r.Context(), bot.ID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, m := range rows {
			members = append(members, m.ID)
		}
	}
	writeJSON(w, http.StatusOK, toBotDTO(bot, members))
}

type botUpdateReq struct {
	Name           *string      `json:"name"`
	Description    *string      `json:"description"`
	SystemPrompt   *string      `json:"system_prompt"`
	ColorPrimary   *string      `json:"color_primary"`
	ColorSecondary *string      `json:"color_secondary"`
	Section        *string      `json:"section"`
	Engine         *string      `json:"engine"`
	Model          *string      `json:"model"`
	Temperature    *float64     `json:"temperature"`
	AllowedTools   []string     `json:"allowed_tools"`
	Extensions     []string     `json:"extensions"`
	Pinned         *bool        `json:"pinned"`
	Members        *[]uuid.UUID `json:"members"`
}

func (a *api) updateBot(w http.ResponseWriter, r *http.Request) {
	bot, ok := a.botFromPath(w, r)
	if !ok {
		return
	}
	var req botUpdateReq
	if err := decodeStrict(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "geçersiz gövde: "+err.Error())
		return
	}

	p := db.UpdateBotParams{
		ID: bot.ID, Name: bot.Name, Description: bot.Description,
		SystemPrompt: bot.SystemPrompt, ColorPrimary: bot.ColorPrimary,
		ColorSecondary: bot.ColorSecondary, Section: bot.Section,
		Engine: bot.Engine, Model: bot.Model, Temperature: bot.Temperature,
		AllowedTools: bot.AllowedTools, Extensions: bot.Extensions, Pinned: bot.Pinned,
	}
	if req.Name != nil {
		if s := strings.TrimSpace(*req.Name); s != "" && len(s) <= 120 {
			p.Name = s
		} else {
			writeErr(w, http.StatusBadRequest, "name boş olamaz, en çok 120 karakter")
			return
		}
	}
	if req.Description != nil {
		p.Description = *req.Description
	}
	if req.SystemPrompt != nil {
		p.SystemPrompt = *req.SystemPrompt
	}
	if req.ColorPrimary != nil {
		p.ColorPrimary = *req.ColorPrimary
	}
	if req.ColorSecondary != nil {
		p.ColorSecondary = *req.ColorSecondary
	}
	if req.Section != nil {
		p.Section = *req.Section
	}
	if req.Engine != nil {
		if !a.knownEngine(*req.Engine) {
			writeErr(w, http.StatusBadRequest, "motor tanınmıyor: "+*req.Engine)
			return
		}
		p.Engine = *req.Engine
	}
	if req.Model != nil {
		p.Model = *req.Model
	}
	if req.Temperature != nil {
		if *req.Temperature < 0 || *req.Temperature > 1 {
			writeErr(w, http.StatusBadRequest, "temperature 0-1 aralığında olmalı")
			return
		}
		p.Temperature = toFloat8(req.Temperature)
	}
	if req.AllowedTools != nil {
		p.AllowedTools = req.AllowedTools
	}
	if req.Extensions != nil {
		// Doğrulama p.Engine'e göre yapılır, bot.Engine'e göre değil: aynı
		// istekte motor da değişiyorsa liste YENİ motorun kataloğuna göre
		// anlamlıdır.
		if ad, yok := a.bilinmeyenEklenti(r.Context(), p.Engine, req.Extensions); yok {
			writeErr(w, http.StatusBadRequest, "eklenti kurulu değil: "+ad)
			return
		}
		p.Extensions = req.Extensions
	}
	if req.Pinned != nil {
		p.Pinned = *req.Pinned
	}

	updated, err := a.st.Q.UpdateBot(r.Context(), p)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	var members []uuid.UUID
	if req.Members != nil {
		if bot.Kind != "team" {
			writeErr(w, http.StatusBadRequest, "members yalnız ekiplerde güncellenir")
			return
		}
		if err := a.st.Q.DeleteTeamMembers(r.Context(), bot.ID); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		for i, m := range *req.Members {
			if err := a.st.Q.AddTeamMember(r.Context(), db.AddTeamMemberParams{
				TeamID: bot.ID, MemberID: m, Position: int32(i),
			}); err != nil {
				writeErr(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		members = *req.Members
	}
	writeJSON(w, http.StatusOK, toBotDTO(updated, members))
}

func (a *api) deleteBot(w http.ResponseWriter, r *http.Request) {
	bot, ok := a.botFromPath(w, r)
	if !ok {
		return
	}
	if err := a.st.Q.DeleteBot(r.Context(), bot.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── sohbet ─────────────────────────────────────────────────────────────

type postMessageReq struct {
	Text string `json:"text"`
	// Origin — isteğin geldiği kanal (K16). Mac istemcisi "mac" gönderir;
	// boşsa "api". Telegram bu uçtan geçmez, kendi gateway'inden akar.
	Origin string `json:"origin"`
	// Key — idempotency anahtarı (ADIM 3). Aynı anahtar tekrar gelirse yeni
	// koşu üretilmez, ilk gönderimin koşusu döner. İstemci üretir: retry'de
	// AYNI anahtarı yeniden göndermelidir. Boşsa korumasız yol.
	Key string `json:"key"`
	// AttachmentIDs — bu mesaja bağlanacak ekler (önce
	// POST /api/bots/{id}/attachments ile yüklendi). ADIM 5.
	AttachmentIDs []uuid.UUID `json:"attachment_ids"`
}

func (r *postMessageReq) Validate() error {
	switch r.Origin {
	case "", orchestrator.OriginAPI, orchestrator.OriginMac, orchestrator.OriginRoutine:
		return nil
	default:
		return errors.New("origin api|mac|routine olmalı")
	}
}

func (a *api) postMessage(w http.ResponseWriter, r *http.Request) {
	bot, ok := a.botFromPath(w, r)
	if !ok {
		return
	}
	var req postMessageReq
	if err := decodeStrict(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "geçersiz gövde: "+err.Error())
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" && len(req.AttachmentIDs) == 0 {
		writeErr(w, http.StatusBadRequest, "text ya da en az bir ek gerekli")
		return
	}
	if err := req.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	run, err := a.orch.EnqueueChatWithKey(r.Context(), bot, req.Text, req.Origin, req.Key, req.AttachmentIDs)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"run_id": run.ID, "status": run.Status})
}

func (a *api) listMessages(w http.ResponseWriter, r *http.Request) {
	bot, ok := a.botFromPath(w, r)
	if !ok {
		return
	}
	// v1'de kanal = botun direct sohbeti; UUID üzerinden oku (ADIM 4).
	conv, err := a.st.BotConversation(r.Context(), bot)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	limit := queryLimit(r, 100)
	rows, err := a.st.Q.ListMessages(r.Context(), db.ListMessagesParams{ConversationID: conv.ID, Limit: limit})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Ekler: mesaj id'leriyle tek sorgu (N+1 yok, ADIM 5).
	ekler, err := a.mesajEklari(r.Context(), rows)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Gönderen adları (ADIM 10): grup zaman tünelinde üye cevapları
	// ilişkilendirmeyle görünür — tek sorgu, harita.
	adlar := map[uuid.UUID]string{}
	if bots, err := a.st.Q.ListBots(r.Context()); err == nil {
		for _, b := range bots {
			adlar[b.ID] = b.Name
		}
	}
	// Sorgu DESC verir; istemciye kronolojik sıra döner.
	out := make([]map[string]any, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		m := rows[i]
		item := map[string]any{
			"id": m.ID, "role": m.Role, "text": m.Text,
			"created_at": m.CreatedAt.UTC(), "bot_id": m.BotID,
		}
		if m.Role == "bot" {
			if ad, ok := adlar[m.BotID]; ok {
				item["sender"] = ad
			}
		}
		if m.RunID.Valid {
			item["run_id"] = m.RunID.UUID
		}
		if ats := ekler[m.ID]; len(ats) > 0 {
			outAts := make([]map[string]any, 0, len(ats))
			for _, at := range ats {
				outAts = append(outAts, map[string]any{
					"id": at.ID, "name": at.Name, "kind": at.Kind, "size": at.Size,
				})
			}
			item["attachments"] = outAts
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, out)
}

// mesajEklari — mesaj id'lerine göre ekleri tek sorguda getirir, haritaya dizer.
func (a *api) mesajEklari(ctx context.Context, msgs []db.Message) (map[uuid.UUID][]db.Attachment, error) {
	if len(msgs) == 0 {
		return nil, nil
	}
	ids := make([]uuid.UUID, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.ID)
	}
	ekler, err := a.st.Q.ListAttachmentsByMessages(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID][]db.Attachment)
	for _, e := range ekler {
		if !e.MessageID.Valid {
			continue
		}
		out[e.MessageID.UUID] = append(out[e.MessageID.UUID], e)
	}
	return out, nil
}

// clearChat — "Sohbeti Temizle": konuşmanın mesajları ve motor imleçleri
// sunucuda silinir, ek dosyaları diskten süpürülür (best-effort).
func (a *api) clearChat(w http.ResponseWriter, r *http.Request) {
	bot, ok := a.botFromPath(w, r)
	if !ok {
		return
	}
	yollar, err := a.st.ClearChat(r.Context(), bot)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, y := range yollar {
		_ = attachments.Remove(a.dataDir, y)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) listBotRuns(w http.ResponseWriter, r *http.Request) {
	bot, ok := a.botFromPath(w, r)
	if !ok {
		return
	}
	rows, err := a.st.Q.ListRunsByBot(r.Context(), db.ListRunsByBotParams{BotID: bot.ID, Limit: queryLimit(r, 50)})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]runDTO, 0, len(rows))
	for _, run := range rows {
		out = append(out, toRunDTO(run))
	}
	writeJSON(w, http.StatusOK, out)
}

// ── yardımcılar ────────────────────────────────────────────────────────

func (a *api) botFromPath(w http.ResponseWriter, r *http.Request) (db.Bot, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "geçersiz bot id")
		return db.Bot{}, false
	}
	bot, err := a.st.Q.GetBot(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "bot bulunamadı")
		return db.Bot{}, false
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return db.Bot{}, false
	}
	return bot, true
}

func queryLimit(r *http.Request, def int32) int32 {
	s := r.URL.Query().Get("limit")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 500 {
		return def
	}
	return int32(n)
}

func toFloat8(v *float64) pgtype.Float8 {
	if v == nil {
		return pgtype.Float8{}
	}
	return pgtype.Float8{Float64: *v, Valid: true}
}

// PATCH /api/messages/{id} — kullanıcı mesajını düzenler ve botu yeniden
// üretir (ADIM 9). Eski sürüm superseded olarak görünmez olur; yeni dal taze
// oturum + satır içi geçmiş tekrarıyla koşar.
func (a *api) editMessage(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "geçersiz mesaj id")
		return
	}
	var req postMessageReq
	if err := decodeStrict(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "geçersiz gövde: "+err.Error())
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" {
		writeErr(w, http.StatusBadRequest, "text boş olamaz")
		return
	}
	if err := req.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	eski, err := a.st.Q.GetMessage(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "mesaj yok")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if eski.Role != "user" {
		writeErr(w, http.StatusUnprocessableEntity, "yalnız kullanıcı mesajı düzenlenir")
		return
	}
	if eski.SupersededBy.Valid {
		// Aynı düzenlemenin RETRY'i (ADIM 9 idem): superseding sürümün
		// anahtarı ile isteğin anahtarı çakışırsa mevcut dal döner.
		if req.Key != "" {
			yeni, aerr := a.st.Q.GetMessage(r.Context(), eski.SupersededBy.UUID)
			if aerr == nil && yeni.IdempotencyKey == req.Key {
				run, rerr := a.st.Q.GetRun(r.Context(), yeni.RunID.UUID)
				if rerr == nil {
					writeJSON(w, http.StatusAccepted, map[string]any{"run_id": run.ID, "status": run.Status})
					return
				}
			}
		}
		writeErr(w, http.StatusConflict, "bu sürüm zaten düzenlendi — güncel sürümü düzenle")
		return
	}
	bot, err := a.st.Q.GetBot(r.Context(), eski.BotID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	run, err := a.orch.EditChatWithKey(r.Context(), bot, eski, req.Text, req.Origin, req.Key)
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, pgx.ErrNoRows) {
			code = http.StatusConflict
		}
		writeErr(w, code, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"run_id": run.ID, "status": run.Status})
}
