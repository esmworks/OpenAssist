// Package crypto — konektör token'ları gibi hassas değerlerin durağan
// şifrelenmesi (PRD §5): AES-256-GCM, anahtar MASTER_KEY'den (64 hex karakter
// = 32 bayt). Çıktı: nonce || ciphertext, base64.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
)

type Sealer struct {
	aead cipher.AEAD
}

// New, 64 hex karakterlik MASTER_KEY ile kurar.
func New(masterKeyHex string) (*Sealer, error) {
	key, err := hex.DecodeString(masterKeyHex)
	if err != nil || len(key) != 32 {
		return nil, errors.New("MASTER_KEY 64 hex karakter (32 bayt) olmalı")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

func (s *Sealer) Seal(plaintext []byte) (string, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := s.aead.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(out), nil
}

func (s *Sealer) Open(sealed string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return nil, fmt.Errorf("çözülemedi: %w", err)
	}
	ns := s.aead.NonceSize()
	if len(raw) < ns {
		return nil, errors.New("çözülemedi: veri çok kısa")
	}
	plain, err := s.aead.Open(nil, raw[:ns], raw[ns:], nil)
	if err != nil {
		return nil, errors.New("çözülemedi: anahtar yanlış ya da veri bozuk")
	}
	return plain, nil
}
