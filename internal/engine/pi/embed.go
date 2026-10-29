package pi

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Gömülü eklentiler binary ile taşınır: dağıtımda ayrı dosya kopyalama adımı
// yoktur, dolayısıyla eklenti ile onu okuyan adaptör sürüm olarak ASLA
// ayrışmaz. (Eski Node köprüsü bin/ altına sembolik bağla kurulurdu; kaynak
// dizin yoksa motor sessizce çalışmaz hale geliyordu.)
//
//go:embed extensions/*.ts
var gomuluEklentiler embed.FS

// OAGateway — onay kapısını ve OpenAssist gateway araçlarını kuran eklenti.
// Bot allowlist'lerinde bu adla geçer.
const OAGateway = "oa-gateway.ts"

// oa-gateway'in kendi kaydettiği araçlar. Adlar eklentiyle BİREBİR aynı
// olmalı: allowlist ve devir akışı bu adlara göre eşleşir.
const (
	AskUserTool  = "mcp__oa__ask_user"
	AskBotTool   = "mcp__oa__ask_bot"
	SendFileTool = "mcp__oa__send_file"
)

// eklentileriKur — gömülü eklentileri ExtensionsDir'e yazar. İçerik aynıysa
// dosyaya dokunulmaz: her tur yeniden yazmak diski gereksiz yorar ve
// kullanıcının açık dosyasını sürekli değiştirilmiş gösterirdi.
func (a *Adapter) eklentileriKur() error {
	if a.ExtensionsDir == "" {
		return nil
	}
	if err := os.MkdirAll(a.ExtensionsDir, 0o700); err != nil {
		return fmt.Errorf("eklenti dizini kurulamadı: %w", err)
	}

	girdiler, err := fs.ReadDir(gomuluEklentiler, "extensions")
	if err != nil {
		return err
	}
	for _, g := range girdiler {
		if g.IsDir() {
			continue
		}
		veri, err := gomuluEklentiler.ReadFile("extensions/" + g.Name())
		if err != nil {
			return err
		}
		yol := filepath.Join(a.ExtensionsDir, g.Name())
		if mevcut, err := os.ReadFile(yol); err == nil && bytes.Equal(mevcut, veri) {
			continue
		}
		if err := os.WriteFile(yol, veri, 0o600); err != nil {
			return fmt.Errorf("eklenti yazılamadı (%s): %w", g.Name(), err)
		}
	}
	return nil
}
