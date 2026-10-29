package runner

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/esmworks/OpenAssist/internal/gateway"
	"github.com/esmworks/OpenAssist/internal/policy"
)

// fakeRunner — Swift runner'ın yerine geçen test istemcisi: hello gönderir,
// call çerçevelerini handler ile cevaplar.
type fakeRunner struct {
	t    *testing.T
	conn *websocket.Conn
	done chan struct{}
}

func dialRunner(t *testing.T, url string, tools []ToolDecl, handle func(callFrame) resultFrame) *fakeRunner {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("runner bağlanamadı: %v", err)
	}
	hello, _ := json.Marshal(helloFrame{Type: "hello", Name: "test-mac", Version: "0", Tools: tools})
	if err := conn.Write(ctx, websocket.MessageText, hello); err != nil {
		t.Fatalf("hello yazılamadı: %v", err)
	}

	fr := &fakeRunner{t: t, conn: conn, done: make(chan struct{})}
	go func() {
		defer close(fr.done)
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			var call callFrame
			if json.Unmarshal(data, &call) != nil || call.Type != "call" {
				continue
			}
			if handle == nil {
				continue // cevapsız bırak (zaman aşımı testleri)
			}
			res := handle(call)
			res.Type, res.ID = "result", call.ID
			out, _ := json.Marshal(res)
			wctx, wcancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = conn.Write(wctx, websocket.MessageText, out)
			wcancel()
		}
	}()
	return fr
}

func (fr *fakeRunner) close() {
	_ = fr.conn.Close(websocket.StatusNormalClosure, "")
	select {
	case <-fr.done:
	case <-time.After(5 * time.Second):
		fr.t.Fatal("fake runner okuma döngüsü kapanmadı")
	}
}

// waitFor — koşul sağlanana dek yoklar (WS kaydı asenkron).
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("beklenen durum gelmedi: %s", what)
}

func testTools() []ToolDecl {
	return []ToolDecl{
		{Name: "read_file", Description: "dosya oku", InputSchema: json.RawMessage(`{"type":"object"}`), Class: "read"},
		{Name: "trash_item", Description: "çöpe taşı", InputSchema: json.RawMessage(`{"type":"object"}`), Class: "write"},
	}
}

func wsURL(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func TestHelloAnnouncesTools(t *testing.T) {
	hub := NewHub()
	srv := httptest.NewServer(hub.WSHandler())
	defer srv.Close()

	if got := hub.GatewayTools(); len(got) != 0 {
		t.Fatalf("bağlantı yokken araç listesi boş olmalı, geldi: %d", len(got))
	}

	fr := dialRunner(t, wsURL(srv), testTools(), nil)
	defer fr.close()

	waitFor(t, "araçlar ilan edildi", func() bool { return len(hub.GatewayTools()) == 2 })
	tools := hub.GatewayTools()
	if tools[0].Name != "read_file" || tools[0].Class.Class != policy.ClassRead {
		t.Fatalf("read_file read sınıfında olmalı: %+v", tools[0])
	}
	if tools[1].Class.Class != policy.ClassWrite {
		t.Fatalf("trash_item write sınıfında olmalı: %+v", tools[1])
	}
}

func TestCallRoundTrip(t *testing.T) {
	hub := NewHub()
	srv := httptest.NewServer(hub.WSHandler())
	defer srv.Close()

	fr := dialRunner(t, wsURL(srv), testTools(), func(c callFrame) resultFrame {
		if c.Tool != "read_file" {
			return resultFrame{OK: false, Error: "beklenmeyen araç: " + c.Tool}
		}
		return resultFrame{OK: true, Text: "dosya içeriği"}
	})
	defer fr.close()
	waitFor(t, "runner bağlandı", hub.Connected)

	text, err := hub.Call(context.Background(), "read_file", json.RawMessage(`{"path":"/tmp/x"}`))
	if err != nil {
		t.Fatalf("çağrı başarısız: %v", err)
	}
	if text != "dosya içeriği" {
		t.Fatalf("beklenmeyen sonuç: %q", text)
	}
}

func TestCallErrorSurfacesAsError(t *testing.T) {
	hub := NewHub()
	srv := httptest.NewServer(hub.WSHandler())
	defer srv.Close()

	fr := dialRunner(t, wsURL(srv), testTools(), func(callFrame) resultFrame {
		return resultFrame{OK: false, Error: "izin reddedildi"}
	})
	defer fr.close()
	waitFor(t, "runner bağlandı", hub.Connected)

	_, err := hub.Call(context.Background(), "read_file", nil)
	if err == nil || err.Error() != "izin reddedildi" {
		t.Fatalf("runner hatası aynen dönmeli, geldi: %v", err)
	}
}

func TestNotConnected(t *testing.T) {
	hub := NewHub()
	if _, err := hub.Call(context.Background(), "read_file", nil); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("ErrNotConnected beklenirdi, geldi: %v", err)
	}
}

func TestDisconnectClearsToolsAndPending(t *testing.T) {
	hub := NewHub()
	srv := httptest.NewServer(hub.WSHandler())
	defer srv.Close()

	// Cevap vermeyen runner: çağrı bekletilirken bağlantıyı düşüreceğiz.
	fr := dialRunner(t, wsURL(srv), testTools(), nil)
	waitFor(t, "runner bağlandı", hub.Connected)

	errc := make(chan error, 1)
	go func() {
		_, err := hub.Call(context.Background(), "read_file", nil)
		errc <- err
	}()
	waitFor(t, "çağrı beklemede", func() bool {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		return hub.sess != nil && len(hub.sess.pending) == 1
	})

	fr.close()

	select {
	case err := <-errc:
		if !errors.Is(err, ErrNotConnected) {
			t.Fatalf("bekleyen çağrı ErrNotConnected ile kapanmalı, geldi: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bekleyen çağrı kopuşta kapanmadı")
	}
	waitFor(t, "araç listesi boşaldı (K2)", func() bool { return len(hub.GatewayTools()) == 0 })
}

func TestSecondConnectionReplacesFirst(t *testing.T) {
	hub := NewHub()
	srv := httptest.NewServer(hub.WSHandler())
	defer srv.Close()

	first := dialRunner(t, wsURL(srv), testTools()[:1], nil)
	waitFor(t, "ilk runner bağlandı", func() bool { return len(hub.GatewayTools()) == 1 })

	second := dialRunner(t, wsURL(srv), testTools(), func(callFrame) resultFrame {
		return resultFrame{OK: true, Text: "ikinciden"}
	})
	defer second.close()
	waitFor(t, "ikinci runner devraldı", func() bool { return len(hub.GatewayTools()) == 2 })

	// İlk bağlantı sunucu tarafından düşürülür; okuma döngüsü hata ile biter.
	select {
	case <-first.done:
	case <-time.After(5 * time.Second):
		t.Fatal("ilk bağlantı düşürülmedi")
	}

	text, err := hub.Call(context.Background(), "read_file", nil)
	if err != nil || text != "ikinciden" {
		t.Fatalf("çağrı ikinci runner'a gitmeli: %q, %v", text, err)
	}
}

// TestUnregisterClosesOnlyOwnPending — yeniden bağlanma yarışı: eski
// bağlantının gecikmiş kapanışı, YENİ oturuma gitmiş bekleyen çağrıyı
// kapatmamalı (düzeltilen hata: pending hub'da tekti, unregister hepsini
// kapatıyordu).
func TestUnregisterClosesOnlyOwnPending(t *testing.T) {
	hub := NewHub()
	oldCh := make(chan resultFrame, 1)
	curCh := make(chan resultFrame, 1)
	old := &session{name: "eski", pending: map[string]chan resultFrame{"a": oldCh}}
	cur := &session{name: "yeni", pending: map[string]chan resultFrame{"b": curCh}}
	hub.mu.Lock()
	hub.sess = cur
	hub.mu.Unlock()

	hub.unregister(old)

	if _, open := <-oldCh; open {
		t.Fatal("eski oturumun bekleyen çağrısı hatayla kapanmalıydı")
	}
	select {
	case <-curCh:
		t.Fatal("yeni oturumun bekleyen çağrısına dokunulmamalıydı")
	default:
	}
	if !hub.Connected() {
		t.Fatal("aktif oturum unregister'dan etkilenmemeliydi")
	}
}

func TestBadHelloRejected(t *testing.T) {
	hub := NewHub()
	srv := httptest.NewServer(hub.WSHandler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(srv), nil)
	if err != nil {
		t.Fatalf("bağlanamadı: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()
	// hello yerine düz result: protokol hatasıyla kapanmalı, kayıt olmamalı.
	_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"result","id":"x"}`))
	_, _, readErr := conn.Read(ctx)
	if readErr == nil {
		t.Fatal("sunucu bağlantıyı kapatmalıydı")
	}
	if hub.Connected() {
		t.Fatal("geçersiz hello ile kayıt olunmamalı")
	}
}

func TestClassForUnknownIsExternal(t *testing.T) {
	c := classFor("garip")
	if c.Class != policy.ClassExternal {
		t.Fatalf("bilinmeyen sınıf external olmalı, geldi: %v", c.Class)
	}
	if e := classFor("execute"); e.Ruleable {
		t.Fatal("execute asla kural alamamalı")
	}
}

// TestDynamicGatewayEmptyToolsList — K2 kanıtı HTTP seviyesinde: Mac bağlı
// değilken motorun göreceği tools/list yanıtı boş dizidir.
func TestDynamicGatewayEmptyToolsList(t *testing.T) {
	hub := NewHub()
	gw := gateway.NewDynamic(hub.GatewayTools)
	srv := httptest.NewServer(gw.Handler())
	defer srv.Close()

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	resp, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("tools/list isteği başarısız: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Result struct {
			Tools []json.RawMessage `json:"tools"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("yanıt çözülemedi: %v", err)
	}
	if len(out.Result.Tools) != 0 {
		t.Fatalf("bağlantı yokken tools/list boş olmalı (K2), geldi: %d", len(out.Result.Tools))
	}

	// Bağlanınca aynı uç araçları göstermeli.
	wsSrv := httptest.NewServer(hub.WSHandler())
	defer wsSrv.Close()
	fr := dialRunner(t, wsURL(wsSrv), testTools(), nil)
	defer fr.close()
	waitFor(t, "araçlar geldi", func() bool { return len(hub.GatewayTools()) == 2 })

	resp2, err := http.Post(srv.URL, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("ikinci tools/list başarısız: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	var out2 struct {
		Result struct {
			Tools []json.RawMessage `json:"tools"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&out2); err != nil {
		t.Fatalf("ikinci yanıt çözülemedi: %v", err)
	}
	if len(out2.Result.Tools) != 2 {
		t.Fatalf("bağlıyken tools/list 2 araç göstermeli, geldi: %d", len(out2.Result.Tools))
	}
}

// TestCanliTutmaBaglantiyiAyaktaTutar — regresyon: boşta duran bağlantıda hiç
// çerçeve akmadığı için Cloudflare ~100 sn'de kapatıyordu; Mac iki dakikada
// bir düşüp geri geliyor, arada bot Mac araçlarını hiç görmüyordu. Ping hem
// akmayı sağlar hem de pong okunduğu için bağlantıyı bozmamalıdır.
func TestCanliTutmaBaglantiyiAyaktaTutar(t *testing.T) {
	hub := NewHub()
	hub.ping = pingAyari{her: 20 * time.Millisecond, bekle: 2 * time.Second}
	srv := httptest.NewServer(hub.WSHandler())
	defer srv.Close()

	fr := dialRunner(t, wsURL(srv), testTools(), func(callFrame) resultFrame {
		return resultFrame{OK: true, Text: "hâlâ buradayım"}
	})
	defer fr.close()
	waitFor(t, "runner bağlandı", hub.Connected)

	time.Sleep(300 * time.Millisecond) // ~15 ping turu
	if !hub.Connected() {
		t.Fatal("ping turları bağlantıyı düşürmemeli")
	}
	// Ayakta olması yetmez, iş de görmeli (ping çerçeveleri veri akışına
	// karışmamalı).
	text, err := hub.Call(context.Background(), "read_file", json.RawMessage(`{"path":"/tmp/x"}`))
	if err != nil {
		t.Fatalf("ping'lerden sonra çağrı düştü: %v", err)
	}
	if text != "hâlâ buradayım" {
		t.Fatalf("beklenmeyen sonuç: %q", text)
	}
}

// TestPingCevapsizKalirsaBaglantiDuser — ölü bağlantı tespiti: TCP ayakta
// kalıp WebSocket ölebilir (vekil arada). Pong gelmiyorsa oturum kapanmalı ki
// Swift tarafı yeniden bağlansın; aksi halde hub "bağlı" görünüp her çağrı
// zaman aşımına düşerdi.
func TestPingCevapsizKalirsaBaglantiDuser(t *testing.T) {
	hub := NewHub()
	hub.ping = pingAyari{her: 20 * time.Millisecond, bekle: 100 * time.Millisecond}
	srv := httptest.NewServer(hub.WSHandler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(srv), nil)
	if err != nil {
		t.Fatalf("runner bağlanamadı: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()
	hello, _ := json.Marshal(helloFrame{Type: "hello", Name: "sessiz-mac", Version: "0", Tools: testTools()})
	if err := conn.Write(ctx, websocket.MessageText, hello); err != nil {
		t.Fatalf("hello yazılamadı: %v", err)
	}
	// Bilerek Read ÇAĞIRMIYORUZ: kütüphane pong'u okuma sırasında üretir,
	// yani bu istemci sağır bir eş gibi davranır.

	waitFor(t, "runner bağlandı", hub.Connected)
	waitFor(t, "cevapsız ping oturumu kapattı", func() bool { return !hub.Connected() })
	if got := hub.GatewayTools(); len(got) != 0 {
		t.Fatalf("oturum kapandıktan sonra araç listesi boşalmalı: %d", len(got))
	}
}
