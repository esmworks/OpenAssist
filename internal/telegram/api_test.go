package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBotAPI — Bot API'nin test ikizi: çağrıları kaydeder, hazır yanıt verir.
type fakeBotAPI struct {
	srv *httptest.Server
	mu  chan struct{} // basit kilit yerine kanal (test tek goroutine)

	calls    []recordedCall
	nextMsgs []int64 // sendMessage'ın döneceği message_id sırası
	updates  []Update
	failNext string // bu metod bir kez ok:false dönsün
	// dosya — /file/bot<token>/<yol> ucunun döndüğü içerik (ADIM 5 indirme).
	dosya []byte
}

type recordedCall struct {
	Method string
	Body   map[string]any
}

func newFakeBotAPI(t *testing.T) *fakeBotAPI {
	t.Helper()
	f := &fakeBotAPI{mu: make(chan struct{}, 1)}
	f.mu <- struct{}{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Dosya indirme ucu Bot API metodu değil: /file/bot<token>/<yol>.
		if strings.Contains(r.URL.Path, "/file/bot") {
			<-f.mu
			f.calls = append(f.calls, recordedCall{
				Method: "downloadFile",
				Body:   map[string]any{"path": r.URL.Path},
			})
			veri := f.dosya
			f.mu <- struct{}{}
			if veri == nil {
				veri = []byte("sahte-dosya-icerigi")
			}
			_, _ = w.Write(veri)
			return
		}

		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			// sendPhoto/sendDocument multipart gelir (ADIM 13): alanlar ve
			// dosya adı kaydedilsin ki test ne gönderildiğini görebilsin.
			body = map[string]any{}
			r.Body = io.NopCloser(bytes.NewReader(raw))
			if mr, err := r.MultipartReader(); err == nil {
				for {
					p, err := mr.NextPart()
					if err != nil {
						break
					}
					veri, _ := io.ReadAll(p)
					if p.FileName() != "" {
						body["file_name"] = p.FileName()
						body["file_field"] = p.FormName()
						body["file_size"] = len(veri)
						body["file_body"] = string(veri)
						continue
					}
					body[p.FormName()] = string(veri)
				}
			}
		} else {
			_ = json.Unmarshal(raw, &body)
		}

		<-f.mu
		f.calls = append(f.calls, recordedCall{Method: method, Body: body})
		fail := f.failNext == method
		if fail {
			f.failNext = ""
		}
		var msgID int64 = 1000
		if method == "sendMessage" && len(f.nextMsgs) > 0 {
			msgID, f.nextMsgs = f.nextMsgs[0], f.nextMsgs[1:]
		}
		ups := f.updates
		f.updates = nil
		f.mu <- struct{}{}

		w.Header().Set("Content-Type", "application/json")
		if fail {
			_, _ = io.WriteString(w, `{"ok":false,"description":"Bad Request: message is not modified","error_code":400}`)
			return
		}
		switch method {
		case "getMe":
			_, _ = io.WriteString(w, `{"ok":true,"result":{"id":7,"username":"oa_test_bot"}}`)
		case "sendMessage":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true, "result": map[string]any{"message_id": msgID},
			})
		case "getUpdates":
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": ups})
		case "getFile":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true, "result": map[string]any{"file_path": "photos/file_1.jpg"},
			})
		default:
			_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeBotAPI) api() *API {
	return &API{Token: "T", BaseURL: f.srv.URL, Client: f.srv.Client()}
}

func (f *fakeBotAPI) lock()   { <-f.mu }
func (f *fakeBotAPI) unlock() { f.mu <- struct{}{} }

func (f *fakeBotAPI) callsOf(method string) []recordedCall {
	f.lock()
	defer f.unlock()
	var out []recordedCall
	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func TestAPIBasics(t *testing.T) {
	f := newFakeBotAPI(t)
	a := f.api()
	ctx := context.Background()

	me, err := a.GetMe(ctx)
	if err != nil || me.Username != "oa_test_bot" {
		t.Fatalf("GetMe: %v %+v", err, me)
	}

	id, err := a.SendMessage(ctx, 42, "selam", nil)
	if err != nil || id != 1000 {
		t.Fatalf("SendMessage: %v %d", err, id)
	}
	sent := f.callsOf("sendMessage")
	if len(sent) != 1 || sent[0].Body["text"] != "selam" || sent[0].Body["chat_id"].(float64) != 42 {
		t.Errorf("gönderim gövdesi: %+v", sent)
	}

	// getUpdates yalnız işlediğimiz türleri istemeli.
	if _, err := a.GetUpdates(ctx, 5, 50); err != nil {
		t.Fatal(err)
	}
	gu := f.callsOf("getUpdates")[0].Body
	if gu["offset"].(float64) != 5 {
		t.Errorf("offset taşınmadı: %+v", gu)
	}
	allowed, _ := json.Marshal(gu["allowed_updates"])
	if !strings.Contains(string(allowed), "callback_query") {
		t.Errorf("allowed_updates: %s", allowed)
	}
}

// "message is not modified" Telegram'ın normal cevabıdır — hata sayılmaz.
func TestEditIgnoresNotModified(t *testing.T) {
	f := newFakeBotAPI(t)
	<-f.mu
	f.failNext = "editMessageText"
	f.mu <- struct{}{}

	if err := f.api().EditMessageText(context.Background(), 1, 2, "aynı metin", nil); err != nil {
		t.Errorf("not modified yutulmalıydı: %v", err)
	}
}

func TestClipLongText(t *testing.T) {
	long := strings.Repeat("ö", maxTelegramText+500) // çok baytlı karakter
	out := clip(long)
	if len([]rune(out)) <= maxTelegramText {
		t.Error("kırpma sonrası not eklenmeli")
	}
	if !strings.Contains(out, "kırpıldı") {
		t.Error("kırpma notu yok")
	}
	if clip("kısa") != "kısa" {
		t.Error("kısa metin değişmemeli")
	}
}

// ADIM 5'in indirme yolu (getFile → /file/bot…) hiç test edilmemişti.
func TestGetFileVeIndirme(t *testing.T) {
	f := newFakeBotAPI(t)
	f.lock()
	f.dosya = []byte("PNG-gövdesi")
	f.unlock()
	a := f.api()
	ctx := context.Background()

	yol, err := a.GetFile(ctx, "FILE123")
	if err != nil || yol != "photos/file_1.jpg" {
		t.Fatalf("GetFile: %v %q", err, yol)
	}
	if body := f.callsOf("getFile")[0].Body; body["file_id"] != "FILE123" {
		t.Errorf("file_id taşınmadı: %+v", body)
	}
	veri, err := a.DownloadFile(ctx, yol)
	if err != nil || string(veri) != "PNG-gövdesi" {
		t.Fatalf("DownloadFile: %v %q", err, veri)
	}
	if !strings.HasSuffix(f.callsOf("downloadFile")[0].Body["path"].(string), "/photos/file_1.jpg") {
		t.Errorf("indirme yolu: %+v", f.callsOf("downloadFile"))
	}
}

// ADIM 13: giden dosya multipart gider — alan adı, dosya adı ve caption
// Telegram'ın beklediği yerlerde olmalı.
func TestSendFileMultipart(t *testing.T) {
	f := newFakeBotAPI(t)
	dir := t.TempDir()
	yol := filepath.Join(dir, "rapor.pdf")
	if err := os.WriteFile(yol, []byte("%PDF-1.7 gövde"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := f.api().SendFile(context.Background(), 42, "sendDocument", "document", yol, "rapor.pdf", "aylık rapor"); err != nil {
		t.Fatalf("SendFile: %v", err)
	}
	çağrı := f.callsOf("sendDocument")
	if len(çağrı) != 1 {
		t.Fatalf("sendDocument çağrısı bekleniyordu: %+v", f.callsOf(""))
	}
	body := çağrı[0].Body
	if body["chat_id"] != "42" || body["caption"] != "aylık rapor" {
		t.Errorf("alanlar: %+v", body)
	}
	if body["file_field"] != "document" || body["file_name"] != "rapor.pdf" {
		t.Errorf("dosya alanı/adı: %+v", body)
	}
	if body["file_body"] != "%PDF-1.7 gövde" {
		t.Errorf("dosya gövdesi bozuk: %+v", body["file_body"])
	}
}

func TestSendFileHatasiYukselir(t *testing.T) {
	f := newFakeBotAPI(t)
	f.lock()
	f.failNext = "sendPhoto"
	f.unlock()
	dir := t.TempDir()
	yol := filepath.Join(dir, "a.png")
	if err := os.WriteFile(yol, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := f.api().SendFile(context.Background(), 1, "sendPhoto", "photo", yol, "a.png", "")
	if err == nil || !strings.Contains(err.Error(), "not modified") {
		t.Errorf("Bot API hatası yükselmeli: %v", err)
	}
	if err := f.api().SendFile(context.Background(), 1, "sendPhoto", "photo", filepath.Join(dir, "yok.png"), "yok.png", ""); err == nil {
		t.Error("olmayan dosya hata vermeli")
	}
}

// Ekli mesajın metni caption'dadır; text alanı boş gelir.
func TestMesajMetniCaptionaDuser(t *testing.T) {
	m := &Message{Text: "  düz mesaj  "}
	if m.Metin() != "düz mesaj" {
		t.Errorf("text: %q", m.Metin())
	}
	m = &Message{Caption: " bu faturayı özetle "}
	if m.Metin() != "bu faturayı özetle" {
		t.Errorf("caption: %q", m.Metin())
	}
	if (&Message{}).Metin() != "" {
		t.Error("boş mesaj boş metin vermeli")
	}
}

func TestAPIErrorSurfacesDescription(t *testing.T) {
	f := newFakeBotAPI(t)
	<-f.mu
	f.failNext = "sendMessage"
	f.mu <- struct{}{}
	_, err := f.api().SendMessage(context.Background(), 1, "x", nil)
	if err == nil || !strings.Contains(err.Error(), "message is not modified") {
		t.Errorf("api hatası açıklamayı taşımalı: %v", err)
	}
}
