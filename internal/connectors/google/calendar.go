package google

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// ── Takvim REST (calendars/primary) ────────────────────────────────────

// Event — bot'a verilen kompakt etkinlik.
type Event struct {
	ID      string
	Summary string
	Start   string // RFC3339 ya da tüm-gün için YYYY-MM-DD
	End     string
	Where   string
}

type calTime struct {
	DateTime string `json:"dateTime,omitempty"`
	Date     string `json:"date,omitempty"`
	TimeZone string `json:"timeZone,omitempty"`
}

func (t calTime) String() string {
	if t.DateTime != "" {
		return t.DateTime
	}
	return t.Date
}

// ListEvents — [timeMin, timeMax) aralığındaki etkinlikler, başlangıca göre.
func (s *Service) ListEvents(ctx context.Context, timeMin, timeMax time.Time, maxResults int) ([]Event, error) {
	if maxResults <= 0 || maxResults > 50 {
		maxResults = 20
	}
	var out struct {
		Items []struct {
			ID       string  `json:"id"`
			Summary  string  `json:"summary"`
			Location string  `json:"location"`
			Start    calTime `json:"start"`
			End      calTime `json:"end"`
		} `json:"items"`
	}
	u := fmt.Sprintf("%s/calendar/v3/calendars/primary/events?singleEvents=true&orderBy=startTime&timeMin=%s&timeMax=%s&maxResults=%d",
		s.CalendarBase,
		url.QueryEscape(timeMin.Format(time.RFC3339)),
		url.QueryEscape(timeMax.Format(time.RFC3339)),
		maxResults)
	if err := s.getJSON(ctx, u, &out); err != nil {
		return nil, err
	}
	evs := make([]Event, 0, len(out.Items))
	for _, it := range out.Items {
		evs = append(evs, Event{
			ID: it.ID, Summary: it.Summary, Where: it.Location,
			Start: it.Start.String(), End: it.End.String(),
		})
	}
	return evs, nil
}

// CreateEvent — primary takvime etkinlik. Katılımcı verilirse Google davet
// GÖNDERİR — araç bu yüzden external sınıfındadır (onay kapısına düşer).
func (s *Service) CreateEvent(ctx context.Context, summary, description string, start, end time.Time, attendees []string) (Event, error) {
	att := make([]map[string]string, 0, len(attendees))
	for _, a := range attendees {
		att = append(att, map[string]string{"email": a})
	}
	body := map[string]any{
		"summary":     summary,
		"description": description,
		"start":       calTime{DateTime: start.Format(time.RFC3339)},
		"end":         calTime{DateTime: end.Format(time.RFC3339)},
	}
	if len(att) > 0 {
		body["attendees"] = att
	}
	var out struct {
		ID      string  `json:"id"`
		Summary string  `json:"summary"`
		Start   calTime `json:"start"`
		End     calTime `json:"end"`
	}
	u := s.CalendarBase + "/calendar/v3/calendars/primary/events?sendUpdates=all"
	if err := s.postJSON(ctx, u, body, &out); err != nil {
		return Event{}, err
	}
	return Event{ID: out.ID, Summary: out.Summary, Start: out.Start.String(), End: out.End.String()}, nil
}
