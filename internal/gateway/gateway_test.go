package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/esmworks/OpenAssist/internal/policy"
)

func testGateway() *Gateway {
	g := New()
	g.Register(Tool{
		Name:        "echo",
		Description: "aynen döndürür",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"msg":{"type":"string"}},"required":["msg"]}`),
		Class:       policy.Classification{Class: policy.ClassRead, Ruleable: true},
		Handler: func(_ context.Context, args json.RawMessage) (string, error) {
			var a struct{ Msg string }
			if err := json.Unmarshal(args, &a); err != nil {
				return "", err
			}
			return "yanki: " + a.Msg, nil
		},
	})
	g.Register(Tool{
		Name:        "patlak",
		Description: "hep hata verir",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Class:       policy.Classification{Class: policy.ClassExternal, Ruleable: true},
		Handler: func(_ context.Context, _ json.RawMessage) (string, error) {
			return "", errors.New("Google bağlı değil — Ayarlar > Konektörler'den bağla")
		},
	})
	return g
}

func rpc(t *testing.T, srv *httptest.Server, body string) (int, rpcResponse) {
	t.Helper()
	resp, err := http.Post(srv.URL, "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out rpcResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return resp.StatusCode, out
}

func TestInitializeAndList(t *testing.T) {
	srv := httptest.NewServer(testGateway().Handler())
	defer srv.Close()

	code, out := rpc(t, srv, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)
	if code != 200 || out.Error != nil {
		t.Fatalf("initialize: code=%d err=%+v", code, out.Error)
	}
	res, _ := json.Marshal(out.Result)
	if !strings.Contains(string(res), `"protocolVersion":"2025-03-26"`) {
		t.Errorf("istemci sürümü aynen dönmeliydi: %s", res)
	}

	// initialized bildirimi (id yok) → 202, gövdesiz.
	if code, _ := rpc(t, srv, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); code != http.StatusAccepted {
		t.Errorf("bildirim 202 olmalı, geldi: %d", code)
	}

	code, out = rpc(t, srv, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	res, _ = json.Marshal(out.Result)
	if code != 200 || !strings.Contains(string(res), `"echo"`) || !strings.Contains(string(res), `"inputSchema"`) {
		t.Errorf("tools/list eksik: %s", res)
	}
}

func TestToolCallSuccessAndError(t *testing.T) {
	srv := httptest.NewServer(testGateway().Handler())
	defer srv.Close()

	_, out := rpc(t, srv, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"msg":"selam"}}}`)
	res, _ := json.Marshal(out.Result)
	if !strings.Contains(string(res), "yanki: selam") || !strings.Contains(string(res), `"isError":false`) {
		t.Errorf("echo sonucu bozuk: %s", res)
	}

	// Araç hatası JSON-RPC hatası DEĞİL — isError'lı sonuç (devir kartı).
	_, out = rpc(t, srv, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"patlak","arguments":{}}}`)
	if out.Error != nil {
		t.Fatalf("araç hatası protokol hatasına dönüşmemeli: %+v", out.Error)
	}
	res, _ = json.Marshal(out.Result)
	if !strings.Contains(string(res), `"isError":true`) || !strings.Contains(string(res), "Konektörler") {
		t.Errorf("hata sonucu bozuk: %s", res)
	}

	// Bilinmeyen araç → protokol hatası (-32602).
	_, out = rpc(t, srv, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"yok","arguments":{}}}`)
	if out.Error == nil || out.Error.Code != -32602 {
		t.Errorf("bilinmeyen araç -32602 olmalı: %+v", out.Error)
	}
}

func TestProtocolEdges(t *testing.T) {
	srv := httptest.NewServer(testGateway().Handler())
	defer srv.Close()

	// GET → 405 (SSE stream desteklenmiyor).
	resp, _ := http.Get(srv.URL)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET 405 olmalı: %d", resp.StatusCode)
	}
	// Bilinmeyen yöntem → -32601.
	_, out := rpc(t, srv, `{"jsonrpc":"2.0","id":6,"method":"resources/list"}`)
	if out.Error == nil || out.Error.Code != -32601 {
		t.Errorf("bilinmeyen yöntem -32601 olmalı: %+v", out.Error)
	}
}

func TestClassifications(t *testing.T) {
	m := testGateway().Classifications()
	got, ok := m["mcp__gw__echo"]
	if !ok || got.Class != policy.ClassRead {
		t.Errorf("sınıf haritası bozuk: %+v (harita: %v)", got, m)
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("çift kayıt panic olmalıydı")
		}
	}()
	g := New()
	tl := Tool{Name: "x", InputSchema: json.RawMessage(`{}`), Handler: func(context.Context, json.RawMessage) (string, error) { return "", nil }}
	g.Register(tl)
	g.Register(tl)
}
