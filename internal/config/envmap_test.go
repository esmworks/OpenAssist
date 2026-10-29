package config

import (
	"strings"
	"testing"
)

func TestPiEnvMapParsing(t *testing.T) {
	t.Setenv("API_TOKEN", gecerliBelirtec)
	t.Setenv("PI_ENV", "ANTHROPIC_API_KEY=sk-test,OPENAI_API_KEY=sk-foo")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.PiEnv) != 2 {
		t.Fatalf("2 anahtar bekleniyordu: %v", c.PiEnv)
	}
	if c.PiEnv["ANTHROPIC_API_KEY"] != "sk-test" {
		t.Fatalf("beklenmeyen değer: %v", c.PiEnv)
	}
}

// TestPiEnvHatasiAnahtariSizdirmaz — üretimde çıplak bir API
// anahtarı yazıldı ve env kütüphanesinin hatası anahtarı olduğu gibi
// sunucu log'una bastı. Hata mesajı bozuk öğeyi tarif eder, değeri ASLA.
func TestPiEnvHatasiAnahtariSizdirmaz(t *testing.T) {
	const sir = "sk-cokgizlibirdeger1234567890"
	t.Setenv("API_TOKEN", gecerliBelirtec)
	t.Setenv("PI_ENV", sir)

	_, err := Load()
	if err == nil {
		t.Fatal("ad=değer olmayan değer reddedilmeliydi")
	}
	if strings.Contains(err.Error(), sir) {
		t.Fatalf("hata mesajı anahtarı sızdırıyor: %v", err)
	}
	// Kullanıcı hatayı düzeltebilmeli: beklenen biçim mesajda olsun.
	if !strings.Contains(err.Error(), "ad=değer") {
		t.Errorf("mesaj beklenen biçimi söylemeli: %v", err)
	}
}

func TestPiEnvBosVeBosluklu(t *testing.T) {
	t.Setenv("API_TOKEN", gecerliBelirtec)

	t.Setenv("PI_ENV", "")
	c, err := Load()
	if err != nil {
		t.Fatalf("boş değer kabul edilmeliydi: %v", err)
	}
	if len(c.PiEnv) != 0 {
		t.Errorf("boş değerde anahtar olmamalı: %v", c.PiEnv)
	}

	// Dokploy'a elle yazarken boşluk ve sondaki virgül olağan.
	t.Setenv("PI_ENV", " ANTHROPIC_API_KEY = sk-bir , OPENAI_API_KEY=sk-iki ,")
	c, err = Load()
	if err != nil {
		t.Fatalf("boşluklu değer kabul edilmeliydi: %v", err)
	}
	if c.PiEnv["ANTHROPIC_API_KEY"] != "sk-bir" ||
		c.PiEnv["OPENAI_API_KEY"] != "sk-iki" {
		t.Errorf("boşluklar budanmalıydı: %v", c.PiEnv)
	}
}

// TestPiEnvDegersizAdReddedilir — "ANTHROPIC_API_KEY=" gibi bir
// değer sessizce boş anahtar kaydeder, motor da anlaşılmaz biçimde düşerdi.
func TestPiEnvDegersizAdReddedilir(t *testing.T) {
	t.Setenv("API_TOKEN", gecerliBelirtec)
	t.Setenv("PI_ENV", "ANTHROPIC_API_KEY=")
	if _, err := Load(); err == nil {
		t.Fatal("değersiz çift reddedilmeliydi")
	}
}
