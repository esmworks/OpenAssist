package google

import (
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	"net/url"
	"strings"
)

// ── Gmail REST (users/me) ──────────────────────────────────────────────

type gmailHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type gmailPart struct {
	MimeType string      `json:"mimeType"`
	Body     gmailBody   `json:"body"`
	Parts    []gmailPart `json:"parts"`
}

type gmailBody struct {
	Data string `json:"data"` // base64url
}

type gmailMessage struct {
	ID       string `json:"id"`
	ThreadID string `json:"threadId"`
	Snippet  string `json:"snippet"`
	Payload  struct {
		Headers []gmailHeader `json:"headers"`
		gmailPart
	} `json:"payload"`
}

// MessageSummary — arama sonucu satırı (bot'a verilen kompakt biçim).
type MessageSummary struct {
	ID      string
	From    string
	Subject string
	Date    string
	Snippet string
}

// SearchMessages — Gmail arama sözdizimi (is:unread, from:, newer_than:2d…).
func (s *Service) SearchMessages(ctx context.Context, query string, maxResults int) ([]MessageSummary, error) {
	if maxResults <= 0 || maxResults > 25 {
		maxResults = 10
	}
	var list struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	u := fmt.Sprintf("%s/gmail/v1/users/me/messages?q=%s&maxResults=%d",
		s.GmailBase, url.QueryEscape(query), maxResults)
	if err := s.getJSON(ctx, u, &list); err != nil {
		return nil, err
	}
	out := make([]MessageSummary, 0, len(list.Messages))
	for _, m := range list.Messages {
		var msg gmailMessage
		mu := fmt.Sprintf("%s/gmail/v1/users/me/messages/%s?format=metadata&metadataHeaders=From&metadataHeaders=Subject&metadataHeaders=Date",
			s.GmailBase, url.PathEscape(m.ID))
		if err := s.getJSON(ctx, mu, &msg); err != nil {
			return nil, err
		}
		sum := MessageSummary{ID: msg.ID, Snippet: msg.Snippet}
		for _, h := range msg.Payload.Headers {
			switch h.Name {
			case "From":
				sum.From = decodeHeader(h.Value)
			case "Subject":
				sum.Subject = decodeHeader(h.Value)
			case "Date":
				sum.Date = h.Value
			}
		}
		out = append(out, sum)
	}
	return out, nil
}

// FullMessage — okuma sonucu: başlıklar + düz metin gövde.
type FullMessage struct {
	ID       string
	ThreadID string
	From     string
	To       string
	Subject  string
	Date     string
	Body     string
}

const maxBodyChars = 20000

func (s *Service) GetMessage(ctx context.Context, id string) (FullMessage, error) {
	var msg gmailMessage
	u := fmt.Sprintf("%s/gmail/v1/users/me/messages/%s?format=full", s.GmailBase, url.PathEscape(id))
	if err := s.getJSON(ctx, u, &msg); err != nil {
		return FullMessage{}, err
	}
	out := FullMessage{ID: msg.ID, ThreadID: msg.ThreadID}
	for _, h := range msg.Payload.Headers {
		switch h.Name {
		case "From":
			out.From = decodeHeader(h.Value)
		case "To":
			out.To = decodeHeader(h.Value)
		case "Subject":
			out.Subject = decodeHeader(h.Value)
		case "Date":
			out.Date = h.Value
		}
	}
	out.Body = firstTextPart(msg.Payload.gmailPart)
	if out.Body == "" {
		out.Body = msg.Snippet
	}
	if len(out.Body) > maxBodyChars {
		out.Body = out.Body[:maxBodyChars] + "\n… (kırpıldı)"
	}
	return out, nil
}

// firstTextPart — çok parçalı mesajda ilk text/plain gövdeyi bulur.
func firstTextPart(p gmailPart) string {
	if strings.HasPrefix(p.MimeType, "text/plain") && p.Body.Data != "" {
		if b, err := base64.URLEncoding.WithPadding(base64.NoPadding).DecodeString(strings.TrimRight(p.Body.Data, "=")); err == nil {
			return string(b)
		}
	}
	for _, sub := range p.Parts {
		if t := firstTextPart(sub); t != "" {
			return t
		}
	}
	return ""
}

// decodeHeader — RFC 2047 kodlu başlıkları çözer (=?UTF-8?B?…?=).
func decodeHeader(v string) string {
	dec := new(mime.WordDecoder)
	if out, err := dec.DecodeHeader(v); err == nil {
		return out
	}
	return v
}

// CreateDraft — RFC 2822 düz metin taslak (Türkçe için UTF-8 + base64).
// threadID verilirse yanıt taslağı aynı yazışmaya bağlanır.
func (s *Service) CreateDraft(ctx context.Context, to, subject, body, threadID string) (string, error) {
	raw := buildRFC2822(to, subject, body)
	payload := map[string]any{
		"message": map[string]any{"raw": raw},
	}
	if threadID != "" {
		payload["message"].(map[string]any)["threadId"] = threadID
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := s.postJSON(ctx, s.GmailBase+"/gmail/v1/users/me/drafts", payload, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// SendDraft — VAR OLAN taslağı gönderir. Onay akışının hedefi budur: bot
// taslağı hazırlar (write), gönderim (external) onay kapısından geçer.
func (s *Service) SendDraft(ctx context.Context, draftID string) (string, error) {
	var out struct {
		Message struct {
			ID string `json:"id"`
		} `json:"message"`
	}
	body := map[string]any{"id": draftID}
	if err := s.postJSON(ctx, s.GmailBase+"/gmail/v1/users/me/drafts/send", body, &out); err != nil {
		return "", err
	}
	return out.Message.ID, nil
}

func buildRFC2822(to, subject, body string) string {
	// Başlık ve gövde UTF-8: konu B-encode, gövde base64 (satır katlama derdi yok).
	msg := strings.Join([]string{
		"To: " + to,
		"Subject: " + mime.BEncoding.Encode("UTF-8", subject),
		"MIME-Version: 1.0",
		`Content-Type: text/plain; charset="UTF-8"`,
		"Content-Transfer-Encoding: base64",
		"",
		base64.StdEncoding.EncodeToString([]byte(body)),
	}, "\r\n")
	return base64.URLEncoding.EncodeToString([]byte(msg))
}
