package google

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"

	"github.com/esmworks/OpenAssist/internal/crypto"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

const testKeyHex = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

// fakeStore — DB'siz konektör deposu.
type fakeStore struct {
	conn    *db.Connector
	upserts []db.UpsertConnectorParams
	errSets []db.SetConnectorErrorParams
}

func (f *fakeStore) GetConnectorByKind(_ context.Context, kind string) (db.Connector, error) {
	if f.conn == nil || f.conn.Kind != kind {
		return db.Connector{}, pgx.ErrNoRows
	}
	return *f.conn, nil
}

func (f *fakeStore) UpsertConnector(_ context.Context, arg db.UpsertConnectorParams) (db.Connector, error) {
	f.upserts = append(f.upserts, arg)
	c := db.Connector{
		ID: uuid.New(), Kind: arg.Kind, Account: arg.Account, Status: "connected",
		SealedRefreshToken: arg.SealedRefreshToken, Scopes: arg.Scopes,
	}
	f.conn = &c
	return c, nil
}

func (f *fakeStore) SetConnectorError(_ context.Context, arg db.SetConnectorErrorParams) error {
	f.errSets = append(f.errSets, arg)
	if f.conn != nil && f.conn.ID == arg.ID {
		f.conn.Status, f.conn.Error = "error", arg.Error
	}
	return nil
}

// fakeGoogle — token + userinfo + gmail + calendar uçlarını taklit eder.
type fakeGoogle struct {
	mux          *http.ServeMux
	srv          *httptest.Server
	tokenCalls   atomic.Int64
	refreshGrant string // "" = başarılı; "invalid_grant" = revoke senaryosu
	lastAuth     atomic.Value
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	f := &fakeGoogle{mux: http.NewServeMux()}
	f.mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		f.tokenCalls.Add(1)
		_ = r.ParseForm()
		if r.Form.Get("grant_type") == "refresh_token" && f.refreshGrant != "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, `{"error":%q}`, f.refreshGrant)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"at-1","token_type":"Bearer","expires_in":3600,"refresh_token":"rt-gizli"}`)
	})
	f.mux.HandleFunc("GET /userinfo", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"email":"kullanici@example.com"}`)
	})
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if auth := r.Header.Get("Authorization"); auth != "" {
			f.lastAuth.Store(auth)
		}
		f.mux.ServeHTTP(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGoogle) service(t *testing.T, st Store) *Service {
	t.Helper()
	sealer, err := crypto.New(testKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	return &Service{
		OAuth: oauth2.Config{
			ClientID: "cid", ClientSecret: "sec", RedirectURL: "http://cb", Scopes: Scopes,
			Endpoint: oauth2.Endpoint{AuthURL: f.srv.URL + "/auth", TokenURL: f.srv.URL + "/token"},
		},
		UserinfoURL:  f.srv.URL + "/userinfo",
		GmailBase:    f.srv.URL,
		CalendarBase: f.srv.URL,
		Store:        st,
		Sealer:       sealer,
		Client:       f.srv.Client(),
	}
}

func TestConnectSealsAndStores(t *testing.T) {
	fg := newFakeGoogle(t)
	fs := &fakeStore{}
	svc := fg.service(t, fs)

	conn, err := svc.Connect(context.Background(), "kod-123")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if conn.Account != "kullanici@example.com" {
		t.Errorf("hesap e-postası: %q", conn.Account)
	}
	if len(fs.upserts) != 1 {
		t.Fatalf("1 upsert bekleniyordu: %d", len(fs.upserts))
	}
	// Token DÜZ YAZI saklanmaz; mühür açılınca orijinal çıkar.
	sealed := fs.upserts[0].SealedRefreshToken
	if strings.Contains(sealed, "rt-gizli") {
		t.Error("refresh token düz yazı saklanmış!")
	}
	rt, err := svc.Sealer.Open(sealed)
	if err != nil || string(rt) != "rt-gizli" {
		t.Errorf("mühür açılamadı: %v %q", err, rt)
	}
}

func TestAuthCodeURLOffline(t *testing.T) {
	fg := newFakeGoogle(t)
	svc := fg.service(t, &fakeStore{})
	u := svc.AuthCodeURL("st-1")
	for _, want := range []string{"access_type=offline", "prompt=consent", "state=st-1", "client_id=cid"} {
		if !strings.Contains(u, want) {
			t.Errorf("AuthCodeURL %q içermeli: %s", want, u)
		}
	}
}

func sealedConn(t *testing.T, svc *Service) *db.Connector {
	t.Helper()
	sealed, err := svc.Sealer.Seal([]byte("rt-gizli"))
	if err != nil {
		t.Fatal(err)
	}
	return &db.Connector{
		ID: uuid.New(), Kind: Kind, Account: "kullanici@example.com",
		Status: "connected", SealedRefreshToken: sealed,
	}
}

func TestAPIClientRefreshesFromSealedToken(t *testing.T) {
	fg := newFakeGoogle(t)
	fg.mux.HandleFunc("GET /gmail/v1/users/me/messages", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"messages":[{"id":"m1"}]}`)
	})
	fg.mux.HandleFunc("GET /gmail/v1/users/me/messages/m1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":"m1","snippet":"selam","payload":{"headers":[
			{"name":"From","value":"Ali <ali@example.com>"},
			{"name":"Subject","value":"=?UTF-8?B?TWVyaGFiYSBEw7xueWE=?="},
			{"name":"Date","value":"Thu, 13 Aug 2026 09:00:00 +0300"}]}}`)
	})
	fs := &fakeStore{}
	svc := fg.service(t, fs)
	fs.conn = sealedConn(t, svc)

	msgs, err := svc.SearchMessages(context.Background(), "is:unread", 10)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Subject != "Merhaba Dünya" || msgs[0].From != "Ali <ali@example.com>" {
		t.Errorf("özet bozuk: %+v", msgs)
	}
	if got := fg.lastAuth.Load(); got != "Bearer at-1" {
		t.Errorf("access token başlığı: %v", got)
	}
	// İkinci çağrı token'ı önbellekten kullanmalı (tek refresh).
	if _, err := svc.SearchMessages(context.Background(), "is:unread", 10); err != nil {
		t.Fatal(err)
	}
	if n := fg.tokenCalls.Load(); n != 1 {
		t.Errorf("1 token isteği bekleniyordu (önbellek), geldi: %d", n)
	}
}

func TestRevokedTokenMarksConnectorError(t *testing.T) {
	fg := newFakeGoogle(t)
	fg.refreshGrant = "invalid_grant"
	fs := &fakeStore{}
	svc := fg.service(t, fs)
	fs.conn = sealedConn(t, svc)

	_, err := svc.SearchMessages(context.Background(), "x", 5)
	if err == nil || !errors.Is(err, ErrNotConnected) {
		t.Fatalf("ErrNotConnected sarmalı bekleniyordu: %v", err)
	}
	if len(fs.errSets) != 1 || fs.conn.Status != "error" {
		t.Errorf("konektör hataya çekilmeliydi: %+v", fs.errSets)
	}
}

func TestNoConnectorGivesHandoffError(t *testing.T) {
	fg := newFakeGoogle(t)
	svc := fg.service(t, &fakeStore{})
	_, err := svc.SearchMessages(context.Background(), "x", 5)
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("devir hatası bekleniyordu: %v", err)
	}
}

func TestGetMessageWalksParts(t *testing.T) {
	fg := newFakeGoogle(t)
	body := base64.URLEncoding.EncodeToString([]byte("Merhaba!\nGövde burada."))
	fg.mux.HandleFunc("GET /gmail/v1/users/me/messages/m2", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "m2", "threadId": "t2", "snippet": "kes",
			"payload": map[string]any{
				"mimeType": "multipart/alternative",
				"headers": []map[string]string{
					{"name": "From", "value": "a@b.c"}, {"name": "Subject", "value": "Konu"},
				},
				"parts": []map[string]any{
					{"mimeType": "text/html", "body": map[string]string{"data": "PGI+"}},
					{"mimeType": "text/plain", "body": map[string]string{"data": body}},
				},
			},
		})
	})
	fs := &fakeStore{}
	svc := fg.service(t, fs)
	fs.conn = sealedConn(t, svc)

	m, err := svc.GetMessage(context.Background(), "m2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.Body, "Gövde burada.") || m.ThreadID != "t2" {
		t.Errorf("gövde çözümü bozuk: %+v", m)
	}
}

func TestDraftAndSend(t *testing.T) {
	fg := newFakeGoogle(t)
	var draftRaw string
	fg.mux.HandleFunc("POST /gmail/v1/users/me/drafts", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Message struct {
				Raw      string `json:"raw"`
				ThreadID string `json:"threadId"`
			} `json:"message"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		draftRaw = in.Message.Raw
		if in.Message.ThreadID != "t-42" {
			t.Errorf("threadId taşınmadı: %q", in.Message.ThreadID)
		}
		_, _ = io.WriteString(w, `{"id":"dr-1"}`)
	})
	fg.mux.HandleFunc("POST /gmail/v1/users/me/drafts/send", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.ID != "dr-1" {
			t.Errorf("draft id: %q", in.ID)
		}
		_, _ = io.WriteString(w, `{"message":{"id":"m-99"}}`)
	})
	fs := &fakeStore{}
	svc := fg.service(t, fs)
	fs.conn = sealedConn(t, svc)

	id, err := svc.CreateDraft(context.Background(), "ali@example.com", "Selam Dünya", "Gövde çok güzel.", "t-42")
	if err != nil || id != "dr-1" {
		t.Fatalf("CreateDraft: %v %q", err, id)
	}
	// raw çözülünce RFC2822 başlıkları görünmeli.
	dec, err := base64.URLEncoding.DecodeString(draftRaw)
	if err != nil {
		t.Fatalf("raw base64url değil: %v", err)
	}
	if !strings.Contains(string(dec), "To: ali@example.com") || !strings.Contains(string(dec), "Subject: =?UTF-8?") {
		t.Errorf("RFC2822 bozuk: %s", dec)
	}

	mid, err := svc.SendDraft(context.Background(), "dr-1")
	if err != nil || mid != "m-99" {
		t.Fatalf("SendDraft: %v %q", err, mid)
	}
}

func TestCalendarListAndCreate(t *testing.T) {
	fg := newFakeGoogle(t)
	fg.mux.HandleFunc("GET /calendar/v3/calendars/primary/events", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("singleEvents") != "true" {
			t.Error("singleEvents=true olmalı")
		}
		_, _ = io.WriteString(w, `{"items":[{"id":"e1","summary":"Standup","start":{"dateTime":"2026-08-14T09:00:00+03:00"},"end":{"dateTime":"2026-08-14T09:15:00+03:00"}}]}`)
	})
	fg.mux.HandleFunc("POST /calendar/v3/calendars/primary/events", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["summary"] != "Kahve" {
			t.Errorf("summary: %v", in["summary"])
		}
		_, _ = io.WriteString(w, `{"id":"e2","summary":"Kahve","start":{"dateTime":"2026-08-15T10:00:00+03:00"},"end":{"dateTime":"2026-08-15T10:30:00+03:00"}}`)
	})
	fs := &fakeStore{}
	svc := fg.service(t, fs)
	fs.conn = sealedConn(t, svc)

	evs, err := svc.ListEvents(context.Background(), time.Now(), time.Now().Add(24*time.Hour), 10)
	if err != nil || len(evs) != 1 || evs[0].Summary != "Standup" {
		t.Fatalf("ListEvents: %v %+v", err, evs)
	}

	ev, err := svc.CreateEvent(context.Background(), "Kahve", "",
		time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC), time.Date(2026, 8, 15, 10, 30, 0, 0, time.UTC), nil)
	if err != nil || ev.ID != "e2" {
		t.Fatalf("CreateEvent: %v %+v", err, ev)
	}
}
