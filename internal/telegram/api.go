// Package telegram — Telegram Bot API gateway (K16). Telegram bir KANALDIR:
// mesajlar aynı runs/messages yolundan akar, kanal messages.origin ile
// ayrışır. v1'de long polling (getUpdates) — public endpoint, domain ve
// webhook konfigürasyonu gerektirmez.
//
// Sınır kuralı: Bot API yabancı kontrattır — yanıtlar toleranslı çözülür
// (bilinmeyen alanlar yok sayılır).
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// API — Bot API istemcisi.
type API struct {
	Token string
	// BaseURL — varsayılan https://api.telegram.org; testte sahte sunucu.
	BaseURL string
	Client  *http.Client
}

func NewAPI(token string) *API {
	return &API{
		Token:   token,
		BaseURL: "https://api.telegram.org",
		// Long polling timeout'undan uzun: getUpdates 50 sn askıda kalabilir.
		Client: &http.Client{Timeout: 70 * time.Second},
	}
}

func (a *API) client() *http.Client {
	if a.Client != nil {
		return a.Client
	}
	return http.DefaultClient
}

// call — Bot API çağrısı. ok:false yanıtı hata olur (description ile).
func (a *API) call(ctx context.Context, method string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	u := fmt.Sprintf("%s/bot%s/%s", strings.TrimRight(a.BaseURL, "/"), a.Token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	var env struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
		ErrorCode   int             `json:"error_code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return fmt.Errorf("telegram %s: yanıt çözülemedi (durum %d): %w", method, resp.StatusCode, err)
	}
	if !env.OK {
		return fmt.Errorf("telegram %s: %s (kod %d)", method, env.Description, env.ErrorCode)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(env.Result, out)
}

// ── modeller (yalnız kullandığımız alanlar) ────────────────────────────

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text"`
	From      *User  `json:"from"`
	// Caption — EKLİ mesajın metni. Telegram fotoğrafın/belgenin yanındaki
	// yazıyı text'e DEĞİL buraya koyar; okumadığımız sürece "bu faturayı
	// özetle" diye gönderilen fotoğraf bota metinsiz ulaşıyordu (ADIM 13).
	Caption string `json:"caption"`
	// MediaGroupID — albüm kimliği: birden çok fotoğraf tek gönderimde
	// yollandığında Telegram bunları AYRI update olarak verir, aynı grup
	// kimliğiyle. Toplamazsak üç fotoğraf üç ayrı koşu açardı (ADIM 13).
	MediaGroupID string `json:"media_group_id"`
	// Photo — fotoğraf boyutları (en sondaki en büyüğüdür). ADIM 5.
	Photo []PhotoSize `json:"photo"`
	// Document — dosya eki. ADIM 5.
	Document *Document `json:"document"`
}

// Metin — mesajın kullanıcı metni: düz mesajda text, ekli mesajda caption.
func (m *Message) Metin() string {
	if s := strings.TrimSpace(m.Text); s != "" {
		return s
	}
	return strings.TrimSpace(m.Caption)
}

type PhotoSize struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size"`
}

type Document struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	FileSize int64  `json:"file_size"`
}

// HasEk — mesajda indirilebilir bir ek var mı (ADIM 5).
func (m *Message) HasEk() bool { return len(m.Photo) > 0 || m.Document != nil }

type Chat struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
}

type User struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    *User    `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

// InlineKeyboard — onay kartı butonları. callback_data tahmin edilemez
// tek kullanımlık token taşır (K16).
type InlineKeyboard struct {
	InlineKeyboard [][]InlineButton `json:"inline_keyboard"`
}

type InlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

// ── uçlar ──────────────────────────────────────────────────────────────

// GetUpdates — long polling. offset = son işlenen update_id + 1.
func (a *API) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]Update, error) {
	in := map[string]any{
		"offset":  offset,
		"timeout": timeoutSec,
		// Yalnız işlediğimiz türler; diğerleri sunucuda birikmesin.
		"allowed_updates": []string{"message", "callback_query"},
	}
	var out []Update
	if err := a.call(ctx, "getUpdates", in, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// SendMessage — düz metin mesaj; markup nil olabilir. Dönen message_id
// sonradan editMessageText ile güncellenir (akan yanıt).
func (a *API) SendMessage(ctx context.Context, chatID int64, text string, markup *InlineKeyboard) (int64, error) {
	in := map[string]any{"chat_id": chatID, "text": clip(text)}
	if markup != nil {
		in["reply_markup"] = markup
	}
	var out Message
	if err := a.call(ctx, "sendMessage", in, &out); err != nil {
		return 0, err
	}
	return out.MessageID, nil
}

// EditMessageText — akan yanıtın canlı güncellenmesi ve karar sonrası kart
// kapatma. Telegram aynı metne edit'i "message is not modified" ile
// reddeder; bu hata yutulur (akış throttle'ı zaten aynı metni gönderebilir).
func (a *API) EditMessageText(ctx context.Context, chatID, messageID int64, text string, markup *InlineKeyboard) error {
	in := map[string]any{"chat_id": chatID, "message_id": messageID, "text": clip(text)}
	if markup != nil {
		in["reply_markup"] = markup
	}
	err := a.call(ctx, "editMessageText", in, nil)
	if err != nil && strings.Contains(err.Error(), "message is not modified") {
		return nil
	}
	return err
}

// AnswerCallbackQuery — buton tıklamasının "yükleniyor" halkasını kapatır;
// text verilirse kullanıcıya küçük bildirim çıkar.
func (a *API) AnswerCallbackQuery(ctx context.Context, id, text string) error {
	in := map[string]any{"callback_query_id": id}
	if text != "" {
		in["text"] = text
	}
	return a.call(ctx, "answerCallbackQuery", in, nil)
}

// GetMe — açılışta token doğrulaması.
func (a *API) GetMe(ctx context.Context) (User, error) {
	var out User
	err := a.call(ctx, "getMe", map[string]any{}, &out)
	return out, err
}

// GetFile — ek indirme yolu (ADIM 5). Dönen FilePath göreli yoldur; içerik
// DownloadFileFile ile /file/bot<token>/<path>'ten okunur.
func (a *API) GetFile(ctx context.Context, fileID string) (string, error) {
	var out struct {
		FilePath string `json:"file_path"`
	}
	if err := a.call(ctx, "getFile", map[string]any{"file_id": fileID}, &out); err != nil {
		return "", err
	}
	return out.FilePath, nil
}

// DownloadFile — ek içeriğini indirir (ADIM 5).
func (a *API) DownloadFile(ctx context.Context, filePath string) ([]byte, error) {
	u := fmt.Sprintf("%s/file/bot%s/%s", strings.TrimRight(a.BaseURL, "/"), a.Token, filePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram dosya indirme: durum %d", resp.StatusCode)
	}
	var b bytes.Buffer
	// Telegram 20 MB'e kadar belge döndürebilir; MaxBytesReader olmadığı için
	// kendimiz sınırladık (ADIM 5 kuralı EkAzami ile aynı sınır).
	limited := io.LimitReader(resp.Body, EkIndirmeAzami+1)
	n, err := io.Copy(&b, limited)
	if err != nil {
		return nil, err
	}
	if n > EkIndirmeAzami {
		return nil, ErrIndirmeBuyuk
	}
	return b.Bytes(), nil
}

// SendPhotoAzami — Telegram'ın sendPhoto üst sınırı (10 MB). Üstündeki
// görüntüler belge olarak gider: sendPhoto 413 dönerdi, kullanıcı da dosyayı
// hiç alamazdı.
const SendPhotoAzami = 10 << 20

// SendFile — yerel bir dosyayı sohbete yükler (ADIM 13: botun ürettiği ek).
// method sendPhoto ya da sendDocument; alan adı method'la aynı kalıptadır
// (photo/document). multipart ŞART: dosya bizde, Telegram'da bir file_id yok.
func (a *API) SendFile(ctx context.Context, chatID int64, method, field, filePath, fileName, caption string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	var gövde bytes.Buffer
	w := multipart.NewWriter(&gövde)
	if err := w.WriteField("chat_id", strconv.FormatInt(chatID, 10)); err != nil {
		return err
	}
	if caption != "" {
		// Caption sınırı metinden kısa (1024): kendi kırpıcımız 4000'e göre
		// ayarlı, burada ayrıca kısaltılır.
		if err := w.WriteField("caption", clipRunes(caption, maxTelegramCaption)); err != nil {
			return err
		}
	}
	part, err := w.CreateFormFile(field, fileName)
	if err != nil {
		return err
	}
	if _, err := io.Copy(part, f); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	u := fmt.Sprintf("%s/bot%s/%s", strings.TrimRight(a.BaseURL, "/"), a.Token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, &gövde)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := a.client().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var env struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
		ErrorCode   int    `json:"error_code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return fmt.Errorf("telegram %s: yanıt çözülemedi (durum %d): %w", method, resp.StatusCode, err)
	}
	if !env.OK {
		return fmt.Errorf("telegram %s: %s (kod %d)", method, env.Description, env.ErrorCode)
	}
	return nil
}

// EkIndirmeAzami — Telegram'dan indirilen tek ekin sınırı (20 MB, ADIM 5).
const EkIndirmeAzami = 20 << 20

// ErrIndirmeBuyuk — Telegram eki sınırdan büyük.
var ErrIndirmeBuyuk = errors.New("telegram: ek indirme sınırını aştı")

// maxTelegramText — Bot API sınırı 4096 rune; uzun yanıtlar kırpılır.
const maxTelegramText = 4000

// maxTelegramCaption — dosya açıklaması sınırı 1024 rune (metinden ayrı sınır).
const maxTelegramCaption = 1000

func clip(s string) string { return clipRunes(s, maxTelegramText) }

func clipRunes(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "\n… (kırpıldı — tamamı Mac uygulamasında)"
}

// ErrEmptyToken — gateway token'sız başlatılamaz.
var ErrEmptyToken = errors.New("telegram: TELEGRAM_BOT_TOKEN boş")
