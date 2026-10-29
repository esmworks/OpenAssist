package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// clip bayt sınırında rune bölmemeli: bölseydi geçersiz UTF-8'i Postgres
// reddeder, denetim kaydı sessizce kaybolurdu.
func TestClipKeepsValidUTF8(t *testing.T) {
	s := strings.Repeat("ğ", 100) // 2 baytlık rune'lar: tek sayı sınırı ortadan keser
	for _, n := range []int{0, 1, 2, 3, 50, 99, 199, 200, 500} {
		got := clip(s, n)
		if !utf8.ValidString(got) {
			t.Errorf("clip(n=%d) geçersiz UTF-8 üretti: %q", n, got)
		}
	}
	if got := clip("kısa", 100); got != "kısa" {
		t.Errorf("sınır altındaki metin değişmemeli: %q", got)
	}
}

func TestHealthURL(t *testing.T) {
	cases := []struct {
		addr, want string
	}{
		{":8080", "http://127.0.0.1:8080/healthz"},
		{"0.0.0.0:8080", "http://127.0.0.1:8080/healthz"},
		{"127.0.0.1:9090", "http://127.0.0.1:9090/healthz"},
		{"bozuk", "http://127.0.0.1:8080/healthz"}, // savunmacı geri düşüş
	}
	for _, c := range cases {
		if got := healthURL(c.addr); got != c.want {
			t.Errorf("healthURL(%q) = %q, beklenen %q", c.addr, got, c.want)
		}
	}
}
