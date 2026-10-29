package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esmworks/OpenAssist/internal/orchestrator"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

type runDTO struct {
	ID     uuid.UUID `json:"id"`
	BotID  uuid.UUID `json:"bot_id"`
	Status string    `json:"status"`
	Prompt string    `json:"prompt"`
	// Origin — koşuyu doğuran kanal (api|mac|telegram|routine). İstemci
	// rutin kaynaklı koşuyu ayrı bildirir (Ayarlar › Bildirimler).
	Origin       string          `json:"origin"`
	Engine       string          `json:"engine"`
	Model        string          `json:"model"`
	Tainted      bool            `json:"tainted"`
	PendingAsk   json.RawMessage `json:"pending_ask,omitempty"`
	Error        string          `json:"error,omitempty"`
	CostUSD      float64         `json:"cost_usd"`
	InputTokens  int64           `json:"input_tokens"`
	OutputTokens int64           `json:"output_tokens"`
	NumTurns     int32           `json:"num_turns"`
	FinalText    string          `json:"final_text,omitempty"`
	Attempt      int32           `json:"attempt"`
	QueuedAt     time.Time       `json:"queued_at"`
	StartedAt    *time.Time      `json:"started_at,omitempty"`
	FinishedAt   *time.Time      `json:"finished_at,omitempty"`
}

func toRunDTO(r db.Run) runDTO {
	return runDTO{
		ID: r.ID, BotID: r.BotID, Status: r.Status, Prompt: r.Prompt, Origin: r.Origin,
		Engine: r.Engine, Model: r.Model, Tainted: r.Tainted, PendingAsk: r.PendingAsk,
		Error: r.Error, CostUSD: r.CostUsd, InputTokens: r.InputTokens,
		OutputTokens: r.OutputTokens, NumTurns: r.NumTurns,
		FinalText: r.FinalText, Attempt: r.Attempt,
		QueuedAt: r.QueuedAt.UTC(), StartedAt: r.StartedAt, FinishedAt: r.FinishedAt,
	}
}

func (a *api) runFromPath(w http.ResponseWriter, r *http.Request) (db.Run, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "geçersiz koşu id")
		return db.Run{}, false
	}
	run, err := a.st.Q.GetRun(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "koşu bulunamadı")
		return db.Run{}, false
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return db.Run{}, false
	}
	return run, true
}

func (a *api) getRun(w http.ResponseWriter, r *http.Request) {
	run, ok := a.runFromPath(w, r)
	if !ok {
		return
	}
	dto := toRunDTO(run)
	// Süreç içinde şu an cevap bekleyen istek varsa kartı zenginleştir
	// (kısa-bekleme penceresi — henüz park edilmedi).
	if ask, waiting := a.orch.PendingAsk(run.ID); waiting {
		if b, err := json.Marshal(ask); err == nil {
			dto.PendingAsk = b
		}
	}
	writeJSON(w, http.StatusOK, dto)
}

type approvalReq struct {
	Decision string `json:"decision"` // allow | deny (izin istekleri)
	Answer   string `json:"answer"`   // soru cevabı (question istekleri)
	// Always — "hep izin ver/reddet": karar kurala dönüşür (K17 vetoları
	// sunucuda işler: yüksek risk ve tainted koşuda allow kuralı yazılmaz).
	Always bool `json:"always"`
}

func (r *approvalReq) Validate() error {
	if r.Decision == "" && r.Answer == "" {
		return errors.New("decision (allow|deny) ya da answer gerekli")
	}
	if r.Decision != "" && r.Decision != "allow" && r.Decision != "deny" {
		return errors.New("decision allow|deny olmalı")
	}
	if r.Always && r.Decision == "" {
		return errors.New("always yalnız decision ile kullanılır")
	}
	return nil
}

func (a *api) resolveApproval(w http.ResponseWriter, r *http.Request) {
	run, ok := a.runFromPath(w, r)
	if !ok {
		return
	}
	var req approvalReq
	if err := decodeStrict(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "geçersiz gövde: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	err := a.orch.ResolveApproval(r.Context(), run.ID, req.Decision, req.Answer, req.Always)
	switch {
	case errors.Is(err, orchestrator.ErrNotAwaiting):
		writeErr(w, http.StatusConflict, "koşu onay/cevap beklemiyor")
	case err != nil:
		writeErr(w, http.StatusBadRequest, err.Error())
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
