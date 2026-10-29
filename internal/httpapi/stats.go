package httpapi

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
)

// stats — koşu maliyet panosu (Faz 9): son N günün toplamları + bot kırılımı
// (tasarım: Ayarlar > İstatistikler, 7/30 gün segmenti).
func (a *api) stats(w http.ResponseWriter, r *http.Request) {
	days := 7
	if s := r.URL.Query().Get("days"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > 365 {
			writeErr(w, http.StatusBadRequest, "days 1-365 aralığında olmalı")
			return
		}
		days = n
	}
	totals, err := a.st.Q.RunStats(r.Context(), int32(days))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	perBot, err := a.st.Q.RunStatsByBot(r.Context(), int32(days))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	type botStat struct {
		BotID   uuid.UUID `json:"bot_id"`
		Runs    int64     `json:"runs"`
		CostUSD float64   `json:"cost_usd"`
	}
	bots := make([]botStat, 0, len(perBot))
	for _, b := range perBot {
		bots = append(bots, botStat{BotID: b.BotID, Runs: b.Runs, CostUSD: b.CostUsd})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"days":           days,
		"total_runs":     totals.TotalRuns,
		"completed":      totals.Completed,
		"failed":         totals.Failed,
		"expired":        totals.Expired,
		"pending":        totals.Pending,
		"total_cost_usd": totals.TotalCostUsd,
		"input_tokens":   totals.InputTokens,
		"output_tokens":  totals.OutputTokens,
		"by_bot":         bots,
	})
}
