package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/esmworks/OpenAssist/internal/routines"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

// routineDTO — API sözleşmesi (Faz 7). interval alanları v1'de taşınır ama
// işletilmez (K14: şema iki tetikleyiciyi de bilir).
type routineDTO struct {
	ID             uuid.UUID  `json:"id"`
	BotID          uuid.UUID  `json:"bot_id"`
	Name           string     `json:"name"`
	TriggerKind    string     `json:"trigger_kind"`
	CronExpr       string     `json:"cron_expr"`
	Timezone       string     `json:"timezone"`
	InputSource    string     `json:"input_source"`
	ExpectedOutput string     `json:"expected_output"`
	ApprovalNote   string     `json:"approval_note"`
	StalePolicy    string     `json:"stale_policy"`
	Enabled        bool       `json:"enabled"`
	NextRunAt      *time.Time `json:"next_run_at,omitempty"`
	LastRunAt      *time.Time `json:"last_run_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

func toRoutineDTO(r db.Routine) routineDTO {
	return routineDTO{
		ID: r.ID, BotID: r.BotID, Name: r.Name, TriggerKind: r.TriggerKind,
		CronExpr: r.CronExpr, Timezone: r.Timezone, InputSource: r.InputSource,
		ExpectedOutput: r.ExpectedOutput, ApprovalNote: r.ApprovalNote,
		StalePolicy: r.StalePolicy, Enabled: r.Enabled,
		NextRunAt: r.NextRunAt, LastRunAt: r.LastRunAt, CreatedAt: r.CreatedAt,
	}
}

func (a *api) listRoutines(w http.ResponseWriter, r *http.Request) {
	var (
		rows []db.Routine
		err  error
	)
	if botRef := r.URL.Query().Get("bot"); botRef != "" {
		id, perr := uuid.Parse(botRef)
		if perr != nil {
			writeErr(w, http.StatusBadRequest, "geçersiz bot id")
			return
		}
		rows, err = a.st.Q.ListRoutinesByBot(r.Context(), id)
	} else {
		rows, err = a.st.Q.ListRoutines(r.Context())
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]routineDTO, 0, len(rows))
	for _, row := range rows {
		out = append(out, toRoutineDTO(row))
	}
	writeJSON(w, http.StatusOK, out)
}

type createRoutineReq struct {
	BotID          uuid.UUID `json:"bot_id"`
	Name           string    `json:"name"`
	CronExpr       string    `json:"cron_expr"`
	Timezone       string    `json:"timezone"`
	InputSource    string    `json:"input_source"`
	ExpectedOutput string    `json:"expected_output"`
	ApprovalNote   string    `json:"approval_note"`
	StalePolicy    string    `json:"stale_policy"`
}

func (r *createRoutineReq) Validate() error {
	if r.Name == "" {
		return errors.New("name boş olamaz")
	}
	if r.StalePolicy == "" {
		r.StalePolicy = "fail"
	}
	if r.StalePolicy != "fail" && r.StalePolicy != "warn_stale" {
		return errors.New("stale_policy fail|warn_stale olmalı")
	}
	// K11 zorunlu alanlar: girdi kaynağı ve beklenen çıktı tarif edilmeden
	// rutin kurulmaz (bozuk tanım sessiz gürültü üretir).
	if r.InputSource == "" || r.ExpectedOutput == "" {
		return errors.New("input_source ve expected_output zorunlu (K11)")
	}
	return routines.Validate("cron", r.CronExpr, r.Timezone)
}

func (a *api) createRoutine(w http.ResponseWriter, r *http.Request) {
	var req createRoutineReq
	if err := decodeStrict(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "geçersiz gövde: "+err.Error())
		return
	}
	if err := req.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := a.st.Q.GetBot(r.Context(), req.BotID); err != nil {
		writeErr(w, http.StatusNotFound, "bot bulunamadı")
		return
	}
	next, err := routines.NextRun(req.CronExpr, req.Timezone, time.Now())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	row, err := a.st.Q.CreateRoutine(r.Context(), db.CreateRoutineParams{
		BotID: req.BotID, Name: req.Name, TriggerKind: "cron",
		CronExpr: req.CronExpr, Timezone: req.Timezone,
		InputSource: req.InputSource, ExpectedOutput: req.ExpectedOutput,
		ApprovalNote: req.ApprovalNote, StalePolicy: req.StalePolicy,
		NextRunAt: &next,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, toRoutineDTO(row))
}

func (a *api) routineFromPath(w http.ResponseWriter, r *http.Request) (db.Routine, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "geçersiz rutin id")
		return db.Routine{}, false
	}
	row, err := a.st.Q.GetRoutine(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "rutin bulunamadı")
		return db.Routine{}, false
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return db.Routine{}, false
	}
	return row, true
}

type updateRoutineReq struct {
	Name           *string `json:"name"`
	CronExpr       *string `json:"cron_expr"`
	Timezone       *string `json:"timezone"`
	InputSource    *string `json:"input_source"`
	ExpectedOutput *string `json:"expected_output"`
	ApprovalNote   *string `json:"approval_note"`
	StalePolicy    *string `json:"stale_policy"`
	Enabled        *bool   `json:"enabled"`
}

func (a *api) updateRoutine(w http.ResponseWriter, r *http.Request) {
	row, ok := a.routineFromPath(w, r)
	if !ok {
		return
	}
	var req updateRoutineReq
	if err := decodeStrict(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "geçersiz gövde: "+err.Error())
		return
	}
	pick := func(p, cur *string) string {
		if p != nil {
			return *p
		}
		return *cur
	}
	name := pick(req.Name, &row.Name)
	cronExpr := pick(req.CronExpr, &row.CronExpr)
	tz := pick(req.Timezone, &row.Timezone)
	stale := pick(req.StalePolicy, &row.StalePolicy)
	if stale != "fail" && stale != "warn_stale" {
		writeErr(w, http.StatusBadRequest, "stale_policy fail|warn_stale olmalı")
		return
	}
	if err := routines.Validate("cron", cronExpr, tz); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	next, err := routines.NextRun(cronExpr, tz, time.Now())
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	upd, err := a.st.Q.UpdateRoutine(r.Context(), db.UpdateRoutineParams{
		ID: row.ID, Name: name, CronExpr: cronExpr, Timezone: tz,
		InputSource:    pick(req.InputSource, &row.InputSource),
		ExpectedOutput: pick(req.ExpectedOutput, &row.ExpectedOutput),
		ApprovalNote:   pick(req.ApprovalNote, &row.ApprovalNote),
		StalePolicy:    stale, NextRunAt: &next,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if req.Enabled != nil && *req.Enabled != row.Enabled {
		nextPtr := &next
		if !*req.Enabled {
			nextPtr = nil // duraklatılan rutin vadesiz kalır
		}
		upd, err = a.st.Q.SetRoutineEnabled(r.Context(), db.SetRoutineEnabledParams{
			ID: row.ID, Enabled: *req.Enabled, NextRunAt: nextPtr,
		})
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, toRoutineDTO(upd))
}

func (a *api) deleteRoutine(w http.ResponseWriter, r *http.Request) {
	row, ok := a.routineFromPath(w, r)
	if !ok {
		return
	}
	if _, err := a.st.Q.DeleteRoutine(r.Context(), row.ID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) listRoutineRuns(w http.ResponseWriter, r *http.Request) {
	row, ok := a.routineFromPath(w, r)
	if !ok {
		return
	}
	rows, err := a.st.Q.ListRoutineRuns(r.Context(), row.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	type item struct {
		FiredAt time.Time `json:"fired_at"`
		Run     runDTO    `json:"run"`
	}
	out := make([]item, 0, len(rows))
	for _, rr := range rows {
		out = append(out, item{FiredAt: rr.FiredAt, Run: toRunDTO(db.Run{
			ID: rr.ID, BotID: rr.BotID, Status: rr.Status, Prompt: rr.Prompt,
			Engine: rr.Engine, Model: rr.Model, Tainted: rr.Tainted,
			Error: rr.Error, CostUsd: rr.CostUsd, InputTokens: rr.InputTokens,
			OutputTokens: rr.OutputTokens, NumTurns: rr.NumTurns,
			FinalText: rr.FinalText, Attempt: rr.Attempt, Origin: rr.Origin,
			QueuedAt: rr.QueuedAt, StartedAt: rr.StartedAt, FinishedAt: rr.FinishedAt,
		})})
	}
	writeJSON(w, http.StatusOK, out)
}

// ── hafıza + skill + denetim ───────────────────────────────────────────

// memoryDTO — hafıza kaydının API sözleşmesi. Liste ve arama aynı gövdeyi
// döner; `revision` istemcinin geçmiş/geri alma yüzeyini kurabilmesi için.
type memoryDTO struct {
	ID        uuid.UUID  `json:"id"`
	Scope     string     `json:"scope"`
	BotID     *uuid.UUID `json:"bot_id,omitempty"`
	Kind      string     `json:"kind"`
	Content   string     `json:"content"`
	Revision  int32      `json:"revision"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

func memoryDTOs(rows []db.Memory) []memoryDTO {
	out := make([]memoryDTO, 0, len(rows))
	for _, m := range rows {
		d := memoryDTO{
			ID: m.ID, Scope: m.Scope, Kind: m.Kind, Content: m.Content,
			Revision: m.Revision, ExpiresAt: m.ExpiresAt, CreatedAt: m.CreatedAt,
		}
		if m.BotID.Valid {
			id := m.BotID.UUID
			d.BotID = &id
		}
		out = append(out, d)
	}
	return out
}

func (a *api) listMemories(w http.ResponseWriter, r *http.Request) {
	rows, err := a.st.Q.ListMemories(r.Context(), queryLimit(r, 200))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, memoryDTOs(rows))
}

func (a *api) deleteMemory(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "geçersiz id")
		return
	}
	n, err := a.st.Q.DeleteMemory(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n == 0 {
		writeErr(w, http.StatusNotFound, "kayıt bulunamadı")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) listSkills(w http.ResponseWriter, r *http.Request) {
	rows, err := a.st.Q.ListSkills(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	type skillDTO struct {
		ID           uuid.UUID `json:"id"`
		Name         string    `json:"name"`
		WhenToUse    string    `json:"when_to_use"`
		Instructions string    `json:"instructions"`
		CreatedAt    time.Time `json:"created_at"`
	}
	out := make([]skillDTO, 0, len(rows))
	for _, s := range rows {
		out = append(out, skillDTO{
			ID: s.ID, Name: s.Name, WhenToUse: s.WhenToUse,
			Instructions: s.Instructions, CreatedAt: s.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *api) listAudit(w http.ResponseWriter, r *http.Request) {
	rows, err := a.st.Q.ListLocalAudit(r.Context(), queryLimit(r, 100))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	type auditDTO struct {
		ID        int64      `json:"id"`
		RunID     *uuid.UUID `json:"run_id,omitempty"`
		Device    string     `json:"device"`
		Tool      string     `json:"tool"`
		Ok        bool       `json:"ok"`
		Result    string     `json:"result"`
		CreatedAt time.Time  `json:"created_at"`
	}
	out := make([]auditDTO, 0, len(rows))
	for _, row := range rows {
		d := auditDTO{
			ID: row.ID, Device: row.Device, Tool: row.Tool,
			Ok: row.Ok, Result: row.Result, CreatedAt: row.CreatedAt,
		}
		if row.RunID.Valid {
			id := row.RunID.UUID
			d.RunID = &id
		}
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, out)
}
