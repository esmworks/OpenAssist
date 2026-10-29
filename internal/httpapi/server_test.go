package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/esmworks/OpenAssist/internal/config"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func doHealthz(t *testing.T, db Pinger) (*http.Response, Health) {
	t.Helper()
	srv := New(config.Config{HTTPAddr: ":0"}, Deps{DB: db})
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	var h Health
	if err := json.NewDecoder(rec.Body).Decode(&h); err != nil {
		t.Fatalf("healthz JSON çözülemedi: %v", err)
	}
	return rec.Result(), h
}

func TestHealthzWithoutDB(t *testing.T) {
	resp, h := doHealthz(t, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, beklenen 200", resp.StatusCode)
	}
	if h.Status != "ok" || h.DB != "not_configured" {
		t.Errorf("beklenmeyen gövde: %+v", h)
	}
}

func TestHealthzWithHealthyDB(t *testing.T) {
	resp, h := doHealthz(t, fakePinger{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, beklenen 200", resp.StatusCode)
	}
	if h.DB != "ok" {
		t.Errorf("db = %q, beklenen ok", h.DB)
	}
}

func TestHealthzWithFailingDB(t *testing.T) {
	resp, h := doHealthz(t, fakePinger{err: errors.New("bağlantı koptu")})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, beklenen 503", resp.StatusCode)
	}
	if h.Status != "degraded" || h.DB != "error" {
		t.Errorf("beklenmeyen gövde: %+v", h)
	}
}

func TestHealthzRejectsPost(t *testing.T) {
	srv := New(config.Config{HTTPAddr: ":0"}, Deps{})
	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, beklenen 405", rec.Code)
	}
}
