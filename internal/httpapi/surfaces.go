// Çapraz-bot yüzeyler (Faz 8, K19): Onay Merkezi toplu onayı, Görevler
// panosu ve Aktivite akışı. Hepsi mevcut runs/routines/local_audit
// verisinin insan-okur birleşimidir — yeni durum makinesi yok.
package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/esmworks/OpenAssist/internal/policy"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

// bulkVetoTools — "Tümünü onayla" ASLA dokunmaz (K19): silme ve satın alma
// benzeri araçlar tek tek onaylanır. Sınıf vetoları (execute, Ruleable=false,
// tainted) ayrıca işler; bu liste ada dayalı son savunmadır.
func bulkVetoed(tool string) bool {
	lower := strings.ToLower(tool)
	for _, pat := range []string{"delete", "trash", "remove", "purchase", "buy", "pay", "sil"} {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

type bulkResultItem struct {
	RunID  uuid.UUID `json:"run_id"`
	Tool   string    `json:"tool,omitempty"`
	Status string    `json:"status"` // approved | skipped
	Reason string    `json:"reason,omitempty"`
}

// bulkApprove — bekleyen İZİN isteklerini topluca onaylar. Sorular, tainted
// koşular, execute/kural-alamaz sınıflar ve silme/satın alma kalıpları
// ATLANIR — sınırlı toplu onay (K19; onay geri alınamaz, sınır baştan).
func (a *api) bulkApprove(w http.ResponseWriter, r *http.Request) {
	rows, err := a.st.Q.ListRunsByStatus(r.Context(), db.ListRunsByStatusParams{
		Status: "approval_pending", Limit: 100,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]bulkResultItem, 0, len(rows))
	for _, run := range rows {
		var ask struct {
			Kind string `json:"kind"`
			Tool string `json:"tool"`
		}
		_ = json.Unmarshal(run.PendingAsk, &ask)
		item := bulkResultItem{RunID: run.ID, Tool: ask.Tool, Status: "skipped"}
		switch {
		case ask.Kind == "question":
			item.Reason = "soru — cevap gerektirir, toplu onaylanamaz"
		case run.Tainted:
			item.Reason = "tainted — dış içerik okundu, tek tek incele"
		case ask.Tool == "":
			item.Reason = "istek çözümlenemedi"
		case bulkVetoed(ask.Tool):
			item.Reason = "silme/satın alma sınıfı — tek tek onayla"
		default:
			cls := a.orch.ClassifyTool(ask.Tool)
			if cls.Class == policy.ClassExecute || !cls.Ruleable {
				item.Reason = "yüksek risk sınıfı — tek tek onayla"
				break
			}
			if err := a.orch.ResolveApproval(r.Context(), run.ID, "allow", "", false); err != nil {
				item.Reason = err.Error()
			} else {
				item.Status = "approved"
			}
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": out})
}

// tasksBoard — Görevler panosu: aktif işler + rutin vitrini (K19).
func (a *api) tasksBoard(w http.ResponseWriter, r *http.Request) {
	type board struct {
		Running  []runDTO     `json:"running"`
		Queued   []runDTO     `json:"queued"`
		Pending  []runDTO     `json:"approval_pending"`
		Routines []routineDTO `json:"routines"`
	}
	var b board
	for status, dst := range map[string]*[]runDTO{
		"running": &b.Running, "queued": &b.Queued, "approval_pending": &b.Pending,
	} {
		rows, err := a.st.Q.ListRunsByStatus(r.Context(), db.ListRunsByStatusParams{Status: status, Limit: 50})
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		out := make([]runDTO, 0, len(rows))
		for _, run := range rows {
			out = append(out, toRunDTO(run))
		}
		*dst = out
	}
	routines, err := a.st.Q.ListRoutines(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	b.Routines = make([]routineDTO, 0, len(routines))
	for _, rt := range routines {
		b.Routines = append(b.Routines, toRoutineDTO(rt))
	}
	writeJSON(w, http.StatusOK, b)
}

// activityFeed — Aktivite: koşular + yerel işlemler tek zaman akışında
// (K19: run_events ham verisi değil, insan-okur satırlar).
func (a *api) activityFeed(w http.ResponseWriter, r *http.Request) {
	limit := queryLimit(r, 50)
	type item struct {
		Type   string     `json:"type"` // run | local
		At     time.Time  `json:"at"`
		BotID  *uuid.UUID `json:"bot_id,omitempty"`
		RunID  *uuid.UUID `json:"run_id,omitempty"`
		Status string     `json:"status,omitempty"`
		Text   string     `json:"text"`
		Origin string     `json:"origin,omitempty"`
		Ok     *bool      `json:"ok,omitempty"`
	}
	runs, err := a.st.Q.ListRecentRuns(r.Context(), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	audits, err := a.st.Q.ListLocalAudit(r.Context(), limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]item, 0, len(runs)+len(audits))
	for _, run := range runs {
		at := run.QueuedAt
		if run.FinishedAt != nil {
			at = *run.FinishedAt
		}
		id := run.ID
		botID := run.BotID
		items = append(items, item{
			Type: "run", At: at, BotID: &botID, RunID: &id,
			Status: run.Status, Text: clipText(run.Prompt, 140), Origin: run.Origin,
		})
	}
	for _, au := range audits {
		ok := au.Ok
		it := item{
			Type: "local", At: au.CreatedAt, Ok: &ok,
			Text: au.Tool + " · " + clipText(au.Result, 140),
		}
		if au.RunID.Valid {
			id := au.RunID.UUID
			it.RunID = &id
		}
		items = append(items, it)
	}
	// Zaman akışı: yeniden eskiye.
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].At.After(items[j-1].At); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
	if int32(len(items)) > limit {
		items = items[:limit]
	}
	writeJSON(w, http.StatusOK, items)
}

func clipText(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
