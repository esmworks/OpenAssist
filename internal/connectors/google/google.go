// Package google — Google konektörü: OAuth code exchange, refresh token'ın
// AES-256-GCM ile mühürlenip connectors tablosunda saklanması ve Gmail/Takvim
// REST istemcileri (Faz 4, PRD §5).
//
// Sınır kuralı: Google API'leri yabancı kontrattır — yanıtlar toleranslı
// çözülür (bilinmeyen alanlar yok sayılır); bizim araç şemalarımız katıdır.
package google

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"golang.org/x/oauth2"

	"github.com/esmworks/OpenAssist/internal/crypto"
	db "github.com/esmworks/OpenAssist/internal/store/db"
)

// Kind — connectors.kind değeri.
const Kind = "google"

// Scopes — v1 botunun ihtiyacı: Gmail oku/ara/taslak/gönder (modify kalıcı
// silme İÇERMEZ — K güvenlik), Takvim etkinlikleri, hesap e-postası.
var Scopes = []string{
	"https://www.googleapis.com/auth/gmail.modify",
	"https://www.googleapis.com/auth/calendar.events",
	"https://www.googleapis.com/auth/userinfo.email",
}

// ErrNotConnected — konektör yok ya da hatada; araç mesajı kullanıcıyı
// yeniden bağlanmaya yönlendirir (devir kartı).
var ErrNotConnected = errors.New("bağlı Google hesabı yok — Ayarlar > Konektörler'den 'Google'ı bağla' adımını çalıştır (/oauth/google/start)")

// Store — google paketinin ihtiyaç duyduğu sorgular; *db.Queries sağlar,
// birim testler sahtesini verir.
type Store interface {
	GetConnectorByKind(ctx context.Context, kind string) (db.Connector, error)
	UpsertConnector(ctx context.Context, arg db.UpsertConnectorParams) (db.Connector, error)
	SetConnectorError(ctx context.Context, arg db.SetConnectorErrorParams) error
}

var _ Store = (*db.Queries)(nil)

// Service — tek Google hesabı (v1 tek kullanıcı). Base URL'ler testte sahte
// sunucuya çevrilir.
type Service struct {
	OAuth        oauth2.Config
	UserinfoURL  string // varsayılan: https://www.googleapis.com/oauth2/v2/userinfo
	GmailBase    string // varsayılan: https://gmail.googleapis.com
	CalendarBase string // varsayılan: https://www.googleapis.com
	Store        Store
	Sealer       *crypto.Sealer
	// Client — dış HTTP; nil ise http.DefaultClient (testte httptest.Client).
	Client *http.Client

	mu sync.Mutex
	ts oauth2.TokenSource // süreç içi access-token önbelleği
}

// NewService — üretim uçlarıyla kurar; testler alanları ezerek kurar.
func NewService(clientID, clientSecret, redirectURL string, st Store, sealer *crypto.Sealer) *Service {
	return &Service{
		OAuth: oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes:       Scopes,
			Endpoint: oauth2.Endpoint{
				AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
				TokenURL: "https://oauth2.googleapis.com/token",
			},
		},
		UserinfoURL:  "https://www.googleapis.com/oauth2/v2/userinfo",
		GmailBase:    "https://gmail.googleapis.com",
		CalendarBase: "https://www.googleapis.com",
		Store:        st,
		Sealer:       sealer,
	}
}

func (s *Service) httpCtx(ctx context.Context) context.Context {
	if s.Client != nil {
		return context.WithValue(ctx, oauth2.HTTPClient, s.Client)
	}
	return ctx
}

// AuthCodeURL — onay ekranı adresi. offline + consent: refresh token her
// bağlanışta yeniden verilsin (Google ilkinden sonra vermeyi keser).
func (s *Service) AuthCodeURL(state string) string {
	return s.OAuth.AuthCodeURL(state, oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"))
}

// Connect — callback'ten gelen code'u token'a çevirir, hesabın e-postasını
// öğrenir, refresh token'ı mühürleyip kalıcılaştırır.
func (s *Service) Connect(ctx context.Context, code string) (db.Connector, error) {
	ctx = s.httpCtx(ctx)
	tok, err := s.OAuth.Exchange(ctx, code)
	if err != nil {
		return db.Connector{}, fmt.Errorf("google: code exchange: %w", err)
	}
	if tok.RefreshToken == "" {
		return db.Connector{}, errors.New("google: refresh token gelmedi — hesabın üçüncü taraf erişimini kaldırıp yeniden bağlan")
	}
	email, err := s.fetchEmail(ctx, tok)
	if err != nil {
		return db.Connector{}, err
	}
	sealed, err := s.Sealer.Seal([]byte(tok.RefreshToken))
	if err != nil {
		return db.Connector{}, fmt.Errorf("google: token mühürlenemedi: %w", err)
	}
	conn, err := s.Store.UpsertConnector(ctx, db.UpsertConnectorParams{
		Kind: Kind, Account: email, SealedRefreshToken: sealed, Scopes: s.OAuth.Scopes,
	})
	if err != nil {
		return db.Connector{}, err
	}
	// Yeni bağlantı: eski token önbelleği geçersiz.
	s.mu.Lock()
	s.ts = nil
	s.mu.Unlock()
	return conn, nil
}

func (s *Service) fetchEmail(ctx context.Context, tok *oauth2.Token) (string, error) {
	client := oauth2.NewClient(ctx, oauth2.StaticTokenSource(tok))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.UserinfoURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("google: userinfo: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("google: userinfo durum %d", resp.StatusCode)
	}
	var u struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&u); err != nil {
		return "", err
	}
	return u.Email, nil
}

// apiClient — mühürlü refresh token'dan oto-yenilenen istemci. invalid_grant
// (revoke) kalıcı hatadır: konektör 'error' durumuna çekilir.
func (s *Service) apiClient(ctx context.Context) (*http.Client, error) {
	ctx = s.httpCtx(ctx)
	s.mu.Lock()
	ts := s.ts
	s.mu.Unlock()

	if ts == nil {
		conn, err := s.Store.GetConnectorByKind(ctx, Kind)
		if err != nil {
			return nil, ErrNotConnected
		}
		rt, err := s.Sealer.Open(conn.SealedRefreshToken)
		if err != nil {
			return nil, fmt.Errorf("google: token açılamadı (MASTER_KEY değişti mi?): %w", err)
		}
		base := s.OAuth.TokenSource(ctx, &oauth2.Token{RefreshToken: string(rt)})
		ts = oauth2.ReuseTokenSource(nil, &revokeAware{svc: s, connID: conn.ID.String(), base: base})
		s.mu.Lock()
		s.ts = ts
		s.mu.Unlock()
	}
	return oauth2.NewClient(ctx, ts), nil
}

// revokeAware — refresh isteği invalid_grant dönerse konektörü hataya çeker
// ve kullanıcıya anlamlı mesaj verir.
type revokeAware struct {
	svc    *Service
	connID string
	base   oauth2.TokenSource
}

func (r *revokeAware) Token() (*oauth2.Token, error) {
	tok, err := r.base.Token()
	if err == nil {
		return tok, nil
	}
	var re *oauth2.RetrieveError
	if errors.As(err, &re) && re.ErrorCode == "invalid_grant" {
		r.svc.markRevoked(r.connID)
		return nil, fmt.Errorf("google: erişim iptal edilmiş (invalid_grant) — yeniden bağlanmak gerekiyor: %w", ErrNotConnected)
	}
	return nil, err
}

func (s *Service) markRevoked(connID string) {
	s.mu.Lock()
	s.ts = nil
	s.mu.Unlock()
	if conn, err := s.Store.GetConnectorByKind(context.Background(), Kind); err == nil && conn.ID.String() == connID {
		_ = s.Store.SetConnectorError(context.Background(), db.SetConnectorErrorParams{
			ID: conn.ID, Error: "erişim iptal edilmiş (invalid_grant); yeniden bağlan",
		})
	}
}

// getJSON / postJSON — Google REST yardımcıları (toleranslı çözüm).
func (s *Service) getJSON(ctx context.Context, url string, out any) error {
	client, err := s.apiClient(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	return doJSON(client, req, out)
}

func (s *Service) postJSON(ctx context.Context, url string, body, out any) error {
	client, err := s.apiClient(ctx)
	if err != nil {
		return err
	}
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return doJSON(client, req, out)
}

func doJSON(client *http.Client, req *http.Request, out any) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var g struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&g)
		return fmt.Errorf("google api %d: %s", resp.StatusCode, g.Error.Message)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
