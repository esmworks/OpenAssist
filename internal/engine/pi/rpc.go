package pi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"sync"
)

// rpcConn — `pi --mode rpc` ile stdio üzerinde JSONL konuşan istemci.
//
// stdout'tan üç mesaj sınıfı gelir:
//
//	{"type":"response","id":…,"success":…}  → gönderdiğimiz komutun yanıtı
//	{"type":"extension_ui_request","id":…}  → eklenti diyaloğu (onay/soru)
//	diğer her satır                         → olay akışı (message_update, tool_*)
//
// ÇERÇEVELEME: pi katı JSONL konuşur, kayıt ayracı YALNIZ LF'tir. pi'nin kendi
// dokümanı Node readline'ı protokole aykırı sayar — U+2028/U+2029'da da böler,
// oysa bunlar JSON dizesi İÇİNDE geçerli karakterlerdir ve bir araç çıktısında
// pekâlâ bulunabilir. bufio.Scanner yalnız \n'de böler; bu yüzden köprüyü
// Node'da değil Go'da tutuyoruz (eski oa-pi-bridge.mjs bu tuzağın üstündeydi).
type rpcConn struct {
	w io.WriteCloser

	mu     sync.Mutex // yazma sırası + id sayacı
	nextID int

	pendMu  sync.Mutex
	pending map[string]chan rpcResponse

	// onEvent olay satırlarını alır. Okuma döngüsünden SIRAYLA çağrılır:
	// olay sırası motor kontratının garantisidir (engine.Executor).
	onEvent func(typ string, line []byte)
	// onUIRequest eklenti diyaloglarını alır. Okuma döngüsünün DIŞINDA
	// koşar (bir onay dakikalarca bekleyebilir, bu sırada olay akışı
	// durmamalı) ama kendi arasında TEK SIRA: uiMu her seferinde yalnız bir
	// diyaloğun işlenmesine izin verir.
	//
	// Neden sıra: onay merkezi koşu BAŞINA tek bekleyen istek tutar
	// (orchestrator.approvalHub, anahtar runID). İki diyalog aynı anda
	// açılırsa ikincisi birincinin kanalını ezer, ilk kart cevaplanamaz ve
	// koşu park eder. pi araç ön-kontrollerini sıraya alır ama araç
	// GÖVDELERİ paralel koşar (docs/extensions.md) — iki ask_user aynı anda
	// pekâlâ soru sorabilir.
	uiMu        sync.Mutex
	onUIRequest func(req uiRequest)

	closeOnce sync.Once
	closed    chan struct{}
}

type rpcResponse struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

// uiRequest — eklentinin ctx.ui çağrısının tel karşılığı. Alanlar method'a
// göre dolar (docs/rpc.md "Extension UI Protocol").
type uiRequest struct {
	ID          string   `json:"id"`
	Method      string   `json:"method"` // select|confirm|input|editor|notify|…
	Title       string   `json:"title"`
	Message     string   `json:"message"`
	Options     []string `json:"options"`
	Prefill     string   `json:"prefill"`
	Placeholder string   `json:"placeholder"`
}

func newRPCConn(w io.WriteCloser) *rpcConn {
	return &rpcConn{
		w:       w,
		pending: make(map[string]chan rpcResponse),
		closed:  make(chan struct{}),
	}
}

// readLoop stdout'u satır satır tüketir; süreç kapanınca bekleyen komutları
// hata ile çözer (kimse sonsuza kadar beklemesin).
func (c *rpcConn) readLoop(r io.Reader) {
	sc := bufio.NewScanner(r)
	// Araç sonuçları tek satırda büyük olabilir (dosya okuma, bash çıktısı).
	sc.Buffer(make([]byte, 0, 256*1024), 32*1024*1024)
	for sc.Scan() {
		c.dispatch(sc.Bytes())
	}
	c.failAll(errors.New("pi rpc süreci kapandı"))
}

func (c *rpcConn) dispatch(line []byte) {
	var head struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		return // protokol dışı satır koşuyu kırmaz
	}

	switch head.Type {
	case "response":
		var resp rpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			return
		}
		c.pendMu.Lock()
		ch, ok := c.pending[head.ID]
		if ok {
			delete(c.pending, head.ID)
		}
		c.pendMu.Unlock()
		if ok {
			ch <- resp
		}
	case "extension_ui_request":
		var req uiRequest
		if err := json.Unmarshal(line, &req); err != nil {
			return
		}
		if c.onUIRequest != nil {
			go func() {
				c.uiMu.Lock()
				defer c.uiMu.Unlock()
				c.onUIRequest(req)
			}()
		}
	default:
		if c.onEvent != nil {
			c.onEvent(head.Type, line)
		}
	}
}

// command — yanıt bekleyen komut yazar. ctx iptal edilirse bekleme biter;
// süreç ölürse closed kanalı çözer.
func (c *rpcConn) command(ctx context.Context, cmd map[string]any) (json.RawMessage, error) {
	// Süreç ÖLDÜKTEN sonra gelen komut: kaydı hiç açma. failAll yalnız o an
	// bekleyenleri çözer; sonradan eklenen bir kaydı çözecek kimse kalmaz ve
	// çağıran (ör. tur sonundaki get_last_assistant_text) süresiz beklerdi —
	// koşu bağlamının çoğu zaman süresi yoktur.
	select {
	case <-c.closed:
		return nil, errors.New("pi rpc süreci kapandı")
	default:
	}

	c.mu.Lock()
	c.nextID++
	id := "oa-" + strconv.Itoa(c.nextID)
	c.mu.Unlock()

	ch := make(chan rpcResponse, 1)
	c.pendMu.Lock()
	c.pending[id] = ch
	c.pendMu.Unlock()

	payload := make(map[string]any, len(cmd)+1)
	for k, v := range cmd {
		payload[k] = v
	}
	payload["id"] = id

	if err := c.writeLine(payload); err != nil {
		c.pendMu.Lock()
		delete(c.pending, id)
		c.pendMu.Unlock()
		return nil, err
	}

	select {
	case res := <-ch:
		if !res.Success {
			msg := res.Error
			if msg == "" {
				msg = "pi komutu başarısız"
			}
			return nil, errors.New(msg)
		}
		return res.Data, nil
	case <-c.closed:
		c.pendMu.Lock()
		delete(c.pending, id)
		c.pendMu.Unlock()
		return nil, errors.New("pi rpc süreci kapandı")
	case <-ctx.Done():
		c.pendMu.Lock()
		delete(c.pending, id)
		c.pendMu.Unlock()
		return nil, ctx.Err()
	}
}

// answerUI — eklenti diyaloğuna cevap yazar (yanıt beklenmez).
func (c *rpcConn) answerUI(id string, fields map[string]any) error {
	payload := map[string]any{"type": "extension_ui_response", "id": id}
	for k, v := range fields {
		payload[k] = v
	}
	return c.writeLine(payload)
}

func (c *rpcConn) failAll(err error) {
	c.closeOnce.Do(func() { close(c.closed) })
	c.pendMu.Lock()
	defer c.pendMu.Unlock()
	for id, ch := range c.pending {
		ch <- rpcResponse{Success: false, Error: err.Error()}
		delete(c.pending, id)
	}
}

func (c *rpcConn) writeLine(v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.w.Write(append(data, '\n'))
	return err
}
