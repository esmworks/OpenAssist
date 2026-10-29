package config

import "testing"

func TestPiModelleriAyristirma(t *testing.T) {
	t.Setenv("API_TOKEN", gecerliBelirtec)
	t.Setenv("PI_MODELS", "MiniMax-M3:400000:128000, glm-5 ,kimi-k2.7-code:262144")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.PiModels) != 3 {
		t.Fatalf("3 model bekleniyordu: %+v", c.PiModels)
	}
	if c.PiModels[0] != (PiModel{ID: "MiniMax-M3", ContextWindow: 400000, MaxTokens: 128000}) {
		t.Errorf("tam öğe yanlış ayrıştı: %+v", c.PiModels[0])
	}
	// Sayı alanları isteğe bağlı: verilmezse pi kendi varsayılanını kullanır.
	if c.PiModels[1] != (PiModel{ID: "glm-5"}) {
		t.Errorf("sayısız öğe yanlış ayrıştı: %+v", c.PiModels[1])
	}
	if c.PiModels[2] != (PiModel{ID: "kimi-k2.7-code", ContextWindow: 262144}) {
		t.Errorf("tek sayılı öğe yanlış ayrıştı: %+v", c.PiModels[2])
	}
}

func TestPiModelleriBosKabulEdilir(t *testing.T) {
	t.Setenv("API_TOKEN", gecerliBelirtec)
	t.Setenv("PI_MODELS", "")
	c, err := Load()
	if err != nil {
		t.Fatalf("boş değer kabul edilmeliydi: %v", err)
	}
	if len(c.PiModels) != 0 {
		t.Errorf("boş değerde model olmamalı: %+v", c.PiModels)
	}
}

// Bozuk sayı SESSİZCE 0'a düşmemeli: 0 "belirtilmedi" anlamına geliyor ve
// yanlış yazılmış bir bağlam penceresi fark edilmeden pi'nin varsayılanıyla
// koşardı.
func TestPiModelleriBozukSayiReddedilir(t *testing.T) {
	t.Setenv("API_TOKEN", gecerliBelirtec)
	for _, ham := range []string{"m:abc", "m:-5", "m:400000:0"} {
		t.Setenv("PI_MODELS", ham)
		if _, err := Load(); err == nil {
			t.Errorf("%q reddedilmeliydi", ham)
		}
	}
}

func TestPiModelleriBosKimlikReddedilir(t *testing.T) {
	t.Setenv("API_TOKEN", gecerliBelirtec)
	t.Setenv("PI_MODELS", ":400000")
	if _, err := Load(); err == nil {
		t.Fatal("boş model kimliği reddedilmeliydi")
	}
}
