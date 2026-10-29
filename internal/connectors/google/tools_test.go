package google

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/esmworks/OpenAssist/internal/gateway"
	"github.com/esmworks/OpenAssist/internal/policy"
)

func TestRegisterToolsClassifications(t *testing.T) {
	fg := newFakeGoogle(t)
	g := gateway.New()
	RegisterTools(g, fg.service(t, &fakeStore{}))

	m := g.Classifications()
	want := map[string]policy.Class{
		"mcp__gw__gmail_search":          policy.ClassRead,
		"mcp__gw__gmail_read":            policy.ClassRead,
		"mcp__gw__gmail_create_draft":    policy.ClassWrite, // gece taslak: sessiz saate takılmaz
		"mcp__gw__gmail_send_draft":      policy.ClassExternal,
		"mcp__gw__calendar_list_events":  policy.ClassRead,
		"mcp__gw__calendar_create_event": policy.ClassExternal,
	}
	for tool, cls := range want {
		got, ok := m[tool]
		if !ok || got.Class != cls {
			t.Errorf("%s sınıfı %s olmalı, geldi: %+v (kayıtlı mı: %v)", tool, cls, got, ok)
		}
	}
	// Okuma araçları koşuyu boyamalı; dış araçlar boyamamalı.
	if !m["mcp__gw__gmail_search"].DataSource || m["mcp__gw__gmail_send_draft"].DataSource {
		t.Error("DataSource bayrakları ters")
	}
}

func TestToolStrictArgs(t *testing.T) {
	fg := newFakeGoogle(t)
	fg.mux.HandleFunc("GET /gmail/v1/users/me/messages", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"messages":[]}`)
	})
	fs := &fakeStore{}
	svc := fg.service(t, fs)
	fs.conn = sealedConn(t, svc)

	g := gateway.New()
	RegisterTools(g, svc)

	call := func(tool, args string) (string, error) {
		for _, tl := range toolsOf(g) {
			if tl.Name == tool {
				return tl.Handler(context.Background(), json.RawMessage(args))
			}
		}
		t.Fatalf("araç yok: %s", tool)
		return "", nil
	}

	// Bilinmeyen alan → hata (bizim kontratımız katı).
	if _, err := call("gmail_search", `{"query":"x","yanlis_alan":1}`); err == nil || !strings.Contains(err.Error(), "yanlis_alan") {
		t.Errorf("bilinmeyen alan reddedilmeli: %v", err)
	}
	// Boş sonuç insan-okur metin döner.
	if out, err := call("gmail_search", `{"query":"is:unread"}`); err != nil || out != "Sonuç yok." {
		t.Errorf("boş arama: %q %v", out, err)
	}
	// RFC3339 olmayan tarih → anlaşılır hata.
	if _, err := call("calendar_create_event", `{"summary":"x","start":"yarın","end":"2026-08-15T10:00:00Z"}`); err == nil || !strings.Contains(err.Error(), "RFC3339") {
		t.Errorf("tarih doğrulaması: %v", err)
	}
}

// toolsOf — gateway'in araç listesine test erişimi (tools/list üzerinden
// dolaylı gitmek yerine handler'ları doğrudan çağırıyoruz).
func toolsOf(g *gateway.Gateway) []gateway.Tool {
	return g.Tools()
}
