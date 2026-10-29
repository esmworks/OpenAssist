package publichttp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"

	"github.com/esmworks/OpenAssist/internal/config"
	"github.com/esmworks/OpenAssist/internal/connectors/google"
	"github.com/esmworks/OpenAssist/internal/crypto"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

const testKeyHex = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

type memStore struct{ conn *db.Connector }

func (m *memStore) GetConnectorByKind(_ context.Context, kind string) (db.Connector, error) {
	if m.conn == nil {
		return db.Connector{}, pgx.ErrNoRows
	}
	return *m.conn, nil
}

func (m *memStore) UpsertConnector(_ context.Context, arg db.UpsertConnectorParams) (db.Connector, error) {
	c := db.Connector{ID: uuid.New(), Kind: arg.Kind, Account: arg.Account, Status: "connected", SealedRefreshToken: arg.SealedRefreshToken}
	m.conn = &c
	return c, nil
}

func (m *memStore) SetConnectorError(_ context.Context, _ db.SetConnectorErrorParams) error {
	return nil
}

func setup(t *testing.T) (*httptest.Server, *memStore) {
	t.Helper()
	// Sahte Google: token + userinfo.
	gmux := http.NewServeMux()
	gmux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("code") != "kod-42" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"at","token_type":"Bearer","expires_in":3600,"refresh_token":"rt"}`)
	})
	gmux.HandleFunc("GET /userinfo", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"email":"erhan@example.com"}`)
	})
	gsrv := httptest.NewServer(gmux)
	t.Cleanup(gsrv.Close)

	sealer, err := crypto.New(testKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	ms := &memStore{}
	svc := &google.Service{
		OAuth: oauth2.Config{
			ClientID: "cid", ClientSecret: "sec", RedirectURL: "http://localhost:8081/oauth/google/callback",
			Scopes:   google.Scopes,
			Endpoint: oauth2.Endpoint{AuthURL: gsrv.URL + "/auth", TokenURL: gsrv.URL + "/token"},
		},
		UserinfoURL: gsrv.URL + "/userinfo",
		Store:       ms, Sealer: sealer, Client: gsrv.Client(),
	}

	cfg := config.Config{PublicHTTPAddr: ":0"}
	srv := httptest.NewServer(New(cfg, Deps{Google: svc}).Handler)
	t.Cleanup(srv.Close)
	return srv, ms
}

// noRedirect — 302'yi takip etme; Location'ı inceleyeceğiz.
func noRedirect(srv *httptest.Server) *http.Client {
	c := *srv.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c
}

func TestOAuthFlowHappyPath(t *testing.T) {
	srv, ms := setup(t)
	client := noRedirect(srv)

	// 1. start → 302, Location'da state var.
	resp, err := client.Get(srv.URL + "/oauth/google/start")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("start 302 olmalı: %d", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state := loc.Query().Get("state")
	if state == "" || loc.Query().Get("access_type") != "offline" {
		t.Fatalf("consent URL bozuk: %s", loc)
	}

	// 2. callback → token exchange + mühürlü kayıt + başarı sayfası.
	resp, err = client.Get(srv.URL + "/oauth/google/callback?state=" + state + "&code=kod-42")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "erhan@example.com") {
		t.Fatalf("callback: %d %s", resp.StatusCode, body)
	}
	if ms.conn == nil || ms.conn.Account != "erhan@example.com" {
		t.Fatalf("konektör kalıcılaşmadı: %+v", ms.conn)
	}
	if strings.Contains(ms.conn.SealedRefreshToken, "rt") && len(ms.conn.SealedRefreshToken) < 24 {
		t.Error("token mühürlenmemiş görünüyor")
	}

	// 3. Aynı state ikinci kez → 400 (tek kullanım).
	resp, _ = client.Get(srv.URL + "/oauth/google/callback?state=" + state + "&code=kod-42")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("state tek kullanımlık olmalı: %d", resp.StatusCode)
	}
}

func TestCallbackRejectsBadState(t *testing.T) {
	srv, _ := setup(t)
	resp, err := srv.Client().Get(srv.URL + "/oauth/google/callback?state=uydurma&code=kod-42")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("sahte state 400 olmalı: %d", resp.StatusCode)
	}
}

func TestCallbackConsentDenied(t *testing.T) {
	srv, _ := setup(t)
	resp, _ := srv.Client().Get(srv.URL + "/oauth/google/callback?error=access_denied")
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "reddedildi") {
		t.Errorf("consent reddi anlatılmalı: %d %s", resp.StatusCode, body)
	}
}

func TestUnconfiguredGoogle503(t *testing.T) {
	srv := httptest.NewServer(New(config.Config{PublicHTTPAddr: ":0"}, Deps{}).Handler)
	defer srv.Close()
	resp, _ := srv.Client().Get(srv.URL + "/oauth/google/start")
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), "GOOGLE_CLIENT_ID") {
		t.Errorf("yapılandırma eksikliği anlatılmalı: %d %s", resp.StatusCode, body)
	}
}
