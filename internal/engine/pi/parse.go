package pi

import (
	"encoding/json"

	"github.com/esmworks/OpenAssist/internal/engine"
)

// parser — pi RPC olaylarını normalize engine.Event'e çevirir ve turun
// kullanım sayaçlarını biriktirir.
//
// Tanınmayan olay türü koşuyu KIRMAZ: ham satırıyla KindUnknown olarak geçer
// (motor çıktısı bizim kontratımız değildir — sınır kuralı). pi sürümü yeni
// bir olay eklediğinde tur düşmez, kayda ham girer.
type parser struct {
	// usage — message_update'in taşıdığı KÜMÜLATİF kullanım. Son görülen
	// dolu değer tutulur: sağlayıcı akış boyunca bildirmezse tamamlanmada
	// tek seferde gelir.
	//
	// Neden get_session_stats DEĞİL: o komut OTURUM toplamını verir; sürdürülen
	// bir oturumda turun maliyeti yerine tüm geçmişin toplamı yazılırdı.
	usage piUsage
	// settled — agent_settled görüldü mü (turun gerçek bitişi).
	settled bool
	// turns — tamamlanan pi turu (assistant yanıtı + araçları) sayısı.
	turns int
	// errMsg — turu düşüren hata. pi hatayı ayrı bir olayla DEĞİL,
	// stopReason="error" taşıyan asistan mesajıyla bildirir.
	errMsg string
}

type piUsage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
	Cost       struct {
		Total float64 `json:"total"`
	} `json:"cost"`
}

func (u piUsage) bos() bool {
	return u.Input == 0 && u.Output == 0 && u.CacheRead == 0 && u.CacheWrite == 0
}

func (p *parser) parseLine(typ string, line []byte) []engine.Event {
	raw := json.RawMessage(append([]byte(nil), line...))

	switch typ {
	case "message_update":
		var ev struct {
			Usage                 piUsage `json:"usage"`
			AssistantMessageEvent struct {
				Type    string `json:"type"`
				Delta   string `json:"delta"`
				Content string `json:"content"`
			} `json:"assistantMessageEvent"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			return []engine.Event{{Kind: engine.KindUnknown, Raw: raw}}
		}
		if !ev.Usage.bos() {
			p.usage = ev.Usage
		}
		switch ev.AssistantMessageEvent.Type {
		case "text_delta":
			if ev.AssistantMessageEvent.Delta == "" {
				return nil
			}
			return []engine.Event{{
				Kind: engine.KindAssistantDelta,
				Text: ev.AssistantMessageEvent.Delta,
				Raw:  raw,
			}}
		case "text_end":
			if ev.AssistantMessageEvent.Content == "" {
				return nil
			}
			return []engine.Event{{
				Kind: engine.KindAssistantText,
				Text: ev.AssistantMessageEvent.Content,
				Raw:  raw,
			}}
		}
		// thinking_* ve toolcall_* akışı: araç olayları zaten
		// tool_execution_* ile geliyor, düşünme metni transkripte
		// girmiyor. Sessizce geçilir.
		return nil

	case "tool_execution_start":
		var ev struct {
			ToolCallID string          `json:"toolCallId"`
			ToolName   string          `json:"toolName"`
			Args       json.RawMessage `json:"args"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			return []engine.Event{{Kind: engine.KindUnknown, Raw: raw}}
		}
		return []engine.Event{{
			Kind: engine.KindToolStarted,
			Tool: &engine.ToolInfo{ID: ev.ToolCallID, Name: ev.ToolName, Input: ev.Args},
			Raw:  raw,
		}}

	case "tool_execution_end":
		var ev struct {
			ToolCallID string `json:"toolCallId"`
			ToolName   string `json:"toolName"`
			IsError    bool   `json:"isError"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			return []engine.Event{{Kind: engine.KindUnknown, Raw: raw}}
		}
		return []engine.Event{{
			Kind: engine.KindToolCompleted,
			Tool: &engine.ToolInfo{ID: ev.ToolCallID, Name: ev.ToolName, IsError: ev.IsError},
			Raw:  raw,
		}}

	case "message_end":
		// pi'nin hata yolu: ayrı bir error olayı YOKTUR; başarısız çağrı
		// stopReason="error" ve errorMessage taşıyan bir asistan mesajı
		// olarak kapanır. Bunu yakalamazsak tur "başarılı ama boş" görünürdü.
		var ev struct {
			Message struct {
				Role         string `json:"role"`
				StopReason   string `json:"stopReason"`
				ErrorMessage string `json:"errorMessage"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil
		}
		if ev.Message.Role != "assistant" {
			return nil // kullanıcı mesajının kapanışı; stopReason taşımaz
		}
		if ev.Message.StopReason != "error" {
			// Başarılı yanıt önceki hatayı SİLER: pi geçici hataları kendisi
			// yeniden dener (auto_retry, varsayılan 3 deneme) ve ilk
			// denemenin hatası kalıcı sayılsaydı, ikinci denemede gelen
			// cevap kullanıcıya ulaşmasına rağmen koşu "başarısız" yazılırdı.
			p.errMsg = ""
			return nil
		}
		msg := ev.Message.ErrorMessage
		if msg == "" {
			msg = "pi turu hatayla kapandı"
		}
		p.errMsg = msg
		return []engine.Event{{Kind: engine.KindError, Err: msg, Raw: raw}}

	case "auto_retry_end":
		// Yeniden deneme döngüsünün YETKİLİ kapanışı: turun akıbetini
		// buradan öğreniyoruz (ölçüldü: 3 denemeden sonra success=false +
		// finalError, ardından agent_settled).
		var ev struct {
			Success    bool   `json:"success"`
			FinalError string `json:"finalError"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil
		}
		if ev.Success {
			p.errMsg = ""
			return nil
		}
		msg := ev.FinalError
		if msg == "" {
			msg = "pi yeniden denemelerden sonra vazgeçti"
		}
		p.errMsg = msg
		return []engine.Event{{Kind: engine.KindError, Err: msg, Raw: raw}}

	case "turn_end":
		p.turns++
		return nil

	case "extension_error":
		// Eklenti patlaması turu düşürmez (pi de düşürmüyor) ama sessiz
		// kalmamalı: kullanıcı "eklentim neden çalışmadı" diye sorduğunda
		// cevabı kayıtta bulunsun. Alan adları rpc-mode.js ile birebir.
		var ev struct {
			ExtensionPath string `json:"extensionPath"`
			Event         string `json:"event"`
			Error         string `json:"error"`
		}
		_ = json.Unmarshal(line, &ev)
		msg := ev.Error
		if ev.ExtensionPath != "" {
			msg = ev.ExtensionPath + ": " + msg
		}
		return []engine.Event{{Kind: engine.KindError, Err: "eklenti hatası — " + msg, Raw: raw}}

	case "agent_settled":
		p.settled = true
		return nil

	case "agent_start", "agent_end", "turn_start",
		"message_start", "tool_execution_update",
		"queue_update", "bash_execution_update":
		// Akışın iç ayrıntısı: normalize sözlükte karşılığı yok ve ham
		// kayda da değmiyor (delta'lar zaten geçti).
		return nil
	}

	return []engine.Event{{Kind: engine.KindUnknown, Raw: raw}}
}
