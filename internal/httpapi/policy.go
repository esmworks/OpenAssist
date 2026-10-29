package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esmworks/OpenAssist/internal/policy"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

// ── politika ayarları (Ayarlar > Botlar; system_info 'policy') ─────────

func (a *api) getPolicy(w http.ResponseWriter, r *http.Request) {
	row, err := a.st.Q.GetSystemInfo(r.Context(), "policy")
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, policy.DefaultSettings())
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	var s policy.Settings
	if json.Unmarshal([]byte(row.Value), &s) != nil {
		writeJSON(w, http.StatusOK, policy.DefaultSettings())
		return
	}
	writeJSON(w, http.StatusOK, s)
}

func (a *api) putPolicy(w http.ResponseWriter, r *http.Request) {
	var s policy.Settings
	if err := decodeStrict(r, &s); err != nil {
		writeErr(w, http.StatusBadRequest, "geçersiz gövde: "+err.Error())
		return
	}
	if err := s.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	b, err := json.Marshal(s)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := a.st.Q.SetSystemInfo(r.Context(), db.SetSystemInfoParams{Key: "policy", Value: string(b)}); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// ── onay kuralları ─────────────────────────────────────────────────────

type ruleDTO struct {
	ID        uuid.UUID  `json:"id"`
	BotID     *uuid.UUID `json:"bot_id,omitempty"` // null = tüm botlar
	Tool      string     `json:"tool"`
	Action    string     `json:"action"`
	Note      string     `json:"note,omitempty"`
	CreatedAt string     `json:"created_at"`
}

func toRuleDTO(r db.ApprovalRule) ruleDTO {
	d := ruleDTO{
		ID: r.ID, Tool: r.Tool, Action: r.Action, Note: r.Note,
		CreatedAt: r.CreatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
	if r.BotID.Valid {
		v := r.BotID.UUID
		d.BotID = &v
	}
	return d
}

func (a *api) listRules(w http.ResponseWriter, r *http.Request) {
	rows, err := a.st.Q.ListApprovalRules(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]ruleDTO, 0, len(rows))
	for _, rr := range rows {
		out = append(out, toRuleDTO(rr))
	}
	writeJSON(w, http.StatusOK, out)
}

type ruleCreateReq struct {
	BotID  *uuid.UUID `json:"bot_id"` // boş = tüm botlar
	Tool   string     `json:"tool"`
	Action string     `json:"action"`
	Note   string     `json:"note"`
}

func (rq *ruleCreateReq) Validate() error {
	if rq.Tool == "" {
		return errors.New("tool zorunlu")
	}
	if rq.Action != "allow" && rq.Action != "deny" {
		return errors.New("action allow|deny olmalı")
	}
	// K17 vetosu: yüksek riskli sınıflara allow kuralı yazılamaz (elle bile).
	if rq.Action == "allow" && !policy.Classify(rq.Tool).Ruleable {
		return errors.New("bu araç sınıfına hep-izin-ver kuralı yazılamaz (K17)")
	}
	return nil
}

func (a *api) createRule(w http.ResponseWriter, r *http.Request) {
	var req ruleCreateReq
	if err := decodeStrict(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "geçersiz gövde: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var botID uuid.NullUUID
	if req.BotID != nil {
		botID = uuid.NullUUID{UUID: *req.BotID, Valid: true}
	}
	rule, err := a.st.Q.UpsertApprovalRule(r.Context(), db.UpsertApprovalRuleParams{
		BotID: botID, Tool: req.Tool, Action: req.Action, Note: req.Note,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, toRuleDTO(rule))
}

func (a *api) deleteRule(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "geçersiz kural id")
		return
	}
	n, err := a.st.Q.DeleteApprovalRule(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n == 0 {
		writeErr(w, http.StatusNotFound, "kural bulunamadı")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── koşu listesi (Onay Merkezi / Görevler panosu) ──────────────────────

var validRunStatuses = map[string]bool{
	"queued": true, "running": true, "approval_pending": true,
	"completed": true, "failed": true, "canceled": true, "expired": true,
}

func (a *api) listRuns(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "approval_pending" // varsayılan: Onay Merkezi
	}
	if !validRunStatuses[status] {
		writeErr(w, http.StatusBadRequest, "geçersiz status")
		return
	}
	rows, err := a.st.Q.ListRunsByStatus(r.Context(), db.ListRunsByStatusParams{
		Status: status, Limit: queryLimit(r, 100),
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]runDTO, 0, len(rows))
	for _, run := range rows {
		out = append(out, toRunDTO(run))
	}
	if status == "approval_pending" {
		out = append(a.canliBekleyenler(r, len(out)), out...)
	}
	writeJSON(w, http.StatusOK, out)
}

// canliBekleyenler — HENÜZ PARK EDİLMEMİŞ ama şu an insan bekleyen koşular.
//
// Neden gerekli: kısa-bekleme penceresi (B.3) boyunca koşu hâlâ 'running'
// durumundadır, yani bu liste boş döner ve Onay Merkezi bomboş görünür. Kart
// ancak pencere dolup koşu park edince (varsayılan 60 sn) beliriyordu —
// kullanıcı o süre boyunca ekranda yalnız "yazıyor" noktalarını görüyor,
// uygulamayı kapatıp açıyor ve bildirim "bir anda" düşüyordu. Oysa sunucu
// isteği ilk saniyede biliyor.
//
// Bunları park edilmiş gibi sunuyoruz: kullanıcı açısından ikisi de "senin
// onayını bekliyor"dur; 'running' olmaları park edip etmediğimizin iç
// ayrıntısı. Erken cevap zaten HIZLI yoldan gider — koşu süreç içinde devam
// eder, park/resume turu hiç yaşanmaz (pencerenin var oluş sebebi bu).
//
// Süreç içi kayıt tek düğümde yaşar (approvalHub). Çok replikalı bir kurulumda
// bu liste yalnız KENDİ düğümünün canlı isteklerini görür; ötekiler park
// edince zaten DB yolundan gelir, yani kötü hâl eski davranıştır.
func (a *api) canliBekleyenler(r *http.Request, parkliSayi int) []runDTO {
	if a.orch == nil {
		return nil
	}
	kalan := queryLimit(r, 100) - int32(parkliSayi)
	if kalan <= 0 {
		return nil // liste zaten park edilmişlerle dolu (negatif LIMIT hata olurdu)
	}
	rows, err := a.st.Q.ListRunsByStatus(r.Context(), db.ListRunsByStatusParams{
		Status: "running", Limit: kalan,
	})
	if err != nil || len(rows) == 0 {
		return nil
	}
	out := make([]runDTO, 0, len(rows))
	for _, run := range rows {
		ask, bekliyor := a.orch.PendingAsk(run.ID)
		if !bekliyor {
			continue
		}
		b, err := json.Marshal(ask)
		if err != nil {
			continue
		}
		dto := toRunDTO(run)
		dto.Status = "approval_pending"
		dto.PendingAsk = b
		out = append(out, dto)
	}
	return out
}
