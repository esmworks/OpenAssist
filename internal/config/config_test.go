package config

import (
	"log/slog"
	"testing"
)

// gecerliBelirtec — API_TOKEN artık zorunlu (özel API'yi o koruyor), yani
// başarı bekleyen her test onu vermek zorunda.
const gecerliBelirtec = "cfg-token-0123456789abcdef0123456789abcdef"

func TestLoadDefaults(t *testing.T) {
	// t.Setenv olmadan da ortam sızabilir; ilgili anahtarları sabitle.
	t.Setenv("API_TOKEN", gecerliBelirtec)
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("PUBLIC_HTTP_ADDR", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("MIGRATE_ON_START", "")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, beklenen :8080", c.HTTPAddr)
	}
	if c.PublicHTTPAddr != ":8081" {
		t.Errorf("PublicHTTPAddr = %q, beklenen :8081", c.PublicHTTPAddr)
	}
	if !c.MigrateOnStart {
		t.Error("MigrateOnStart varsayılanı true olmalı")
	}
	if lvl, _ := c.SlogLevel(); lvl != slog.LevelInfo {
		t.Errorf("LogLevel varsayılanı info olmalı, geldi: %v", lvl)
	}
}

func TestLoadCustomValues(t *testing.T) {
	t.Setenv("API_TOKEN", gecerliBelirtec)
	t.Setenv("HTTP_ADDR", "127.0.0.1:9090")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("MIGRATE_ON_START", "false")
	t.Setenv("DATABASE_URL", "postgres://x:y@localhost:5432/z")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.HTTPAddr != "127.0.0.1:9090" {
		t.Errorf("HTTPAddr = %q", c.HTTPAddr)
	}
	if c.MigrateOnStart {
		t.Error("MigrateOnStart=false okunmalıydı")
	}
	if lvl, _ := c.SlogLevel(); lvl != slog.LevelDebug {
		t.Errorf("LogLevel debug olmalı, geldi: %v", lvl)
	}
}

func TestInvalidLogLevelIsError(t *testing.T) {
	t.Setenv("LOG_LEVEL", "sessiz")

	if _, err := Load(); err == nil {
		t.Fatal("geçersiz LOG_LEVEL hata döndürmeli — sessiz varsayılan yok")
	}
}

func TestInvalidBoolIsError(t *testing.T) {
	t.Setenv("LOG_LEVEL", "info")
	t.Setenv("MIGRATE_ON_START", "evet")

	if _, err := Load(); err == nil {
		t.Fatal("bozuk bool env hata döndürmeli")
	}
}

// TestPublicBaseURLSemaSart — Dokploy'a "asistan.example.com" yazmak kolay,
// ama değer redirect URI'sinin kökü olarak birleştirilir. Şemasız değer eski
// boş-değil kontrolünü geçiyordu ve hata aylar sonra, Google konektörü
// bağlanırken patlıyordu.
func TestPublicBaseURLSemaSart(t *testing.T) {
	t.Setenv("API_TOKEN", gecerliBelirtec)
	t.Setenv("PUBLIC_BASE_URL", "asistan.example.com")
	if _, err := Load(); err == nil {
		t.Fatal("şemasız PUBLIC_BASE_URL reddedilmeliydi")
	}

	t.Setenv("PUBLIC_BASE_URL", "https://asistan.example.com")
	if _, err := Load(); err != nil {
		t.Fatalf("tam URL kabul edilmeliydi: %v", err)
	}
}

// TestPublicBaseURLSondakiEgikCizgi — "…com/" + "/oauth/…" çift eğik çizgi
// üretirdi; Google redirect_uri'yi harfi harfine eşleştirir.
func TestPublicBaseURLSondakiEgikCizgi(t *testing.T) {
	t.Setenv("API_TOKEN", gecerliBelirtec)
	t.Setenv("PUBLIC_BASE_URL", "https://asistan.example.com/")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.PublicBaseURL != "https://asistan.example.com" {
		t.Errorf("sondaki eğik çizgi budanmalıydı: %q", c.PublicBaseURL)
	}
}

// TestAPITokenZorunlu — özel API'yi (bot yönetimi, onaylar, runner WS) yalnız
// bu belirteç koruyor. Boş bırakılabilseydi core korumasız açılırdı ve
// "unuttum" ile "bilerek kapattım" ayırt edilemezdi — fail-closed.
func TestAPITokenZorunlu(t *testing.T) {
	t.Setenv("API_TOKEN", "")
	if _, err := Load(); err == nil {
		t.Fatal("API_TOKEN boşken açılış reddedilmeliydi")
	}

	// Kısa/tahmin edilebilir değer de reddedilir.
	t.Setenv("API_TOKEN", "gizli123")
	if _, err := Load(); err == nil {
		t.Fatal("kısa API_TOKEN reddedilmeliydi")
	}

	t.Setenv("API_TOKEN", gecerliBelirtec)
	c, err := Load()
	if err != nil {
		t.Fatalf("geçerli API_TOKEN kabul edilmeliydi: %v", err)
	}
	if c.APIToken != gecerliBelirtec {
		t.Errorf("APIToken = %q", c.APIToken)
	}
	// MCP yüzeyi varsayılan olarak loopback'te: dışarıdan erişilmemeli.
	if c.InternalHTTPAddr != "127.0.0.1:8082" {
		t.Errorf("InternalHTTPAddr = %q, beklenen 127.0.0.1:8082", c.InternalHTTPAddr)
	}
}
