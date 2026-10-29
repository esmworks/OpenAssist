package crypto

import (
	"strings"
	"testing"
)

const testKey = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"

func TestSealRoundtrip(t *testing.T) {
	s, err := New(testKey)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := s.Seal([]byte("gizli-oauth-token"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "gizli") {
		t.Error("şifreli çıktı düz metni içermemeli")
	}
	plain, err := s.Open(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "gizli-oauth-token" {
		t.Errorf("roundtrip bozuk: %q", plain)
	}
}

func TestSealIsNonDeterministic(t *testing.T) {
	s, _ := New(testKey)
	a, _ := s.Seal([]byte("x"))
	b, _ := s.Seal([]byte("x"))
	if a == b {
		t.Error("aynı düz metin iki farklı şifreli çıktı üretmeli (rastgele nonce)")
	}
}

func TestWrongKeyFails(t *testing.T) {
	s1, _ := New(testKey)
	s2, _ := New("ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
	sealed, _ := s1.Seal([]byte("x"))
	if _, err := s2.Open(sealed); err == nil {
		t.Error("yanlış anahtar hatasız çözmemeli")
	}
}

func TestBadKeyRejected(t *testing.T) {
	for _, k := range []string{"", "kisa", "zz" + testKey[2:]} {
		if _, err := New(k); err == nil {
			t.Errorf("geçersiz anahtar kabul edilmemeli: %q", k)
		}
	}
}

func TestTamperDetected(t *testing.T) {
	s, _ := New(testKey)
	sealed, _ := s.Seal([]byte("x"))
	tampered := sealed[:len(sealed)-4] + "AAA="
	if _, err := s.Open(tampered); err == nil {
		t.Error("bozulmuş veri hatasız çözülmemeli (GCM bütünlük)")
	}
}
