package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/esmworks/OpenAssist/internal/bus"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

// runEvents — koşu olaylarının SSE akışı. Kaldığın yerden devam:
// Last-Event-ID başlığı (ya da ?after=) run_events.id'dir; kalıcı olaylar
// geri doldurulur, sonrası canlı bus'tan gelir. content.delta olayları
// kalıcı değildir (id'siz akar) — kopan istemci metnin bütününü
// turn.completed/mesajlardan alır.
//
// Akış üç yoldan kapanır: bağlanma anında koşu zaten terminalse, canlı
// bus.KindRunSettled terminal durum bildirdiğinde, ya da güvenlik ağı olarak
// keepalive turundaki durum yoklamasında.
func (a *api) runEvents(w http.ResponseWriter, r *http.Request) {
	run, ok := a.runFromPath(w, r)
	if !ok {
		return
	}
	fl, canFlush := w.(http.Flusher)
	if !canFlush {
		writeErr(w, http.StatusInternalServerError, "akış desteklenmiyor")
		return
	}

	lastID := int64(0)
	if s := r.Header.Get("Last-Event-ID"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			lastID = n
		}
	} else if s := r.URL.Query().Get("after"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			lastID = n
		}
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	send := func(ev bus.Event) {
		if ev.ID > 0 {
			_, _ = fmt.Fprintf(w, "id: %d\n", ev.ID)
		}
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Kind, ev.Payload)
		fl.Flush()
	}
	endStream := func() {
		_, _ = fmt.Fprint(w, "event: stream.end\ndata: {}\n\n")
		fl.Flush()
	}

	// Önce abone ol, sonra geri doldur — arada düşen olay kaçmasın;
	// çiftler lastID ile elenir.
	live, cancel := a.bus.Subscribe(run.ID)
	defer cancel()

	for {
		rows, err := a.st.Q.ListRunEventsAfter(r.Context(), db.ListRunEventsAfterParams{RunID: run.ID, ID: lastID})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return
		}
		for _, ev := range rows {
			send(bus.Event{ID: ev.ID, RunID: ev.RunID, Kind: ev.Kind, Payload: ev.Payload, At: ev.CreatedAt})
			lastID = ev.ID
		}
		if len(rows) < 1000 {
			break
		}
	}

	// Koşu geri doldurma anında bitmişse akışı kapat.
	if cur, err := a.st.Q.GetRun(r.Context(), run.ID); err == nil && isTerminal(cur.Status) {
		endStream()
		return
	}

	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			// Güvenlik ağı: kapanış işareti kaçtıysa (reaper'ın expired'a
			// çektiği koşu, düşen bus olayı) akış burada kapanır.
			if cur, err := a.st.Q.GetRun(r.Context(), run.ID); err == nil && isTerminal(cur.Status) {
				endStream()
				return
			}
			_, _ = fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		case ev, okc := <-live:
			if !okc {
				return
			}
			if ev.ID > 0 && ev.ID <= lastID {
				continue // geri doldurmayla çift düştü
			}
			send(ev)
			if ev.ID > 0 {
				lastID = ev.ID
			}
			// turn.completed koşunun DB durumu yazılmadan ÖNCE akar — ona
			// bakarak kapatmak yarışa girer (durum hâlâ 'running' görünür).
			// Kararı durum yazıldıktan sonra gelen run.settled verir.
			if ev.Kind == bus.KindRunSettled {
				var p struct {
					Status string `json:"status"`
				}
				if json.Unmarshal(ev.Payload, &p) == nil && isTerminal(p.Status) {
					endStream()
					return
				}
				// approval_pending: akış açık kalır — resume aynı koşuda sürer.
			}
		}
	}
}

// isTerminal — koşu için başka olay gelmeyecek durumlar. approval_pending
// terminal DEĞİLDİR: onay gelince aynı koşu resume ile devam eder; TTL
// dolarsa (expired) artık devam edilemez.
func isTerminal(status string) bool {
	return status == "completed" || status == "failed" || status == "canceled" || status == "expired"
}
