package google

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/esmworks/OpenAssist/internal/gateway"
	"github.com/esmworks/OpenAssist/internal/policy"
)

// RegisterTools — Google araçlarını gateway'e sınıf bildirimiyle kaydeder.
// Sınıf gerekçeleri (K17/§5.1):
//   - search/read/list: read + DataSource → koşuyu tainted yapar
//   - create_draft: write — dışa dönük DEĞİL; gece taslak hazırlama sessiz
//     saat vetosuna takılmaz (Ek A ritmi)
//   - send_draft / create_event: external → onay kapısı + sessiz saat
func RegisterTools(g *gateway.Gateway, s *Service) {
	read := policy.Classification{Class: policy.ClassRead, DataSource: true, Ruleable: true}

	g.Register(gateway.Tool{
		Name:        "gmail_search",
		Description: "Gmail'de mesaj arar (Gmail arama sözdizimi: is:unread, from:, newer_than:2d…). Kompakt özet listesi döner.",
		InputSchema: schema(`{"query":{"type":"string","description":"Gmail arama sorgusu"},"max_results":{"type":"integer","minimum":1,"maximum":25}}`, "query"),
		Class:       read,
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a struct {
				Query      string `json:"query"`
				MaxResults int    `json:"max_results"`
			}
			if err := strictArgs(raw, &a); err != nil {
				return "", err
			}
			msgs, err := s.SearchMessages(ctx, a.Query, a.MaxResults)
			if err != nil {
				return "", err
			}
			if len(msgs) == 0 {
				return "Sonuç yok.", nil
			}
			var b strings.Builder
			for _, m := range msgs {
				fmt.Fprintf(&b, "id=%s | kimden=%s | konu=%s | tarih=%s\n  %s\n", m.ID, m.From, m.Subject, m.Date, m.Snippet)
			}
			return b.String(), nil
		},
	})

	g.Register(gateway.Tool{
		Name:        "gmail_read",
		Description: "Bir Gmail mesajının tamamını okur (başlıklar + düz metin gövde).",
		InputSchema: schema(`{"message_id":{"type":"string"}}`, "message_id"),
		Class:       read,
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a struct {
				MessageID string `json:"message_id"`
			}
			if err := strictArgs(raw, &a); err != nil {
				return "", err
			}
			m, err := s.GetMessage(ctx, a.MessageID)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("id=%s thread=%s\nKimden: %s\nKime: %s\nKonu: %s\nTarih: %s\n\n%s",
				m.ID, m.ThreadID, m.From, m.To, m.Subject, m.Date, m.Body), nil
		},
	})

	g.Register(gateway.Tool{
		Name:        "gmail_create_draft",
		Description: "Gmail taslağı oluşturur (GÖNDERMEZ). thread_id verilirse yanıt taslağı olur. Gönderim ayrı araçtır ve onay gerektirir.",
		InputSchema: schema(`{"to":{"type":"string"},"subject":{"type":"string"},"body":{"type":"string"},"thread_id":{"type":"string"}}`, "to", "subject", "body"),
		Class:       policy.Classification{Class: policy.ClassWrite, Ruleable: true},
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a struct {
				To       string `json:"to"`
				Subject  string `json:"subject"`
				Body     string `json:"body"`
				ThreadID string `json:"thread_id"`
			}
			if err := strictArgs(raw, &a); err != nil {
				return "", err
			}
			id, err := s.CreateDraft(ctx, a.To, a.Subject, a.Body, a.ThreadID)
			if err != nil {
				return "", err
			}
			return "Taslak oluşturuldu: draft_id=" + id, nil
		},
	})

	g.Register(gateway.Tool{
		Name:        "gmail_send_draft",
		Description: "VAR OLAN Gmail taslağını gönderir. Dışa dönük eylemdir; onay kapısından geçer.",
		InputSchema: schema(`{"draft_id":{"type":"string"}}`, "draft_id"),
		Class:       policy.Classification{Class: policy.ClassExternal, Ruleable: true},
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a struct {
				DraftID string `json:"draft_id"`
			}
			if err := strictArgs(raw, &a); err != nil {
				return "", err
			}
			id, err := s.SendDraft(ctx, a.DraftID)
			if err != nil {
				return "", err
			}
			return "Gönderildi: message_id=" + id, nil
		},
	})

	g.Register(gateway.Tool{
		Name:        "calendar_list_events",
		Description: "Birincil takvimdeki etkinlikleri listeler. Aralık verilmezse önümüzdeki 7 gün.",
		InputSchema: schema(`{"time_min":{"type":"string","description":"RFC3339"},"time_max":{"type":"string","description":"RFC3339"},"max_results":{"type":"integer","minimum":1,"maximum":50}}`),
		Class:       read,
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a struct {
				TimeMin    string `json:"time_min"`
				TimeMax    string `json:"time_max"`
				MaxResults int    `json:"max_results"`
			}
			if err := strictArgs(raw, &a); err != nil {
				return "", err
			}
			minT, maxT, err := eventRange(a.TimeMin, a.TimeMax)
			if err != nil {
				return "", err
			}
			evs, err := s.ListEvents(ctx, minT, maxT, a.MaxResults)
			if err != nil {
				return "", err
			}
			if len(evs) == 0 {
				return "Etkinlik yok.", nil
			}
			var b strings.Builder
			for _, e := range evs {
				fmt.Fprintf(&b, "id=%s | %s | %s → %s", e.ID, e.Summary, e.Start, e.End)
				if e.Where != "" {
					fmt.Fprintf(&b, " | yer=%s", e.Where)
				}
				b.WriteString("\n")
			}
			return b.String(), nil
		},
	})

	g.Register(gateway.Tool{
		Name:        "calendar_create_event",
		Description: "Birincil takvime etkinlik ekler. Katılımcı verilirse Google DAVET GÖNDERİR — onay kapısından geçer.",
		InputSchema: schema(`{"summary":{"type":"string"},"description":{"type":"string"},"start":{"type":"string","description":"RFC3339"},"end":{"type":"string","description":"RFC3339"},"attendees":{"type":"array","items":{"type":"string"}}}`, "summary", "start", "end"),
		Class:       policy.Classification{Class: policy.ClassExternal, Ruleable: true},
		Handler: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var a struct {
				Summary     string   `json:"summary"`
				Description string   `json:"description"`
				Start       string   `json:"start"`
				End         string   `json:"end"`
				Attendees   []string `json:"attendees"`
			}
			if err := strictArgs(raw, &a); err != nil {
				return "", err
			}
			st, err := time.Parse(time.RFC3339, a.Start)
			if err != nil {
				return "", fmt.Errorf("start RFC3339 olmalı: %w", err)
			}
			en, err := time.Parse(time.RFC3339, a.End)
			if err != nil {
				return "", fmt.Errorf("end RFC3339 olmalı: %w", err)
			}
			ev, err := s.CreateEvent(ctx, a.Summary, a.Description, st, en, a.Attendees)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Etkinlik oluşturuldu: id=%s | %s | %s → %s", ev.ID, ev.Summary, ev.Start, ev.End), nil
		},
	})
}

// schema — object şeması kurar; bizim kontratımız katı:
// additionalProperties=false.
func schema(props string, required ...string) json.RawMessage {
	req := "[]"
	if len(required) > 0 {
		b, _ := json.Marshal(required)
		req = string(b)
	}
	return json.RawMessage(fmt.Sprintf(
		`{"type":"object","properties":{%s},"required":%s,"additionalProperties":false}`, props, req))
}

// strictArgs — araç argümanları bizim kontratımızdır: bilinmeyen alan hatadır.
func strictArgs(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// eventRange — verilmezse şimdi..+7 gün.
func eventRange(minS, maxS string) (time.Time, time.Time, error) {
	now := time.Now()
	minT, maxT := now, now.Add(7*24*time.Hour)
	var err error
	if minS != "" {
		if minT, err = time.Parse(time.RFC3339, minS); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("time_min RFC3339 olmalı: %w", err)
		}
	}
	if maxS != "" {
		if maxT, err = time.Parse(time.RFC3339, maxS); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("time_max RFC3339 olmalı: %w", err)
		}
	}
	return minT, maxT, nil
}
